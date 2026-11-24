// Lectura del historial de una clienta (F5, D6): UNA sola transacción
// REPEATABLE READ de solo lectura con tope de 25 s, así los conteos, la última
// visita, las listas y el gasto son la MISMA foto. Cada lista tiene LIMIT 20 y
// el orden es «más reciente primero».
//
// Todas las queries van scopeadas por salon_id Y client_id: el historial de la
// ficha de otro salón no existe acá.
package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// clientHistoryTimeout es el tope de la transacción del historial: 25 s, por
// debajo de los 30 s del servidor HTTP (la lección de F4).
const clientHistoryTimeout = 25 * time.Second

// ClientHistory implementa ports.ClientHistoryRepository.
type ClientHistory struct {
	Pool *pgxpool.Pool
	// Timeout acota la transacción entera (0 = clientHistoryTimeout).
	Timeout time.Duration
}

// History lee la foto del historial. `now` decide qué turnos son «próximos» y
// `limit` acota cada lista.
func (r ClientHistory) History(salonID, clientID string, now time.Time, limit int) (domain.ClientHistoryData, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = clientHistoryTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return domain.ClientHistoryData{}, err
	}
	defer rollbackTx(tx) // no-op si ya commiteó

	if limit <= 0 {
		limit = 20
	}
	var data domain.ClientHistoryData
	// Conteos del cliente y los dos instantes que resumen el historial. Un
	// turno `done` es una visita; `no_show` y `cancelled` se cuentan aparte; los
	// `booked` FUTUROS son los próximos. Los booked viejos (sin marcar) no son
	// ni visita ni próximo.
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status = 'done')::int,
		        count(*) FILTER (WHERE status = 'no_show')::int,
		        count(*) FILTER (WHERE status = 'cancelled')::int,
		        count(*) FILTER (WHERE status = 'booked' AND starts_at > $3)::int,
		        max(starts_at) FILTER (WHERE status = 'done'),
		        min(starts_at) FILTER (WHERE status = 'booked' AND starts_at > $3)
		 FROM bookings
		 WHERE salon_id = $1 AND client_id = $2`,
		salonID, clientID, now.UTC()).Scan(
		&data.Visits, &data.NoShows, &data.Cancelled, &data.Upcoming,
		&data.LastDoneAt, &data.NextBookedAt); err != nil {
		return domain.ClientHistoryData{}, err
	}
	// Turnos recientes: el nombre del servicio y el de la profesional salen del
	// mismo salón (un turno sin servicio —reserva de staff— deja "").
	rows, err := tx.Query(ctx,
		`SELECT b.id, b.starts_at, b.status, COALESCE(s.name, ''), COALESCE(u.name, '')
		 FROM bookings b
		 LEFT JOIN services s ON s.id = b.service_id AND s.salon_id = b.salon_id
		 LEFT JOIN users u ON u.id = b.professional_id
		 WHERE b.salon_id = $1 AND b.client_id = $2
		 ORDER BY b.starts_at DESC, b.id DESC
		 LIMIT $3`,
		salonID, clientID, limit)
	if err != nil {
		return domain.ClientHistoryData{}, err
	}
	data.Bookings = make([]domain.ClientHistoryBooking, 0, limit)
	for rows.Next() {
		var b domain.ClientHistoryBooking
		var status string
		if err := rows.Scan(&b.ID, &b.StartsAt, &status, &b.ServiceName, &b.ProfessionalName); err != nil {
			rows.Close()
			return domain.ClientHistoryData{}, err
		}
		b.StartsAt = b.StartsAt.UTC()
		b.Status = domain.BookingStatus(status)
		data.Bookings = append(data.Bookings, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.ClientHistoryData{}, err
	}
	// Gasto: SOLO las ventas con client_id (D2: las ventas viejas no se
	// atribuyen) y dentro del mismo salón.
	var total int64
	if err := tx.QueryRow(ctx,
		`SELECT count(*)::int, COALESCE(SUM(total_cents), 0)::bigint
		 FROM sales WHERE salon_id = $1 AND client_id = $2`,
		salonID, clientID).Scan(&data.Spend.SalesCount, &total); err != nil {
		return domain.ClientHistoryData{}, err
	}
	cents, err := bigintToInt(total, "el gasto de la clienta")
	if err != nil {
		return domain.ClientHistoryData{}, err
	}
	data.Spend.TotalCents = cents

	rows, err = tx.Query(ctx,
		`SELECT id, created_at, total_cents, method
		 FROM sales
		 WHERE salon_id = $1 AND client_id = $2
		 ORDER BY created_at DESC, id DESC
		 LIMIT $3`,
		salonID, clientID, limit)
	if err != nil {
		return domain.ClientHistoryData{}, err
	}
	data.Sales = make([]domain.ClientHistorySale, 0, limit)
	for rows.Next() {
		var s domain.ClientHistorySale
		var method string
		if err := rows.Scan(&s.ID, &s.CreatedAt, &s.TotalCents, &method); err != nil {
			rows.Close()
			return domain.ClientHistoryData{}, err
		}
		s.CreatedAt = s.CreatedAt.UTC()
		s.Method = domain.PaymentMethod(method)
		data.Sales = append(data.Sales, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.ClientHistoryData{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.ClientHistoryData{}, err
	}
	return data, nil
}
