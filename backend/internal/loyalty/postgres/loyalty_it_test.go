// Integración con Postgres real de la fidelidad (F9, D2..D7). Se saltea sin
// SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la DB: la consulta de
// visitas (qué cuenta y qué no), el upsert que conserva counting_from, la
// entrega atómica con su guarda final, el UPDATE condicional del uso, la
// idempotencia por los índices únicos y el aislamiento por salón.
package postgres

import (
	"context"
	"errors"
	"fmt"
	clientspostgres "salon-saas/internal/clients/postgres"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

type loyFix struct {
	pool     *pgxpool.Pool
	ctx      context.Context
	programs LoyaltyPrograms
	rewards  LoyaltyRewards
	// s-it-loy es el salón de las pruebas (zona de Auckland), s-it-loy2 el de OTRO tenant.
	svcA, svcB, svcOther domain.Service
	lia, mora, otherCl   domain.Client
	auckland             *time.Location
	// t0 es el counting_from de las pruebas.
	t0 time.Time
}

const (
	loySalon  = "s-it-loy"
	loySalon2 = "s-it-loy2"
	loyUser   = "u-it-loy-prof"
)

// loyFixtures deja un escenario limpio: dos salones, un usuario que vende,
// servicios y clientas.
func loyFixtures(t *testing.T) *loyFix {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-loy%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-loy%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-loy-prof','Prof Fidelidad','loyprof@test.com','x')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-loy','s-it-loy','Salón Fidelidad IT','Pacific/Auckland')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-loy2','s-it-loy2','Salón Fidelidad IT 2','America/Mexico_City')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		// Que un salón con programa, vales y ventas se pueda borrar (RESTRICT/CASCADE).
		if _, err := pool.Exec(context.Background(), `DELETE FROM salons WHERE id LIKE 's-it-loy%'`); err != nil {
			t.Errorf("borrar el salón con sus vales: %v", err)
		}
	})
	loc, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	f := &loyFix{pool: pool, ctx: ctx, programs: LoyaltyPrograms{Pool: pool}, rewards: LoyaltyRewards{Pool: pool}, auckland: loc,
		t0: time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)}
	services := pgstore.Services{Pool: pool}
	mk := func(salon, name string) domain.Service {
		s, err := domain.NewService(salon, name, 45, 15000, 10)
		if err != nil {
			t.Fatalf("servicio: %v", err)
		}
		if err := services.Save(s); err != nil {
			t.Fatalf("guardar servicio: %v", err)
		}
		return s
	}
	f.svcA, f.svcB, f.svcOther = mk(loySalon, "Corte A"), mk(loySalon, "Tinte B"), mk(loySalon2, "Corte Ajeno")
	clients := clientspostgres.Clients{Pool: pool}
	mc := func(salon, name string) domain.Client {
		c, err := domain.NewClient(salon, name, "", "", "")
		if err != nil {
			t.Fatalf("clienta: %v", err)
		}
		if err := clients.Save(c); err != nil {
			t.Fatalf("guardar clienta: %v", err)
		}
		return c
	}
	f.lia, f.mora, f.otherCl = mc(loySalon, "Lía Fidelidad"), mc(loySalon, "Mora Fidelidad"), mc(loySalon2, "Clienta Ajena")
	return f
}

// saveProgram guarda un programa de N visitas con el servicio A; el primer
// guardado fija counting_from = f.t0.
func (f *loyFix) saveProgram(t *testing.T, enabled bool, visits int, validity *int) domain.LoyaltyProgram {
	t.Helper()
	p, err := domain.NewLoyaltyProgram(loySalon, enabled, visits, f.svcA.ID, validity, f.t0)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.programs.Save(p)
	if err != nil {
		t.Fatalf("guardar programa: %v", err)
	}
	return saved
}

// sale siembra una venta con SQL directo. kind: service | two | products |
// package | mixed. clientID "" = sin vínculo (NULL).
func (f *loyFix) sale(t *testing.T, salon, clientID string, at time.Time, kind string) string {
	t.Helper()
	id := domain.NewID()
	var client any
	if clientID != "" {
		client = clientID
	}
	packages := 0
	if kind == "package" {
		packages = 1000
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,created_at,client_id,packages_cents)
		 VALUES($1,$2,$3,'Clienta',1000,'cash',$4,$5,$6)`, id, salon, loyUser, at, client, packages); err != nil {
		t.Fatalf("venta %s: %v", kind, err)
	}
	svc := f.svcA.ID
	if salon == loySalon2 {
		svc = f.svcOther.ID
	}
	item := func() {
		if _, err := f.pool.Exec(f.ctx,
			`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents) VALUES($1,$2,$3,1,500)`,
			domain.NewID(), id, svc); err != nil {
			t.Fatalf("línea de servicio: %v", err)
		}
	}
	product := func() {
		pid := domain.NewID()
		if _, err := f.pool.Exec(f.ctx,
			`INSERT INTO products(id,salon_id,name,price_cents) VALUES($1,$2,$3,500)`, pid, salon, "Prod "+pid[:6]); err != nil {
			t.Fatalf("producto: %v", err)
		}
		if _, err := f.pool.Exec(f.ctx,
			`INSERT INTO sale_products(id,sale_id,product_id,qty,unit_price_cents) VALUES($1,$2,$3,1,500)`, domain.NewID(), id, pid); err != nil {
			t.Fatalf("línea de producto: %v", err)
		}
	}
	switch kind {
	case "service":
		item()
	case "two":
		item()
		item()
	case "products":
		product()
	case "mixed":
		item()
		product()
	}
	return id
}

// visitSales siembra n ventas de servicio de la clienta, todas después de t0.
func (f *loyFix) visitSales(t *testing.T, clientID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		f.sale(t, loySalon, clientID, f.t0.Add(time.Duration(i+1)*time.Minute), "service")
	}
}

func (f *loyFix) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	return itdb.CountRows(t, f.pool, q, args...)
}

func wantLoyErr(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("se esperaba el error %q", text)
	}
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("error %q, quería %q de la familia %v", err.Error(), text, kind)
	}
}

func grantReq(clientID, op string, now time.Time, loc *time.Location) domain.LoyaltyGrantRequest {
	return domain.LoyaltyGrantRequest{SalonID: loySalon, ClientID: clientID, ClientOpID: op, Now: now, Loc: loc}
}

func redeemReq(clientID, rewardID, op string, now time.Time) domain.LoyaltyRedeemRequest {
	return domain.LoyaltyRedeemRequest{SalonID: loySalon, ClientID: clientID, RewardID: rewardID, ClientOpID: op, Actor: loyUser, Now: now}
}

// --- D3/D4: programa ----------------------------------------------------------

func TestLoyaltyPrograms_saveAndFind(t *testing.T) {
	f := loyFixtures(t)
	if _, err := f.programs.Find(loySalon); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nunca guardado: %v, quería ErrNotFound", err)
	}
	days := 30
	first := f.saveProgram(t, true, 5, &days)
	if !first.Enabled || first.VisitsRequired != 5 || first.RewardServiceID != f.svcA.ID || first.RewardServiceName != "Corte A" ||
		first.ValidityDays == nil || *first.ValidityDays != 30 || !first.CountingFrom.Equal(f.t0) {
		t.Fatalf("primer guardado: %+v", first)
	}
	// El segundo guardado REEMPLAZA los datos y CONSERVA counting_from aunque llegue otro.
	later, err := domain.NewLoyaltyProgram(loySalon, false, 8, f.svcB.ID, nil, f.t0.Add(72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.programs.Save(later)
	if err != nil {
		t.Fatalf("segundo guardado: %v", err)
	}
	if second.Enabled || second.VisitsRequired != 8 || second.RewardServiceName != "Tinte B" || second.ValidityDays != nil ||
		!second.CountingFrom.Equal(f.t0) {
		t.Fatalf("segundo guardado: %+v (counting_from debe seguir en %v)", second, f.t0)
	}
	got, err := f.programs.Find(loySalon)
	if err != nil || got.VisitsRequired != 8 || got.RewardServiceID != f.svcB.ID || got.ValidityDays != nil || !got.CountingFrom.Equal(f.t0) || got.Enabled {
		t.Fatalf("Find: %+v %v", got, err)
	}
	if n := f.count(t, `SELECT count(*) FROM loyalty_programs WHERE salon_id=$1`, loySalon); n != 1 {
		t.Fatalf("hay %d filas, quería 1", n)
	}
	// Aislamiento: el otro salón no tiene programa.
	if _, err := f.programs.Find(loySalon2); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v", err)
	}
}

// Un servicio de OTRO salón no entra por el adaptador aunque la FK lo permita.
func TestLoyaltyPrograms_saveRejectsForeignService(t *testing.T) {
	f := loyFixtures(t)
	p, err := domain.NewLoyaltyProgram(loySalon, true, 5, f.svcOther.ID, nil, f.t0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.programs.Save(p)
	wantLoyErr(t, err, domain.ErrValidation, "Servicio inválido")
	if n := f.count(t, `SELECT count(*) FROM loyalty_programs WHERE salon_id=$1`, loySalon); n != 0 {
		t.Fatal("un servicio ajeno dejó un programa guardado")
	}
}

// --- D2: qué cuenta como visita -------------------------------------------------

func TestLoyaltyRewards_visits(t *testing.T) {
	f := loyFixtures(t)
	visits := func(client string) int {
		t.Helper()
		n, err := f.rewards.Visits(loySalon, client, f.t0)
		if err != nil {
			t.Fatalf("visitas: %v", err)
		}
		return n
	}
	after := f.t0.Add(time.Hour)
	if visits(f.lia.ID) != 0 {
		t.Fatal("sin ventas son 0 visitas")
	}
	f.sale(t, loySalon, f.lia.ID, after, "service")
	if got := visits(f.lia.ID); got != 1 {
		t.Fatalf("una venta con servicio: %d, quería 1", got)
	}
	// Dos líneas de servicio en UNA venta siguen siendo UNA visita.
	f.sale(t, loySalon, f.lia.ID, after, "two")
	if got := visits(f.lia.ID); got != 2 {
		t.Fatalf("venta con dos servicios: %d, quería 2", got)
	}
	// Mixta (servicio + producto) SÍ cuenta.
	f.sale(t, loySalon, f.lia.ID, after, "mixed")
	if got := visits(f.lia.ID); got != 3 {
		t.Fatalf("venta mixta: %d, quería 3", got)
	}
	// Solo productos NO.
	f.sale(t, loySalon, f.lia.ID, after, "products")
	// La compra de un paquete (packages_cents > 0, sin líneas) NO.
	f.sale(t, loySalon, f.lia.ID, after, "package")
	// Sin vínculo de clienta NO suma a nadie.
	f.sale(t, loySalon, "", after, "service")
	// Otra clienta del salón NO suma a Lía.
	f.sale(t, loySalon, f.mora.ID, after, "service")
	// Una venta del otro salón (otra clienta) NO suma.
	f.sale(t, loySalon2, f.otherCl.ID, after, "service")
	// Antes de counting_from NO; exactamente en counting_from SÍ (>=).
	f.sale(t, loySalon, f.lia.ID, f.t0.Add(-time.Microsecond), "service")
	f.sale(t, loySalon, f.lia.ID, f.t0, "service")
	if got := visits(f.lia.ID); got != 4 {
		t.Fatalf("total de Lía: %d, quería 4 (3 + la de counting_from exacto)", got)
	}
	if got := visits(f.mora.ID); got != 1 {
		t.Fatalf("Mora: %d, quería 1", got)
	}
	// Aislamiento: la clienta de otro salón consultada con ESTE salón no existe: 0.
	if n, err := f.rewards.Visits(loySalon, f.otherCl.ID, f.t0); err != nil || n != 0 {
		t.Fatalf("clienta ajena con este salón: %d %v", n, err)
	}
	// La consulta es de solo lectura: no escribió nada.
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards`); n != 0 {
		t.Fatalf("contar visitas escribió %d vales", n)
	}
}

// --- D6: entregar ---------------------------------------------------------------

func TestLoyaltyRewards_grantRules(t *testing.T) {
	f := loyFixtures(t)
	now := time.Date(2026, 3, 10, 11, 30, 0, 0, time.UTC) // 11 de marzo 00:30 en Auckland
	f.visitSales(t, f.lia.ID, 5)

	// Nunca guardado y apagado: 409 «no activada».
	_, err := f.rewards.Grant(grantReq(f.lia.ID, "", now, f.auckland))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "La fidelidad no está activada")
	f.saveProgram(t, false, 5, nil)
	_, err = f.rewards.Grant(grantReq(f.lia.ID, "", now, f.auckland))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "La fidelidad no está activada")

	// Sin visitas suficientes.
	days := 1
	f.saveProgram(t, true, 6, &days)
	_, err = f.rewards.Grant(grantReq(f.lia.ID, "", now, f.auckland))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "No hay premios disponibles")
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards`); n != 0 {
		t.Fatalf("un rechazo dejó %d vales", n)
	}

	// Con 6 visitas toca el primero: snapshot, vencimiento local y número.
	f.visitSales(t, f.lia.ID, 1)
	f.sale(t, loySalon, f.lia.ID, f.t0.Add(time.Hour), "service") // 7 visitas
	r, err := f.rewards.Grant(grantReq(f.lia.ID, "", now, f.auckland))
	if err != nil {
		t.Fatalf("entregar: %v", err)
	}
	wantExpiry := time.Date(2026, 3, 12, 23, 59, 59, 999_999_000, f.auckland).UTC()
	if r.RewardNumber != 1 || r.ServiceID != f.svcA.ID || r.ServiceName != "Corte A" || r.ClientID != f.lia.ID ||
		r.ExpiresAt == nil || !r.ExpiresAt.Equal(wantExpiry) || !r.GrantedAt.Equal(now) || r.RedeemedAt != nil {
		t.Fatalf("vale: %+v (vencimiento esperado %v)", r, wantExpiry)
	}
	// Segunda entrega sin visitas nuevas: 409 y UNA fila.
	_, err = f.rewards.Grant(grantReq(f.lia.ID, "", now, f.auckland))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "No hay premios disponibles")
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE client_id=$1`, f.lia.ID); n != 1 {
		t.Fatalf("hay %d vales, quería 1", n)
	}
	// El vale conserva la FOTO aunque el servicio se renombre.
	if _, err := f.pool.Exec(f.ctx, `UPDATE services SET name='Corte renombrado' WHERE id=$1`, f.svcA.ID); err != nil {
		t.Fatal(err)
	}
	list, err := f.rewards.ListByClient(loySalon, f.lia.ID)
	if err != nil || len(list) != 1 || list[0].ServiceName != "Corte A" {
		t.Fatalf("la foto del servicio cambió: %+v %v", list, err)
	}
	// Con 12 visitas el número 2.
	f.visitSales(t, f.lia.ID, 5) // 12
	r2, err := f.rewards.Grant(grantReq(f.lia.ID, "", now, f.auckland))
	if err != nil || r2.RewardNumber != 2 {
		t.Fatalf("segundo vale: %+v %v", r2, err)
	}
}

func TestLoyaltyRewards_grantIsTenantScoped(t *testing.T) {
	f := loyFixtures(t)
	f.saveProgram(t, true, 2, nil)
	f.visitSales(t, f.lia.ID, 4)
	// Una clienta de OTRO salón pedida con este salón: no existe, no entrega.
	_, err := f.rewards.Grant(grantReq(f.otherCl.ID, "", time.Now(), time.UTC))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("clienta ajena: %v, quería ErrNotFound", err)
	}
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards`); n != 0 {
		t.Fatalf("la clienta ajena recibió %d vales", n)
	}
}

func TestLoyaltyRewards_grantIdempotentByOp(t *testing.T) {
	f := loyFixtures(t)
	f.saveProgram(t, true, 2, nil)
	f.visitSales(t, f.lia.ID, 20) // 10 premios disponibles: el repetido NO debe entregar otro
	f.visitSales(t, f.mora.ID, 20)
	now := time.Now().UTC()
	first, err := f.rewards.Grant(grantReq(f.lia.ID, "op-g1", now, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.rewards.Grant(grantReq(f.lia.ID, "op-g1", now.Add(time.Hour), time.UTC))
	if err != nil || again.ID != first.ID || again.RewardNumber != 1 || !again.GrantedAt.Equal(first.GrantedAt.UTC()) {
		t.Fatalf("el repetido: %+v %v (primero %+v)", again, err, first)
	}
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards`); n != 1 {
		t.Fatalf("el repetido dejó %d vales, quería 1", n)
	}
	// El mismo id para OTRA clienta es 409 y no entrega.
	_, err = f.rewards.Grant(grantReq(f.mora.ID, "op-g1", now, time.UTC))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "Esa operación ya se usó en otro pedido")
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards`); n != 1 {
		t.Fatalf("el op id reusado dejó %d vales", n)
	}
}

// --- D7: usar -------------------------------------------------------------------

// grantOne entrega un vale a Lía con el programa de 2 visitas.
func (f *loyFix) grantOne(t *testing.T, validity *int, now time.Time) domain.LoyaltyReward {
	t.Helper()
	f.saveProgram(t, true, 2, validity)
	var have int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM loyalty_rewards WHERE client_id=$1`, f.lia.ID).Scan(&have); err != nil {
		t.Fatal(err)
	}
	f.visitSales(t, f.lia.ID, 2)
	var visits int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM sales WHERE client_id=$1 AND created_at >= $2`, f.lia.ID, f.t0).Scan(&visits); err != nil {
		t.Fatal(err)
	}
	if visits/2 <= have {
		t.Fatalf("faltan visitas: %d", visits)
	}
	r, err := f.rewards.Grant(grantReq(f.lia.ID, "", now, time.UTC))
	if err != nil {
		t.Fatalf("entregar: %v", err)
	}
	return r
}

func TestLoyaltyRewards_redeem(t *testing.T) {
	f := loyFixtures(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := f.grantOne(t, nil, now)
	// El otro salón TIENE un programa encendido: así el pedido llega al vale y
	// se prueba que no es suyo (sin programa el 409 «no activada» va primero).
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO loyalty_programs(salon_id,enabled,visits_required,reward_service_id,counting_from) VALUES($1,true,2,$2,now())`,
		loySalon2, f.svcOther.ID); err != nil {
		t.Fatal(err)
	}

	// Inexistente, de otra clienta o de otro salón: ErrNotFound y el vale intacto.
	for name, req := range map[string]domain.LoyaltyRedeemRequest{
		"inexistente":     redeemReq(f.lia.ID, "no-existe", "", now),
		"otra clienta":    redeemReq(f.mora.ID, r.ID, "", now),
		"clienta ajena":   redeemReq(f.otherCl.ID, r.ID, "", now),
		"otro salón":      {SalonID: loySalon2, ClientID: f.otherCl.ID, RewardID: r.ID, Actor: loyUser, Now: now},
		"id con espacios": redeemReq(f.lia.ID, " "+r.ID, "", now),
	} {
		if _, err := f.rewards.Redeem(req); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s: %v, quería ErrNotFound", name, err)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE redeemed_at IS NOT NULL`); n != 0 {
		t.Fatalf("un rechazo usó el vale")
	}

	// Uso con profesional y nota.
	req := redeemReq(f.lia.ID, r.ID, "", now)
	req.ProfessionalID, req.Note = loyUser, "Cumpleaños"
	got, err := f.rewards.Redeem(req)
	if err != nil {
		t.Fatalf("usar: %v", err)
	}
	if got.ID != r.ID || got.RedeemedAt == nil || !got.RedeemedAt.Equal(now) || got.RedeemedBy != loyUser ||
		got.RedeemedProfessionalID != loyUser || got.RedeemNote != "Cumpleaños" || got.ServiceName != "Corte A" {
		t.Fatalf("vale usado: %+v", got)
	}
	// Ya usado: 409 y no cambia nada.
	_, err = f.rewards.Redeem(redeemReq(f.lia.ID, r.ID, "", now.Add(time.Hour)))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "Ese premio ya se usó")
	var at time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT redeemed_at FROM loyalty_rewards WHERE id=$1`, r.ID).Scan(&at); err != nil || !at.Equal(now) {
		t.Fatalf("redeemed_at cambió: %v %v", at, err)
	}
}

func TestLoyaltyRewards_redeemExpiredAndDisabled(t *testing.T) {
	f := loyFixtures(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	days := 1
	r := f.grantOne(t, &days, now)
	exp := *r.ExpiresAt
	// EXACTO en expires_at ya venció (vigente mientras expires_at > now).
	_, err := f.rewards.Redeem(redeemReq(f.lia.ID, r.ID, "", exp))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "El premio venció")
	// Un microsegundo antes todavía vale... pero con el programa apagado no.
	f.saveProgram(t, false, 2, &days)
	_, err = f.rewards.Redeem(redeemReq(f.lia.ID, r.ID, "", exp.Add(-time.Microsecond)))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "La fidelidad no está activada")
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE redeemed_at IS NOT NULL`); n != 0 {
		t.Fatal("el rechazo usó el vale")
	}
	f.saveProgram(t, true, 2, &days)
	if _, err := f.rewards.Redeem(redeemReq(f.lia.ID, r.ID, "", exp.Add(-time.Microsecond))); err != nil {
		t.Fatalf("antes de vencer y con el programa encendido: %v", err)
	}
}

func TestLoyaltyRewards_redeemIdempotentByOp(t *testing.T) {
	f := loyFixtures(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := f.grantOne(t, nil, now)
	b := f.grantOne(t, nil, now)
	first, err := f.rewards.Redeem(redeemReq(f.lia.ID, a.ID, "op-r1", now))
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.rewards.Redeem(redeemReq(f.lia.ID, a.ID, "op-r1", now.Add(time.Hour)))
	if err != nil || again.ID != first.ID || again.RedeemedAt == nil || !again.RedeemedAt.Equal(now) {
		t.Fatalf("el repetido: %+v %v", again, err)
	}
	// El mismo id para OTRO vale: 409 y el otro vale sigue sin usar.
	_, err = f.rewards.Redeem(redeemReq(f.lia.ID, b.ID, "op-r1", now))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "Esa operación ya se usó en otro pedido")
	if n := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE redeemed_at IS NOT NULL`); n != 1 {
		t.Fatalf("hay %d vales usados, quería 1", n)
	}
	// El mismo id para OTRA clienta (o salón) que no es dueña del vale: 409.
	_, err = f.rewards.Redeem(redeemReq(f.mora.ID, a.ID, "op-r1", now))
	wantLoyErr(t, err, domain.ErrAlreadyExists, "Esa operación ya se usó en otro pedido")
}

// --- D5: lecturas -----------------------------------------------------------------

func TestLoyaltyRewards_listAndCount(t *testing.T) {
	f := loyFixtures(t)
	f.saveProgram(t, true, 2, nil)
	for n := 1; n <= 105; n++ {
		if _, err := f.pool.Exec(f.ctx,
			`INSERT INTO loyalty_rewards(id,salon_id,client_id,reward_number,service_id,service_name,granted_at)
			 VALUES($1,$2,$3,$4,$5,'Corte A',now())`, fmt.Sprintf("r-it-loy-%03d", n), loySalon, f.lia.ID, n, f.svcA.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := f.rewards.CountGranted(loySalon, f.lia.ID); err != nil || n != 105 {
		t.Fatalf("entregados: %d %v", n, err)
	}
	list, err := f.rewards.ListByClient(loySalon, f.lia.ID)
	if err != nil || len(list) != 100 || list[0].RewardNumber != 105 || list[99].RewardNumber != 6 {
		t.Fatalf("lista: %d primeros %v %v", len(list), list[0].RewardNumber, err)
	}
	// Aislamiento por clienta y por salón.
	if n, _ := f.rewards.CountGranted(loySalon, f.mora.ID); n != 0 {
		t.Fatalf("Mora tiene %d", n)
	}
	if n, _ := f.rewards.CountGranted(loySalon2, f.lia.ID); n != 0 {
		t.Fatalf("otro salón ve %d", n)
	}
	if l, _ := f.rewards.ListByClient(loySalon2, f.lia.ID); len(l) != 0 {
		t.Fatalf("otro salón lista %d", len(l))
	}
}
