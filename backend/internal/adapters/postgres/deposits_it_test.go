// Integración con Postgres real de las señas (F11, D2/D7/D9/D10). Se saltea sin
// SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la DB: el upsert de la
// política, el libro mayor append-only, los UPDATE condicionales por estado de
// origen, la idempotencia por client_op_id/booking_id, la guarda única del evento
// del banco, el barrido (vencimiento, reembolso por ventana, `done`/`no_show`
// que no reembolsan), la carrera barrido↔callback en los DOS órdenes y el cursor
// opaco de la pantalla.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

const (
	depSalon  = "s-it-dep"
	depSalon2 = "s-it-dep2"
	depUser   = "u-it-dep-prof"
)

type depFix struct {
	pool     *pgxpool.Pool
	ctx      context.Context
	deposits Deposits
	policies DepositPolicies
	// t0 es el instante de las fixtures (una hora antes de ahora).
	t0 time.Time
}

// depFixtures deja dos salones y una profesional limpios para las pruebas.
func depFixtures(t *testing.T) *depFix {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-dep%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-dep%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('` + depUser + `','Prof Seña','dep@test.com','x')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('` + depSalon + `','s-it-dep','Salón Señas IT','America/Mexico_City')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('` + depSalon2 + `','s-it-dep2','Salón Señas IT 2','America/Mexico_City')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	return &depFix{
		pool: pool, ctx: ctx,
		deposits: Deposits{Pool: pool}, policies: DepositPolicies{Pool: pool},
		t0: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond),
	}
}

// booking inserta un turno del salón (con su profesional) y devuelve su id.
func (f *depFix) booking(t *testing.T, salonID string, status domain.BookingStatus, startsAt time.Time) string {
	t.Helper()
	id := "b-it-dep-" + domain.NewID()
	// Cada turno con su propia profesional: la EXCLUDE por (salón, profesional)
	// no puede rechazar dos fixtures del mismo test.
	prof := "u-it-dep-" + domain.NewID()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO users(id,name,email,pass_hash) VALUES($1,'Prof IT',$2,'x')`,
		prof, prof+"@test.com"); err != nil {
		t.Fatalf("profesional: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at)
		 VALUES($1,$2,$3,'Lía Pérez','555',$4,$5,$6,'',$7)`,
		id, salonID, prof, startsAt.UTC(), startsAt.UTC().Add(time.Hour), string(status), f.t0); err != nil {
		t.Fatalf("turno: %v", err)
	}
	return id
}

// newDeposit crea una sena PENDIENTE con un cargo válido (43 chars base64url).
func (f *depFix) newDeposit(t *testing.T, salonID, bookingID string, amount, percent int, created, expires time.Time) domain.BookingDeposit {
	t.Helper()
	chargeID, err := domain.NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.deposits.Create(domain.BookingDeposit{
		ID: domain.NewID(), SalonID: salonID, BookingID: bookingID,
		AmountCents: amount, Percent: percent, Status: domain.DepositPending,
		ChargeID: chargeID, CreatedAt: created, ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("crear seña: %v", err)
	}
	return d
}

// eventKinds lee el libro mayor de una sena.
func (f *depFix) eventKinds(t *testing.T, depositID string) []string {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, `SELECT kind FROM deposit_events WHERE deposit_id=$1 ORDER BY created_at, id`, depositID)
	if err != nil {
		t.Fatalf("eventos: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("evento: %v", err)
		}
		out = append(out, k)
	}
	return out
}

// setBookingStatus cambia el estado del turno directamente (el barrido lee el
// estado real, no uno simulado).
func (f *depFix) setBookingStatus(t *testing.T, bookingID string, status domain.BookingStatus) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE bookings SET status=$2 WHERE id=$1`, bookingID, string(status)); err != nil {
		t.Fatalf("estado del turno: %v", err)
	}
}

func approvedEvent(chargeID, eventID string, amount int, now time.Time) domain.DepositCallback {
	return domain.DepositCallback{
		EventID: eventID, ChargeID: chargeID, Reference: "ref",
		Kind: domain.CallbackApproved, AmountCents: amount, Now: now,
	}
}

// D3: la política es UN upsert por salón y se relee tal como quedó guardada.
func TestDepositsIT_policyUpsert(t *testing.T) {
	f := depFixtures(t)
	if _, err := f.policies.Find(depSalon); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sin fila: %v", err)
	}
	saved, err := f.policies.Save(domain.DepositPolicy{
		SalonID: depSalon, Saved: true, Enabled: true, Percent: 30, HoldMinutes: 30,
		RefundHours: 24, UpdatedAt: f.t0,
	})
	if err != nil || !saved.Enabled || saved.Percent != 30 {
		t.Fatalf("guardar: %+v (%v)", saved, err)
	}
	// Segundo guardado: reemplaza y no duplica.
	if _, err := f.policies.Save(domain.DepositPolicy{
		SalonID: depSalon, Saved: true, Enabled: false, Percent: 50, HoldMinutes: 60,
		RefundHours: 48, UpdatedAt: f.t0.Add(time.Minute),
	}); err != nil {
		t.Fatalf("segundo guardado: %v", err)
	}
	got, err := f.policies.Find(depSalon)
	if err != nil {
		t.Fatalf("leer: %v", err)
	}
	if got.Enabled || got.Percent != 50 || got.HoldMinutes != 60 || got.RefundHours != 48 {
		t.Fatalf("política guardada: %+v", got)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM deposit_policies WHERE salon_id=$1`, depSalon).Scan(&n); err != nil || n != 1 {
		t.Fatalf("filas: %d (%v)", n, err)
	}
}

// D8: Create es idempotente por booking_id y por client_op_id, y el evento
// `created` entra UNA vez.
func TestDepositsIT_createIdempotent(t *testing.T) {
	f := depFixtures(t)
	starts := f.t0.Add(48 * time.Hour)
	b := f.booking(t, depSalon, domain.BookingBooked, starts)
	expires := f.t0.Add(30 * time.Minute)
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, expires)
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 1 || kinds[0] != domain.DepositEventCreated {
		t.Fatalf("libro mayor: %v", kinds)
	}
	again, err := f.deposits.Create(domain.BookingDeposit{
		ID: domain.NewID(), SalonID: depSalon, BookingID: b, AmountCents: 4500, Percent: 30,
		Status: domain.DepositPending, ChargeID: d.ChargeID, CreatedAt: f.t0.Add(time.Minute),
		ExpiresAt: expires.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("reintento por turno: %v", err)
	}
	if again.ID != d.ID || again.ChargeID != d.ChargeID {
		t.Fatalf("el reintento devuelve la original: %+v vs %+v", again, d)
	}
	// Mismo client_op_id con OTRO turno también devuelve la original.
	b2 := f.booking(t, depSalon, domain.BookingBooked, starts.Add(2*time.Hour))
	if _, err := f.pool.Exec(f.ctx, `UPDATE booking_deposits SET client_op_id='op-it-1' WHERE id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	other, err := f.deposits.Create(domain.BookingDeposit{
		ID: domain.NewID(), SalonID: depSalon, BookingID: b2, AmountCents: 4500, Percent: 30,
		Status: domain.DepositPending, ChargeID: mustChargeID(t), CreatedAt: f.t0.Add(2 * time.Minute),
		ExpiresAt: expires.Add(2 * time.Minute), ClientOpID: "op-it-1",
	})
	if err != nil {
		t.Fatalf("reintento por op: %v", err)
	}
	if other.ID != d.ID {
		t.Fatalf("el client_op_id repetido devuelve la misma seña: %+v", other)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM booking_deposits WHERE salon_id=$1`, depSalon).Scan(&n); err != nil || n != 1 {
		t.Fatalf("señas: %d (%v)", n, err)
	}
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 1 {
		t.Fatalf("el reintento no escribe eventos: %v", kinds)
	}
	// Dos altas simultáneas: una gana y la otra recibe ErrAlreadyExists.
	b3 := f.booking(t, depSalon, domain.BookingBooked, starts.Add(4*time.Hour))
	first, err := f.deposits.Create(domain.BookingDeposit{
		ID: domain.NewID(), SalonID: depSalon, BookingID: b3, AmountCents: 100, Percent: 30,
		Status: domain.DepositPending, ChargeID: mustChargeID(t), CreatedAt: f.t0, ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("alta: %v", err)
	}
	_, err = f.deposits.Create(domain.BookingDeposit{
		ID: domain.NewID(), SalonID: depSalon, BookingID: b3, AmountCents: 100, Percent: 30,
		Status: domain.DepositPending, ChargeID: mustChargeID(t), CreatedAt: f.t0, ExpiresAt: expires,
	})
	// La lectura previa de la idempotencia devuelve la original sin error; si la
	// carrera llegara al INSERT, el índice único da ErrAlreadyExists. Lo
	// inaceptable es un duplicado o un error de otro tipo.
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) && !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("segunda alta: %v", err)
	}
	kept, err := f.deposits.FindByBooking(depSalon, b3)
	if err != nil || kept.ID != first.ID {
		t.Fatalf("la primera sigue: %+v (%v)", kept, err)
	}
}

// containsKind dice si el libro mayor tiene un tipo de evento (el orden entre
// eventos del mismo instante lo decide el id: no se asume).
func containsKind(kinds []string, want string) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

func mustChargeID(t *testing.T) string {
	t.Helper()
	id, err := domain.NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// D7: un evento aprobado se aplica UNA vez; un segundo evento del mismo cargo no
// vuelve a transicionar.
func TestDepositsIT_approvedOnce(t *testing.T) {
	f := depFixtures(t)
	starts := f.t0.Add(48 * time.Hour)
	b := f.booking(t, depSalon, domain.BookingBooked, starts)
	now := time.Now().UTC()
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(30*time.Minute))

	got, err := f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-it-1", 4500, now))
	if err != nil || !got.Applied || got.Deposit.Status != domain.DepositPaid || got.Deposit.PaidAt == nil {
		t.Fatalf("aprobar: %+v (%v)", got, err)
	}
	again, err := f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-it-1", 4500, now))
	if err != nil || again.Applied {
		t.Fatalf("reenvío: %+v (%v)", again, err)
	}
	other, err := f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-it-2", 4500, now))
	if err != nil || other.Applied {
		t.Fatalf("segundo evento: %+v (%v)", other, err)
	}
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 2 || kinds[1] != domain.DepositEventPaid {
		t.Fatalf("libro mayor: %v", kinds)
	}
	// El índice único del evento del banco es la guarda FINAL: insertar a mano el
	// mismo bank_event_id falla.
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO deposit_events(id,deposit_id,kind,amount_cents,bank_event_id) VALUES($1,$2,'paid',4500,'ev-it-1')`,
		domain.NewID(), d.ID); err == nil {
		t.Fatal("el índice único de bank_event_id tiene que rechazar el duplicado")
	}
}

// D7: un intento rechazado deja la seña pendiente y se registra una sola vez.
func TestDepositsIT_declinedStaysPending(t *testing.T) {
	f := depFixtures(t)
	b := f.booking(t, depSalon, domain.BookingBooked, f.t0.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, f.t0.Add(30*time.Minute))
	cb := domain.DepositCallback{EventID: "ev-dec-1", ChargeID: d.ChargeID, Kind: domain.CallbackDeclined,
		AmountCents: 4500, Now: time.Now().UTC()}
	if ok, err := f.deposits.ApplyDeclined(cb); err != nil || !ok {
		t.Fatalf("rechazo: %v (%v)", ok, err)
	}
	if ok, err := f.deposits.ApplyDeclined(cb); err != nil || ok {
		t.Fatalf("rechazo repetido: %v (%v)", ok, err)
	}
	got, err := f.deposits.FindByCharge(d.ChargeID)
	if err != nil || got.Status != domain.DepositPending {
		t.Fatalf("la seña sigue pendiente: %+v (%v)", got, err)
	}
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 2 || kinds[1] != domain.DepositEventAttemptDeclined {
		t.Fatalf("libro mayor: %v", kinds)
	}
}

// D7: contra una seña vencida el aprobado es PAGO TARDÍO: no escribe nada, quien
// llama reembolsa, y SettleLateRefund cierra UNA vez.
func TestDepositsIT_latePaymentRefundOnce(t *testing.T) {
	f := depFixtures(t)
	b := f.booking(t, depSalon, domain.BookingBooked, f.t0.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, f.t0.Add(time.Minute))
	// Vencida por el barrido.
	if _, err := f.pool.Exec(f.ctx, `UPDATE booking_deposits SET status='expired' WHERE id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	got, err := f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-late-1", 4500, now))
	if err != nil || !got.LatePayment || got.Applied {
		t.Fatalf("pago tardío: %+v (%v)", got, err)
	}
	// Sin escribir: la sena sigue expired y no hay evento del banco.
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 1 {
		t.Fatalf("el pago tardío no escribe: %v", kinds)
	}
	settled, err := f.deposits.SettleLateRefund(domain.DepositSettlement{
		DepositID: d.ID, Status: domain.DepositRefunded, BankEventID: "ev-late-1",
		Detail: "pago tardio", Now: now,
	})
	if err != nil || !settled {
		t.Fatalf("cerrar: %v (%v)", settled, err)
	}
	again, err := f.deposits.SettleLateRefund(domain.DepositSettlement{
		DepositID: d.ID, Status: domain.DepositRefunded, BankEventID: "ev-late-1",
		Detail: "pago tardio", Now: now,
	})
	if err != nil || again {
		t.Fatalf("cerrar dos veces: %v (%v)", again, err)
	}
	got2, err := f.deposits.FindByCharge(d.ChargeID)
	if err != nil || got2.Status != domain.DepositRefunded || got2.SettledAt == nil {
		t.Fatalf("estado final: %+v (%v)", got2, err)
	}
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 2 || kinds[1] != domain.DepositEventLatePaymentRefunded {
		t.Fatalf("libro mayor: %v", kinds)
	}
}

// D9(a): ExpirePending vence, deja su evento y vuelve a devolver las ya vencidas
// cuyo turno sigue retenido (un tick incompleto no deja el cupo tomado).
func TestDepositsIT_expirePending(t *testing.T) {
	f := depFixtures(t)
	b := f.booking(t, depSalon, domain.BookingBooked, f.t0.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, f.t0.Add(time.Minute))
	now := time.Now().UTC()
	// Una sena futura no se toca.
	future := f.newDeposit(t, depSalon, f.booking(t, depSalon, domain.BookingBooked, f.t0.Add(72*time.Hour)),
		4500, 30, f.t0, now.Add(time.Hour))

	expired, err := f.deposits.ExpirePending(now, 10)
	if err != nil {
		t.Fatalf("vencer: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != d.ID || expired[0].Status != domain.DepositExpired {
		t.Fatalf("vencidas: %+v", expired)
	}
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 2 || kinds[1] != domain.DepositEventExpired {
		t.Fatalf("libro mayor: %v", kinds)
	}
	// Segundo tick: la ya vencida con turno booked vuelve (para reintentar la
	// cancelación) y la futura sigue intacta.
	again, err := f.deposits.ExpirePending(now, 10)
	if err != nil {
		t.Fatalf("segundo tick: %v", err)
	}
	if len(again) != 1 || again[0].ID != d.ID {
		t.Fatalf("reintento de cancelación: %+v", again)
	}
	_ = future
	// Con el turno cerrado ya no vuelve nunca.
	f.setBookingStatus(t, b, domain.BookingCancelled)
	third, err := f.deposits.ExpirePending(now, 10)
	if err != nil || len(third) != 0 {
		t.Fatalf("con el turno cerrado no vuelve: %+v (%v)", third, err)
	}
	// Doble vencimiento simultáneo: el UPDATE condicional deja UNA sola transición.
	if kinds := f.eventKinds(t, d.ID); len(kinds) != 2 {
		t.Fatalf("un solo evento de vencimiento: %v", kinds)
	}
}

// D9(b): solo las `paid` con el turno `cancelled` entran al cierre; `done` y
// `no_show` no se reembolsan nunca.
func TestDepositsIT_paidCancelledOnly(t *testing.T) {
	f := depFixtures(t)
	now := time.Now().UTC()
	mk := func(status domain.BookingStatus) domain.BookingDeposit {
		b := f.booking(t, depSalon, domain.BookingBooked, f.t0.Add(48*time.Hour))
		d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(time.Hour))
		if _, err := f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-"+d.ID, 4500, now)); err != nil {
			t.Fatalf("aprobar: %v", err)
		}
		// El turno se cierra DESPUÉS de pagar: es el escenario de D9(b).
		f.setBookingStatus(t, b, status)
		return d
	}
	cancelled := mk(domain.BookingCancelled)
	done := mk(domain.BookingDone)
	noShow := mk(domain.BookingNoShow)

	rows, err := f.deposits.ListPaidCancelled(10)
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != cancelled.ID {
		t.Fatalf("solo la cancelada: %+v", rows)
	}
	// Cerrar como perdida y verificar el evento.
	ok, err := f.deposits.SettlePaidCancelled(domain.DepositSettlement{
		DepositID: cancelled.ID, Status: domain.DepositForfeited, Detail: "fuera de la ventana", Now: now,
	})
	if err != nil || !ok {
		t.Fatalf("cerrar: %v (%v)", ok, err)
	}
	got, err := f.deposits.FindByCharge(cancelled.ChargeID)
	if err != nil || got.Status != domain.DepositForfeited {
		t.Fatalf("estado: %+v (%v)", got, err)
	}
	if kinds := f.eventKinds(t, cancelled.ID); len(kinds) != 3 || !containsKind(kinds, domain.DepositEventForfeited) {
		t.Fatalf("libro mayor: %v", kinds)
	}
	// done y no_show quedaron `paid` (nunca se tocan).
	for _, d := range []domain.BookingDeposit{done, noShow} {
		got, _ := f.deposits.FindByCharge(d.ChargeID)
		if got.Status != domain.DepositPaid {
			t.Fatalf("done/no_show no se reembolsan: %+v", got)
		}
	}
}

// D10: la lista va de la más nueva a la más vieja, filtra por estado, usa el
// cursor opaco y no cruza salones.
func TestDepositsIT_listCursorAndIsolation(t *testing.T) {
	f := depFixtures(t)
	now := f.t0
	var ids []string
	for i := 0; i < 3; i++ {
		b := f.booking(t, depSalon, domain.BookingBooked, f.t0.Add(time.Duration(48+i)*time.Hour))
		d := f.newDeposit(t, depSalon, b, 100+i, 30, now.Add(time.Duration(i)*time.Minute), now.Add(time.Hour))
		ids = append(ids, d.ID)
	}
	otherB := f.booking(t, depSalon2, domain.BookingBooked, f.t0.Add(60*time.Hour))
	f.newDeposit(t, depSalon2, otherB, 500, 30, now, now.Add(time.Hour))

	page, err := f.deposits.List(depSalon, domain.DepositQuery{Limit: 2})
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(page) != 2 || page[0].ID != ids[2] || page[1].ID != ids[1] {
		t.Fatalf("orden: %+v", page)
	}
	last := page[len(page)-1]
	next := domain.EncodeDepositCursor(domain.DepositCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	if _, err := domain.DecodeDepositCursor(next); err != nil {
		t.Fatalf("cursor: %v", err)
	}
	page2, err := f.deposits.List(depSalon, domain.DepositQuery{Limit: 2, Before: &domain.DepositCursor{CreatedAt: last.CreatedAt, ID: last.ID}})
	if err != nil {
		t.Fatalf("segunda página: %v", err)
	}
	if len(page2) != 1 || page2[0].ID != ids[0] {
		t.Fatalf("segunda página: %+v", page2)
	}
	// Filtro por estado: las pendientes del otro salón no aparecen.
	if _, err := f.pool.Exec(f.ctx, `UPDATE booking_deposits SET status='paid', paid_at=now() WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	paid, err := f.deposits.List(depSalon, domain.DepositQuery{Limit: 10, Status: domain.DepositPaid})
	if err != nil || len(paid) != 1 || paid[0].ID != ids[0] {
		t.Fatalf("filtro: %+v (%v)", paid, err)
	}
	all, err := f.deposits.List(depSalon, domain.DepositQuery{Limit: 10})
	if err != nil || len(all) != 3 {
		t.Fatalf("aislamiento: %+v (%v)", all, err)
	}
}

// --- Barrido completo (D9) con los adaptadores reales ------------------------

// itGateway es el banco del barrido: modela la IDEMPOTENCIA POR CARGO del puerto
// Refund (reembolsar dos veces devuelve nil sin duplicar), así el contador es de
// reembolsos REALES y no de llamadas.
type itGateway struct {
	secret   string
	mu       sync.Mutex
	charges  int
	refunds  map[string]int
	refunded map[string]bool
}

func newITGateway() *itGateway {
	return &itGateway{secret: "it-simbank-secret", refunds: map[string]int{}, refunded: map[string]bool{}}
}

func (g *itGateway) Enabled() bool { return true }

func (g *itGateway) CreateCharge(ctx context.Context, r ports.ChargeRequest) (ports.Charge, error) {
	id, err := domain.NewDepositChargeID()
	if err != nil {
		return ports.Charge{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.charges++
	return ports.Charge{ID: id, PayPath: "/pagar#" + id}, nil
}

func (g *itGateway) Refund(ctx context.Context, chargeID string, amount int, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.refunded[chargeID] {
		g.refunded[chargeID] = true
		g.refunds[chargeID]++
	}
	return nil
}

func (g *itGateway) ParseCallback(header http.Header, body []byte, now time.Time) (ports.CallbackEvent, error) {
	if err := domain.VerifyPaymentSignature(g.secret, header.Get("X-Simbank-Signature"), body, now); err != nil {
		return ports.CallbackEvent{}, err
	}
	return decodeCallback(body)
}

func (g *itGateway) refundCount(chargeID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refunds[chargeID]
}

func (g *itGateway) chargeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.charges
}

// itMailer cuenta los correos de confirmación (el envío es asíncrono).
type itMailer struct {
	mu   sync.Mutex
	msgs int
}

func (m *itMailer) Send(context.Context, ports.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs++
	return nil
}

func (m *itMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.msgs
}

// sweepEnv cablea el caso de uso de la seña con los adaptadores REALES.
func (f *depFix) sweepEnv(t *testing.T, gateway *itGateway, mailer *itMailer) app.DepositService {
	t.Helper()
	return app.DepositService{
		Policies: f.policies, Deposits: f.deposits, Gateway: gateway,
		Bookings: BookingCanceller{Bookings: Bookings{Pool: f.pool}},
		Notices:  f.deposits, Mailer: mailer,
	}
}

// decodeCallback parsea el cuerpo firmado del callback (la misma forma que
// manda el banco simulado).
func decodeCallback(body []byte) (ports.CallbackEvent, error) {
	var p struct {
		EventID     string `json:"event_id"`
		ChargeID    string `json:"charge_id"`
		Reference   string `json:"reference"`
		Kind        string `json:"kind"`
		AmountCents int    `json:"amount_cents"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
	}
	return ports.CallbackEvent{EventID: p.EventID, ChargeID: p.ChargeID, Reference: p.Reference,
		Kind: p.Kind, AmountCents: p.AmountCents}, nil
}

// signedCallback arma la cabecera y el cuerpo de un evento del banco.
func signedCallback(t *testing.T, secret, chargeID, eventID, kind string, amount int) (http.Header, []byte) {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"event_id":%q,"charge_id":%q,"reference":"ref","kind":%q,"amount_cents":%d}`,
		eventID, chargeID, kind, amount))
	h := http.Header{}
	h.Set("X-Simbank-Signature", domain.SignPayment(secret, time.Now().UTC(), body))
	return h, body
}

// D9: el barrido vence y cancela por el camino existente, reembolsa dentro de la
// ventana, pierde la seña fuera de ella y NUNCA toca `done` ni `no_show`.
func TestDepositsSweepIT_expiryAndRefundWindow(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	mailer := &itMailer{}
	svc := f.sweepEnv(t, gateway, mailer)
	if _, err := f.policies.Save(domain.DepositPolicy{SalonID: depSalon, Saved: true, Enabled: true,
		Percent: 30, HoldMinutes: 30, RefundHours: 24, UpdatedAt: f.t0}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// (a) pendiente vencida con el turno reservado: el barrido la vence y cancela.
	bExpired := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	dExpired := f.newDeposit(t, depSalon, bExpired, 4500, 30, f.t0, now.Add(-time.Minute))

	paid := func(startsIn time.Duration, status domain.BookingStatus) domain.BookingDeposit {
		b := f.booking(t, depSalon, domain.BookingBooked, now.Add(startsIn))
		d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(time.Hour))
		if _, err := f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-sweep-"+d.ID, 4500, now)); err != nil {
			t.Fatalf("aprobar: %v", err)
		}
		if status != domain.BookingBooked {
			f.setBookingStatus(t, b, status)
		}
		return d
	}
	// (b) pagada, turno cancelado y faltan 48 h: se devuelve.
	dRefund := paid(48*time.Hour, domain.BookingCancelled)
	// (c) pagada, turno cancelado y falta 1 h: se pierde.
	dForfeit := paid(time.Hour, domain.BookingCancelled)
	// (d)/(e) pagadas con el turno atendido o no-show: no se tocan.
	dDone := paid(48*time.Hour, domain.BookingDone)
	dNoShow := paid(48*time.Hour, domain.BookingNoShow)

	res, err := svc.Sweep(50)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Expired != 1 || res.Cancelled != 1 || res.Refunded != 1 || res.Forfeited != 1 {
		t.Fatalf("resultado: %+v", res)
	}
	// (a) la reserva se canceló por el camino existente (SetStatus en bookings).
	var bookingStatus string
	if err := f.pool.QueryRow(f.ctx, `SELECT status FROM bookings WHERE id=$1`, bExpired).Scan(&bookingStatus); err != nil {
		t.Fatal(err)
	}
	if bookingStatus != string(domain.BookingCancelled) {
		t.Fatalf("turno tras vencer: %q", bookingStatus)
	}
	// Un segundo tick no repite ningún cierre.
	if res2, err := svc.Sweep(50); err != nil || res2.Expired != 0 || res2.Cancelled != 0 || res2.Refunded != 0 || res2.Forfeited != 0 {
		t.Fatalf("segundo tick: %+v (%v)", res2, err)
	}
	// Reembolsos: SOLO la que estaba dentro de la ventana.
	if n := gateway.refundCount(dRefund.ChargeID); n != 1 {
		t.Fatalf("reembolso: %d", n)
	}
	if n := gateway.refundCount(dForfeit.ChargeID); n != 0 {
		t.Fatalf("la seña perdida no se reembolsa: %d", n)
	}
	for _, d := range []domain.BookingDeposit{dDone, dNoShow} {
		if n := gateway.refundCount(d.ChargeID); n != 0 {
			t.Fatalf("done/no_show nunca reembolsan: %d", n)
		}
		got, _ := f.deposits.FindByCharge(d.ChargeID)
		if got.Status != domain.DepositPaid {
			t.Fatalf("done/no_show quedan pagadas: %+v", got)
		}
	}
	for want, d := range map[string]domain.BookingDeposit{
		domain.DepositExpired:   dExpired,
		domain.DepositRefunded:  dRefund,
		domain.DepositForfeited: dForfeit,
	} {
		got, err := f.deposits.FindByCharge(d.ChargeID)
		if err != nil || got.Status != want {
			t.Fatalf("estado de %s: %+v (%v)", want, got, err)
		}
	}
}

// D9: el borde de la ventana de devolución — a las 24 h exactas todavía se
// devuelve; un segundo menos y se pierde.
func TestDepositsSweepIT_refundWindowBoundary(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	svc := f.sweepEnv(t, gateway, &itMailer{})
	if _, err := f.policies.Save(domain.DepositPolicy{SalonID: depSalon, Saved: true, Enabled: true,
		Percent: 30, HoldMinutes: 30, RefundHours: 24, UpdatedAt: f.t0}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mk := func(startsAt time.Time) domain.BookingDeposit {
		b := f.booking(t, depSalon, domain.BookingBooked, startsAt)
		d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(time.Hour))
		if _, err := f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-bound-"+d.ID, 4500, now)); err != nil {
			t.Fatalf("aprobar: %v", err)
		}
		f.setBookingStatus(t, b, domain.BookingCancelled)
		return d
	}
	// Unos segundos de margen para que el reloj del barrido no cruce el borde.
	inside := mk(now.Add(24*time.Hour + 3*time.Second))
	outside := mk(now.Add(24*time.Hour - 3*time.Second))

	if _, err := svc.Sweep(50); err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if n := gateway.refundCount(inside.ChargeID); n != 1 {
		t.Fatalf("dentro de la ventana se devuelve: %d", n)
	}
	if n := gateway.refundCount(outside.ChargeID); n != 0 {
		t.Fatalf("fuera de la ventana se pierde: %d", n)
	}
}

// D9/D7: la carrera barrido↔callback, en los DOS órdenes. Gana quien haga
// primero el UPDATE condicional; el callback perdedor cae en el pago tardío.
func TestDepositsSweepIT_sweeperCallbackRaceBothOrders(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	mailer := &itMailer{}
	svc := f.sweepEnv(t, gateway, mailer)
	if _, err := f.policies.Save(domain.DepositPolicy{SalonID: depSalon, Saved: true, Enabled: true,
		Percent: 30, HoldMinutes: 30, RefundHours: 24, UpdatedAt: f.t0}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	// Orden A: primero el callback (paga), después el barrido.
	bA := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	dA := f.newDeposit(t, depSalon, bA, 4500, 30, f.t0, now.Add(30*time.Minute))
	hA, bodyA := signedCallback(t, gateway.secret, dA.ChargeID, "ev-race-a", domain.CallbackApproved, 4500)
	if err := svc.ApplyCallback(hA, bodyA); err != nil {
		t.Fatalf("callback A: %v", err)
	}
	if got, _ := f.deposits.FindByCharge(dA.ChargeID); got.Status != domain.DepositPaid {
		t.Fatalf("A pagada: %+v", got)
	}
	// El turno se cancela después: el barrido devuelve la seña una vez.
	f.setBookingStatus(t, bA, domain.BookingCancelled)
	if _, err := svc.Sweep(50); err != nil {
		t.Fatalf("barrido A: %v", err)
	}
	if n := gateway.refundCount(dA.ChargeID); n != 1 {
		t.Fatalf("un solo reembolso en A: %d", n)
	}
	if got, _ := f.deposits.FindByCharge(dA.ChargeID); got.Status != domain.DepositRefunded {
		t.Fatalf("A devuelta: %+v", got)
	}

	// Orden B: primero el barrido (vence y cancela), después el callback.
	bB := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	dB := f.newDeposit(t, depSalon, bB, 4500, 30, f.t0, now.Add(-time.Minute))
	if _, err := svc.Sweep(50); err != nil {
		t.Fatalf("barrido B: %v", err)
	}
	if got, _ := f.deposits.FindByCharge(dB.ChargeID); got.Status != domain.DepositExpired {
		t.Fatalf("B vencida: %+v", got)
	}
	hB, bodyB := signedCallback(t, gateway.secret, dB.ChargeID, "ev-race-b", domain.CallbackApproved, 4500)
	if err := svc.ApplyCallback(hB, bodyB); err != nil {
		t.Fatalf("callback B: %v", err)
	}
	if n := gateway.refundCount(dB.ChargeID); n != 1 {
		t.Fatalf("un solo reembolso en B: %d", n)
	}
	gotB, _ := f.deposits.FindByCharge(dB.ChargeID)
	if gotB.Status != domain.DepositRefunded {
		t.Fatalf("B devuelta: %+v", gotB)
	}
	// El turno B no revive: sigue cancelado.
	var st string
	if err := f.pool.QueryRow(f.ctx, `SELECT status FROM bookings WHERE id=$1`, bB).Scan(&st); err != nil || st != string(domain.BookingCancelled) {
		t.Fatalf("turno B: %q (%v)", st, err)
	}
	// Las dos terminan devueltas: en A hubo un `paid` y después el `refunded` del
	// barrido; en B el pago tardío se reembolsó sin `paid`. Ninguna queda pagada.
	for _, d := range []domain.BookingDeposit{dA, dB} {
		if got, _ := f.deposits.FindByCharge(d.ChargeID); got.Status != domain.DepositRefunded {
			t.Fatalf("las dos devueltas: %+v", got)
		}
	}
}

// Guarda de FORMA de las transiciones (D7/D9): cada cambio de estado es UN UPDATE
// condicional por el estado de ORIGEN, no un SET ciego. La condición `pending`
// del UPDATE de pago NO se vigila acá: se prueba con comportamiento en
// TestDepositsIT_paidUpdateIsConditional (un escritor concurrente que no toma el
// advisory lock). Las de `settle` son la red FINAL debajo del lock (si mañana un
// camino nuevo escribe sin tomarlo) y se vigilan por su texto, igual que la lista
// cerrada de la auditoría vigila routes.go. Quitar `status=$3`/`origin` las rompe.
func TestDepositsIT_transitionsAreConditional(t *testing.T) {
	src, err := os.ReadFile("deposits.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, want := range []string{
		// pending -> expired
		"SET status=$2 WHERE id=$1 AND status=$3",
		// paid -> refunded|forfeited y pending|expired -> refunded (settle)
		"SET status=$2, settled_at=$3 WHERE id=$1 AND `+origin",
		`return r.settle(s, domain.DepositEventLatePaymentRefunded, ` + "`status IN ('pending','expired')`",
		`return r.settle(s, event, ` + "`status = 'paid'`" + `)`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("falta la condición de origen de una transición: %q", want)
		}
	}
}

// awaitLockedBookingDeposit espera a que OTRA conexión quede esperando un lock
// sobre booking_deposits (el UPDATE del pago bloqueado por el row lock de la
// prueba). Es lo que vuelve determinista el intercalado.
func awaitLockedBookingDeposit(t *testing.T, f *depFix) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock'
			    AND query LIKE '%booking_deposits%'`).Scan(&n); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("el UPDATE del pago no quedó esperando el row lock: no se pudo armar el intercalado")
}

// D7/D9: la condición `status='pending'` del UPDATE del pago es una guarda REAL y
// no decoración. Se prueba con un escritor concurrente que NO toma el advisory
// lock de la seña: el estado cambia entre la lectura y el UPDATE (lo que haría
// cualquier camino nuevo o un cambio de estado fuera del repositorio). Con la
// condición el pago no toca nada y el llamador cae en el pago tardío; sin la
// condición pisaría una seña ya cerrada y el libro mayor quedaría con un `paid`
// falso.
func TestDepositsIT_paidUpdateIsConditional(t *testing.T) {
	f := depFixtures(t)
	now := time.Now().UTC()
	b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(30*time.Minute))

	// El «otro camino»: vence la seña y RETIENE el row lock hasta que el UPDATE
	// del pago esté esperando. No toma el advisory lock a propósito.
	other, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatalf("transacción del otro camino: %v", err)
	}
	defer func() { _ = other.Rollback(f.ctx) }()
	var locked string
	if err := other.QueryRow(f.ctx, `SELECT id FROM booking_deposits WHERE id=$1 FOR UPDATE`, d.ID).Scan(&locked); err != nil {
		t.Fatalf("row lock: %v", err)
	}
	if _, err := other.Exec(f.ctx, `UPDATE booking_deposits SET status='expired' WHERE id=$1`, d.ID); err != nil {
		t.Fatalf("vencer desde el otro camino: %v", err)
	}

	done := make(chan struct{})
	var (
		applied domain.DepositApply
		applyEr error
	)
	go func() {
		defer close(done)
		applied, applyEr = f.deposits.ApplyApproved(approvedEvent(d.ChargeID, "ev-condicional-1", 4500, now))
	}()

	awaitLockedBookingDeposit(t, f)
	if err := other.Commit(f.ctx); err != nil {
		t.Fatalf("commit del otro camino: %v", err)
	}
	<-done

	if applyEr != nil {
		t.Fatalf("aplicar el pago: %v", applyEr)
	}
	if applied.Applied {
		t.Fatalf("el pago pisó una seña ya cerrada: %+v", applied.Deposit)
	}
	if !applied.LatePayment {
		t.Fatalf("el pago tenía que caer en el pago tardío: %+v", applied)
	}
	got, err := f.deposits.FindByCharge(d.ChargeID)
	if err != nil {
		t.Fatalf("releer la seña: %v", err)
	}
	if got.Status != domain.DepositExpired || got.PaidAt != nil {
		t.Fatalf("la seña cerrada no puede quedar pagada: %+v", got)
	}
	for _, kind := range f.eventKinds(t, d.ID) {
		if kind == domain.DepositEventPaid {
			t.Fatal("el UPDATE condicional dejó un evento `paid` en una seña vencida")
		}
	}
}

// D7: el índice único parcial de deposit_events.bank_event_id es la guarda FINAL
// de «un evento del banco se aplica UNA sola vez». El advisory lock por seña y la
// lectura previa ya serializan el camino normal (los 30 callbacks simultáneos del
// test de concurrencia no necesitan el índice), así que la guarda se prueba EN LA
// BASE: el mismo bank_event_id no puede entrar dos veces, y un evento NUESTRO
// (bank_event_id NULL) sí puede repetirse porque el índice es parcial.
func TestDepositsIT_bankEventIndexIsTheGuard(t *testing.T) {
	f := depFixtures(t)
	now := time.Now().UTC()
	b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(30*time.Minute))
	insert := func(kind, bankEventID string) error {
		_, err := f.pool.Exec(f.ctx,
			`INSERT INTO deposit_events(id,deposit_id,kind,amount_cents,bank_event_id,created_at)
			 VALUES($1,$2,$3,4500,NULLIF($4,''),$5)`,
			domain.NewID(), d.ID, kind, bankEventID, now)
		return err
	}
	if err := insert(domain.DepositEventPaid, "ev-guarda-unica"); err != nil {
		t.Fatalf("primer evento del banco: %v", err)
	}
	if err := insert(domain.DepositEventPaid, "ev-guarda-unica"); err == nil {
		t.Fatal("el MISMO bank_event_id entró dos veces: falta el índice único (un cobro se aplicaría dos veces)")
	}
	// El índice es PARCIAL: los eventos propios (sin bank_event_id) se repiten.
	for i := 0; i < 2; i++ {
		if err := insert(domain.DepositEventExpired, ""); err != nil {
			t.Fatalf("evento propio %d: %v", i, err)
		}
	}
}
