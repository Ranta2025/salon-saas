// Integración contra Postgres real de las liquidaciones (F3.3, S-F3.3-1..4).
// Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run PayrollRun -v
//
// Lo que SOLO se prueba contra la DB: la foto congelada frente al catálogo, las
// ventas y el modelo de pago; la EXCLUDE por salón (solapes y períodos
// adyacentes); el advisory lock con 8 creaciones y 8 pagos simultáneos; el
// borrado que libera días; la inmutabilidad de la pagada en las queries
// (UPDATE/DELETE condicionales) y el rollback de una transacción que falla
// después de marcar la corrida.
package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/payroll"
	"salon-saas/internal/ports"
)

// payrollRunNow es el «hoy» de estos tests: 20/3/2026 en CDMX (12:00 local).
// Febrero está cerrado; marzo está en curso.
var payrollRunNow = time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)

func payrollRunService(pool *pgxpool.Pool) payroll.PayrollRunService {
	repo := PayrollRuns{Pool: pool}
	return payroll.PayrollRunService{
		Runs: repo, Tx: repo,
		PayModels: PayModels{Pool: pool}, Sales: PayrollSales{Pool: pool},
		Memberships: pgstore.Memberships{Pool: pool}, Settings: pgstore.Salons{Pool: pool},
		Now: func() time.Time { return payrollRunNow },
	}
}

func payrollRunActor(t *testing.T, pool *pgxpool.Pool, userID, salonID string) domain.Membership {
	t.Helper()
	m, err := (pgstore.Memberships{Pool: pool}).Find(userID, salonID)
	if err != nil {
		t.Fatalf("actor %s: %v", userID, err)
	}
	return m
}

// febSales deja a Ana con dos ventas cerradas de febrero: 2 × 1000 al 10% (foto)
// y 1 × 500 SIN foto (se estima con la tarifa actual del corte, 10%).
// Comisión: (20000 + 5000) / 100 = 250 centavos, una sola división.
func febSales(t *testing.T, ctx context.Context, pool *pgxpool.Pool, corte domain.Service) {
	t.Helper()
	insertPaySale(t, ctx, pool, "sale-pr-1", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 2, 10, 18, 0, 0, 0, time.UTC), 2000,
		paySaleItem{ServiceID: corte.ID, Qty: 2, PriceCent: 1000, Pct: intPtr(10)})
	insertPaySale(t, ctx, pool, "sale-pr-2", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 2, 20, 18, 0, 0, 0, time.UTC), 500,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 500, Pct: nil})
}

// La creación congela: editar el catálogo, cargar ventas del período y cambiar
// el modelo de pago NO mueve las líneas guardadas (se leen de nuevo).
func TestPayrollRunsIT_crear_congela_la_foto(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	febSales(t, ctx, pool, corte)
	svc := payrollRunService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	run, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	if run.Status != domain.PayrollRunOpen || run.From != "2026-02-01" || run.To != "2026-02-28" {
		t.Fatalf("cabecera: %+v", run)
	}
	if len(run.Lines) != 1 {
		t.Fatalf("líneas = %d, esperaba 1: %+v", len(run.Lines), run.Lines)
	}
	frozen := run.Lines[0]
	if frozen.Name != "Ana IT" || frozen.SalesCount != 2 || frozen.SalesTotalCents != 2500 ||
		frozen.CommissionCents != 250 || frozen.FixedCents != 0 ||
		frozen.TotalCents != 250 || !frozen.Estimated {
		t.Fatalf("línea congelada: %+v", frozen)
	}

	// El mundo cambia después de crear.
	if _, err := pool.Exec(ctx, `UPDATE services SET commission_pct=90 WHERE id=$1`, corte.ID); err != nil {
		t.Fatalf("catálogo: %v", err)
	}
	// Más ventas del período, con ids propios.
	insertPaySale(t, ctx, pool, "sale-pr-3", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 2, 25, 18, 0, 0, 0, time.UTC), 4000,
		paySaleItem{ServiceID: corte.ID, Qty: 2, PriceCent: 2000, Pct: intPtr(10)})
	if _, err := pool.Exec(ctx,
		`INSERT INTO member_pay(salon_id,user_id,fixed_cents,fixed_period,commission_enabled)
		 VALUES('s-it-pay','u-it-pay-a',70000,'month',true)
		 ON CONFLICT (salon_id,user_id) DO UPDATE SET fixed_cents=EXCLUDED.fixed_cents`,
	); err != nil {
		t.Fatalf("modelo de pago: %v", err)
	}

	got, err := svc.Get(owner, "s-it-pay", run.ID)
	if err != nil {
		t.Fatalf("leer de nuevo: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0] != frozen {
		t.Fatalf("la liquidación cambió sola: %+v", got.Lines)
	}
	// También en la tabla: la foto no se reescribe.
	var commission, fixed, total, count int
	if err := pool.QueryRow(ctx,
		`SELECT commission_cents, fixed_cents, total_cents, sales_count
		 FROM payroll_lines WHERE run_id=$1 AND user_id='u-it-pay-a'`, run.ID).
		Scan(&commission, &fixed, &total, &count); err != nil {
		t.Fatalf("fila guardada: %v", err)
	}
	if commission != 250 || fixed != 0 || total != 250 || count != 2 {
		t.Fatalf("fila guardada = %d/%d/%d/%d", commission, fixed, total, count)
	}

	// La vista previa, en cambio, SÍ es viva: ahora ve el 90% y el fijo nuevo.
	previewSvc := payroll.PayrollService{
		Pay: PayModels{Pool: pool}, Sales: PayrollSales{Pool: pool},
		Memberships: pgstore.Memberships{Pool: pool}, Settings: pgstore.Salons{Pool: pool},
		Now: func() time.Time { return payrollRunNow },
	}
	preview, err := previewSvc.Preview(owner, "s-it-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	var live payroll.PayrollLine
	found := false
	for _, l := range preview.Lines {
		if l.UserID == "u-it-pay-a" {
			live, found = l, true
		}
	}
	if !found || live.CommissionCents == frozen.CommissionCents || live.FixedCents != 70000 {
		t.Fatalf("la vista previa debía reflejar el cambio: %+v", live)
	}
}

// Período en curso, período vacío, solapes de cualquier tipo y días adyacentes.
// Una corrida abierta bloquea; borrarla libera los días.
func TestPayrollRunsIT_periodo_solape_y_borrado(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	svc := payrollRunService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	// Marzo sigue en curso: 400 con la frase del contrato.
	_, err := svc.Create(owner, "s-it-pay", "month", "2026-03-10")
	if err == nil || err.Error() != domain.MsgPayrollPeriodNotClosed {
		t.Fatalf("período en curso: %v", err)
	}
	// Enero sin ventas ni fijos: 400 "No hay nada para liquidar".
	_, err = svc.Create(owner, "s-it-pay", "month", "2026-01-10")
	if err == nil || err.Error() != domain.MsgPayrollNothingToPay {
		t.Fatalf("período vacío: %v", err)
	}

	febSales(t, ctx, pool, corte)
	feb, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("crear febrero: %v", err)
	}
	// Mismo período y solapes de otro tipo: 409 con la frase del contrato.
	for _, c := range []struct{ kind, date string }{
		{"month", "2026-02-20"},
		{"fortnight", "2026-02-03"}, // 1-15/2
		{"fortnight", "2026-02-20"}, // 16-28/2
		{"week", "2026-02-17"},      // 16-22/2
	} {
		_, err := svc.Create(owner, "s-it-pay", c.kind, c.date)
		if err == nil || err.Error() != domain.MsgPayrollRunOverlaps {
			t.Fatalf("%s %s: %v", c.kind, c.date, err)
		}
	}

	// La quincena 1-15 de marzo es adyacente: no pisa febrero y se puede crear
	// (con una venta de marzo para que haya algo que liquidar).
	insertPaySale(t, ctx, pool, "sale-pr-mar", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 3, 3, 18, 0, 0, 0, time.UTC), 1000,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 1000, Pct: intPtr(10)})
	if _, err := svc.Create(owner, "s-it-pay", "fortnight", "2026-03-03"); err != nil {
		t.Fatalf("períodos adyacentes: %v", err)
	}

	// Borrar la abierta libera sus días.
	if err := svc.Delete(owner, "s-it-pay", feb.ID); err != nil {
		t.Fatalf("borrar abierta: %v", err)
	}
	if _, err := svc.Get(owner, "s-it-pay", feb.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la borrada sigue: %v", err)
	}
	if _, err := svc.Create(owner, "s-it-pay", "fortnight", "2026-02-03"); err != nil {
		t.Fatalf("los días liberados: %v", err)
	}

	// Una PAGADA también bloquea los días y no se puede borrar. Se paga la
	// quincena 1-15/2 que acaba de ocupar los días liberados.
	var runID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM payroll_runs WHERE salon_id='s-it-pay' AND from_date='2026-02-01'`).
		Scan(&runID); err != nil {
		t.Fatalf("buscar la quincena de febrero: %v", err)
	}
	if _, err := svc.Pay(owner, "s-it-pay", runID); err != nil {
		t.Fatalf("pagar la quincena: %v", err)
	}
	if _, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10"); err == nil || err.Error() != domain.MsgPayrollRunOverlaps {
		t.Fatalf("una pagada bloquea los días: %v", err)
	}
	// Semana vs quincena: la semana 9-15/2 está adentro de la quincena pagada.
	if _, err := svc.Create(owner, "s-it-pay", "week", "2026-02-11"); err == nil || err.Error() != domain.MsgPayrollRunOverlaps {
		t.Fatalf("semana vs quincena: %v", err)
	}
	if err := svc.Delete(owner, "s-it-pay", runID); err == nil || err.Error() != domain.MsgPayrollRunPaid {
		t.Fatalf("borrar pagada: %v", err)
	}
}

// 8 goroutines creando el mismo período, 20 rondas: exactamente un 201 y siete
// 409, una corrida y ni un 500. Además, dos tipos solapados a la vez.
func TestPayrollRunsIT_concurrencia_creaciones(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	febSales(t, ctx, pool, corte)
	svc := payrollRunService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	const rounds = 20
	outcomes := map[string]int{}
	for round := 0; round < rounds; round++ {
		if _, err := pool.Exec(ctx, `DELETE FROM payroll_runs WHERE salon_id='s-it-pay'`); err != nil {
			t.Fatalf("limpieza de la ronda %d: %v", round, err)
		}
		var wg sync.WaitGroup
		results := make([]error, 8)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, results[i] = svc.Create(owner, "s-it-pay", "month", "2026-02-10")
			}(i)
		}
		wg.Wait()
		ok, conflict, other := 0, 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				ok++
			case err.Error() == domain.MsgPayrollRunOverlaps:
				conflict++
			default:
				other++
				t.Logf("ronda %d: error inesperado: %v", round, err)
			}
		}
		if ok != 1 || conflict != 7 || other != 0 {
			t.Fatalf("ronda %d: ok=%d conflictos=%d otros=%d", round, ok, conflict, other)
		}
		var runs, lines int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM payroll_runs WHERE salon_id='s-it-pay'`).Scan(&runs); err != nil {
			t.Fatalf("contar corridas: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM payroll_lines l JOIN payroll_runs r ON r.id=l.run_id WHERE r.salon_id='s-it-pay'`).Scan(&lines); err != nil {
			t.Fatalf("contar líneas: %v", err)
		}
		if runs != 1 || lines != 1 {
			t.Fatalf("ronda %d: corridas=%d líneas=%d", round, runs, lines)
		}
		outcomes["201"] += ok
		outcomes["409"] += conflict
	}
	t.Logf("%d rondas: %d creadas, %d conflictos", rounds, outcomes["201"], outcomes["409"])

	// Dos tipos solapados creados a la vez: uno gana, el otro 409, una corrida.
	if _, err := pool.Exec(ctx, `DELETE FROM payroll_runs WHERE salon_id='s-it-pay'`); err != nil {
		t.Fatalf("limpieza: %v", err)
	}
	var wg sync.WaitGroup
	pairs := []struct{ kind, date string }{{"month", "2026-02-10"}, {"fortnight", "2026-02-03"}}
	results := make([]error, len(pairs))
	for i, p := range pairs {
		wg.Add(1)
		go func(i int, kind, date string) {
			defer wg.Done()
			_, results[i] = svc.Create(owner, "s-it-pay", kind, date)
		}(i, p.kind, p.date)
	}
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) {
		t.Fatalf("debía ganar exactamente una: %v / %v", results[0], results[1])
	}
	var runs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM payroll_runs WHERE salon_id='s-it-pay'`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("corridas tras la carrera de tipos: %d (%v)", runs, err)
	}
}

// Ajuste, pago atómico, inmutabilidad en las queries y 8 pagos simultáneos.
func TestPayrollRunsIT_ajuste_pago_e_inmutabilidad(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	febSales(t, ctx, pool, corte)
	svc := payrollRunService(pool)
	repo := PayrollRuns{Pool: pool}
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	run, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	// Ajuste válido.
	got, err := svc.AdjustLine(owner, "s-it-pay", run.ID, "u-it-pay-a", -250, "venta duplicada")
	if err != nil {
		t.Fatalf("ajuste: %v", err)
	}
	if line, _ := got.FindLine("u-it-pay-a"); line.AdjustmentCents != -250 ||
		line.AdjustmentNote != "venta duplicada" || line.TotalCents != 0 {
		t.Fatalf("línea ajustada: %+v", line)
	}
	// Un centavo más y el total queda negativo: 400 con la frase del contrato.
	if _, err := svc.AdjustLine(owner, "s-it-pay", run.ID, "u-it-pay-a", -251, "de más"); err == nil ||
		err.Error() != domain.MsgPayrollNegativeTotal {
		t.Fatalf("total negativo: %v", err)
	}
	// El CHECK de la DB también frena al repositorio directo (no solo la API).
	if err := repo.InPayrollTx("s-it-pay", func(tx ports.PayrollRunTx) error {
		return tx.AdjustLine("s-it-pay", run.ID, "u-it-pay-a", -251, "x")
	}); err == nil {
		t.Fatal("el repositorio debía rechazar el total negativo")
	}

	// Pago.
	paid, err := svc.Pay(owner, "s-it-pay", run.ID)
	if err != nil {
		t.Fatalf("pagar: %v", err)
	}
	if !paid.Paid() || paid.PaidAt == nil || paid.PaidBy != "u-it-pay-owner" {
		t.Fatalf("pagada: %+v", paid)
	}
	// Segundo pago, ajuste y borrado por la API: 409 con la frase exacta.
	if _, err := svc.Pay(owner, "s-it-pay", run.ID); err == nil || err.Error() != domain.MsgPayrollRunPaid {
		t.Fatalf("segundo pago: %v", err)
	}
	if _, err := svc.AdjustLine(owner, "s-it-pay", run.ID, "u-it-pay-a", 100, "tarde"); err == nil ||
		err.Error() != domain.MsgPayrollRunPaid {
		t.Fatalf("ajuste de pagada: %v", err)
	}
	if err := svc.Delete(owner, "s-it-pay", run.ID); err == nil || err.Error() != domain.MsgPayrollRunPaid {
		t.Fatalf("borrar pagada: %v", err)
	}

	// Y por el REPOSITORIO, sin pasar por la API: la condición status='open' de
	// las queries es la que impide tocar una pagada.
	err = repo.InPayrollTx("s-it-pay", func(tx ports.PayrollRunTx) error {
		if err := tx.PayRun("s-it-pay", run.ID, "u-it-pay-a"); !errors.Is(err, domain.ErrAlreadyExists) {
			return errors.New("el segundo pago directo debía ser ErrAlreadyExists")
		}
		if err := tx.AdjustLine("s-it-pay", run.ID, "u-it-pay-a", 500, "directo"); !errors.Is(err, domain.ErrAlreadyExists) {
			return errors.New("el ajuste directo de una pagada debía ser ErrAlreadyExists")
		}
		if err := tx.DeleteRun("s-it-pay", run.ID); !errors.Is(err, domain.ErrAlreadyExists) {
			return errors.New("el borrado directo de una pagada debía ser ErrAlreadyExists")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("intentos directos sobre la pagada: %v", err)
	}
	after, err := svc.Get(owner, "s-it-pay", run.ID)
	if err != nil {
		t.Fatalf("la pagada desapareció: %v", err)
	}
	if line, _ := after.FindLine("u-it-pay-a"); line.AdjustmentCents != -250 ||
		line.AdjustmentNote != "venta duplicada" || line.TotalCents != 0 {
		t.Fatalf("una escritura directa cambió la pagada: %+v", line)
	}

	// Atomicidad: la transacción marca la corrida y DESPUÉS falla; nada cambia.
	// Enero está libre (la de febrero ya está pagada) y tiene una venta propia.
	insertPaySale(t, ctx, pool, "sale-pr-ene", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 1, 5, 18, 0, 0, 0, time.UTC), 1000,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 1000, Pct: intPtr(10)})
	run2, err := svc.Create(owner, "s-it-pay", "fortnight", "2026-01-03")
	if err != nil {
		t.Fatalf("crear enero: %v", err)
	}
	err = repo.InPayrollTx("s-it-pay", func(tx ports.PayrollRunTx) error {
		if err := tx.PayRun("s-it-pay", run2.ID, "u-it-pay-owner"); err != nil {
			return err
		}
		return errors.New("falla simulada después del UPDATE de la corrida")
	})
	if err == nil {
		t.Fatal("la transacción debía fallar")
	}
	still, err := svc.Get(owner, "s-it-pay", run2.ID)
	if err != nil {
		t.Fatalf("la corrida desapareció: %v", err)
	}
	if still.Paid() || still.PaidAt != nil || still.PaidBy != "" {
		t.Fatalf("el rollback no deshizo el pago: %+v", still)
	}

	// 8 pagos simultáneos sobre una corrida abierta: uno gana y siete 409.
	insertPaySale(t, ctx, pool, "sale-pr-ene2", "s-it-pay", "u-it-pay-a",
		time.Date(2026, 1, 20, 18, 0, 0, 0, time.UTC), 1000,
		paySaleItem{ServiceID: corte.ID, Qty: 1, PriceCent: 1000, Pct: intPtr(10)})
	run3, err := svc.Create(owner, "s-it-pay", "fortnight", "2026-01-20")
	if err != nil {
		t.Fatalf("crear la quincena 16-28/2: %v", err)
	}
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = svc.Pay(owner, "s-it-pay", run3.ID)
		}(i)
	}
	wg.Wait()
	ok, conflict, other := 0, 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case err.Error() == domain.MsgPayrollRunPaid:
			conflict++
		default:
			other++
			t.Logf("pago simultáneo: %v", err)
		}
	}
	if ok != 1 || conflict != 7 || other != 0 {
		t.Fatalf("pagos simultáneos: ok=%d conflictos=%d otros=%d", ok, conflict, other)
	}
}

// Aislamiento por tenant, listado con totales y exportación con la foto.
func TestPayrollRunsIT_tenant_listado_y_export(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	febSales(t, ctx, pool, corte)
	svc := payrollRunService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	// El salón 2 necesita quien lo administre y una venta propia.
	if err := (pgstore.Memberships{Pool: pool}).Save(domain.Membership{
		UserID: "u-it-pay-c", SalonID: "s-it-pay2", Role: domain.RoleOwner,
	}); err != nil {
		t.Fatalf("owner del salón 2: %v", err)
	}
	svc2, _ := domain.NewService("s-it-pay2", "Corte Ajeno PR", 45, 150000, 10)
	if err := (pgstore.Services{Pool: pool}).Save(svc2); err != nil {
		t.Fatalf("catálogo del salón 2: %v", err)
	}
	insertPaySale(t, ctx, pool, "sale-pr-otro", "s-it-pay2", "u-it-pay-c",
		time.Date(2026, 2, 12, 18, 0, 0, 0, time.UTC), 1000,
		paySaleItem{ServiceID: svc2.ID, Qty: 1, PriceCent: 1000, Pct: intPtr(10)})
	owner2 := payrollRunActor(t, pool, "u-it-pay-c", "s-it-pay2")

	mine, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("crear salón 1: %v", err)
	}
	other, err := svc2Run(svc, owner2, "s-it-pay2")
	if err != nil {
		t.Fatalf("crear salón 2: %v", err)
	}

	// El salón 1 no ve, no lee, no ajusta, no paga, no borra ni exporta la del 2.
	if _, err := svc.Get(owner, "s-it-pay", other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get ajeno: %v", err)
	}
	if _, err := svc.AdjustLine(owner, "s-it-pay", other.ID, "u-it-pay-c", 0, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ajuste ajeno: %v", err)
	}
	if _, err := svc.Pay(owner, "s-it-pay", other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pago ajeno: %v", err)
	}
	if err := svc.Delete(owner, "s-it-pay", other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrado ajeno: %v", err)
	}
	if _, _, err := svc.ExportCSV(owner, "s-it-pay", other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("export ajeno: %v", err)
	}

	// Listado: solo las del salón, la más nueva primero, con totales.
	list, err := svc.List(owner, "s-it-pay")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 1 || list[0].ID != mine.ID || list[0].LinesCount != 1 ||
		list[0].Totals.CommissionCents != 250 || list[0].Totals.TotalCents != 250 {
		t.Fatalf("listado: %+v", list)
	}
	list2, err := svc.List(owner2, "s-it-pay2")
	if err != nil || len(list2) != 1 || list2[0].ID != other.ID {
		t.Fatalf("listado del salón 2: %+v (%v)", list2, err)
	}

	// Export: nombre seguro y la fila congelada.
	name, data, err := svc.ExportCSV(owner, "s-it-pay", mine.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if name != "nomina-month-2026-02-01-2026-02-28.csv" {
		t.Fatalf("filename = %q", name)
	}
	text := string(data)
	if len(text) < 3 || text[:3] != "\xef\xbb\xbf" {
		t.Fatalf("sin BOM: %q", text)
	}
	if want := "Ana IT,Activo,2,25.00,2.50,0.00,0.00,,2.50\r\n"; !strings.Contains(text, want) {
		t.Fatalf("CSV sin la fila congelada %q: %q", want, text)
	}
}

// svc2Run crea la liquidación del salón 2 reusando el mismo servicio pero con
// el actor de ese salón.
func svc2Run(svc payroll.PayrollRunService, actor domain.Membership, salonID string) (domain.PayrollRun, error) {
	return svc.Create(actor, salonID, "month", "2026-02-10")
}
