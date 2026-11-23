// Integración contra Postgres real del reporte de ganancia neta (F3b, D1/D2/D6).
// Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run ProfitIT -v
//
// Lo que SOLO se prueba contra la DB: que las ventas y los pagos se agrupen por
// día LOCAL del salón (`AT TIME ZONE`) en los bordes de 23:59 y 00:00 de zonas
// con y sin horario de verano (incluidos el día de 23 h y el de 25 h), que una
// liquidación ABIERTA no sume y al pagarse sume en el día de `paid_at`, el salón
// sin movimientos (ceros, no un 500), el aislamiento por tenant y las lecturas
// concurrentes con altas y borrados de gastos.
package pgstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// profitFixtures deja los salones del reporte limpios: uno en Nueva York, uno en
// Auckland, uno sin movimientos y uno de OTRO tenant.
func profitFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)

	// Misma limpieza compartida que expensesFixtures: los salones primero (el
	// cascade se lleva ventas, gastos y liquidaciones) y después las cuentas.
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-exp%' OR id LIKE 's-it-profit%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-exp%' OR id LIKE 'u-it-profit%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-profit-owner','Dueña Profit','profit-owner@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-profit-pro','Ana Profit','profit-pro@test.com','x')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-profit-ny','s-it-profit-ny','Salón Profit NY','America/New_York')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-profit-auck','s-it-profit-auck','Salón Profit Auckland','Pacific/Auckland')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-profit-vacio','s-it-profit-vacio','Salón Profit Vacío','America/New_York')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-profit-otro','s-it-profit-otro','Salón Profit Ajeno','America/New_York')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mems := Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-profit-owner", SalonID: "s-it-profit-ny", Role: domain.RoleOwner},
		{UserID: "u-it-profit-owner", SalonID: "s-it-profit-auck", Role: domain.RoleOwner},
		{UserID: "u-it-profit-owner", SalonID: "s-it-profit-vacio", Role: domain.RoleOwner},
		{UserID: "u-it-profit-pro", SalonID: "s-it-profit-ny", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-it-profit-owner", SalonID: "s-it-profit-otro", Role: domain.RoleOwner},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	return pool, ctx
}

// insertProfitSale deja una venta con created_at EXACTO (sin ítems: el ingreso es
// el total de la venta).
func insertProfitSale(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	id, salonID, profID string, at time.Time, total int) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,created_at)
		 VALUES($1,$2,$3,'Clienta',$4,'cash',$5)`,
		id, salonID, profID, total, at.UTC()); err != nil {
		t.Fatalf("venta %s: %v", id, err)
	}
}

// insertProfitExpense deja un gasto con su día local y su categoría.
func insertProfitExpense(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	id, salonID, category, spentOn string, amount int) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO expenses(id,salon_id,category,amount_cents,spent_on,description,created_by)
		 VALUES($1,$2,$3,$4,$5::date,'',$6)`,
		id, salonID, category, amount, spentOn, "u-it-profit-owner"); err != nil {
		t.Fatalf("gasto %s: %v", id, err)
	}
}

// insertProfitRun deja una liquidación con UNA línea y el estado/paid_at dados.
// Las fechas del período son distintas en cada corrida para no chocar con la
// EXCLUDE por salón (la ventana del reporte NO las usa).
func insertProfitRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	id, salonID, from, to string, paidAt *time.Time, total int) {
	t.Helper()
	status := "open"
	if paidAt != nil {
		status = "paid"
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payroll_runs(id,salon_id,kind,from_date,to_date,status,created_by,paid_at,paid_by)
		 VALUES($1,$2,'week',$3::date,$4::date,$5,'u-it-profit-owner',$6,$7)`,
		id, salonID, from, to, status, paidAt, "u-it-profit-owner"); err != nil {
		t.Fatalf("corrida %s: %v", id, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payroll_lines(run_id,user_id,name_snapshot,total_cents)
		 VALUES($1,'u-it-profit-pro','Ana Profit',$2)`, id, total); err != nil {
		t.Fatalf("línea %s: %v", id, err)
	}
}

// profitExpense arma un gasto del salón de estos tests (creador de SU fixture).
func profitExpense(id, salonID, category, spentOn string, amount int) domain.Expense {
	return domain.Expense{
		ID: id, SalonID: salonID, Category: domain.ExpenseCategory(category),
		AmountCents: amount, SpentOn: spentOn, Description: "gasto " + id,
		CreatedBy: "u-it-profit-owner", CreatedAt: time.Now().UTC(),
	}
}

func atUTC(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

// dayOf busca un día del reporte por fecha.
func dayOf(t *testing.T, data domain.ProfitData, date string) (int, int, int) {
	t.Helper()
	return data.RevenueByDay[date], data.ExpensesByDay[date], data.PayrollByDay[date]
}

// El día es el LOCAL del salón: una venta a las 23:59 del 15/6 (NY) cae el 15 y
// una a las 00:00 del 16/6 cae el 16, aunque en UTC sean el mismo día.
func TestProfitIT_bordes_de_dia_local_ny_sin_dst(t *testing.T) {
	pool, ctx := profitFixtures(t)
	repo := Profit{Pool: pool}
	salon := "s-it-profit-ny"
	// Junio en NY es EDT (UTC-4): 15/6 00:00 local = 04:00Z, 23:59 local =
	// 16/6 03:59Z; 16/6 00:00 local = 04:00Z.
	insertProfitSale(t, ctx, pool, "sale-profit-ny-1", salon, "u-it-profit-pro",
		atUTC(2026, 6, 16, 3, 59), 1000)
	insertProfitSale(t, ctx, pool, "sale-profit-ny-2", salon, "u-it-profit-pro",
		atUTC(2026, 6, 16, 4, 0), 2000)
	insertProfitSale(t, ctx, pool, "sale-profit-ny-3", salon, "u-it-profit-pro",
		atUTC(2026, 6, 15, 4, 0), 3000)
	insertProfitSale(t, ctx, pool, "sale-profit-ny-4", salon, "u-it-profit-pro",
		atUTC(2026, 6, 15, 3, 59), 4000)
	insertProfitExpense(t, ctx, pool, "e-profit-ny-1", salon, "rent", "2026-06-15", 500)
	insertProfitExpense(t, ctx, pool, "e-profit-ny-2", salon, "supplies", "2026-06-16", 700)
	// Pagada el 15/6 a las 23:59 local (16/6 03:59Z) y el 16/6 a las 00:00 local.
	paid15 := atUTC(2026, 6, 16, 3, 59)
	paid16 := atUTC(2026, 6, 16, 4, 0)
	insertProfitRun(t, ctx, pool, "run-profit-ny-1", salon, "2026-01-05", "2026-01-11", &paid15, 200)
	insertProfitRun(t, ctx, pool, "run-profit-ny-2", salon, "2026-01-12", "2026-01-18", &paid16, 300)
	// Una ABIERTA: no suma aunque tenga líneas.
	insertProfitRun(t, ctx, pool, "run-profit-ny-open", salon, "2026-01-19", "2026-01-25", nil, 999)

	loc := mustLoadLoc(t, "America/New_York")
	p, err := domain.NewProfitPeriod("day", "2026-06-15", time.Time{}, loc)
	if err != nil {
		t.Fatalf("período: %v", err)
	}
	from, to := p.Window()
	data, err := repo.DailyProfit(salon, "America/New_York", from, to)
	if err != nil {
		t.Fatalf("reporte: %v", err)
	}
	// Solo el 15/6, con la nómina PAGADA (200) y sin la abierta.
	if len(data.RevenueByDay) != 1 || data.RevenueByDay["2026-06-15"] != 4000 {
		t.Fatalf("ingresos del 15/6 = %v", data.RevenueByDay)
	}
	if _, _, payroll := dayOf(t, data, "2026-06-15"); payroll != 200 {
		t.Fatalf("nómina del 15/6 = %d, esperaba 200 (la abierta no cuenta)", payroll)
	}
	if revenue, expenses, payroll := dayOf(t, data, "2026-06-15"); revenue != 4000 || expenses != 500 || payroll != 200 {
		t.Fatalf("día 15/6 = %d/%d/%d", revenue, expenses, payroll)
	}

	// Día 16/6: las ventas de 00:00 en adelante, su gasto y su nómina.
	p, _ = domain.NewProfitPeriod("day", "2026-06-16", time.Time{}, loc)
	from, to = p.Window()
	data, err = repo.DailyProfit(salon, "America/New_York", from, to)
	if err != nil {
		t.Fatalf("reporte del 16: %v", err)
	}
	if revenue, expenses, payroll := dayOf(t, data, "2026-06-16"); revenue != 2000 || expenses != 700 || payroll != 300 {
		t.Fatalf("día 16/6 = %d/%d/%d", revenue, expenses, payroll)
	}

	// La semana 15..21 rellena el reporte y la suma de días da los totales.
	p, _ = domain.NewProfitPeriod("week", "2026-06-15", time.Time{}, loc)
	from, to = p.Window()
	data, err = repo.DailyProfit(salon, "America/New_York", from, to)
	if err != nil {
		t.Fatalf("reporte de la semana: %v", err)
	}
	report := domain.NewProfitReport(p, data)
	if len(report.Days) != 7 {
		t.Fatalf("días = %d", len(report.Days))
	}
	if report.Totals.RevenueCents != 6000 || report.Totals.ExpensesCents != 1200 ||
		report.Totals.PayrollCents != 500 || report.Totals.NetCents != 4300 {
		t.Fatalf("totales = %+v", report.Totals)
	}
	for _, d := range report.Days {
		if d.RevenueCents-d.ExpensesCents-d.PayrollCents != d.NetCents {
			t.Fatalf("neto inconsistente: %+v", d)
		}
	}
}

// Los días de cambio de horario: 8/3/2026 dura 23 horas y 1/11/2026, 25. El
// borde de las 00:00 y las 23:59 locales cae donde debe en los dos.
func TestProfitIT_dias_de_23_y_25_horas(t *testing.T) {
	pool, ctx := profitFixtures(t)
	repo := Profit{Pool: pool}
	salon := "s-it-profit-ny"
	// 8/3/2026 (23 h). Antes del cambio NY es EST (UTC-5) y después EDT (UTC-4):
	//   7/3 23:59 local = 8/3 04:59Z (día 7)
	//   8/3 00:00 local = 8/3 05:00Z (día 8, entra)
	//   8/3 23:59 local = 9/3 03:59Z (día 8, entra)
	//   9/3 00:00 local = 9/3 04:00Z (día 9, no entra)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-1", salon, "u-it-profit-pro", atUTC(2026, 3, 8, 4, 59), 800)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-2", salon, "u-it-profit-pro", atUTC(2026, 3, 8, 5, 0), 100)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-3", salon, "u-it-profit-pro", atUTC(2026, 3, 9, 3, 59), 200)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-4", salon, "u-it-profit-pro", atUTC(2026, 3, 9, 4, 0), 400)
	// 1/11/2026 (25 h). Antes EDT (UTC-4), después EST (UTC-5):
	//   31/10 23:59 local = 1/11 03:59Z (día 31/10)
	//   1/11 00:00 local = 1/11 04:00Z (día 1/11, entra)
	//   1/11 23:59 local = 2/11 04:59Z (día 1/11, entra)
	//   2/11 00:00 local = 2/11 05:00Z (día 2/11, no entra)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-5", salon, "u-it-profit-pro", atUTC(2026, 11, 1, 3, 59), 8000)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-6", salon, "u-it-profit-pro", atUTC(2026, 11, 1, 4, 0), 10)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-7", salon, "u-it-profit-pro", atUTC(2026, 11, 2, 4, 59), 20)
	insertProfitSale(t, ctx, pool, "sale-profit-dst-8", salon, "u-it-profit-pro", atUTC(2026, 11, 2, 5, 0), 40)
	// Gastos en esos dos días.
	insertProfitExpense(t, ctx, pool, "e-profit-dst-1", salon, "other", "2026-03-08", 50)
	insertProfitExpense(t, ctx, pool, "e-profit-dst-2", salon, "other", "2026-11-01", 60)
	// Nómina pagada en el día de 25 horas, a las 23:59 locales.
	paid25 := atUTC(2026, 11, 2, 4, 59)
	insertProfitRun(t, ctx, pool, "run-profit-dst-1", salon, "2026-11-01", "2026-11-07", &paid25, 70)

	loc := mustLoadLoc(t, "America/New_York")
	for _, tc := range []struct {
		date                       string
		revenue, expenses, payroll int
	}{
		{"2026-03-08", 300, 50, 0},
		{"2026-11-01", 30, 60, 70},
	} {
		p, err := domain.NewProfitPeriod("day", tc.date, time.Time{}, loc)
		if err != nil {
			t.Fatalf("período %s: %v", tc.date, err)
		}
		from, to := p.Window()
		data, err := repo.DailyProfit(salon, "America/New_York", from, to)
		if err != nil {
			t.Fatalf("reporte %s: %v", tc.date, err)
		}
		if revenue, expenses, payroll := dayOf(t, data, tc.date); revenue != tc.revenue ||
			expenses != tc.expenses || payroll != tc.payroll {
			t.Fatalf("día %s = %d/%d/%d, esperaba %d/%d/%d", tc.date,
				revenue, expenses, payroll, tc.revenue, tc.expenses, tc.payroll)
		}
	}
}

// Otra zona con horario de verano propio (Auckland, UTC+12/+13): los bordes y el
// cambio de hora también caen en el día local.
func TestProfitIT_bordes_auckland(t *testing.T) {
	pool, ctx := profitFixtures(t)
	repo := Profit{Pool: pool}
	salon := "s-it-profit-auck"
	// Junio en Auckland es NZST (UTC+12): 15/6 00:00 local = 14/6 12:00Z.
	insertProfitSale(t, ctx, pool, "sale-profit-auck-1", salon, "u-it-profit-pro", atUTC(2026, 6, 14, 12, 0), 4000)
	insertProfitSale(t, ctx, pool, "sale-profit-auck-2", salon, "u-it-profit-pro", atUTC(2026, 6, 15, 11, 59), 1000)
	insertProfitSale(t, ctx, pool, "sale-profit-auck-3", salon, "u-it-profit-pro", atUTC(2026, 6, 15, 12, 0), 2000)
	// Noviembre: NZDT (UTC+13). 1/11 23:59 local = 1/11 10:59Z; 2/11 00:00 = 11:00Z.
	insertProfitSale(t, ctx, pool, "sale-profit-auck-4", salon, "u-it-profit-pro", atUTC(2026, 11, 1, 10, 59), 300)
	insertProfitSale(t, ctx, pool, "sale-profit-auck-5", salon, "u-it-profit-pro", atUTC(2026, 11, 1, 11, 0), 600)
	// La nómina pagada el 15/6 a las 23:59 local (15/6 11:59Z).
	paid := atUTC(2026, 6, 15, 11, 59)
	insertProfitRun(t, ctx, pool, "run-profit-auck-1", salon, "2026-06-01", "2026-06-07", &paid, 700)

	loc := mustLoadLoc(t, "Pacific/Auckland")
	p, err := domain.NewProfitPeriod("day", "2026-06-15", time.Time{}, loc)
	if err != nil {
		t.Fatalf("período: %v", err)
	}
	from, to := p.Window()
	data, err := repo.DailyProfit(salon, "Pacific/Auckland", from, to)
	if err != nil {
		t.Fatalf("reporte: %v", err)
	}
	if revenue, expenses, payroll := dayOf(t, data, "2026-06-15"); revenue != 5000 || expenses != 0 || payroll != 700 {
		t.Fatalf("15/6 en Auckland = %d/%d/%d", revenue, expenses, payroll)
	}
	if _, ok := data.RevenueByDay["2026-06-16"]; ok {
		t.Fatalf("la venta de las 00:00 del 16 no puede caer en el 15: %v", data.RevenueByDay)
	}

	p, _ = domain.NewProfitPeriod("day", "2026-11-01", time.Time{}, loc)
	from, to = p.Window()
	data, err = repo.DailyProfit(salon, "Pacific/Auckland", from, to)
	if err != nil {
		t.Fatalf("reporte de noviembre: %v", err)
	}
	if revenue, _, _ := dayOf(t, data, "2026-11-01"); revenue != 300 {
		t.Fatalf("1/11 en Auckland = %d (la de las 00:00 del 2 no entra)", revenue)
	}
}

// Una liquidación ABIERTA no suma y al pagarse suma en el día de paid_at; el
// salón sin movimientos es un reporte de ceros (no un 500); el aislamiento por
// tenant y el desglose por categoría.
func TestProfitIT_abierta_no_suma_vacio_y_categorias(t *testing.T) {
	pool, ctx := profitFixtures(t)
	repo := Profit{Pool: pool}
	salon := "s-it-profit-ny"
	insertProfitExpense(t, ctx, pool, "e-profit-cat-1", salon, "rent", "2026-06-15", 100)
	insertProfitExpense(t, ctx, pool, "e-profit-cat-2", salon, "supplies", "2026-06-15", 200)
	insertProfitExpense(t, ctx, pool, "e-profit-cat-3", salon, "rent", "2026-06-16", 500)
	insertProfitExpense(t, ctx, pool, "e-profit-cat-4", salon, "utilities", "2026-06-16", 300)
	// El otro tenant tiene un gasto enorme: no puede aparecer.
	insertProfitExpense(t, ctx, pool, "e-profit-cat-ajeno", "s-it-profit-otro", "other", "2026-06-15", 999999)
	// Una abierta con 999 y una pagada con 250 el 16/6.
	insertProfitRun(t, ctx, pool, "run-profit-cat-open", salon, "2026-05-04", "2026-05-10", nil, 999)
	paid := atUTC(2026, 6, 16, 15, 0) // 11:00 en NY
	insertProfitRun(t, ctx, pool, "run-profit-cat-paid", salon, "2026-05-11", "2026-05-17", &paid, 250)

	loc := mustLoadLoc(t, "America/New_York")
	p, err := domain.NewProfitPeriod("week", "2026-06-15", time.Time{}, loc)
	if err != nil {
		t.Fatalf("período: %v", err)
	}
	from, to := p.Window()
	data, err := repo.DailyProfit(salon, "America/New_York", from, to)
	if err != nil {
		t.Fatalf("reporte: %v", err)
	}
	report := domain.NewProfitReport(p, data)
	if report.Totals.PayrollCents != 250 {
		t.Fatalf("nómina = %d, esperaba 250 (la abierta NO cuenta)", report.Totals.PayrollCents)
	}
	if report.Totals.ExpensesCents != 1100 || report.Totals.RevenueCents != 0 ||
		report.Totals.NetCents != -1350 {
		t.Fatalf("totales = %+v", report.Totals)
	}
	// Categorías: solo > 0, por código (rent, supplies, utilities).
	want := []domain.ExpenseCategoryTotal{
		{Category: domain.ExpenseRent, TotalCents: 600},
		{Category: domain.ExpenseSupplies, TotalCents: 200},
		{Category: domain.ExpenseUtilities, TotalCents: 300},
	}
	if len(report.ExpensesByCategory) != len(want) {
		t.Fatalf("categorías = %+v", report.ExpensesByCategory)
	}
	for i := range want {
		if report.ExpensesByCategory[i] != want[i] {
			t.Fatalf("categorías = %+v, esperaba %+v", report.ExpensesByCategory, want)
		}
	}

	// Salón sin movimientos: ceros, no error, y todos los días del período.
	empty, err := repo.DailyProfit("s-it-profit-vacio", "America/New_York", from, to)
	if err != nil {
		t.Fatalf("salón vacío: %v", err)
	}
	emptyReport := domain.NewProfitReport(p, empty)
	if len(emptyReport.Days) != 7 || emptyReport.Totals != (domain.ProfitTotals{}) {
		t.Fatalf("reporte vacío = %+v", emptyReport)
	}
	for _, d := range emptyReport.Days {
		if d != (domain.ProfitDayTotal{Date: d.Date}) {
			t.Fatalf("un día del salón vacío no está en cero: %+v", d)
		}
	}
}

// Lecturas del reporte mientras se crean y se borran gastos: nunca un error y la
// foto siempre es coherente (los gastos por día y por categoría salen de la
// MISMA transacción).
func TestProfitIT_lecturas_concurrentes(t *testing.T) {
	pool, ctx := profitFixtures(t)
	repo := Profit{Pool: pool}
	expenses := Expenses{Pool: pool}
	salon := "s-it-profit-ny"
	insertProfitSale(t, ctx, pool, "sale-profit-race", salon, "u-it-profit-pro", atUTC(2026, 6, 15, 15, 0), 1000)
	for i := 0; i < 10; i++ {
		if err := expenses.Save(profitExpense("e-profit-race-"+string(rune('a'+i)), salon, "rent",
			"2026-06-15", 100)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	loc := mustLoadLoc(t, "America/New_York")
	p, _ := domain.NewProfitPeriod("week", "2026-06-15", time.Time{}, loc)
	from, to := p.Window()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := "e-profit-race-live-" + string(rune('a'+i%10))
			if err := expenses.Save(profitExpense(id, salon, "other", "2026-06-16", 50)); err != nil {
				t.Errorf("alta concurrente: %v", err)
				return
			}
			_ = expenses.Delete(salon, id)
		}
	}()

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				data, err := repo.DailyProfit(salon, "America/New_York", from, to)
				if err != nil {
					t.Errorf("lectura concurrente: %v", err)
					return
				}
				var byDay, byCategory int
				for _, v := range data.ExpensesByDay {
					byDay += v
				}
				for _, c := range data.ExpensesByCategory {
					byCategory += c.TotalCents
				}
				if byDay != byCategory {
					t.Errorf("la foto salió a medias: días=%d categorías=%d", byDay, byCategory)
					return
				}
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func mustLoadLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("zona %s: %v", name, err)
	}
	return loc
}
