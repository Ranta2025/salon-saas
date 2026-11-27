// Integración contra Postgres real de la nómina (F3.2, S-F3.2-1..4). Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run Payroll -v
//
// Lo que se prueba acá SOLO se prueba contra la DB: el agregado SQL de la
// comisión (Σ qty × precio × pct con la foto o la tarifa actual), los bordes de
// la ventana en la zona del salón, una fila legacy sin foto, un profesional dado
// de baja que conserva sus ventas, dos salones, y los CHECK/FK del modelo de
// pago de la migración 024.
package pgstore

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

// payrollFixtures deja el escenario de la nómina limpio: dos salones en la zona
// de CDMX, un dueño, dos profesionales en el salón 1 (uno se da de baja después)
// y una profesional en el salón 2. Ids fijos para que el test sea re-ejecutable.
func payrollFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)

	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-pay%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-pay%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pay-owner','Dueña IT','pay-owner@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pay-a','Ana IT','pay-ana@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pay-b','Beto IT','pay-beto@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pay-c','Carla IT','pay-carla@test.com','x')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-pay','s-it-pay','Salón Nómina IT','America/Mexico_City')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-pay2','s-it-pay2','Salón Nómina IT 2','America/Mexico_City')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mems := Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-pay-owner", SalonID: "s-it-pay", Role: domain.RoleOwner},
		{UserID: "u-it-pay-a", SalonID: "s-it-pay", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-it-pay-b", SalonID: "s-it-pay", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-it-pay-c", SalonID: "s-it-pay2", Role: domain.RoleProfessional, Attends: true},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	return pool, ctx
}

// paySaleItem y insertPaySale son los helpers compartidos de itdb, con el
// nombre que ya usaban los IT de este paquete.
type paySaleItem = itdb.PaySaleItem

func insertPaySale(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	id, salonID, profID string, at time.Time, total int, items ...paySaleItem) {
	t.Helper()
	itdb.InsertPaySale(t, ctx, pool, id, salonID, profID, at, total, items...)
}

func intPtr(v int) *int { return itdb.IntPtr(v) }

// payrollCatalog crea el catálogo de los dos salones y devuelve los servicios.
func payrollCatalog(t *testing.T, pool *pgxpool.Pool) (corte, color, gratis domain.Service) {
	t.Helper()
	services := Services{Pool: pool}
	corte, _ = domain.NewService("s-it-pay", "Corte IT", 45, 150000, 10)
	color, _ = domain.NewService("s-it-pay", "Color IT", 90, 300000, 25)
	gratis, _ = domain.NewService("s-it-pay", "Cortesía IT", 30, 0, 0)
	otro, _ := domain.NewService("s-it-pay2", "Corte Ajeno IT", 45, 150000, 90)
	for _, s := range []domain.Service{corte, color, gratis, otro} {
		if err := services.Save(s); err != nil {
			t.Fatalf("catálogo: %v", err)
		}
	}
	return corte, color, gratis
}

// windowOfMonth deriva el período con el dominio (ya probado aparte) y devuelve
// la ventana en instantes, comprobando que es la medianoche LOCAL del salón.
func windowOfMonth(t *testing.T, pool *pgxpool.Pool, salonID, kind, date string) (time.Time, time.Time) {
	t.Helper()
	settings, err := Salons{Pool: pool}.Settings(salonID)
	if err != nil {
		t.Fatalf("settings del salón: %v", err)
	}
	loc, err := settings.Location()
	if err != nil {
		t.Fatalf("zona del salón: %v", err)
	}
	p, err := domain.NewPayPeriod(kind, date, loc)
	if err != nil {
		t.Fatalf("período: %v", err)
	}
	return p.Window()
}

// El agregado de la comisión contra valores calculados A MANO, con una fila
// legacy (NULL), un profesional dado de baja con ventas, los bordes de la
// ventana y dos salones.
func TestPayrollIT_agregados_por_profesional(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, color, gratis := payrollCatalog(t, pool)
	svc2, _ := domain.NewService("s-it-pay2", "Corte Ajeno B", 45, 999999, 90)
	if err := (Services{Pool: pool}).Save(svc2); err != nil {
		t.Fatalf("catálogo del otro salón: %v", err)
	}

	from, to := windowOfMonth(t, pool, "s-it-pay", "month", "2026-03-10")
	// CDMX es UTC-6 todo el año: marzo es [1/3 00:00 local, 1/4 00:00 local) =
	// [2026-03-01T06:00Z, 2026-04-01T06:00Z). Hand-computed, no derivado.
	if want := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Fatalf("inicio de la ventana = %s, esperaba %s", from.UTC(), want)
	}
	if want := time.Date(2026, 4, 1, 6, 0, 0, 0, time.UTC); !to.Equal(want) {
		t.Fatalf("fin de la ventana = %s, esperaba %s", to.UTC(), want)
	}

	// Ana (salón 1):
	//  (1) 3 × 333 al 10% de FOTO: 9990 -> 99 centavos. Venta EXACTAMENTE en el
	//      inicio de la ventana: entra.
	insertPaySale(t, ctx, pool, "sale-pay-1", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC), 999,
		paySaleItem{ServiceID: corte.ID, Qty: 3, PriceCent: 333, Pct: intPtr(10)})
	//  (2) 1 × 1000 al 30% (foto) + 2 × 500 con la foto en NULL (legacy) -> la
	//      tarifa ACTUAL del corte es 10: 30000 + 10000 = 40000 -> 400 centavos y
	//      la fila queda estimada.
	insertPaySale(t, ctx, pool, "sale-pay-2", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 3, 31, 23, 59, 0, 0, time.UTC), 2000,
		paySaleItem{ServiceID: color.ID, Qty: 1, PriceCent: 1000, Pct: intPtr(30)},
		paySaleItem{ServiceID: corte.ID, Qty: 2, PriceCent: 500, Pct: nil})
	//  (3) 1 × 500 al 0%: 0 centavos, pero cuenta como venta.
	insertPaySale(t, ctx, pool, "sale-pay-3", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 3, 15, 18, 0, 0, 0, time.UTC), 500,
		paySaleItem{ServiceID: gratis.ID, Qty: 1, PriceCent: 500, Pct: intPtr(0)})
	// Bordes que NO entran: exactamente el fin de la ventana y antes del inicio.
	insertPaySale(t, ctx, pool, "sale-pay-4", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 4, 1, 6, 0, 0, 0, time.UTC), 9999,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 9999, Pct: intPtr(10)})
	insertPaySale(t, ctx, pool, "sale-pay-5", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 2, 28, 23, 0, 0, 0, time.UTC), 5000,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 5000, Pct: intPtr(10)})
	// Beto: 1 × 2000 al 10% -> 20000 -> 200 centavos. Después se da de baja: sus
	// ventas siguen contando.
	insertPaySale(t, ctx, pool, "sale-pay-6", "s-it-pay", "u-it-pay-b",
		time.Date(2026, 3, 10, 18, 0, 0, 0, time.UTC), 2000,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 2000, Pct: intPtr(10)})
	// El salón 2 tiene su propia venta grande: no puede aparecer en el salón 1.
	insertPaySale(t, ctx, pool, "sale-pay-7", "s-it-pay2", "u-it-pay-c",
		time.Date(2026, 3, 15, 18, 0, 0, 0, time.UTC), 999999,
		paySaleItem{ServiceID: svc2.ID, Qty: 1, PriceCent: 999999, Pct: intPtr(90)})

	// Beto dado de baja DESPUÉS de vender (ended_at): el histórico no se toca.
	if _, err := pool.Exec(ctx,
		`UPDATE memberships SET ended_at=$1 WHERE user_id='u-it-pay-b' AND salon_id='s-it-pay'`,
		time.Now().UTC()); err != nil {
		t.Fatalf("baja: %v", err)
	}

	repo := PayrollSales{Pool: pool}
	totals, err := repo.CommissionTotals("s-it-pay", from, to)
	if err != nil {
		t.Fatalf("agregado: %v", err)
	}
	if len(totals) != 2 {
		t.Fatalf("esperaba Ana y Beto, dio %d: %+v", len(totals), totals)
	}
	byUser := map[string]domain.PayrollSalesTotal{}
	for _, tt := range totals {
		byUser[tt.UserID] = tt
	}
	ana := byUser["u-it-pay-a"]
	// 999 + 2000 + 500 = 3499; 3 ventas; 9990 + 40000 + 0 = 49990 -> 499.
	if ana.Name != "Ana IT" || ana.SalesCount != 3 || ana.SalesTotalCents != 3499 ||
		ana.CommissionBase != 49990 || !ana.Estimated || ana.CommissionCents() != 499 {
		t.Fatalf("Ana: %+v (comisión %d)", ana, ana.CommissionCents())
	}
	beto := byUser["u-it-pay-b"]
	if beto.Name != "Beto IT" || beto.SalesCount != 1 || beto.SalesTotalCents != 2000 ||
		beto.CommissionBase != 20000 || beto.Estimated || beto.CommissionCents() != 200 {
		t.Fatalf("Beto (dado de baja): %+v", beto)
	}

	// «Mis comisiones»: la consulta de UNA persona da lo mismo que su fila.
	one, err := repo.CommissionTotalFor("s-it-pay", "u-it-pay-a", from, to)
	if err != nil {
		t.Fatalf("agregado de una persona: %v", err)
	}
	if one.SalesCount != 3 || one.CommissionBase != 49990 || one.Name != "Ana IT" {
		t.Fatalf("agregado de Ana por persona: %+v", one)
	}
	// Una persona sin ventas devuelve el total en cero, no un error.
	zero, err := repo.CommissionTotalFor("s-it-pay", "u-it-pay-owner", from, to)
	if err != nil {
		t.Fatalf("agregado sin ventas: %v", err)
	}
	if zero.SalesCount != 0 || zero.CommissionBase != 0 || zero.SalesTotalCents != 0 {
		t.Fatalf("sin ventas debe dar cero: %+v", zero)
	}

	// Aislamiento por tenant: el salón 2 solo trae a su profesional.
	other, err := repo.CommissionTotals("s-it-pay2", from, to)
	if err != nil {
		t.Fatalf("agregado del salón 2: %v", err)
	}
	if len(other) != 1 || other[0].UserID != "u-it-pay-c" || other[0].CommissionBase != 89_999_910 {
		t.Fatalf("salón 2: %+v", other)
	}
	if other[0].CommissionCents() != 899_999 {
		t.Fatalf("comisión del salón 2 = %d, esperaba 899999", other[0].CommissionCents())
	}
}

// La FOTO manda: editar la tarifa del catálogo después no cambia las líneas con
// foto y sí cambia la estimada (que es lo que la marca `estimated` avisa).
func TestPayrollIT_foto_vs_estimado(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	from, to := windowOfMonth(t, pool, "s-it-pay", "month", "2026-03-10")

	// 1 × 1000 al 20% (foto) + 1 × 1000 sin foto.
	insertPaySale(t, ctx, pool, "sale-pay-foto", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 3, 10, 18, 0, 0, 0, time.UTC), 2000,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 1000, Pct: intPtr(20)},
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 1000, Pct: nil})

	repo := PayrollSales{Pool: pool}
	before, err := repo.CommissionTotals("s-it-pay", from, to)
	if err != nil {
		t.Fatalf("agregado: %v", err)
	}
	// 20000 (foto 20) + 10000 (tarifa actual 10) = 30000 -> 300.
	if len(before) != 1 || before[0].CommissionBase != 30000 || !before[0].Estimated {
		t.Fatalf("antes de editar el catálogo: %+v", before)
	}

	// El dueño sube la tarifa del corte al 60%: la línea con foto no se mueve.
	up, _ := corte.WithDetails(corte.Name, corte.DurationMin, corte.PriceCents, 60, true)
	if err := (Services{Pool: pool}).Save(up); err != nil {
		t.Fatalf("editar catálogo: %v", err)
	}
	after, err := repo.CommissionTotals("s-it-pay", from, to)
	if err != nil {
		t.Fatalf("agregado después: %v", err)
	}
	// 20000 (foto 20, sin cambios) + 60000 (estimado con la tarifa nueva) = 80000.
	if len(after) != 1 || after[0].CommissionBase != 80000 || !after[0].Estimated {
		t.Fatalf("después de editar el catálogo: %+v", after)
	}
}

// La migración 024: columnas, CHECKs, FK compuesta a memberships y el default de
// D3. También que reaplicarla sobre datos existentes no cambia nada.
func TestMemberPayIT_modelo_defaults_y_constraints(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	repo := PayModels{Pool: pool}

	// Sin fila: solo comisión (0, month, true), con nombre, rol y estado.
	got, err := repo.FindPayModel("s-it-pay", "u-it-pay-a")
	if err != nil {
		t.Fatalf("modelo por defecto: %v", err)
	}
	if got.FixedCents != 0 || got.FixedPeriod != domain.PayMonth || !got.CommissionEnabled {
		t.Fatalf("el default debe ser (0, month, true): %+v", got)
	}
	if got.Name != "Ana IT" || got.Role != domain.RoleProfessional || !got.Active {
		t.Fatalf("el modelo debe traer a la persona: %+v", got)
	}

	// Guardar y releer; el upsert actualiza sin duplicar la fila.
	model, err := domain.NewMemberPay("s-it-pay", "u-it-pay-a", 150000, "fortnight", false)
	if err != nil {
		t.Fatalf("modelo: %v", err)
	}
	if err := repo.SavePayModel(model); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	if got, _ := repo.FindPayModel("s-it-pay", "u-it-pay-a"); got.FixedCents != 150000 ||
		got.FixedPeriod != domain.PayFortnight || got.CommissionEnabled {
		t.Fatalf("no quedó guardado: %+v", got)
	}
	again, _ := domain.NewMemberPay("s-it-pay", "u-it-pay-a", 5000, "week", true)
	if err := repo.SavePayModel(again); err != nil {
		t.Fatalf("segundo guardado: %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM member_pay WHERE salon_id='s-it-pay' AND user_id='u-it-pay-a'`).Scan(&rows); err != nil {
		t.Fatalf("contar filas: %v", err)
	}
	if rows != 1 {
		t.Fatalf("el upsert no debe duplicar: hay %d filas", rows)
	}
	if got, _ := repo.FindPayModel("s-it-pay", "u-it-pay-a"); got.FixedCents != 5000 ||
		got.FixedPeriod != domain.PayWeek || !got.CommissionEnabled {
		t.Fatalf("el upsert no actualizó: %+v", got)
	}

	// Una persona de otro salón no existe para este salón.
	if _, err := repo.FindPayModel("s-it-pay", "u-it-pay-c"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("persona de otro salón: esperaba NotFound, dio %v", err)
	}
	// Y la FK compuesta impide guardarle un modelo acá.
	wrong, _ := domain.NewMemberPay("s-it-pay", "u-it-pay-c", 1000, "month", true)
	if err := repo.SavePayModel(wrong); err == nil {
		t.Fatal("la FK compuesta debe rechazar a quien no es miembro del salón")
	}

	// CHECKs de la migración 024: monto negativo, monto arriba del máximo y
	// período desconocido no entran ni por SQL directo.
	for _, c := range []struct{ name, q string }{
		{"monto negativo", `INSERT INTO member_pay(salon_id,user_id,fixed_cents,fixed_period,commission_enabled)
			VALUES('s-it-pay','u-it-pay-b',-1,'month',true)`},
		{"monto arriba del máximo", `INSERT INTO member_pay(salon_id,user_id,fixed_cents,fixed_period,commission_enabled)
			VALUES('s-it-pay','u-it-pay-b',2000000001,'month',true)`},
		{"período desconocido", `INSERT INTO member_pay(salon_id,user_id,fixed_cents,fixed_period,commission_enabled)
			VALUES('s-it-pay','u-it-pay-b',100,'day',true)`},
	} {
		if _, err := pool.Exec(ctx, c.q); err == nil {
			t.Fatalf("%s debe ser rechazado por la DB", c.name)
		}
	}

	// Listado: todos los miembros del salón (activos y dados de baja) y nadie del
	// otro salón. La baja conserva su modelo.
	if _, err := pool.Exec(ctx,
		`UPDATE memberships SET ended_at=now() WHERE user_id='u-it-pay-b' AND salon_id='s-it-pay'`); err != nil {
		t.Fatalf("baja: %v", err)
	}
	beto, _ := domain.NewMemberPay("s-it-pay", "u-it-pay-b", 90000, "month", false)
	if err := repo.SavePayModel(beto); err != nil {
		t.Fatalf("modelo de la baja: %v", err)
	}
	list, err := repo.ListPayModels("s-it-pay")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("esperaba 3 miembros (dueña, Ana y Beto), dio %d: %+v", len(list), list)
	}
	seen := map[string]domain.MemberPayView{}
	for _, v := range list {
		seen[v.UserID] = v
	}
	if _, ok := seen["u-it-pay-c"]; ok {
		t.Fatal("el listado no puede traer gente de otro salón")
	}
	if v := seen["u-it-pay-b"]; v.Active || v.FixedCents != 90000 || v.CommissionEnabled {
		t.Fatalf("la baja debe salir con su modelo y active=false: %+v", v)
	}
	if v := seen["u-it-pay-a"]; v.FixedCents != 5000 || v.FixedPeriod != domain.PayWeek {
		t.Fatalf("Ana debe salir con su modelo: %+v", v)
	}
	// El orden es por nombre, como la pantalla.
	if list[0].Name != "Ana IT" || list[1].Name != "Beto IT" || list[2].Name != "Dueña IT" {
		t.Fatalf("orden por nombre: %+v", list)
	}

	// Reaplicar la 024 sobre la base CON datos: no falla y no cambia nada.
	sql, err := os.ReadFile("../../../migrations/024_member_pay.sql")
	if err != nil {
		t.Fatalf("leer 024: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("reaplicar 024: %v", err)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM member_pay WHERE salon_id='s-it-pay'`).Scan(&after); err != nil {
		t.Fatalf("contar después de reaplicar: %v", err)
	}
	if after != 2 {
		t.Fatalf("reaplicar la 024 cambió los datos: %d filas", after)
	}
}
