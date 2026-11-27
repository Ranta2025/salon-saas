// Integración contra Postgres real de la lectura de una liquidación (F3.3).
// Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run PayrollRunRead -v
//
// El defecto: `findPayrollRun` leía la cabecera y las líneas en DOS consultas
// separadas. Un borrado concurrente entre las dos devolvía 200 con una corrida
// SIN líneas (una liquidación fantasma). La lectura tiene que ser UNA foto
// consistente: o la corrida entera o 404, nunca media.
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// waitForPayrollLinesRead espera a que la lectura de las líneas esté bloqueada
// por el lock de la tabla que sostiene el test (así la carrera es determinista
// y no una cuestión de milisegundos).
func waitForPayrollLinesRead(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*)::int FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock'
			    AND query ILIKE '%payroll_lines%'`).Scan(&waiting); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("la lectura de las líneas no llegó a esperar el lock de la tabla")
}

// 20 rondas de borrado contra lectura. En las rondas pares el borrado gana: la
// lectura es 404. En las impares la lectura ya leyó la cabecera y está esperando
// para leer las líneas cuando el borrado commitea: aun así devuelve la corrida
// ENTERA (la foto es anterior al borrado), nunca una corrida sin líneas.
func TestPayrollRunReadIT_borrado_y_lectura_en_una_sola_foto(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	febSales(t, ctx, pool, corte)
	svc := payrollRunService(pool)
	repo := PayrollRuns{Pool: pool}
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	const rounds = 20
	fullRuns, notFound := 0, 0
	for round := 0; round < rounds; round++ {
		if _, err := pool.Exec(ctx, `DELETE FROM payroll_runs WHERE salon_id='s-it-pay'`); err != nil {
			t.Fatalf("ronda %d limpieza: %v", round, err)
		}
		run, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10")
		if err != nil {
			t.Fatalf("ronda %d crear: %v", round, err)
		}
		if len(run.Lines) != 1 {
			t.Fatalf("ronda %d: la corrida nació con %d líneas", round, len(run.Lines))
		}

		if round%2 == 0 {
			// El borrado termina ANTES de la lectura: 404 limpio.
			if err := repo.InPayrollTx("s-it-pay", func(tx ports.PayrollRunTx) error {
				return tx.DeleteRun("s-it-pay", run.ID)
			}); err != nil {
				t.Fatalf("ronda %d borrar: %v", round, err)
			}
			if _, err := repo.FindRun("s-it-pay", run.ID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("ronda %d: la corrida borrada debía ser 404, dio %v", round, err)
			}
			notFound++
			continue
		}

		// Carrera forzada: el test bloquea la tabla que lee la SEGUNDA consulta
		// de la lectura (memberships), la lectura ya tiene la cabecera, el
		// borrado commitea y recién ahí se suelta el lock.
		lockTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("ronda %d lock tx: %v", round, err)
		}
		if _, err := lockTx.Exec(ctx, `LOCK TABLE memberships IN ACCESS EXCLUSIVE MODE`); err != nil {
			_ = lockTx.Rollback(ctx)
			t.Fatalf("ronda %d lock: %v", round, err)
		}
		type readResult struct {
			run domain.PayrollRun
			err error
		}
		done := make(chan readResult, 1)
		go func() {
			got, err := repo.FindRun("s-it-pay", run.ID)
			done <- readResult{run: got, err: err}
		}()
		waitForPayrollLinesRead(t, ctx, pool)
		if err := repo.InPayrollTx("s-it-pay", func(tx ports.PayrollRunTx) error {
			return tx.DeleteRun("s-it-pay", run.ID)
		}); err != nil {
			_ = lockTx.Rollback(ctx)
			t.Fatalf("ronda %d borrar en el medio: %v", round, err)
		}
		if err := lockTx.Rollback(ctx); err != nil {
			t.Fatalf("ronda %d soltar lock: %v", round, err)
		}
		got := <-done
		if got.err != nil {
			t.Fatalf("ronda %d: la lectura debía devolver la corrida entera: %v", round, got.err)
		}
		if got.run.ID != run.ID || len(got.run.Lines) != 1 {
			t.Fatalf("ronda %d: corrida INCOMPLETA (id=%q, líneas=%d): la cabecera y las líneas "+
				"se leyeron en fotos distintas", round, got.run.ID, len(got.run.Lines))
		}
		fullRuns++
	}
	if fullRuns+notFound != rounds || fullRuns == 0 || notFound == 0 {
		t.Fatalf("resultados: %d corridas enteras, %d 404 (esperaba las dos clases)", fullRuns, notFound)
	}
	t.Logf("%d rondas: %d corridas enteras, %d 404", rounds, fullRuns, notFound)
}
