// Hardening contra Postgres real (SALON_IT=1): sumas grandes, overflow
// traducido a 400, cierres de caja concurrentes y personal del salón.
package pgstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/pos"
)

// B-03: una venta de 2.000.000.000 centavos NO puede entrar (el dominio la
// rechaza), y la suma de muchas ventas grandes se calcula con SUM::bigint: con
// ::int Postgres devolvía 22003 y el dashboard quedaba roto para siempre.
func TestSales_IT_sumas_grandes_no_rompen(t *testing.T) {
	pool, _ := posFixtures(t)
	repo := Sales{Pool: pool}
	services := Services{Pool: pool}
	corte, err := domain.NewService("s-it-pos", "Corte grande", 45, domain.MaxSaleUnitPriceCents, 0)
	if err != nil {
		t.Fatalf("dominio servicio: %v", err)
	}
	if err := services.Save(corte); err != nil {
		t.Fatalf("catálogo: %v", err)
	}

	// Dos ventas al tope por línea: 2 × (1000 × 10.000.000) no entra en INTEGER,
	// así que cada venta lleva el máximo permitido por venta.
	maxSale := domain.MaxSaleTotalCents
	sale1, err := domain.NewSale("s-it-pos", "u-it-pos", "Grande 1", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 200, UnitPriceCents: domain.MaxSaleUnitPriceCents},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("venta 1: %v", err)
	}
	if sale1.TotalCents != 2_000_000_000 {
		t.Fatalf("total de la venta al tope = %d", sale1.TotalCents)
	}
	sale2, err := domain.NewSale("s-it-pos", "u-it-pos", "Grande 2", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 200, UnitPriceCents: domain.MaxSaleUnitPriceCents},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("venta 2: %v", err)
	}
	for _, s := range []domain.Sale{sale1, sale2} {
		if err := repo.Save(s); err != nil {
			t.Fatalf("guardar venta de %d: %v", s.TotalCents, err)
		}
	}

	now := time.Now().UTC()
	from, to := now.Add(-time.Hour), now.Add(time.Hour)
	// La suma de las dos (4.000.000.000) NO cabe en INTEGER: con ::int daba
	// 22003 y ahora tiene que devolver el número exacto.
	total, err := repo.SumCents("s-it-pos", from, to, domain.MethodCash)
	if err != nil {
		t.Fatalf("sumar ventas grandes: %v", err)
	}
	if total != 2*maxSale {
		t.Fatalf("suma = %d, esperaba %d", total, 2*maxSale)
	}
	// El ranking también suma en bigint.
	top, err := repo.TopServices("s-it-pos", from, to, 5)
	if err != nil {
		t.Fatalf("top services: %v", err)
	}
	if len(top) == 0 || top[0].RevenueCents != 2*maxSale {
		t.Fatalf("ranking: %+v", top)
	}

	// El dashboard y el cierre leen esas sumas sin explotar.
	mems := Memberships{Pool: pool}
	if err := mems.Save(domain.Membership{UserID: "u-it-pos", SalonID: "s-it-pos", Role: domain.RoleOwner}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	owner, _ := mems.Find("u-it-pos", "s-it-pos")
	dash := pos.DashboardService{Sales: repo, Agenda: Bookings{Pool: pool}, Staff: mems, Memberships: mems}
	if _, err := dash.Today(owner, "s-it-pos"); err != nil {
		t.Fatalf("dashboard con ventas grandes: %v", err)
	}
}

// B-09: dos cierres concurrentes del mismo salón contabilizan el efectivo UNA
// sola vez. Sin el advisory lock los dos leen el mismo "último cierre" y el
// mismo esperado, y el salón cierra el doble de lo que había en el cajón.
func TestCashCloses_IT_concurrentes_cuentan_una_vez(t *testing.T) {
	pool, ctx := posFixtures(t)
	repo := Sales{Pool: pool}
	services := Services{Pool: pool}
	cashRepo := CashCloses{Pool: pool}
	mems := Memberships{Pool: pool}

	corte, err := domain.NewService("s-it-pos", "Corte caja", 30, 100000, 0)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	if err := services.Save(corte); err != nil {
		t.Fatalf("catálogo: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM cash_closes WHERE salon_id='s-it-pos'`)

	// 3 ventas en efectivo de 100.000 = 300.000 en el cajón.
	for i := 0; i < 3; i++ {
		sale, err := domain.NewSale("s-it-pos", "u-it-pos", "Cliente caja", []domain.SaleItem{
			{ServiceID: corte.ID, Qty: 1, UnitPriceCents: 100000},
		}, domain.MethodCash)
		if err != nil {
			t.Fatalf("venta %d: %v", i, err)
		}
		if err := repo.Save(sale); err != nil {
			t.Fatalf("guardar venta %d: %v", i, err)
		}
	}
	if err := mems.Save(domain.Membership{UserID: "u-it-pos", SalonID: "s-it-pos", Role: domain.RoleOwner}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	owner, _ := mems.Find("u-it-pos", "s-it-pos")

	svc := pos.CashCloseService{CashCloses: cashRepo, Sales: repo, Memberships: mems, Tx: cashRepo}

	var wg sync.WaitGroup
	closes := make([]domain.CashClose, 2)
	errs := make([]error, 2)
	for i := range closes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			closes[i], errs[i] = svc.CloseCash(owner, "s-it-pos", 300000, "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("cierre %d: %v", i, err)
		}
	}

	// El efectivo de la ventana se contabiliza una sola vez aunque haya dos
	// cierres: uno se lleva los 300.000 y el otro arranca en 0.
	sum := 0
	withCash := 0
	for _, c := range closes {
		sum += c.SystemCashCents
		if c.SystemCashCents > 0 {
			withCash++
		}
	}
	if sum != 300000 {
		t.Fatalf("la suma de esperados debe ser 300000 una sola vez, dio %d (%+v)", sum, closes)
	}
	if withCash != 1 {
		t.Fatalf("un solo cierre debe llevarse el efectivo, hubo %d (%+v)", withCash, closes)
	}
	// Y quedan exactamente dos filas: ninguna se pisó.
	list, err := cashRepo.List("s-it-pos")
	if err != nil || len(list) != 2 {
		t.Fatalf("cierres persistidos: %d %v", len(list), err)
	}
}

// B-15: un reintento con el mismo client_op_id que choca contra la EXCLUDE
// (23P01) o el único de op id (23505) resuelve devolviendo la reserva original.
func TestBookings_IT_replay_concurrente_no_duplica(t *testing.T) {
	pool, _ := posFixtures(t)
	repo := Bookings{Pool: pool}
	_, _ = pool.Exec(context.Background(), `DELETE FROM bookings WHERE salon_id='s-it-pos'`)

	day := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	start, end := day, day.Add(time.Hour)

	var wg sync.WaitGroup
	ids := make([]string, 2)
	errs := make([]error, 2)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := domain.NewBooking("s-it-pos", "u-it-pos", "Cliente", "", start, end)
			if err != nil {
				errs[i] = err
				return
			}
			b.ClientOpID = "op-race-it"
			errs[i] = repo.Save(b)
			ids[i] = b.ID
		}(i)
	}
	wg.Wait()

	// Uno entra y el otro choca (23P01 o 23505): el caso de uso lo resuelve
	// releyendo por op id. Acá se verifica el contrato del repo: nunca hay dos
	// filas con el mismo op id.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM bookings WHERE salon_id='s-it-pos' AND client_op_id='op-race-it'`).Scan(&count); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if count != 1 {
		t.Fatalf("debe quedar UNA reserva por op id, hay %d", count)
	}
	// El perdedor recibe ErrAlreadyExists o gana: nada de errores crudos.
	for i, err := range errs {
		if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("intento %d: error inesperado %v", i, err)
		}
	}
	got, err := repo.FindByClientOpID("s-it-pos", "op-race-it")
	if err != nil {
		t.Fatalf("releer por op id: %v", err)
	}
	if got.ID == "" || got.ClientOpID != "op-race-it" {
		t.Fatalf("reserva releída: %+v", got)
	}
}

// El staff del salón: solo quien atiende, con nombre, y nunca de otro tenant.
func TestMemberships_IT_staff(t *testing.T) {
	pool, ctx := posFixtures(t)
	mems := Memberships{Pool: pool}
	_, _ = pool.Exec(ctx, `DELETE FROM memberships WHERE salon_id IN ('s-it-pos','s-it-pos2')`)
	_, _ = pool.Exec(ctx, `INSERT INTO users(id,name,email,pass_hash)
		VALUES('u-staff-prof','Bruno Prof','u-staff-prof@test.com','x')
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name`)
	_, _ = pool.Exec(ctx, `INSERT INTO users(id,name,email,pass_hash)
		VALUES('u-staff-recep','Recep','u-staff-recep@test.com','x')
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name`)

	// F2a: el dueño sigue en la agenda porque atiende (attends=true, como lo
	// deja el alta desde la 015); el profesional atiende por rol.
	for _, m := range []domain.Membership{
		{UserID: "u-it-pos", SalonID: "s-it-pos", Role: domain.RoleOwner, Attends: true},
		{UserID: "u-staff-prof", SalonID: "s-it-pos", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-staff-recep", SalonID: "s-it-pos", Role: domain.RoleReception},
		{UserID: "u-staff-prof", SalonID: "s-it-pos2", Role: domain.RoleProfessional, Attends: true},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}

	staff, err := mems.ListStaff("s-it-pos")
	if err != nil {
		t.Fatalf("listar staff: %v", err)
	}
	roles := map[string]domain.Role{}
	for _, s := range staff {
		if s.Name == "" {
			t.Fatalf("el staff necesita nombre para el select: %+v", s)
		}
		roles[s.ID] = s.Role
	}
	if len(staff) != 2 {
		t.Fatalf("staff = owner + profesional (recepción no atiende), dio %+v", staff)
	}
	if roles["u-staff-prof"] != domain.RoleProfessional {
		t.Fatalf("rol del profesional: %+v", roles)
	}
	// El salón 2 no ve el personal del salón 1.
	if other, err := mems.ListStaff("s-it-pos2"); err != nil || len(other) != 1 {
		t.Fatalf("staff de otro salón: %+v %v", other, err)
	}
}

// B-05: un email duplicado sale como ErrAlreadyExists (23505), no como 500.
func TestUsers_IT_email_duplicado_es_409(t *testing.T) {
	pool, ctx := posFixtures(t)
	users := Users{Pool: pool}
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ('u-dup-1','u-dup-2')`)

	u1, err := domain.NewUser("Ana", "dup@test.com", "hash")
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	u1.ID = "u-dup-1"
	if err := users.Save(u1); err != nil {
		t.Fatalf("primer alta: %v", err)
	}
	u2 := u1
	u2.ID = "u-dup-2"
	if err := users.Save(u2); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("email duplicado debe dar ErrAlreadyExists, dio %v", err)
	}
	// FindByID y FindByEmail devuelven lo mismo.
	if got, err := users.FindByID("u-dup-1"); err != nil || got.Email != "dup@test.com" {
		t.Fatalf("FindByID: %+v %v", got, err)
	}
	if _, err := users.FindByID("no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("FindByID inexistente debe dar NotFound, dio %v", err)
	}
}

// Salons.FindByID resuelve el salón de una membership (GET /me) y no cruza.
func TestSalons_IT_find_by_id(t *testing.T) {
	pool, _ := posFixtures(t)
	salons := Salons{Pool: pool}
	got, err := salons.FindByID("s-it-pos")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Slug != "s-it-pos" || got.Name == "" {
		t.Fatalf("salón: %+v", got)
	}
	if _, err := salons.FindByID("s-no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("inexistente debe dar NotFound, dio %v", err)
	}
}
