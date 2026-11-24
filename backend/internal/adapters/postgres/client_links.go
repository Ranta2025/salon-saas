// El vínculo de un turno o una venta con la ficha de la clienta (F5, D1/D3).
// Es un UPDATE APARTE del INSERT (D1: no se tocan las sentencias de turnos y
// ventas) y el caso de uso lo llama best-effort: un fallo se loguea y nunca
// tumba el turno ni la venta.
//
// Todas las queries van scopeadas por salon_id y por id: la ficha de otro salón
// no se puede vincular aunque el id llegue por el body (D3-c).
package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClientLinks implementa ports.ClientLinkRepository.
type ClientLinks struct{ Pool *pgxpool.Pool }

// LinkBookingByID vincula el turno con ESA ficha. El JOIN exige que la ficha sea
// del MISMO salón y que el turno sea del salón: sin filas afectadas no hay
// error (best-effort, el turno ya existe).
func (r ClientLinks) LinkBookingByID(salonID, bookingID, clientID string) error {
	_, err := r.Pool.Exec(context.Background(),
		`UPDATE bookings b SET client_id = c.id
		 FROM clients c
		 WHERE b.salon_id = $1 AND b.id = $2
		   AND c.id = $3 AND c.salon_id = b.salon_id`,
		salonID, bookingID, clientID)
	return pgError(err)
}

// LinkBookingByPhone vincula el turno del personal con la ÚNICA ficha del salón
// cuyo teléfono normalizado coincide (la misma regla que domain.NormalizePhone y
// que la columna generada phone_norm). Sin coincidencia, teléfono vacío o dos
// fichas con ese teléfono, no toca nada: NUNCA crea una ficha (D3-b). Solo
// escribe filas con client_id NULL, así que reintentar no pisa un vínculo.
func (r ClientLinks) LinkBookingByPhone(salonID, bookingID string) error {
	_, err := r.Pool.Exec(context.Background(),
		`UPDATE bookings b SET client_id = c.id
		 FROM clients c
		 WHERE b.salon_id = $1 AND b.id = $2 AND b.client_id IS NULL
		   AND c.salon_id = b.salon_id
		   AND c.phone_norm <> ''
		   AND c.phone_norm = regexp_replace(b.client_phone, '[^0-9]', '', 'g')
		   AND (
		     SELECT count(*) FROM clients c2
		     WHERE c2.salon_id = b.salon_id AND c2.phone_norm = c.phone_norm
		   ) = 1`,
		salonID, bookingID)
	return pgError(err)
}

// LinkSaleByID vincula la venta del POS con la ficha elegida a mano. El JOIN
// exige la misma pertenencia al salón: una ficha de otro tenant no se vincula.
func (r ClientLinks) LinkSaleByID(salonID, saleID, clientID string) error {
	_, err := r.Pool.Exec(context.Background(),
		`UPDATE sales s SET client_id = c.id
		 FROM clients c
		 WHERE s.salon_id = $1 AND s.id = $2
		   AND c.id = $3 AND c.salon_id = s.salon_id`,
		salonID, saleID, clientID)
	return pgError(err)
}
