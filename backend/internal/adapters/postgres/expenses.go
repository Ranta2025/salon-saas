// Gastos operativos en Postgres (F3b, D3/D4 del contrato F3b): migración 026.
//
// Todas las queries van scopeadas por salon_id: el gasto de otro tenant no se
// lista, no se encuentra, no se edita y no se borra (ErrNotFound -> 404 «Gasto no
// encontrado»). El gasto guarda un DÍA LOCAL (`spent_on DATE`): no depende de la
// zona horaria y el adaptador no la toca.
package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Expenses implementa ports.ExpenseRepository.
type Expenses struct{ Pool *pgxpool.Pool }

// expenseColumns es el SELECT único del gasto: lo usan la lectura, el listado y
// las respuestas de las escrituras para que digan exactamente lo mismo.
const expenseColumns = `
	 id, salon_id, category, amount_cents, spent_on, description,
	 COALESCE(created_by, ''), created_at`

// Save inserta el gasto. Los CHECK y las FK de la migración son la red de
// seguridad de los límites que ya validó el dominio (una referencia inexistente
// o un monto fuera de rango es 400 por pgError, nunca un 500).
func (r Expenses) Save(e domain.Expense) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO expenses(id,salon_id,category,amount_cents,spent_on,description,created_by,created_at)
		 VALUES($1,$2,$3,$4,$5::date,$6,NULLIF($7,''),$8)`,
		e.ID, e.SalonID, string(e.Category), e.AmountCents, e.SpentOn, e.Description,
		e.CreatedBy, e.CreatedAt)
	return pgError(err)
}

// ListRange trae hasta limit gastos del salón con spent_on en [from, to]
// (inclusivo), del más nuevo al más viejo. Pide UNA fila de más para poder decir
// si había más sin hacer un COUNT aparte.
func (r Expenses) ListRange(salonID, from, to string, limit int) ([]domain.Expense, bool, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.Pool.Query(context.Background(),
		`SELECT`+expenseColumns+`
		 FROM expenses
		 WHERE salon_id=$1 AND spent_on >= $2::date AND spent_on <= $3::date
		 ORDER BY spent_on DESC, created_at DESC, id DESC
		 LIMIT $4`,
		salonID, from, to, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]domain.Expense, 0, limit)
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// FindByID resuelve un gasto del salón. El id de otro tenant es ErrNotFound.
func (r Expenses) FindByID(salonID, id string) (domain.Expense, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT`+expenseColumns+` FROM expenses WHERE id=$1 AND salon_id=$2`, id, salonID)
	e, err := scanExpense(row)
	if err != nil {
		return domain.Expense{}, notFound(err)
	}
	return e, nil
}

// Update reemplaza los cuatro campos del gasto. El WHERE lleva el salón: sin
// filas afectadas es ErrNotFound (nunca un upsert que se lleve otro tenant).
func (r Expenses) Update(e domain.Expense) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE expenses
		 SET category=$3, amount_cents=$4, spent_on=$5::date, description=$6
		 WHERE id=$1 AND salon_id=$2`,
		e.ID, e.SalonID, string(e.Category), e.AmountCents, e.SpentOn, e.Description)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Delete borra el gasto del salón (D4: DELETE físico, es dinero operativo). Sin
// filas afectadas es ErrNotFound.
func (r Expenses) Delete(salonID, id string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`DELETE FROM expenses WHERE id=$1 AND salon_id=$2`, id, salonID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanExpense lee una fila de expenseColumns.
func scanExpense(row interface{ Scan(dest ...any) error }) (domain.Expense, error) {
	var e domain.Expense
	var category string
	var spentOn time.Time
	if err := row.Scan(&e.ID, &e.SalonID, &category, &e.AmountCents, &spentOn,
		&e.Description, &e.CreatedBy, &e.CreatedAt); err != nil {
		return domain.Expense{}, err
	}
	e.Category = domain.ExpenseCategory(category)
	e.SpentOn = dateOf(spentOn)
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}

var _ ports.ExpenseRepository = Expenses{}
