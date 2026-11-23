// Lecturas masivas de la exportación CSV (F4, D8 del contrato Fase 6): métodos
// NUEVOS, cada uno UNA sola sentencia SQL y siempre scopeados por salon_id. No
// tocan los repositorios existentes (`GET /sales`, `/cash-closes`, `/clients`)
// ni sus queries: así el mismo archivo no puede mezclar dos fotos distintas.
package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// Exports implementa ports.ExportRepository.
type Exports struct{ Pool *pgxpool.Pool }

// exportLimit normaliza el LIMIT: el caso de uso siempre pide tope + 1, pero un
// 0 o negativo no puede convertirse en «sin filas» por accidente.
func exportLimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return domain.ExportRowsCap + 1
}

// ListExportSales implementa ports.ExportRepository: una fila por VENTA con el
// nombre de la cuenta profesional (vacío si no hay fila en users: LEFT JOIN) y
// la suma de cantidades de sus ítems, más reciente primero.
func (r Exports) ListExportSales(salonID string, from, to time.Time, limit int) ([]domain.ExportSaleRow, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT s.created_at, s.id, COALESCE(u.name, ''), s.client_name, s.method,
		        COALESCE((SELECT SUM(si.qty) FROM sale_items si WHERE si.sale_id = s.id), 0)::bigint,
		        s.total_cents
		 FROM sales s
		 LEFT JOIN users u ON u.id = s.professional_id
		 WHERE s.salon_id = $1 AND s.created_at >= $2 AND s.created_at < $3
		 ORDER BY s.created_at DESC, s.id
		 LIMIT $4`,
		salonID, from, to, exportLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ExportSaleRow, 0, 16)
	for rows.Next() {
		var row domain.ExportSaleRow
		var method string
		var items int64
		if err := rows.Scan(&row.CreatedAt, &row.SaleID, &row.ProfessionalName,
			&row.ClientName, &method, &items, &row.TotalCents); err != nil {
			return nil, err
		}
		row.CreatedAt = row.CreatedAt.UTC()
		row.Method = domain.PaymentMethod(method)
		if row.Items, err = bigintToInt(items, "la cantidad de ítems de la venta"); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListExportCashCloses implementa ports.ExportRepository: cierres con closed_at
// en la ventana, más reciente primero.
func (r Exports) ListExportCashCloses(salonID string, from, to time.Time, limit int) ([]domain.ExportCashCloseRow, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT opened_at, closed_at, system_cash_cents, counted_cents, difference_cents, note
		 FROM cash_closes
		 WHERE salon_id = $1 AND closed_at >= $2 AND closed_at < $3
		 ORDER BY closed_at DESC, id
		 LIMIT $4`,
		salonID, from, to, exportLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ExportCashCloseRow, 0, 16)
	for rows.Next() {
		var row domain.ExportCashCloseRow
		if err := rows.Scan(&row.OpenedAt, &row.ClosedAt, &row.SystemCashCents,
			&row.CountedCents, &row.DifferenceCents, &row.Note); err != nil {
			return nil, err
		}
		row.OpenedAt = row.OpenedAt.UTC()
		row.ClosedAt = row.ClosedAt.UTC()
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListExportClients implementa ports.ExportRepository: las fichas del salón
// ordenadas por nombre (el id desempata para que el archivo sea estable).
func (r Exports) ListExportClients(salonID string, limit int) ([]domain.ExportClientRow, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT name, phone, email, notes, created_at
		 FROM clients
		 WHERE salon_id = $1
		 ORDER BY name, id
		 LIMIT $2`,
		salonID, exportLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ExportClientRow, 0, 16)
	for rows.Next() {
		var row domain.ExportClientRow
		if err := rows.Scan(&row.Name, &row.Phone, &row.Email, &row.Notes, &row.CreatedAt); err != nil {
			return nil, err
		}
		row.CreatedAt = row.CreatedAt.UTC()
		out = append(out, row)
	}
	return out, rows.Err()
}
