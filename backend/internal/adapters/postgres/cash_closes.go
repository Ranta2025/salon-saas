package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// CashCloses: cierres de caja por turno, uno atrás del otro.
type CashCloses struct{ Pool *pgxpool.Pool }

const cashCloseColumns = `id,salon_id,opened_at,closed_at,system_cash_cents,counted_cents,difference_cents,note`

func scanCashClose(scan func(...interface{}) error) (domain.CashClose, error) {
	var c domain.CashClose
	if err := scan(&c.ID, &c.SalonID, &c.OpenedAt, &c.ClosedAt, &c.SystemCashCents,
		&c.CountedCents, &c.DifferenceCents, &c.Note); err != nil {
		return domain.CashClose{}, err
	}
	// UTC estable en el borde, igual que en bookings.
	c.OpenedAt = c.OpenedAt.UTC()
	c.ClosedAt = c.ClosedAt.UTC()
	return c, nil
}

func (r CashCloses) Save(c domain.CashClose) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO cash_closes(id,salon_id,opened_at,closed_at,system_cash_cents,counted_cents,difference_cents,note)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (id) DO NOTHING`,
		c.ID, c.SalonID, c.OpenedAt, c.ClosedAt, c.SystemCashCents,
		c.CountedCents, c.DifferenceCents, c.Note)
	return pgError(err)
}

func (r CashCloses) Last(salonID string) (domain.CashClose, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+cashCloseColumns+` FROM cash_closes
		 WHERE salon_id=$1 ORDER BY closed_at DESC, id DESC LIMIT 1`, salonID)
	c, err := scanCashClose(row.Scan)
	if err != nil {
		return domain.CashClose{}, notFound(err)
	}
	return c, nil
}

func (r CashCloses) List(salonID string) ([]domain.CashClose, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+cashCloseColumns+` FROM cash_closes
		 WHERE salon_id=$1 ORDER BY closed_at DESC, id DESC`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CashClose
	for rows.Next() {
		c, err := scanCashClose(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
