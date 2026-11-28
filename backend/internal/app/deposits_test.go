package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"salon-saas/internal/testutil"
	"strings"
	"sync"
	"testing"
	"time"

	dep "salon-saas/internal/deposits"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// depSecret es el secreto compartido del banco simulado en los tests.
const depSecret = "s3creto-de-desarrollo"

// depClock es el reloj fijo de los tests de F11.
var depClock = time.Date(2026, 4, 20, 15, 0, 0, 0, time.UTC)

// depEnv arma un salón s1 con todos los roles, el banco simulado, la tienda de
// señas en memoria y un reloj fijo.
type depEnv struct {
	svc      *dep.DepositService
	policy   *dep.DepositPolicyService
	mems     *testutil.FakeMemberships
	policies *fakeDepositPolicies
	deposits *fakeDeposits
	gateway  *fakeDepositGateway
	bookings *fakeDepositBookings
	notices  *fakeDepositNotices
	mailer   *testutil.FakeMailer

	owner, admin, recep, prof, client, removed, other domain.Membership
}

func newDepEnv(t *testing.T) *depEnv {
	t.Helper()
	e := &depEnv{
		mems:     &testutil.FakeMemberships{},
		policies: &fakeDepositPolicies{},
		bookings: newFakeDepositBookings(),
		notices:  &fakeDepositNotices{},
		mailer:   &testutil.FakeMailer{},
	}
	e.deposits = &fakeDeposits{bookings: e.bookings}
	e.gateway = newFakeDepositGateway(depSecret)
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{
		"s1": {SalonID: "s1", Timezone: "America/Argentina/Buenos_Aires", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30},
	}}
	// El correo se manda en línea: el test no espera goroutines.
	run := func(job func(ctx context.Context)) { job(context.Background()) }
	e.svc = &dep.DepositService{
		Policies: e.policies, Deposits: e.deposits, Gateway: e.gateway,
		Bookings: e.bookings, Notices: e.notices, Mailer: e.mailer,
		Settings: settings, Memberships: e.mems,
		AppURL: "http://localhost:8081",
		Run:    run,
		Now:    func() time.Time { return depClock },
	}
	e.policy = &dep.DepositPolicyService{
		Policies: e.policies, Memberships: e.mems, Gateway: e.gateway,
		Now: func() time.Time { return depClock },
	}
	e.owner = testutil.Miembro(e.mems, "owner", "s1", domain.RoleOwner)
	e.admin = testutil.Miembro(e.mems, "admin", "s1", domain.RoleAdmin)
	e.recep = testutil.Miembro(e.mems, "recep", "s1", domain.RoleReception)
	_ = e.mems.Save(domain.Membership{UserID: "prof", SalonID: "s1", Role: domain.RoleProfessional, Attends: true})
	e.prof, _ = e.mems.Find("prof", "s1")
	e.client = testutil.Miembro(e.mems, "client", "s1", domain.RoleClient)
	e.other = testutil.Miembro(e.mems, "owner2", "s2", domain.RoleOwner)
	e.removed = testutil.Miembro(e.mems, "removed", "s1", domain.RoleAdmin)
	at := depClock.Add(-time.Hour)
	e.removed.EndedAt = &at
	e.mems.ByKey["removed|s1"] = e.removed
	return e
}

// seedPolicy guarda la política del salón directamente en el repositorio.
func (e *depEnv) seedPolicy(enabled bool, percent, hold, refund int) domain.DepositPolicy {
	p := domain.DepositPolicy{
		SalonID: "s1", Saved: true, Enabled: enabled,
		Percent: percent, HoldMinutes: hold, RefundHours: refund, UpdatedAt: depClock,
	}
	e.policies.seed(p)
	return p
}

// seedBooking deja un turno en la agenda (la reserva que la sena retiene).
func (e *depEnv) seedBooking(id string, status domain.BookingStatus, startsAt time.Time) domain.Booking {
	return e.bookings.add("s1", id, status, startsAt)
}

// seedNotice deja lo que el correo de confirmación necesita del turno.
func (e *depEnv) seedNotice(bookingID, email string, startsAt time.Time) {
	e.notices.seed(domain.BookingNotice{
		SalonID: "s1", BookingID: bookingID, SalonName: "Salón Uno",
		ClientName: "Ana Pérez", ClientEmail: email, StartsAt: startsAt,
	})
}

// deposit crea una sena con la política sembrada y un turno vivo.
func (e *depEnv) deposit(t *testing.T, bookingID string, priceCents int, startsAt time.Time) domain.BookingDeposit {
	t.Helper()
	if _, err := e.policies.Find("s1"); err != nil {
		e.seedPolicy(true, 30, 30, 24)
	}
	e.seedBooking(bookingID, domain.BookingBooked, startsAt)
	e.seedNotice(bookingID, "ana@mail.com", startsAt)
	d, err := e.svc.CreateForBooking(domain.DepositOrder{
		SalonID: "s1", BookingID: bookingID, PriceCents: priceCents, Description: "Seña Corte",
	})
	if err != nil {
		t.Fatalf("crear la sena: %v", err)
	}
	if d == nil {
		t.Fatal("no se creó la sena")
	}
	return *d
}

// order es la orden de D8 con la que la reserva pública pide su sena.
func depOrder(bookingID string, priceCents int) domain.DepositOrder {
	return domain.DepositOrder{
		SalonID: "s1", BookingID: bookingID, PriceCents: priceCents,
		ClientOpID: "op-" + bookingID, Description: "Seña Corte",
	}
}

// okErr exige que el error sea de ese tipo de dominio.
func requireKind(t *testing.T, err error, kind error) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("error = %v, quiero %v", err, kind)
	}
}

func TestDepositService_CreateForBooking_sin_politica_o_apagada_no_hay_sena(t *testing.T) {
	e := newDepEnv(t)
	e.seedBooking("b1", domain.BookingBooked, depClock.Add(48*time.Hour))

	// Nunca guardada: comportamiento de hoy, sin cargo.
	d, err := e.svc.CreateForBooking(depOrder("b1", 50000))
	if err != nil || d != nil {
		t.Fatalf("sin política: d=%v err=%v, quiero nil/nil", d, err)
	}
	if e.gateway.chargeCount() != 0 {
		t.Fatal("sin política no se puede crear un cargo")
	}

	// Apagada: lo mismo.
	e.seedPolicy(false, 30, 30, 24)
	d, err = e.svc.CreateForBooking(depOrder("b1", 50000))
	if err != nil || d != nil {
		t.Fatalf("política apagada: d=%v err=%v, quiero nil/nil", d, err)
	}
	if e.gateway.chargeCount() != 0 {
		t.Fatal("con la política apagada no se crea ningún cargo")
	}

	// Precio 0: no hay seña aunque la política esté activa.
	e.seedPolicy(true, 30, 30, 24)
	d, err = e.svc.CreateForBooking(depOrder("b1", 0))
	if err != nil || d != nil {
		t.Fatalf("precio 0: d=%v err=%v, quiero nil/nil", d, err)
	}
	if e.gateway.chargeCount() != 0 {
		t.Fatal("con precio 0 no se crea ningún cargo")
	}
}

func TestDepositService_CreateForBooking_monto_cargo_y_evento(t *testing.T) {
	e := newDepEnv(t)
	e.seedPolicy(true, 30, 45, 24)
	startsAt := depClock.Add(72 * time.Hour)
	e.seedBooking("b1", domain.BookingBooked, startsAt)

	d, err := e.svc.CreateForBooking(depOrder("b1", 50000))
	if err != nil || d == nil {
		t.Fatalf("crear la sena: d=%v err=%v", d, err)
	}
	// D4: 50.000 al 30% = 15.000 (sin floats).
	if d.AmountCents != 15000 || d.Percent != 30 {
		t.Fatalf("monto/porcentaje = %d/%d, quiero 15000/30", d.AmountCents, d.Percent)
	}
	if d.Status != domain.DepositPending || d.BookingID != "b1" || d.SalonID != "s1" {
		t.Fatalf("sena = %+v", d)
	}
	charge := e.gateway.lastCharge()
	if charge.Reference != "b1" || charge.AmountCents != 15000 || charge.Description != "Seña Corte" {
		t.Fatalf("cargo = %+v", charge)
	}
	if !charge.ExpiresAt.Equal(depClock.Add(45 * time.Minute)) {
		t.Fatalf("vencimiento del cargo = %s", charge.ExpiresAt)
	}
	if d.ExpiresAt != charge.ExpiresAt {
		t.Fatalf("la sena vence en %s y el cargo en %s", d.ExpiresAt, charge.ExpiresAt)
	}
	if n := e.deposits.countEvents(domain.DepositEventCreated); n != 1 {
		t.Fatalf("eventos created = %d, quiero 1", n)
	}
	events := e.deposits.eventsOf(d.ID)
	if len(events) != 1 || events[0].AmountCents != 15000 || events[0].Kind != domain.DepositEventCreated {
		t.Fatalf("libro mayor = %+v", events)
	}
}

func TestDepositService_CreateForBooking_idempotente_16_veces_un_cargo(t *testing.T) {
	e := newDepEnv(t)
	startsAt := depClock.Add(48 * time.Hour)
	first := e.deposit(t, "b1", 50000, startsAt)
	if e.gateway.chargeCount() != 1 {
		t.Fatalf("cargos = %d, quiero 1", e.gateway.chargeCount())
	}
	for i := 0; i < 16; i++ {
		d, err := e.svc.CreateForBooking(depOrder("b1", 50000))
		if err != nil || d == nil {
			t.Fatalf("reintento %d: d=%v err=%v", i, d, err)
		}
		if d.ID != first.ID || d.ChargeID != first.ChargeID {
			t.Fatalf("reintento %d devolvió otra sena: %+v", i, d)
		}
	}
	if e.gateway.chargeCount() != 1 {
		t.Fatalf("cargos = %d, quiero 1 (el reintento no crea otro cargo)", e.gateway.chargeCount())
	}
	if n := e.deposits.countEvents(domain.DepositEventCreated); n != 1 {
		t.Fatalf("eventos created = %d, quiero 1", n)
	}
}

func TestDepositService_CreateForBooking_errores(t *testing.T) {
	e := newDepEnv(t)
	e.seedPolicy(true, 30, 30, 24)
	e.seedBooking("b1", domain.BookingBooked, depClock.Add(48*time.Hour))

	// El banco falla al crear el cargo: error y ninguna fila.
	e.gateway.createErr = errors.New("banco caído")
	if _, err := e.svc.CreateForBooking(depOrder("b1", 50000)); err == nil {
		t.Fatal("un cargo fallido tiene que devolver error")
	}
	if _, err := e.deposits.FindByBooking("s1", "b1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no debería haber sena guardada: %v", err)
	}
	e.gateway.createErr = nil

	// Sin banco configurado y con la política activa: 409 del contrato.
	e.policy.Gateway = nil
	e.svc.Gateway = &ports_noopGateway{}
	_, err := e.svc.CreateForBooking(depOrder("b1", 50000))
	requireKind(t, err, domain.ErrAlreadyExists)
	if err == nil || err.Error() != domain.MsgDepositGatewayUnavailable {
		t.Fatalf("error = %v, quiero %q", err, domain.MsgDepositGatewayUnavailable)
	}

	// Un id sucio (NUL) es validación y no llega al banco.
	e.svc.Gateway = e.gateway
	if _, err := e.svc.CreateForBooking(domain.DepositOrder{SalonID: "s\x001", BookingID: "b1", PriceCents: 50000}); err == nil {
		t.Fatal("un salón con NUL tiene que ser error")
	}
}

// La carrera del alta: el INSERT choca con el UNIQUE del turno (otro pedido
// ganó) y el caso de uso devuelve la sena que sí entró, sin 500.
func TestDepositService_CreateForBooking_carrera_devuelve_la_original(t *testing.T) {
	e := newDepEnv(t)
	e.seedPolicy(true, 30, 30, 24)
	startsAt := depClock.Add(48 * time.Hour)
	winner := e.deposit(t, "b1", 50000, startsAt)
	// El caso de uso no ve la fila en la primera lectura y el INSERT choca.
	e.deposits.hideOnce = true
	e.deposits.raceOnce = true
	got, err := e.svc.CreateForBooking(depOrder("b1", 50000))
	if err != nil || got == nil {
		t.Fatalf("la carrera tiene que devolver la sena original: d=%v err=%v", got, err)
	}
	if got.ID != winner.ID {
		t.Fatalf("devolvió otra sena: %+v", got)
	}
}

// ports_noopGateway es el NoopGateway del puerto, con nombre local para que el
// test no dependa de nil.
type ports_noopGateway struct{}

func (ports_noopGateway) Enabled() bool { return false }
func (ports_noopGateway) CreateCharge(context.Context, ports.ChargeRequest) (ports.Charge, error) {
	return ports.Charge{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
}
func (ports_noopGateway) Refund(context.Context, string, int, string) error { return nil }
func (ports_noopGateway) ParseCallback(http.Header, []byte, time.Time) (ports.CallbackEvent, error) {
	return ports.CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
}

// --- Callback (D7) ------------------------------------------------------------

// callback arma la cabecera firmada y el cuerpo que manda el banco.
func (e *depEnv) callback(kind, eventID, chargeID string, amount int) (http.Header, []byte) {
	body, err := json.Marshal(callbackBody{EventID: eventID, ChargeID: chargeID, Reference: "b1", Kind: kind, AmountCents: amount})
	if err != nil {
		panic(err)
	}
	// Se firma con el reloj ACTUAL del caso de uso: la ventana es de +-300 s.
	h := http.Header{}
	h.Set("X-Simbank-Signature", domain.SignPayment(depSecret, e.svc.NowUTC(), body))
	return h, body
}

func TestDepositService_ApplyCallback_aprobado_paga_y_manda_un_correo(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))

	header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)
	if err := e.svc.ApplyCallback(header, body); err != nil {
		t.Fatalf("callback aprobado: %v", err)
	}
	got, err := e.deposits.FindByCharge(d.ChargeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DepositPaid || got.PaidAt == nil {
		t.Fatalf("sena = %+v, quiero paid con paid_at", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventPaid); n != 1 {
		t.Fatalf("eventos paid = %d, quiero 1", n)
	}
	events := e.deposits.eventsOf(d.ID)
	var paidEvent domain.DepositEvent
	for _, ev := range events {
		if ev.Kind == domain.DepositEventPaid {
			paidEvent = ev
		}
	}
	if paidEvent.BankEventID != "ev-1" {
		t.Fatalf("el evento paid no guardó el id del banco: %+v", paidEvent)
	}
	msgs := e.mailer.Messages()
	if len(msgs) != 1 {
		t.Fatalf("correos = %d, quiero 1", len(msgs))
	}
	if msgs[0].To != "ana@mail.com" || !strings.Contains(msgs[0].Subject, "Salón Uno") {
		t.Fatalf("correo = %+v", msgs[0])
	}

	// El reenvío del banco: 200 sin efecto y SIN segundo correo.
	if err := e.svc.ApplyCallback(header, body); err != nil {
		t.Fatalf("reenvío: %v", err)
	}
	if n := e.deposits.countEvents(domain.DepositEventPaid); n != 1 {
		t.Fatalf("eventos paid = %d, quiero 1", n)
	}
	if n := len(e.mailer.Messages()); n != 1 {
		t.Fatalf("correos = %d, quiero 1", n)
	}
}

func TestDepositService_ApplyCallback_30_simultaneos_un_pago_y_un_correo(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)

	var wg sync.WaitGroup
	errs := make(chan error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.svc.ApplyCallback(header, body)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("callback concurrente: %v", err)
		}
	}
	if n := e.deposits.countEvents(domain.DepositEventPaid); n != 1 {
		t.Fatalf("eventos paid = %d, quiero 1", n)
	}
	if n := len(e.mailer.Messages()); n != 1 {
		t.Fatalf("correos = %d, quiero 1", n)
	}
}

// El mismo cargo con dos eventos DISTINTOS: una sola transición y un solo correo.
func TestDepositService_ApplyCallback_dos_eventos_una_transicion(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	h1, b1 := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)
	h2, b2 := e.callback(domain.CallbackApproved, "ev-2", d.ChargeID, d.AmountCents)
	for _, cb := range []struct {
		h http.Header
		b []byte
	}{{h1, b1}, {h2, b2}} {
		if err := e.svc.ApplyCallback(cb.h, cb.b); err != nil {
			t.Fatalf("callback: %v", err)
		}
	}
	if n := e.deposits.countEvents(domain.DepositEventPaid); n != 1 {
		t.Fatalf("eventos paid = %d, quiero 1", n)
	}
	if n := len(e.mailer.Messages()); n != 1 {
		t.Fatalf("correos = %d, quiero 1", n)
	}
}

func TestDepositService_ApplyCallback_rechazos_no_mueven_la_sena(t *testing.T) {
	for _, kind := range []string{domain.CallbackDeclined, domain.CallbackInsufficient} {
		t.Run(kind, func(t *testing.T) {
			e := newDepEnv(t)
			d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
			header, body := e.callback(kind, "ev-"+kind, d.ChargeID, d.AmountCents)
			if err := e.svc.ApplyCallback(header, body); err != nil {
				t.Fatalf("callback %s: %v", kind, err)
			}
			got, err := e.deposits.FindByCharge(d.ChargeID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != domain.DepositPending || got.PaidAt != nil {
				t.Fatalf("sena = %+v, quiero pending", got)
			}
			if n := e.deposits.countEvents(domain.DepositEventAttemptDeclined); n != 1 {
				t.Fatalf("eventos attempt_declined = %d, quiero 1", n)
			}
			if n := len(e.mailer.Messages()); n != 0 {
				t.Fatalf("correos = %d, quiero 0", n)
			}
			// El reenvío del mismo intento no duplica el evento.
			if err := e.svc.ApplyCallback(header, body); err != nil {
				t.Fatalf("reenvío: %v", err)
			}
			if n := e.deposits.countEvents(domain.DepositEventAttemptDeclined); n != 1 {
				t.Fatalf("eventos attempt_declined = %d, quiero 1", n)
			}
		})
	}
}

func TestDepositService_ApplyCallback_monto_distinto_400_sin_efecto(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents+1)
	err := e.svc.ApplyCallback(header, body)
	if err == nil {
		t.Fatal("un monto distinto tiene que fallar")
	}
	requireKind(t, err, domain.ErrValidation)
	if err.Error() != domain.MsgDepositInvalidAmount {
		t.Fatalf("texto = %q, quiero %q", err.Error(), domain.MsgDepositInvalidAmount)
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositPending {
		t.Fatalf("el monto inválido no puede mover la sena: %+v", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventPaid); n != 0 {
		t.Fatalf("eventos paid = %d, quiero 0", n)
	}
	if n := len(e.mailer.Messages()); n != 0 {
		t.Fatalf("correos = %d, quiero 0", n)
	}
}

func TestDepositService_ApplyCallback_firma_invalida_400_sin_efecto(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)
	// Cuerpo alterado después de firmar: la firma ya no vale.
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] = '9'
	err := e.svc.ApplyCallback(header, tampered)
	if err == nil {
		t.Fatal("una firma inválida tiene que fallar")
	}
	requireKind(t, err, domain.ErrValidation)
	if err.Error() != domain.MsgPaymentSignatureInvalid {
		t.Fatalf("texto = %q, quiero %q", err.Error(), domain.MsgPaymentSignatureInvalid)
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositPending {
		t.Fatalf("una firma inválida no puede mover la sena: %+v", got)
	}
	if n := len(e.mailer.Messages()); n != 0 {
		t.Fatalf("correos = %d, quiero 0", n)
	}
}

func TestDepositService_ApplyCallback_cargo_desconocido_404(t *testing.T) {
	e := newDepEnv(t)
	chargeID, err := domain.NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	header, body := e.callback(domain.CallbackApproved, "ev-1", chargeID, 15000)
	err = e.svc.ApplyCallback(header, body)
	requireKind(t, err, domain.ErrNotFound)
	if err.Error() != domain.MsgDepositNotFound {
		t.Fatalf("texto = %q, quiero %q", err.Error(), domain.MsgDepositNotFound)
	}
}

// Pago tardío: la sena venció (y el barrido canceló la reserva): se devuelve el
// dinero UNA vez, la reserva NO revive y no hay correo de confirmación.
func TestDepositService_ApplyCallback_pago_tardio_reembolsa_una_vez(t *testing.T) {
	for _, via := range []string{"vencida", "cancelada"} {
		t.Run(via, func(t *testing.T) {
			e := newDepEnv(t)
			d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
			switch via {
			case "vencida":
				// El reloj avanza más allá de la espera y el barrido la vence.
				e.svc.Now = func() time.Time { return depClock.Add(time.Hour) }
				e.policy.Now = e.svc.Now
				if _, err := e.svc.Sweep(10); err != nil {
					t.Fatalf("barrido: %v", err)
				}
			case "cancelada":
				e.bookings.setStatus("b1", domain.BookingCancelled)
			}

			header, body := e.callback(domain.CallbackApproved, "ev-late", d.ChargeID, d.AmountCents)
			if err := e.svc.ApplyCallback(header, body); err != nil {
				t.Fatalf("pago tardío: %v", err)
			}
			got, err := e.deposits.FindByCharge(d.ChargeID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != domain.DepositRefunded || got.SettledAt == nil {
				t.Fatalf("sena = %+v, quiero refunded", got)
			}
			if n := e.gateway.refundCount(); n != 1 {
				t.Fatalf("reembolsos = %d, quiero 1", n)
			}
			if r := e.gateway.lastRefund(); r.ChargeID != d.ChargeID || r.AmountCents != d.AmountCents {
				t.Fatalf("reembolso = %+v", r)
			}
			if n := e.deposits.countEvents(domain.DepositEventLatePaymentRefunded); n != 1 {
				t.Fatalf("eventos late_payment_refunded = %d, quiero 1", n)
			}
			if n := e.deposits.countEvents(domain.DepositEventPaid); n != 0 {
				t.Fatalf("un pago tardío NO es un pago: eventos paid = %d", n)
			}
			if n := len(e.mailer.Messages()); n != 0 {
				t.Fatalf("correos = %d, quiero 0 (no se confirma un turno que se canceló)", n)
			}
			// La reserva sigue cancelada: el pago tardío no la reactiva.
			if st := e.bookings.status("b1"); st != domain.BookingCancelled {
				t.Fatalf("estado del turno = %s, quiero cancelled", st)
			}
			// El reenvío no reembolsa de nuevo.
			if err := e.svc.ApplyCallback(header, body); err != nil {
				t.Fatalf("reenvío: %v", err)
			}
			if n := e.gateway.attemptCount(); n != 1 {
				t.Fatalf("llamadas a Refund = %d, quiero 1", n)
			}
		})
	}
}

// Si el reembolso falla, la sena NO se marca devuelta: el reintento del banco
// vuelve a pedirlo. Ese es el orden «reembolsar y después cerrar».
func TestDepositService_ApplyCallback_reembolso_fallido_no_cierra_la_sena(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	e.bookings.setStatus("b1", domain.BookingCancelled)
	e.gateway.refundErr = errors.New("banco caído")
	header, body := e.callback(domain.CallbackApproved, "ev-late", d.ChargeID, d.AmountCents)
	if err := e.svc.ApplyCallback(header, body); err == nil {
		t.Fatal("un reembolso fallido tiene que devolver error (el banco reintenta)")
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositPending {
		t.Fatalf("la sena no puede quedar cerrada si el reembolso falló: %+v", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventLatePaymentRefunded); n != 0 {
		t.Fatalf("eventos late_payment_refunded = %d, quiero 0", n)
	}

	// El banco reintenta y ahora sí devuelve el dinero.
	e.gateway.refundErr = nil
	if err := e.svc.ApplyCallback(header, body); err != nil {
		t.Fatalf("reintento: %v", err)
	}
	got, _ = e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositRefunded {
		t.Fatalf("sena = %+v, quiero refunded", got)
	}
	if n := e.gateway.refundCount(); n != 1 {
		t.Fatalf("reembolsos = %d, quiero 1", n)
	}
	if n := e.deposits.countEvents(domain.DepositEventLatePaymentRefunded); n != 1 {
		t.Fatalf("eventos = %d, quiero 1", n)
	}
}

// Sin banco el callback no existe (D12): 404 antes de tocar la base.
func TestDepositService_ApplyCallback_sin_banco_404(t *testing.T) {
	e := newDepEnv(t)
	e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	e.gateway.enabled = false
	header, body := e.callback(domain.CallbackApproved, "ev-1", "x", 1)
	err := e.svc.ApplyCallback(header, body)
	requireKind(t, err, domain.ErrNotFound)
	if e.deposits.findCalls() != 0 {
		t.Fatal("sin banco no se consulta la base")
	}
}

// --- Lookup (D8) --------------------------------------------------------------

func TestDepositService_Lookup_forma_y_estado(t *testing.T) {
	e := newDepEnv(t)
	startsAt := depClock.Add(48 * time.Hour)
	d := e.deposit(t, "b1", 50000, startsAt)

	got, err := e.svc.Lookup(d.ChargeID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Status != domain.DepositPending || got.AmountCents != 15000 {
		t.Fatalf("lookup = %+v", got)
	}
	if !got.ExpiresAt.Equal(d.ExpiresAt) || !got.StartsAt.Equal(startsAt) {
		t.Fatalf("fechas = %+v, quiero expires=%s starts=%s", got, d.ExpiresAt, startsAt)
	}

	// Un id con forma inválida NUNCA llega a la base: 404 «Pago no encontrado».
	before := e.deposits.findCalls()
	for _, bad := range []string{"", "corto", strings.Repeat("a", 44), strings.Repeat("a", 42) + "=", strings.Repeat("a", 42) + "\x00", strings.Repeat("a", 42) + "\xff"} {
		_, err := e.svc.Lookup(bad)
		requireKind(t, err, domain.ErrNotFound)
		if err.Error() != domain.MsgDepositNotFound {
			t.Fatalf("texto = %q, quiero %q", err.Error(), domain.MsgDepositNotFound)
		}
	}
	if e.deposits.findCalls() != before {
		t.Fatal("un id con forma inválida llegó a la base")
	}

	// Un id bien formado pero desconocido es el mismo 404.
	unknown, err := domain.NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Lookup(unknown); err == nil || err.Error() != domain.MsgDepositNotFound {
		t.Fatalf("cargo desconocido: %v", err)
	}
}

// --- Pantalla «Señas» (D10) --------------------------------------------------

func TestDepositService_List_permiso_antes_de_parametros(t *testing.T) {
	e := newDepEnv(t)
	e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	// Un profesional no ve la pantalla: 403 con parámetros basura y sin consultar.
	for _, actor := range []domain.Membership{e.prof, e.client, e.removed} {
		_, err := e.svc.List(actor, "s1", "estado-invalido", "999999", "no-es-un-cursor")
		requireKind(t, err, domain.ErrForbidden)
	}
	if e.deposits.listCalls() != 0 {
		t.Fatal("sin permiso no se consulta la pantalla")
	}
	// Otro salón: 403.
	_, err := e.svc.List(e.other, "s1", "", "", "")
	requireKind(t, err, domain.ErrForbidden)
}

func TestDepositService_List_roles_y_paginacion(t *testing.T) {
	e := newDepEnv(t)
	var ids []string
	for i, id := range []string{"b1", "b2", "b3"} {
		// created_at distinto para que el orden sea determinista.
		e.svc.Now = func() time.Time { return depClock.Add(time.Duration(i) * time.Minute) }
		d := e.deposit(t, id, 50000, depClock.Add(48*time.Hour))
		ids = append(ids, d.ID)
	}
	e.svc.Now = func() time.Time { return depClock }
	// El más nuevo primero, y el cursor sigue con el resto.
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep} {
		page, err := e.svc.List(actor, "s1", "", "2", "")
		if err != nil {
			t.Fatalf("listar: %v", err)
		}
		if len(page) != 2 || page[0].ID != ids[2] || page[1].ID != ids[1] {
			t.Fatalf("página 1 = %+v", page)
		}
		next, err := e.svc.List(actor, "s1", "", "2", domain.EncodeDepositCursor(domain.DepositCursor{CreatedAt: page[1].CreatedAt, ID: page[1].ID}))
		if err != nil {
			t.Fatalf("página 2: %v", err)
		}
		if len(next) != 1 || next[0].ID != ids[0] {
			t.Fatalf("página 2 = %+v", next)
		}
	}
	// Parámetros inválidos con permiso: los textos del dominio.
	if _, err := e.svc.List(e.owner, "s1", "", "0", ""); err == nil || err.Error() != domain.MsgDepositLimitRange {
		t.Fatalf("límite inválido: %v", err)
	}
	if _, err := e.svc.List(e.owner, "s1", "nope", "", ""); err == nil || err.Error() != domain.MsgDepositStatusInvalid {
		t.Fatalf("estado inválido: %v", err)
	}
}

// --- Barrido (D9) ------------------------------------------------------------

func TestDepositService_Sweep_vence_y_cancela_la_reserva(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	// Todavía no venció: el barrido no hace nada.
	res, err := e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Expired != 0 || e.bookings.cancelCount() != 0 {
		t.Fatalf("barrido prematuro = %+v", res)
	}
	// Pasada la espera: vence y cancela por el camino existente.
	e.svc.Now = func() time.Time { return depClock.Add(31 * time.Minute) }
	res, err = e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Expired != 1 || res.Cancelled != 1 {
		t.Fatalf("barrido = %+v, quiero 1 vencida y 1 cancelada", res)
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositExpired {
		t.Fatalf("sena = %+v, quiero expired", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventExpired); n != 1 {
		t.Fatalf("eventos expired = %d, quiero 1", n)
	}
	if st := e.bookings.status("b1"); st != domain.BookingCancelled {
		t.Fatalf("turno = %s, quiero cancelled", st)
	}
	// Otro tick no vuelve a cancelar ni a vencer.
	res, err = e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("segundo barrido: %v", err)
	}
	if res.Expired != 0 || e.bookings.cancelCount() != 1 {
		t.Fatalf("segundo barrido = %+v (cancelaciones %d)", res, e.bookings.cancelCount())
	}
}

// Si la cancelación falla, el próximo tick la reintenta (el turno no queda
// retenido para siempre).
func TestDepositService_Sweep_reintenta_la_cancelacion(t *testing.T) {
	e := newDepEnv(t)
	e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	e.svc.Now = func() time.Time { return depClock.Add(31 * time.Minute) }
	e.bookings.fail = errors.New("base caída")
	res, err := e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Expired != 1 || res.Cancelled != 0 || res.Failed != 1 {
		t.Fatalf("barrido = %+v", res)
	}
	if st := e.bookings.status("b1"); st != domain.BookingBooked {
		t.Fatalf("el turno sigue retenido: %s", st)
	}
	e.bookings.fail = nil
	res, err = e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Cancelled != 1 {
		t.Fatalf("el reintento tiene que cancelar: %+v", res)
	}
	if st := e.bookings.status("b1"); st != domain.BookingCancelled {
		t.Fatalf("turno = %s, quiero cancelled", st)
	}
}

func TestDepositService_Sweep_reembolsa_dentro_de_la_ventana(t *testing.T) {
	e := newDepEnv(t)
	e.seedPolicy(true, 30, 120, 24)
	startsAt := depClock.Add(48 * time.Hour)
	d := e.deposit(t, "b1", 50000, startsAt)
	header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)
	if err := e.svc.ApplyCallback(header, body); err != nil {
		t.Fatalf("pagar: %v", err)
	}
	// La reserva se cancela desde el panel: sobran 47 h para la ventana de 24 h.
	e.bookings.setStatus("b1", domain.BookingCancelled)

	res, err := e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Refunded != 1 || res.Forfeited != 0 {
		t.Fatalf("barrido = %+v, quiero 1 reembolsada", res)
	}
	if n := e.gateway.refundCount(); n != 1 {
		t.Fatalf("reembolsos = %d, quiero 1", n)
	}
	if r := e.gateway.lastRefund(); r.ChargeID != d.ChargeID || r.AmountCents != d.AmountCents {
		t.Fatalf("reembolso = %+v", r)
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositRefunded || got.SettledAt == nil {
		t.Fatalf("sena = %+v, quiero refunded", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventRefunded); n != 1 {
		t.Fatalf("eventos refunded = %d, quiero 1", n)
	}
	// Otro tick no vuelve a reembolsar.
	if _, err := e.svc.Sweep(10); err != nil {
		t.Fatalf("segundo barrido: %v", err)
	}
	if n := e.gateway.attemptCount(); n != 1 {
		t.Fatalf("llamadas a Refund = %d, quiero 1", n)
	}
}

func TestDepositService_Sweep_pierde_la_sena_fuera_de_la_ventana(t *testing.T) {
	e := newDepEnv(t)
	e.seedPolicy(true, 30, 120, 24)
	startsAt := depClock.Add(2 * time.Hour) // faltan 2 h y la ventana es de 24
	d := e.deposit(t, "b1", 50000, startsAt)
	header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)
	if err := e.svc.ApplyCallback(header, body); err != nil {
		t.Fatalf("pagar: %v", err)
	}
	e.bookings.setStatus("b1", domain.BookingCancelled)

	res, err := e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Refunded != 0 || res.Forfeited != 1 {
		t.Fatalf("barrido = %+v, quiero 1 perdida", res)
	}
	if n := e.gateway.attemptCount(); n != 0 {
		t.Fatalf("llamadas a Refund = %d, quiero 0 (no se devuelve)", n)
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositForfeited || got.SettledAt == nil {
		t.Fatalf("sena = %+v, quiero forfeited", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventForfeited); n != 1 {
		t.Fatalf("eventos forfeited = %d, quiero 1", n)
	}
}

// done y no_show NO reembolsan nunca: el barrido ni las mira.
func TestDepositService_Sweep_atendida_o_no_show_no_reembolsan(t *testing.T) {
	for _, status := range []domain.BookingStatus{domain.BookingDone, domain.BookingNoShow} {
		t.Run(string(status), func(t *testing.T) {
			e := newDepEnv(t)
			d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
			header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)
			if err := e.svc.ApplyCallback(header, body); err != nil {
				t.Fatalf("pagar: %v", err)
			}
			e.bookings.setStatus("b1", status)
			res, err := e.svc.Sweep(10)
			if err != nil {
				t.Fatalf("barrido: %v", err)
			}
			if res.Refunded != 0 || res.Forfeited != 0 || res.Failed != 0 {
				t.Fatalf("barrido = %+v, quiero nada", res)
			}
			if n := e.gateway.attemptCount(); n != 0 {
				t.Fatalf("llamadas a Refund = %d, quiero 0", n)
			}
			got, _ := e.deposits.FindByCharge(d.ChargeID)
			if got.Status != domain.DepositPaid {
				t.Fatalf("sena = %+v, quiero paid", got)
			}
		})
	}
}

// Carrera barrido vs callback: el resultado es SIEMPRE coherente (pagada con el
// turno vivo, o devuelta con el turno cancelado); nunca las dos cosas ni un 500.
// Un segundo antes del vencimiento la sena se paga; en el instante del
// vencimiento el callback perdedor cae en el pago tardío (D7/D9).
func TestDepositService_Sweep_carrera_con_el_callback(t *testing.T) {
	for _, at := range []time.Duration{-time.Second, 0} {
		for i := 0; i < 10; i++ {
			e := newDepEnv(t)
			d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
			// El barrido y el banco llegan a la vez, en el borde del vencimiento.
			e.svc.Now = func() time.Time { return d.ExpiresAt.Add(at) }
			header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)

			var wg sync.WaitGroup
			wg.Add(2)
			var sweepErr, cbErr error
			go func() {
				defer wg.Done()
				_, sweepErr = e.svc.Sweep(10)
			}()
			go func() {
				defer wg.Done()
				cbErr = e.svc.ApplyCallback(header, body)
			}()
			wg.Wait()
			if sweepErr != nil || cbErr != nil {
				t.Fatalf("borde %s iteración %d: barrido=%v callback=%v", at, i, sweepErr, cbErr)
			}
			got, err := e.deposits.FindByCharge(d.ChargeID)
			if err != nil {
				t.Fatal(err)
			}
			booking := e.bookings.status("b1")
			switch got.Status {
			case domain.DepositPaid:
				if booking == domain.BookingCancelled {
					t.Fatalf("borde %s iteración %d: pagada con el turno cancelado", at, i)
				}
				if n := e.deposits.countEvents(domain.DepositEventPaid); n != 1 {
					t.Fatalf("borde %s iteración %d: eventos paid = %d", at, i, n)
				}
				if n := len(e.mailer.Messages()); n != 1 {
					t.Fatalf("borde %s iteración %d: correos = %d, quiero 1", at, i, n)
				}
			case domain.DepositRefunded:
				if booking != domain.BookingCancelled {
					t.Fatalf("borde %s iteración %d: devuelta con el turno vivo", at, i)
				}
				if n := e.gateway.refundCount(); n != 1 {
					t.Fatalf("borde %s iteración %d: reembolsos = %d, quiero 1", at, i, n)
				}
				if n := e.deposits.countEvents(domain.DepositEventLatePaymentRefunded); n != 1 {
					t.Fatalf("borde %s iteración %d: eventos tardíos = %d", at, i, n)
				}
				if n := len(e.mailer.Messages()); n != 0 {
					t.Fatalf("borde %s iteración %d: correos = %d, quiero 0", at, i, n)
				}
			default:
				t.Fatalf("borde %s iteración %d: estado inesperado %s", at, i, got.Status)
			}
		}
	}
}

// F11 review R4-001: if the bank is off or unconfigured when the sweep must
// refund, the deposit must NOT be closed as refunded (the money never went
// back and the row would never be retried): it counts as a failure and stays
// paid until the bank is back.
func TestDepositService_Sweep_sin_banco_no_marca_devuelta(t *testing.T) {
	e := newDepEnv(t)
	e.seedPolicy(true, 30, 120, 24)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	header, body := e.callback(domain.CallbackApproved, "ev-1", d.ChargeID, d.AmountCents)
	if err := e.svc.ApplyCallback(header, body); err != nil {
		t.Fatalf("pagar: %v", err)
	}
	e.bookings.setStatus("b1", domain.BookingCancelled)

	e.gateway.mu.Lock()
	e.gateway.enabled = false
	e.gateway.mu.Unlock()
	res, err := e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if res.Refunded != 0 || res.Failed != 1 {
		t.Fatalf("barrido = %+v, quiero 0 reembolsadas y 1 fallida", res)
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositPaid || got.SettledAt != nil {
		t.Fatalf("sena = %+v, quiero paid sin cerrar", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventRefunded); n != 0 {
		t.Fatalf("eventos refunded = %d, quiero 0", n)
	}

	// Con el banco de vuelta el siguiente tick la devuelve de verdad.
	e.gateway.mu.Lock()
	e.gateway.enabled = true
	e.gateway.mu.Unlock()
	res, err = e.svc.Sweep(10)
	if err != nil {
		t.Fatalf("segundo barrido: %v", err)
	}
	if res.Refunded != 1 || e.gateway.refundCount() != 1 {
		t.Fatalf("barrido = %+v, reembolsos = %d, quiero 1 y 1", res, e.gateway.refundCount())
	}
}

// F11 review R4-002: a late payment must cancel the booking BEFORE the deposit
// is closed as refunded, and a cancellation failure must come back as an error
// (the bank retries): otherwise a refunded deposit would leave a `booked`
// booking holding the slot forever and no sweep would ever see it again.
func TestDepositService_ApplyCallback_pago_tardio_con_cancelacion_fallida_se_reintenta(t *testing.T) {
	e := newDepEnv(t)
	d := e.deposit(t, "b1", 50000, depClock.Add(48*time.Hour))
	// El reloj pasa la espera y el barrido vence la sena, pero la cancelación del
	// turno falla en ese tick: el turno sigue `booked`.
	e.svc.Now = func() time.Time { return depClock.Add(time.Hour) }
	e.policy.Now = e.svc.Now
	e.bookings.fail = errors.New("la base no responde")
	if _, err := e.svc.Sweep(10); err != nil {
		t.Fatalf("barrido: %v", err)
	}
	if st := e.bookings.status("b1"); st != domain.BookingBooked {
		t.Fatalf("precondición: el turno debía seguir booked, es %s", st)
	}

	header, body := e.callback(domain.CallbackApproved, "ev-late", d.ChargeID, d.AmountCents)
	if err := e.svc.ApplyCallback(header, body); err == nil {
		t.Fatal("el pago tardío con la cancelación fallida debe devolver error para que el banco reintente")
	}
	got, _ := e.deposits.FindByCharge(d.ChargeID)
	if got.Status == domain.DepositRefunded {
		t.Fatalf("la sena no puede cerrarse como devuelta si el turno quedó retenido: %+v", got)
	}

	// El banco reintenta con la base de vuelta: se cancela el turno, se cierra una
	// sola vez y el reembolso del banco es idempotente por cargo.
	e.bookings.fail = nil
	if err := e.svc.ApplyCallback(header, body); err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if st := e.bookings.status("b1"); st != domain.BookingCancelled {
		t.Fatalf("estado del turno = %s, quiero cancelled", st)
	}
	got, _ = e.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositRefunded || got.SettledAt == nil {
		t.Fatalf("sena = %+v, quiero refunded", got)
	}
	if n := e.deposits.countEvents(domain.DepositEventLatePaymentRefunded); n != 1 {
		t.Fatalf("eventos late_payment_refunded = %d, quiero 1", n)
	}
}
