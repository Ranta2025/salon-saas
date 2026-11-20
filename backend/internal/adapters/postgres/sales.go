package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// Sales: ventas del POS con sus ítems. La venta y sus líneas son una sola
// operación: si falla un ítem no queda una venta sin detalle (transacción).
type Sales struct{ Pool *pgxpool.Pool }

func (r Sales) Save(s domain.Sale) error {
	ctx := context.Background()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op si ya commiteó

	if _, err := tx.Exec(ctx,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,client_op_id,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.ID, s.SalonID, s.ProfessionalID, s.ClientName, s.TotalCents, string(s.Method), s.ClientOpID, s.CreatedAt); err != nil {
		return pgError(err)
	}
	for _, it := range s.Items {
		if _, err := tx.Exec(ctx,
			`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents)
			 VALUES($1,$2,$3,$4,$5)`,
			domain.NewID(), s.ID, it.ServiceID, it.Qty, it.UnitPriceCents); err != nil {
			return pgError(err)
		}
	}
	return tx.Commit(ctx)
}

// FindByClientOpID busca una venta ya registrada por su operation id.
// Base de la idempotencia de la cola offline (ver FindByClientOpID de Bookings).
func (r Sales) FindByClientOpID(salonID, clientOpID string) (domain.Sale, error) {
	var s domain.Sale
	var method string
	err := r.Pool.QueryRow(context.Background(),
		`SELECT `+saleColumns+` FROM sales
		 WHERE salon_id=$1 AND client_op_id=$2 AND client_op_id <> ''`,
		salonID, clientOpID).Scan(&s.ID, &s.SalonID, &s.ProfessionalID, &s.ClientName,
		&s.TotalCents, &method, &s.ClientOpID, &s.CreatedAt)
	if err != nil {
		return domain.Sale{}, notFound(err)
	}
	s.Method = domain.PaymentMethod(method)
	s.CreatedAt = s.CreatedAt.UTC()
	rows, err := r.Pool.Query(context.Background(),
		`SELECT service_id, qty, unit_price_cents FROM sale_items WHERE sale_id=$1 ORDER BY id`,
		s.ID)
	if err != nil {
		return domain.Sale{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var it domain.SaleItem
		if err := rows.Scan(&it.ServiceID, &it.Qty, &it.UnitPriceCents); err != nil {
			return domain.Sale{}, err
		}
		s.Items = append(s.Items, it)
	}
	return s, rows.Err()
}

const saleColumns = `id,salon_id,professional_id,client_name,total_cents,method,client_op_id,created_at`

func (r Sales) ListRange(salonID string, from, to time.Time) ([]domain.Sale, error) {
	ctx := context.Background()
	rows, err := r.Pool.Query(ctx,
		`SELECT `+saleColumns+` FROM sales
		 WHERE salon_id=$1 AND created_at >= $2 AND created_at < $3
		 ORDER BY created_at DESC, id`,
		salonID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sales []domain.Sale
	for rows.Next() {
		var s domain.Sale
		var method string
		if err := rows.Scan(&s.ID, &s.SalonID, &s.ProfessionalID, &s.ClientName, &s.TotalCents, &method, &s.ClientOpID, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Method = domain.PaymentMethod(method)
		s.CreatedAt = s.CreatedAt.UTC()
		sales = append(sales, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(sales) == 0 {
		return sales, nil
	}

	// Ítems de esas ventas: same scope (salon_id) para no traer líneas ajenas.
	itemRows, err := r.Pool.Query(ctx,
		`SELECT si.sale_id, si.service_id, si.qty, si.unit_price_cents
		 FROM sale_items si
		 JOIN sales s ON s.id = si.sale_id
		 WHERE s.salon_id=$1 AND s.created_at >= $2 AND s.created_at < $3
		 ORDER BY si.sale_id, si.id`,
		salonID, from, to)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()
	items := map[string][]domain.SaleItem{}
	for itemRows.Next() {
		var saleID string
		var it domain.SaleItem
		if err := itemRows.Scan(&saleID, &it.ServiceID, &it.Qty, &it.UnitPriceCents); err != nil {
			return nil, err
		}
		items[saleID] = append(items[saleID], it)
	}
	if err := itemRows.Err(); err != nil {
		return nil, err
	}
	for i := range sales {
		sales[i].Items = items[sales[i].ID]
		if sales[i].Items == nil {
			sales[i].Items = []domain.SaleItem{}
		}
	}
	return sales, nil
}

// SumCents suma totales en [from,to). method vacío = todos los métodos.
func (r Sales) SumCents(salonID string, from, to time.Time, method domain.PaymentMethod) (int, error) {
	var total int
	var err error
	if method == "" {
		err = r.Pool.QueryRow(context.Background(),
			`SELECT COALESCE(SUM(total_cents),0)::int FROM sales
			 WHERE salon_id=$1 AND created_at >= $2 AND created_at < $3`,
			salonID, from, to).Scan(&total)
	} else {
		err = r.Pool.QueryRow(context.Background(),
			`SELECT COALESCE(SUM(total_cents),0)::int FROM sales
			 WHERE salon_id=$1 AND created_at >= $2 AND created_at < $3 AND method=$4`,
			salonID, from, to, string(method)).Scan(&total)
	}
	if err != nil {
		return 0, err
	}
	return total, nil
}

// TopServices: ranking por cantidad vendida; el ingreso del servicio desempata.
func (r Sales) TopServices(salonID string, from, to time.Time, limit int) ([]domain.TopService, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.Pool.Query(context.Background(),
		`SELECT si.service_id, sv.name, SUM(si.qty)::int, SUM(si.qty * si.unit_price_cents)::int
		 FROM sale_items si
		 JOIN sales s ON s.id = si.sale_id
		 JOIN services sv ON sv.id = si.service_id AND sv.salon_id = s.salon_id
		 WHERE s.salon_id=$1 AND s.created_at >= $2 AND s.created_at < $3
		 GROUP BY si.service_id, sv.name
		 ORDER BY 3 DESC, 4 DESC, sv.name
		 LIMIT $4`,
		salonID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TopService
	for rows.Next() {
		var t domain.TopService
		if err := rows.Scan(&t.ServiceID, &t.Name, &t.Qty, &t.RevenueCents); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
