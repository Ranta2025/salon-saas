package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// CashCloses: cierres de caja por turno, uno atrás del otro.
type CashCloses struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la espera del lock del salón y la transacción de
	// InSalonTx (H7). Cero = el valor por defecto (txDefaultTimeout).
	TxTimeout time.Duration
}

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

// InSalonTx implementa ports.SalonTxRunner (B-09): ejecuta fn dentro de una
// transacción que primero toma un advisory lock del salón.
//
// Por qué xact lock: dos cierres simultáneos leían el MISMO "último cierre",
// sumaban la MISMA ventana de efectivo y guardaban dos cierres con el efectivo
// completo (el salón cerraba $300.000 dos veces). El lock serializa: el segundo
// ve el cierre del primero y su ventana arranca donde terminó.
//
// El lock se libera solo al terminar la transacción (rollback o commit), así que
// no puede quedar colgado por un error.
func (r CashCloses) InSalonTx(salonID string, fn func(tx ports.CashCloseTx) error) error {
	// H7: mismo tope que en las reservas, para que un cierre trabado no deje a
	// los demás cierres del salón esperando el lock para siempre.
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx) // no-op si ya commiteó

	// hashtext(salon_id) da una clave de 32 bits estable por salón: salones
	// distintos no se esperan entre sí.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "cash-close:"+salonID); err != nil {
		return err
	}
	if err := fn(cashCloseTx{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// cashCloseTx es la vista transaccional de la caja.
type cashCloseTx struct {
	tx  pgx.Tx
	ctx context.Context
}

func (c cashCloseTx) Save(close domain.CashClose) error {
	_, err := c.tx.Exec(c.ctx,
		`INSERT INTO cash_closes(id,salon_id,opened_at,closed_at,system_cash_cents,counted_cents,difference_cents,note)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (id) DO NOTHING`,
		close.ID, close.SalonID, close.OpenedAt, close.ClosedAt, close.SystemCashCents,
		close.CountedCents, close.DifferenceCents, close.Note)
	return pgError(err)
}

func (c cashCloseTx) Last(salonID string) (domain.CashClose, error) {
	row := c.tx.QueryRow(c.ctx,
		`SELECT `+cashCloseColumns+` FROM cash_closes
		 WHERE salon_id=$1 ORDER BY closed_at DESC, id DESC LIMIT 1`, salonID)
	close, err := scanCashClose(row.Scan)
	if err != nil {
		return domain.CashClose{}, notFound(err)
	}
	return close, nil
}

func (c cashCloseTx) List(salonID string) ([]domain.CashClose, error) {
	rows, err := c.tx.Query(c.ctx,
		`SELECT `+cashCloseColumns+` FROM cash_closes
		 WHERE salon_id=$1 ORDER BY closed_at DESC, id DESC`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CashClose
	for rows.Next() {
		close, err := scanCashClose(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, close)
	}
	return out, rows.Err()
}

func (c cashCloseTx) SumCashCents(salonID string, from, to time.Time) (int, error) {
	sales := Sales{}
	return sales.sumCents(c.ctx, c.tx, salonID, from, to, domain.MethodCash)
}

var _ ports.SalonTxRunner = CashCloses{}
var _ ports.CashCloseTx = cashCloseTx{}
