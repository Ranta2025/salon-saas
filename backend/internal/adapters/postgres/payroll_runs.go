// Liquidaciones de nómina en Postgres (F3.3, D6 del contrato F3): migración
// 025. La cabecera congela el período y las líneas congelan los montos; desde
// que una corrida está pagada, TODAS las escrituras de este archivo llevan la
// condición status='open' en el SQL (no alcanza con que la API lo controle).
//
// La creación corre en UNA transacción que primero toma un advisory lock por
// salón ("payroll-write:<salon>", un prefijo que no choca con los de agenda,
// cobertura de ausencias ni miembros) y después inserta cabecera + líneas. La
// EXCLUDE de la migración es la red de seguridad: dos creaciones simultáneas
// del mismo período dejan una sola corrida (23P01 -> ErrAlreadyExists -> 409).
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// payrollWriteLockPrefix separa el lock de liquidaciones de los otros locks por
// salón ("booking-write:", "booking-cover:", "member-write:").
const payrollWriteLockPrefix = "payroll-write:"

// PayrollRuns implementa ports.PayrollRunRepository y ports.PayrollTxRunner.
type PayrollRuns struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la espera del lock del salón y la transacción (0 = el
	// tope por defecto de txtimeout.go).
	TxTimeout time.Duration
}

// payrollRunColumns es el SELECT único de la cabecera: lo usan el listado, el
// detalle y la lectura dentro de la transacción para que digan lo mismo.
const payrollRunColumns = `
	 r.id, r.salon_id, r.kind, r.from_date, r.to_date, r.status,
	 COALESCE(r.created_by, ''), r.created_at, r.paid_at, COALESCE(r.paid_by, '')`

// payrollLineColumns es el SELECT único de las líneas. `active` sale de la
// membresía ACTUAL (una baja posterior se informa como tal) y el nombre es la
// foto congelada al crear.
const payrollLineColumns = `
	 l.user_id, l.name_snapshot, COALESCE(m.ended_at IS NULL, false) AS active,
	 l.sales_count, l.sales_total_cents, l.commission_cents, l.fixed_cents,
	 l.adjustment_cents, l.adjustment_note, l.total_cents, l.estimated`

// ListRuns implementa ports.PayrollRunRepository.
func (r PayrollRuns) ListRuns(salonID string) ([]domain.PayrollRunSummary, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT r.id, r.kind, r.from_date, r.to_date, r.status, r.created_at, r.paid_at,
		        COUNT(l.user_id)::int,
		        COALESCE(SUM(l.commission_cents), 0)::bigint,
		        COALESCE(SUM(l.fixed_cents), 0)::bigint,
		        COALESCE(SUM(l.adjustment_cents), 0)::bigint,
		        COALESCE(SUM(l.total_cents), 0)::bigint
		 FROM payroll_runs r
		 LEFT JOIN payroll_lines l ON l.run_id = r.id
		 WHERE r.salon_id = $1
		 GROUP BY r.id, r.kind, r.from_date, r.to_date, r.status, r.created_at, r.paid_at
		 ORDER BY r.from_date DESC, r.created_at DESC, r.id DESC`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PayrollRunSummary, 0, 8)
	for rows.Next() {
		var s domain.PayrollRunSummary
		var kind, status string
		var from, to time.Time
		var commission, fixed, adjustment, total int64
		if err := rows.Scan(&s.ID, &kind, &from, &to, &status, &s.CreatedAt, &s.PaidAt,
			&s.LinesCount, &commission, &fixed, &adjustment, &total); err != nil {
			return nil, err
		}
		s.Kind = domain.PayPeriodKind(kind)
		s.Status = domain.PayrollRunStatus(status)
		s.From, s.To = dateOf(from), dateOf(to)
		var err error
		if s.Totals.CommissionCents, err = bigintToInt(commission, "la comisión de la liquidación"); err != nil {
			return nil, err
		}
		if s.Totals.FixedCents, err = bigintToInt(fixed, "el fijo de la liquidación"); err != nil {
			return nil, err
		}
		if s.Totals.AdjustmentCents, err = bigintToIntSigned(adjustment, "el ajuste de la liquidación"); err != nil {
			return nil, err
		}
		if s.Totals.TotalCents, err = bigintToInt(total, "el total de la liquidación"); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// FindRun implementa ports.PayrollRunRepository: la corrida con sus líneas,
// scopeada por salón (la de otro tenant no existe: ErrNotFound).
//
// Cabecera y líneas se leen en UNA MISMA foto (transacción REPEATABLE READ de
// solo lectura): sin eso, un borrado concurrente entre las dos consultas
// devolvía 200 con una corrida sin líneas. Así la respuesta es la corrida
// ENTERA o 404, nunca media.
func (r PayrollRuns) FindRun(salonID, runID string) (domain.PayrollRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return domain.PayrollRun{}, err
	}
	defer rollbackTx(tx) // no-op si ya commiteó

	run, err := findPayrollRun(ctx, tx, salonID, runID)
	if err != nil {
		return domain.PayrollRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PayrollRun{}, err
	}
	return run, nil
}

// InPayrollTx implementa ports.PayrollTxRunner: una transacción con advisory
// lock por salón. El lock se libera al commitear o al revertir.
func (r PayrollRuns) InPayrollTx(salonID string, fn func(tx ports.PayrollRunTx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx) // no-op si ya commiteó

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		payrollWriteLockPrefix+salonID); err != nil {
		return err
	}
	if err := fn(payrollRunTx{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// payrollRunTx es la vista transaccional de las liquidaciones.
type payrollRunTx struct {
	tx  pgx.Tx
	ctx context.Context
}

// SaveRun inserta la cabecera y TODAS las líneas en la misma transacción. Un
// choque de días lo levanta la EXCLUDE (23P01 -> ErrAlreadyExists: 409).
func (t payrollRunTx) SaveRun(run domain.PayrollRun) error {
	if _, err := t.tx.Exec(t.ctx,
		`INSERT INTO payroll_runs(id,salon_id,kind,from_date,to_date,status,created_by,created_at,paid_at,paid_by)
		 VALUES($1,$2,$3,$4::date,$5::date,$6,$7,$8,$9,$10)`,
		run.ID, run.SalonID, string(run.Kind), run.From, run.To, string(run.Status),
		run.CreatedBy, run.CreatedAt, run.PaidAt, run.PaidBy); err != nil {
		return pgError(err)
	}
	batch := &pgx.Batch{}
	for _, l := range run.Lines {
		batch.Queue(
			`INSERT INTO payroll_lines(run_id,user_id,name_snapshot,sales_count,sales_total_cents,
			        commission_cents,fixed_cents,adjustment_cents,adjustment_note,total_cents,estimated)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			run.ID, l.UserID, l.Name, l.SalesCount, l.SalesTotalCents,
			l.CommissionCents, l.FixedCents, l.AdjustmentCents, l.AdjustmentNote,
			l.TotalCents, l.Estimated)
	}
	results := t.tx.SendBatch(t.ctx, batch)
	defer func() { _ = results.Close() }()
	for range run.Lines {
		if _, err := results.Exec(); err != nil {
			return pgError(err)
		}
	}
	return nil
}

// FindRun lee la corrida dentro de la transacción (la recién creada o la que
// se está pagando).
func (t payrollRunTx) FindRun(salonID, runID string) (domain.PayrollRun, error) {
	return findPayrollRun(t.ctx, t.tx, salonID, runID)
}

// Settings lee la configuración del salón DENTRO de la transacción, después de
// que InPayrollTx tomó el advisory lock: la creación deriva el período con la
// zona que vale bajo el lock, nunca con una que el PUT está cambiando.
func (t payrollRunTx) Settings(salonID string) (domain.SalonSettings, error) {
	return readSalonSettings(t.ctx, t.tx, salonID)
}

// PreviewSource implementa ports.PayrollPreviewSource sobre la conexión de la
// transacción (nunca pide otra al pool).
func (t payrollRunTx) PreviewSource() ports.PayrollPreviewSource {
	return payrollPreviewTx{ctx: t.ctx, q: t.tx}
}

// payrollPreviewTx son las lecturas de la vista previa dentro de la transacción.
type payrollPreviewTx struct {
	ctx context.Context
	q   querier
}

func (p payrollPreviewTx) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	return listPayModels(p.ctx, p.q, salonID)
}

func (p payrollPreviewTx) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	return commissionTotals(p.ctx, p.q, salonID, from, to, "")
}

// AdjustLine cambia el ajuste y la nota SOLO si la corrida sigue abierta y la
// persona está en ella. El total se recalcula en el mismo UPDATE desde la foto
// de comisión y fijo (que no cambian), así no depende de lo que leyó la API.
func (t payrollRunTx) AdjustLine(salonID, runID, userID string, adjustmentCents int, note string) error {
	tag, err := t.tx.Exec(t.ctx,
		`UPDATE payroll_lines l
		 SET adjustment_cents = $4,
		     adjustment_note = $5,
		     total_cents = l.commission_cents + l.fixed_cents + $4
		 WHERE l.run_id = $1 AND l.user_id = $2
		   AND EXISTS (SELECT 1 FROM payroll_runs r
		               WHERE r.id = $1 AND r.salon_id = $3 AND r.status = 'open')`,
		runID, userID, salonID, adjustmentCents, note)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	return t.explainNoRows(salonID, runID)
}

// PayRun marca la corrida pagada con un UPDATE CONDICIONAL: dos pagos
// simultáneos no pueden ganar los dos.
func (t payrollRunTx) PayRun(salonID, runID, paidBy string) error {
	tag, err := t.tx.Exec(t.ctx,
		`UPDATE payroll_runs
		 SET status = 'paid', paid_at = now(), paid_by = $3
		 WHERE id = $1 AND salon_id = $2 AND status = 'open'`,
		runID, salonID, paidBy)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	return t.explainNoRows(salonID, runID)
}

// DeleteRun borra la corrida (las líneas se van en cascada) SOLO si está
// abierta, liberando los días para volver a liquidarlos.
func (t payrollRunTx) DeleteRun(salonID, runID string) error {
	tag, err := t.tx.Exec(t.ctx,
		`DELETE FROM payroll_runs WHERE id = $1 AND salon_id = $2 AND status = 'open'`,
		runID, salonID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	return t.explainNoRows(salonID, runID)
}

// explainNoRows distingue por qué una escritura condicional no tocó nada: la
// corrida no existe (ErrNotFound -> 404) o está pagada (ErrAlreadyExists ->
// 409). Se consulta solo en el camino de error.
func (t payrollRunTx) explainNoRows(salonID, runID string) error {
	var status string
	err := t.tx.QueryRow(t.ctx,
		`SELECT status FROM payroll_runs WHERE id = $1 AND salon_id = $2`, runID, salonID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == string(domain.PayrollRunPaid) {
		return domain.ErrAlreadyExists
	}
	// Abierta y sin filas tocadas: la persona no está en la corrida (o la
	// corrida cambió de estado entre el UPDATE y esta lectura).
	return domain.ErrNotFound
}

// findPayrollRun lee la cabecera y sus líneas con el scope de salón.
func findPayrollRun(ctx context.Context, q querier, salonID, runID string) (domain.PayrollRun, error) {
	row := q.QueryRow(ctx,
		`SELECT`+payrollRunColumns+` FROM payroll_runs r WHERE r.id = $1 AND r.salon_id = $2`,
		runID, salonID)
	run, err := scanPayrollRun(row)
	if err != nil {
		return domain.PayrollRun{}, notFound(err)
	}
	rows, err := q.Query(ctx,
		`SELECT`+payrollLineColumns+`
		 FROM payroll_lines l
		 LEFT JOIN memberships m ON m.user_id = l.user_id AND m.salon_id = $2
		 WHERE l.run_id = $1
		 ORDER BY l.total_cents DESC, l.name_snapshot, l.user_id`, runID, salonID)
	if err != nil {
		return domain.PayrollRun{}, err
	}
	defer rows.Close()
	run.Lines = make([]domain.PayrollLine, 0, 8)
	for rows.Next() {
		line, err := scanPayrollLine(rows)
		if err != nil {
			return domain.PayrollRun{}, err
		}
		run.Lines = append(run.Lines, line)
	}
	return run, rows.Err()
}

// scanPayrollRun lee una fila de payrollRunColumns.
func scanPayrollRun(row interface{ Scan(dest ...any) error }) (domain.PayrollRun, error) {
	var run domain.PayrollRun
	var kind, status string
	var from, to time.Time
	if err := row.Scan(&run.ID, &run.SalonID, &kind, &from, &to, &status,
		&run.CreatedBy, &run.CreatedAt, &run.PaidAt, &run.PaidBy); err != nil {
		return domain.PayrollRun{}, err
	}
	run.Kind = domain.PayPeriodKind(kind)
	run.Status = domain.PayrollRunStatus(status)
	run.From, run.To = dateOf(from), dateOf(to)
	return run, nil
}

// scanPayrollLine lee una fila de payrollLineColumns.
func scanPayrollLine(row interface{ Scan(dest ...any) error }) (domain.PayrollLine, error) {
	var l domain.PayrollLine
	if err := row.Scan(&l.UserID, &l.Name, &l.Active, &l.SalesCount, &l.SalesTotalCents,
		&l.CommissionCents, &l.FixedCents, &l.AdjustmentCents, &l.AdjustmentNote,
		&l.TotalCents, &l.Estimated); err != nil {
		return domain.PayrollLine{}, err
	}
	return l, nil
}

// dateOf formatea una DATE de Postgres como la fecha local del contrato.
func dateOf(t time.Time) string { return t.Format("2006-01-02") }

// bigintToIntSigned es bigintToInt para columnas que pueden ser negativas (el
// ajuste).
func bigintToIntSigned(v int64, what string) (int, error) {
	if v < math.MinInt || v > math.MaxInt {
		return 0, fmt.Errorf("%w: %s supera el máximo representable", domain.ErrValidation, what)
	}
	return int(v), nil
}

var (
	_ ports.PayrollRunRepository = PayrollRuns{}
	_ ports.PayrollTxRunner      = PayrollRuns{}
)
