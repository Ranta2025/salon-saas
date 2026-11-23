// Reporte de ganancia neta en Postgres (F3b, D1/D2/D6 del contrato F3b).
//
// Las tres componentes salen de UNA sola transacción REPEATABLE READ de solo
// lectura (la misma técnica de FindRun): ingresos de `sales`, gastos de
// `expenses` y nómina PAGADA (Σ `payroll_lines.total_cents` de las corridas con
// status='paid') de `payroll_runs`. Así el reporte es una foto: una venta
// cobrada o una liquidación pagada a mitad de camino no puede entrar en una
// componente y no en otra.
//
// El día es el LOCAL del salón (`AT TIME ZONE $2`), nunca UTC: la ventana es
// [from, to), las medianoches locales que derivó el dominio con time.Date, así
// que los días de 23 y 25 horas de los cambios de horario entran enteros.
package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Profit implementa ports.ProfitRepository sobre una transacción de lectura.
type Profit struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la espera y la transacción (0 = el tope de txtimeout.go).
	TxTimeout time.Duration
}

// DailyProfit devuelve los montos por día local y el total por categoría de los
// gastos del período. Ver el comentario del paquete: una sola transacción.
func (r Profit) DailyProfit(salonID, timezone string, from, to time.Time) (domain.ProfitData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return domain.ProfitData{}, err
	}
	defer rollbackTx(tx) // no-op si ya commiteó

	data := domain.ProfitData{
		RevenueByDay:  map[string]int{},
		ExpensesByDay: map[string]int{},
		PayrollByDay:  map[string]int{},
	}
	// Ingresos: el total de cada venta, por día LOCAL del salón.
	if err := dailyTotals(ctx, tx, &data.RevenueByDay,
		`SELECT (s.created_at AT TIME ZONE $2)::date AS day, SUM(s.total_cents)::bigint
		 FROM sales s
		 WHERE s.salon_id = $1 AND s.created_at >= $3 AND s.created_at < $4
		 GROUP BY 1`, salonID, timezone, from, to); err != nil {
		return domain.ProfitData{}, err
	}
	// Nómina: SOLO las corridas PAGADAS, por la suma de las líneas congeladas y
	// en el día local de paid_at (D1). Las abiertas no tienen paid_at y quedan
	// fuera del filtro y de la ventana: no cuentan.
	if err := dailyTotals(ctx, tx, &data.PayrollByDay,
		`SELECT (r.paid_at AT TIME ZONE $2)::date AS day, SUM(l.total_cents)::bigint
		 FROM payroll_runs r
		 JOIN payroll_lines l ON l.run_id = r.id
		 WHERE r.salon_id = $1 AND r.status = 'paid' AND r.paid_at IS NOT NULL
		   AND r.paid_at >= $3 AND r.paid_at < $4
		 GROUP BY 1`, salonID, timezone, from, to); err != nil {
		return domain.ProfitData{}, err
	}
	// Gastos: `spent_on` ya es un día local, así que solo se filtra el rango. Se
	// agrupa por (día, categoría): de la MISMA lectura salen el total por día y
	// el desglose por categoría, sin una segunda foto.
	if err := expenseTotals(ctx, tx, &data, salonID, timezone, from, to); err != nil {
		return domain.ProfitData{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProfitData{}, err
	}
	return data, nil
}

// dailyTotals corre la query de un componente y llena el mapa día -> centavos.
func dailyTotals(ctx context.Context, q querier, out *map[string]int,
	query, salonID, timezone string, from, to time.Time) error {
	rows, err := q.Query(ctx, query, salonID, timezone, from.UTC(), to.UTC())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var total int64
		if err := rows.Scan(&day, &total); err != nil {
			return err
		}
		cents, err := bigintToInt(total, "el total del día")
		if err != nil {
			return err
		}
		(*out)[dateOf(day)] = cents
	}
	return rows.Err()
}

// expenseTotals llena el total por día y el desglose por categoría con UNA sola
// lectura de gastos agrupada por (día, categoría).
func expenseTotals(ctx context.Context, q querier, data *domain.ProfitData,
	salonID, timezone string, from, to time.Time) error {
	rows, err := q.Query(ctx,
		`SELECT e.spent_on, e.category, SUM(e.amount_cents)::bigint
		 FROM expenses e
		 WHERE e.salon_id = $1
		   AND e.spent_on >= ($3 AT TIME ZONE $2)::date
		   AND e.spent_on <  ($4 AT TIME ZONE $2)::date
		 GROUP BY 1, 2
		 ORDER BY 1, 2`, salonID, timezone, from.UTC(), to.UTC())
	if err != nil {
		return err
	}
	defer rows.Close()
	byCategory := make(map[domain.ExpenseCategory]int, 4)
	for rows.Next() {
		var day time.Time
		var category string
		var total int64
		if err := rows.Scan(&day, &category, &total); err != nil {
			return err
		}
		cents, err := bigintToInt(total, "el total del día")
		if err != nil {
			return err
		}
		date := dateOf(day)
		data.ExpensesByDay[date] += cents
		byCategory[domain.ExpenseCategory(category)] += cents
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for category, total := range byCategory {
		data.ExpensesByCategory = append(data.ExpensesByCategory,
			domain.ExpenseCategoryTotal{Category: category, TotalCents: total})
	}
	return nil
}

var _ ports.ProfitRepository = Profit{}
