// Integración contra Postgres real para catálogo + POS (requiere SALON_IT=1).
// Aplica las migraciones del repo dos veces (idempotencia) y luego prueba los
// repositorios: scoping por salón, índice único de nombres activos, transacción
// de venta + ítems, suma de efectivo para el cierre y ranking del dashboard.
// Corre con: SALON_IT=1 go test ./internal/pos/postgres/ -run IT -v
package postgres_test

import (
	"context"
	"errors"
	pgstore "salon-saas/internal/adapters/postgres"
	pospostgres "salon-saas/internal/pos/postgres"
	staffingpostgres "salon-saas/internal/staffing/postgres"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	bookingspostgres "salon-saas/internal/bookings/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

// applyMigrations aplica las migraciones con el runner compartido de los tests de
// integración (itdb): dos pasadas, en orden léxico y bajo el advisory lock para
// que los paquetes IT que corren en paralelo no se pisen.

// posFixtures deja el escenario limpio y devuelve el pool. Ids fijos (mismo
// patrón que el test de carrera) para que el test sea re-ejecutable.
func posFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)

	// Limpieza acotada a las fixtures de este archivo (el salón cascadea todo).
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-pos%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-pos%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pos','Pos IT','posit@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-it-pos','s-it-pos','Salón POS IT')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-it-pos2','s-it-pos2','Salón POS IT 2')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	return pool, ctx
}

func TestServices_IT(t *testing.T) {
	pool, _ := posFixtures(t)
	repo := staffingpostgres.Services{Pool: pool}

	corte, err := domain.NewService("s-it-pos", "Corte IT", 45, 150000, 30)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if err := repo.Save(corte); err != nil {
		t.Fatalf("guardar: %v", err)
	}

	// El índice único (salon_id, lower(name)) WHERE active traduce 23505.
	dup, _ := domain.NewService("s-it-pos", "corte it", 30, 90000, 10)
	if err := repo.Save(dup); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("nombre activo duplicado debe dar ErrAlreadyExists, dio %v", err)
	}
	// Nombre igual en otro salón: permitido.
	other, _ := domain.NewService("s-it-pos2", "Corte IT", 30, 90000, 10)
	if err := repo.Save(other); err != nil {
		t.Fatalf("mismo nombre en otro salón: %v", err)
	}

	// FindByID scopeado: el mismo id no se ve desde otro tenant.
	if got, err := repo.FindByID("s-it-pos", corte.ID); err != nil || got.Name != "Corte IT" {
		t.Fatalf("FindByID propio: %v %+v", err, got)
	}
	if _, err := repo.FindByID("s-it-pos2", corte.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant debe dar ErrNotFound, dio %v", err)
	}

	// Update por id (upsert) refleja los cambios.
	up, err := corte.WithDetails("Corte y brushing IT", 60, 180000, 35, true)
	if err != nil {
		t.Fatalf("dominio update: %v", err)
	}
	if err := repo.Save(up); err != nil {
		t.Fatalf("actualizar: %v", err)
	}
	if got, _ := repo.FindByID("s-it-pos", corte.ID); got.PriceCents != 180000 || got.DurationMin != 60 {
		t.Fatalf("no persistió el update: %+v", got)
	}

	// Desactivar libera el nombre (el índice es WHERE active).
	off, _ := up.WithDetails(up.Name, up.DurationMin, up.PriceCents, up.CommissionPct, false)
	if err := repo.Save(off); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	if err := repo.Save(dup); err != nil {
		t.Fatalf("nombre libre tras desactivar: %v", err)
	}
	// List: sin inactivos vs con inactivos; scopeado por salón.
	active, err := repo.List("s-it-pos", false)
	if err != nil || len(active) != 1 || active[0].Name != "corte it" {
		t.Fatalf("activos: %v %+v", err, active)
	}
	all, err := repo.List("s-it-pos", true)
	if err != nil || len(all) != 2 {
		t.Fatalf("con inactivos: %v %+v", err, all)
	}
	if got, _ := repo.List("s-it-pos2", true); len(got) != 1 {
		t.Fatalf("otro salón debe tener 1, dio %d", len(got))
	}
	// Violación de CHECK (duración 0): 23514 -> ErrValidation, nunca SQL crudo.
	bad := corte
	bad.ID = "svc-check-violation"
	bad.DurationMin = 0
	if err := repo.Save(bad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("CHECK debe traducir a ErrValidation, dio %v", err)
	}
}

func TestSales_IT(t *testing.T) {
	pool, _ := posFixtures(t)
	services := staffingpostgres.Services{Pool: pool}
	repo := pospostgres.Sales{Pool: pool}

	corte, _ := domain.NewService("s-it-pos", "Corte IT", 45, 150000, 30)
	brushing, _ := domain.NewService("s-it-pos", "Brushing IT", 30, 80000, 20)
	for _, s := range []domain.Service{corte, brushing} {
		if err := services.Save(s); err != nil {
			t.Fatalf("catálogo: %v", err)
		}
	}

	sale, err := domain.NewSale("s-it-pos", "u-it-pos", "Lía IT", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 2, UnitPriceCents: 150000},
		{ServiceID: brushing.ID, Qty: 1, UnitPriceCents: 80000},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("dominio venta: %v", err)
	}
	if err := repo.Save(sale); err != nil {
		t.Fatalf("guardar venta: %v", err)
	}
	// Otra venta con tarjeta, para que la caja solo cuente el efectivo.
	card, _ := domain.NewSale("s-it-pos", "u-it-pos", "Ana IT", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 1, UnitPriceCents: 150000},
	}, domain.MethodCard)
	if err := repo.Save(card); err != nil {
		t.Fatalf("venta con tarjeta: %v", err)
	}

	now := time.Now().UTC()
	from, to := now.Add(-time.Hour), now.Add(time.Hour)
	list, err := repo.ListRange("s-it-pos", from, to)
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("esperaba 2 ventas, dio %d", len(list))
	}
	var got *domain.Sale
	for i := range list {
		if list[i].ID == sale.ID {
			got = &list[i]
		}
	}
	if got == nil {
		t.Fatal("la venta no volvió del listado")
	}
	if got.TotalCents != 380000 {
		t.Fatalf("total persistido = 380000, dio %d", got.TotalCents)
	}
	if len(got.Items) != 2 || got.Method != domain.MethodCash || got.ClientName != "Lía IT" {
		t.Fatalf("venta mal hidratada: %+v", got)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Fatalf("created_at debe volver en UTC, dio %v", got.CreatedAt.Location())
	}

	// Sumas: efectivo (cierre de caja) vs todos los métodos (ingresos).
	if cash, err := repo.SumCents("s-it-pos", from, to, domain.MethodCash); err != nil || cash != 380000 {
		t.Fatalf("efectivo = 380000, dio %d (%v)", cash, err)
	}
	if all, err := repo.SumCents("s-it-pos", from, to, ""); err != nil || all != 530000 {
		t.Fatalf("todos = 530000, dio %d (%v)", all, err)
	}
	// Ventana vacía y otro salón: cero, sin fuga entre tenants.
	if sum, _ := repo.SumCents("s-it-pos", now.Add(-5*time.Hour), now.Add(-4*time.Hour), ""); sum != 0 {
		t.Fatalf("ventana vacía debe dar 0, dio %d", sum)
	}
	if sum, _ := repo.SumCents("s-it-pos2", from, to, ""); sum != 0 {
		t.Fatalf("otro salón debe dar 0, dio %d", sum)
	}

	// Ranking del dashboard: corte 3 unidades / 450000; brushing 1 / 80000.
	top, err := repo.TopServices("s-it-pos", from, to, 5)
	if err != nil || len(top) != 2 {
		t.Fatalf("top services: %v %+v", err, top)
	}
	if top[0].ServiceID != corte.ID || top[0].Qty != 3 || top[0].RevenueCents != 450000 || top[0].Name != "Corte IT" {
		t.Fatalf("ranking mal ordenado/agrupado: %+v", top)
	}

	// FK inexistente -> ErrValidation (sin fugar SQL).
	ghost, _ := domain.NewSale("s-it-pos", "u-it-pos", "Fantasma", []domain.SaleItem{
		{ServiceID: "svc-no-existe", Qty: 1, UnitPriceCents: 1000},
	}, domain.MethodCash)
	if err := repo.Save(ghost); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("FK inexistente debe dar ErrValidation, dio %v", err)
	}
	// La transacción no deja venta huérfana: la venta fallida no existe.
	if _, err := repo.SumCents("s-it-pos", from, to, domain.MethodTransfer); err != nil {
		t.Fatalf("sumar transferencia: %v", err)
	}
	if n, _ := repo.ListRange("s-it-pos", from, to); len(n) != 2 {
		t.Fatalf("la venta fallida no debe persistirse, hay %d", len(n))
	}
}

func TestCashCloses_IT(t *testing.T) {
	pool, _ := posFixtures(t)
	repo := pospostgres.CashCloses{Pool: pool}

	if _, err := repo.Last("s-it-pos"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sin cierres debe dar ErrNotFound, dio %v", err)
	}
	now := time.Now().UTC()
	first, err := domain.NewCashClose("s-it-pos", now.Add(-8*time.Hour), now.Add(-time.Hour), 250000, 248000, "faltó vuelto IT")
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if err := repo.Save(first); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	second, err := domain.NewCashClose("s-it-pos", first.ClosedAt, now, 50000, 50000, "")
	if err != nil {
		t.Fatalf("segundo dominio: %v", err)
	}
	if err := repo.Save(second); err != nil {
		t.Fatalf("guardar segundo: %v", err)
	}
	// Otro salón no ve estos cierres.
	if _, err := repo.Last("s-it-pos2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón no debe ver cierres, dio %v", err)
	}

	last, err := repo.Last("s-it-pos")
	if err != nil || last.ID != second.ID {
		t.Fatalf("Last debe ser el más reciente: %v %+v", err, last)
	}
	list, err := repo.List("s-it-pos")
	if err != nil || len(list) != 2 {
		t.Fatalf("List: %v %d", err, len(list))
	}
	if list[0].ID != second.ID || list[0].DifferenceCents != 0 {
		t.Fatalf("orden/diferencia del segundo cierre: %+v", list[0])
	}
	if list[1].DifferenceCents != -2000 || list[1].Note != "faltó vuelto IT" {
		t.Fatalf("diferencia del primer cierre: %+v", list[1])
	}
	if list[1].OpenedAt.Location() != time.UTC {
		t.Fatalf("opened_at debe volver en UTC, dio %v", list[1].OpenedAt.Location())
	}
}

// TestMigrations_idempotent_IT: el runner del boot re-aplica TODO en cada
// arranque. Si una migración no fuera re-ejecutable, el API no levantaría.
func TestMigrations_idempotent_IT(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()
	itdb.ApplyMigrations(t, pool) // ya aplica dos veces; un tercer pase por si acaso
	itdb.ApplyMigrations(t, pool)

	ctx := context.Background()
	for _, tc := range []struct{ kind, name string }{
		{"table", "services"},
		{"table", "sales"},
		{"table", "sale_items"},
		{"table", "cash_closes"},
		{"index", "services_active_name_idx"},
		{"index", "sales_salon_created_idx"},
		{"index", "cash_closes_salon_closed_idx"},
	} {
		var found bool
		switch tc.kind {
		case "table":
			err := pool.QueryRow(ctx, `SELECT true FROM information_schema.tables WHERE table_name=$1`, tc.name).Scan(&found)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("consultar tabla %s: %v", tc.name, err)
			}
		case "index":
			err := pool.QueryRow(ctx, `SELECT true FROM pg_indexes WHERE indexname=$1`, tc.name).Scan(&found)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("consultar índice %s: %v", tc.name, err)
			}
		}
		if !found {
			t.Fatalf("falta %s %q después de migrar", tc.kind, tc.name)
		}
	}
}

// TestDashboard_IT valida el SQL de los agregados del panel: minutos reservados
// (EXTRACT/SUM en numeric -> int) y capacidad instalada. Es el único camino que
// no pasa por los fakes.
func TestDashboard_IT(t *testing.T) {
	pool, _ := posFixtures(t)
	bookings := bookingspostgres.Bookings{Pool: pool}
	mems := pgstore.Memberships{Pool: pool}

	if err := mems.Save(domain.Membership{UserID: "u-it-pos", SalonID: "s-it-pos", Role: domain.RoleProfessional}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	if n, err := mems.CountProfessionals("s-it-pos"); err != nil || n != 1 {
		t.Fatalf("profesionales = 1, dio %d (%v)", n, err)
	}
	if n, err := mems.CountProfessionals("s-it-pos2"); err != nil || n != 0 {
		t.Fatalf("otro salón debe tener 0, dio %d (%v)", n, err)
	}

	// B-18: no se agendan turnos en el pasado, así que el día de prueba es
	// futuro (relativo a "hoy", no una fecha fija que se vence).
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(48 * time.Hour)
	mk := func(start, end time.Time, status domain.BookingStatus) domain.Booking {
		b, err := domain.NewBooking("s-it-pos", "u-it-pos", "Cliente IT", "", start, end)
		if err != nil {
			t.Fatalf("booking: %v", err)
		}
		b.Status = status
		if err := bookings.Save(b); err != nil {
			t.Fatalf("guardar booking: %v", err)
		}
		return b
	}
	mk(day.Add(9*time.Hour), day.Add(11*time.Hour), domain.BookingBooked)     // 120 min
	mk(day.Add(11*time.Hour), day.Add(12*time.Hour), domain.BookingDone)      // 60 min
	mk(day.Add(15*time.Hour), day.Add(16*time.Hour), domain.BookingCancelled) // no cuenta

	count, minutes, err := bookings.StatsRange("s-it-pos", day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("StatsRange: %v", err)
	}
	if count != 2 || minutes != 180 {
		t.Fatalf("esperaba 2 turnos / 180 min, dio %d / %d", count, minutes)
	}
	// Ventana de otro día y otro salón: cero.
	if c, m, _ := bookings.StatsRange("s-it-pos", day.Add(24*time.Hour), day.Add(48*time.Hour)); c != 0 || m != 0 {
		t.Fatalf("otro día debe dar 0/0, dio %d/%d", c, m)
	}
	if c, _, _ := bookings.StatsRange("s-it-pos2", day, day.Add(24*time.Hour)); c != 0 {
		t.Fatalf("otro salón debe dar 0 turnos, dio %d", c)
	}
}
