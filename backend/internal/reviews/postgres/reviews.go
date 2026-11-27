// Adaptador de Postgres del módulo de reseñas (F13, D2..D7): la configuración
// del salón, la lista del panel, ocultar/mostrar, la sección pública y el canje
// del enlace, más los pedidos de reseña del barrido.
//
// Dos reglas viven acá porque son de la base y no del caso de uso:
//
//   - El CANJE es UN solo UPDATE condicional por token_hash (`used_at IS NULL`
//     AND `expires_at > now`) seguido del INSERT de la reseña, en UNA
//     transacción: dos POST simultáneos del mismo enlace se serializan en el
//     lock de la fila y el segundo ya no encuentra el token (404 uniforme). El
//     UNIQUE(booking_id) de `reviews` es la última barrera: si algún día el
//     UPDATE dejara pasar dos, el segundo INSERT rompe con 23505 en vez de
//     duplicar la reseña.
//   - El RECLAMO del envío es `sent_at IS NULL`: el INSERT del pedido o el
//     UPDATE de reutilización (rota el token) fijan sent_at en el MISMO paso en
//     que dicen «gané». Dos barredores no pueden mandar dos correos, y un envío
//     fallido se deshace (Unclaim) para que el próximo tick reintente.
//
// Toda consulta va scopeada por salon_id: la reseña o la configuración de otro
// salón es ErrNotFound y no escribe nada. Los 40P01/40001 se traducen a 409.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/reviews"
)

// Reviews implementa ports.ReviewRepository y ports.ReviewRequestRepository: las
// dos caras del módulo comparten el pool y la traducción de errores.
type Reviews struct {
	Pool *pgxpool.Pool
	// TxTimeout acota cada transacción (0 = default).
	TxTimeout time.Duration
}

// reviewColumns es la lista única de columnas de una reseña.
const reviewColumns = `id,salon_id,booking_id,rating,comment,author_name,service_name,hidden,created_at`

func scanReview(scan func(dest ...any) error) (ports.Review, error) {
	var r ports.Review
	if err := scan(&r.ID, &r.SalonID, &r.BookingID, &r.Rating, &r.Comment, &r.AuthorName,
		&r.ServiceName, &r.Hidden, &r.CreatedAt); err != nil {
		return ports.Review{}, err
	}
	r.CreatedAt = r.CreatedAt.UTC()
	return r, nil
}

// --- Configuración (D6) ---------------------------------------------------------

// Settings lee la configuración del salón. Sin fila (nunca guardada) es
// ErrNotFound: el caso de uso responde los valores de fábrica.
func (r Reviews) Settings(salonID string) (ports.ReviewSettings, error) {
	var s ports.ReviewSettings
	err := r.Pool.QueryRow(context.Background(),
		`SELECT salon_id, enabled, delay_hours FROM review_settings WHERE salon_id=$1`, salonID).
		Scan(&s.SalonID, &s.Enabled, &s.DelayHours)
	if err != nil {
		return ports.ReviewSettings{}, pgstore.NotFound(err)
	}
	s.Saved = true
	return s, nil
}

// SaveSettings guarda (upsert) la configuración. La FK del salón es la guarda de
// que la fila es de un salón real.
func (r Reviews) SaveSettings(s ports.ReviewSettings) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO review_settings(salon_id,enabled,delay_hours,updated_at)
		 VALUES($1,$2,$3,now())
		 ON CONFLICT (salon_id) DO UPDATE SET
		   enabled=EXCLUDED.enabled, delay_hours=EXCLUDED.delay_hours, updated_at=now()`,
		s.SalonID, s.Enabled, s.DelayHours)
	return pgstore.PGError(err)
}

// --- Panel (D6) ------------------------------------------------------------------

// List devuelve una página del panel del salón, del más nuevo al más viejo, con
// el mismo cursor opaco de la auditoría ((created_at, id) estrictamente menor).
func (r Reviews) List(salonID string, q ports.ReviewQuery) ([]ports.Review, error) {
	var beforeAt *time.Time
	var beforeID *string
	if q.Before != nil {
		t, id := q.Before.CreatedAt.UTC(), q.Before.ID
		beforeAt, beforeID = &t, &id
	}
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+reviewColumns+`
		 FROM reviews
		 WHERE salon_id=$1
		   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::text))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		salonID, beforeAt, beforeID, q.Limit)
	if err != nil {
		return nil, pgstore.PGError(err)
	}
	defer rows.Close()
	out := make([]ports.Review, 0, q.Limit)
	for rows.Next() {
		r, err := scanReview(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetHidden oculta o muestra UNA reseña del salón. Es UN UPDATE scopeado por
// salon_id: la reseña de otro salón no afecta filas (ErrNotFound) y repetir la
// misma llamada responde igual (idempotente, D6). Nunca edita ni borra.
func (r Reviews) SetHidden(salonID, reviewID string, hidden bool) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE reviews SET hidden=$3 WHERE id=$1 AND salon_id=$2`, reviewID, salonID, hidden)
	if err != nil {
		return pgstore.PGError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// --- Web pública (D7) ---------------------------------------------------------------

// Visible arma la sección pública con las reseñas VISIBLES del salón: el total,
// el promedio SIN redondear (lo redondea el caso de uso) y las 6 más recientes
// con el día en la zona horaria del salón. Una reseña oculta en el panel no
// aparece ni suma.
func (r Reviews) Visible(salonID string) (ports.PublicReviews, error) {
	ctx := context.Background()
	var tz string
	err := r.Pool.QueryRow(ctx, `SELECT timezone FROM salons WHERE id=$1`, salonID).Scan(&tz)
	if err != nil {
		return ports.PublicReviews{}, pgstore.NotFound(err)
	}
	// Una zona ilegible (fila vieja o escrita a mano) no puede tumbar la landing:
	// se usa el default de fábrica.
	if _, err := time.LoadLocation(tz); err != nil {
		tz = domain.DefaultTimezone
	}
	var count int
	var average float64
	if err := r.Pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(avg(rating), 0) FROM reviews WHERE salon_id=$1 AND NOT hidden`,
		salonID).Scan(&count, &average); err != nil {
		return ports.PublicReviews{}, pgstore.PGError(err)
	}
	out := ports.PublicReviews{Average: average, Count: count, Items: []ports.PublicReview{}}
	if count == 0 {
		return out, nil
	}
	rows, err := r.Pool.Query(ctx,
		`SELECT rating, comment, author_name, to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD')
		 FROM reviews
		 WHERE salon_id=$1 AND NOT hidden
		 ORDER BY created_at DESC, id DESC
		 LIMIT $3`, salonID, tz, reviews.MaxPublicReviews)
	if err != nil {
		return ports.PublicReviews{}, pgstore.PGError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item ports.PublicReview
		if err := rows.Scan(&item.Rating, &item.Comment, &item.Author, &item.Date); err != nil {
			return ports.PublicReviews{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}

// --- Canje del enlace (D4) -----------------------------------------------------------

// Submit canjea el token y crea la reseña en UNA transacción:
//
//  1. `UPDATE review_requests SET used_at (and delivered_at when it was never marked:
//     redeeming proves the mail arrived) WHERE token_hash AND used_at IS NULL
//     AND expires_at > now` — el consumo atómico del enlace. Sin filas (usado,
//     vencido o desconocido) es ErrNotFound, el MISMO error para los tres.
//  2. El nombre del turno y del servicio para el snapshot (D5).
//  3. El INSERT de la reseña: UNIQUE(booking_id) es la barrera final.
func (r Reviews) Submit(req ports.ReviewSubmit) (ports.Review, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return ports.Review{}, err
	}
	defer pgstore.RollbackTx(tx)

	var requestID, salonID, bookingID string
	err = tx.QueryRow(ctx,
		`UPDATE review_requests SET used_at=$2, delivered_at=COALESCE(delivered_at, $2)
		 WHERE token_hash=$1 AND used_at IS NULL AND expires_at > $2
		 RETURNING id, salon_id, booking_id`,
		req.TokenHash, req.Now.UTC()).Scan(&requestID, &salonID, &bookingID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Uniforme: desconocido, vencido o ya usado.
		return ports.Review{}, domain.ErrNotFound
	}
	if err != nil {
		return ports.Review{}, pgstore.BusyError(err, reviews.MsgLinkInvalid)
	}
	// El snapshot del turno: nombre de la clienta (para D5, que recorta el
	// apellido) y el nombre del servicio de HOY.
	var clientName, serviceName string
	if err := tx.QueryRow(ctx,
		`SELECT b.client_name, COALESCE(sv.name, '')
		 FROM bookings b LEFT JOIN services sv ON sv.id = b.service_id
		 WHERE b.id=$1 AND b.salon_id=$2`, bookingID, salonID).Scan(&clientName, &serviceName); err != nil {
		return ports.Review{}, pgstore.BusyError(err, reviews.MsgLinkInvalid)
	}
	review := ports.Review{
		ID: domain.NewID(), SalonID: salonID, BookingID: bookingID,
		Rating: req.Rating, Comment: req.Comment,
		AuthorName: reviews.AuthorName(clientName), ServiceName: reviews.ServiceName(serviceName),
		CreatedAt: req.Now.UTC(),
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO reviews(id,salon_id,booking_id,rating,comment,author_name,service_name,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING `+reviewColumns,
		review.ID, review.SalonID, review.BookingID, review.Rating, review.Comment,
		review.AuthorName, review.ServiceName, review.CreatedAt).Scan(&review.ID, &review.SalonID,
		&review.BookingID, &review.Rating, &review.Comment, &review.AuthorName, &review.ServiceName,
		&review.Hidden, &review.CreatedAt)
	if err != nil {
		// 23505 (una reseña ya existe para ese turno) es el 409 del contrato de
		// errores: con el UPDATE condicional del paso 1 no tendría que pasar, y
		// que pase es preferible a duplicar la reseña.
		return ports.Review{}, pgstore.PGError(err)
	}
	review.CreatedAt = review.CreatedAt.UTC()
	if err := tx.Commit(ctx); err != nil {
		return ports.Review{}, pgstore.BusyError(err, reviews.MsgLinkInvalid)
	}
	return review, nil
}

// --- Pedidos de reseña (D3/D4) ---------------------------------------------------------

// Link resuelve el enlace vigente y sin usar.
func (r Reviews) Link(tokenHash string, now time.Time) (ports.ReviewLink, error) {
	var l ports.ReviewLink
	err := r.Pool.QueryRow(context.Background(),
		`SELECT sa.name, COALESCE(sv.name, ''), b.starts_at, sa.timezone
		 FROM review_requests rr
		 JOIN bookings b ON b.id = rr.booking_id
		 JOIN salons sa ON sa.id = rr.salon_id
		 LEFT JOIN services sv ON sv.id = b.service_id
		 WHERE rr.token_hash=$1 AND rr.used_at IS NULL AND rr.expires_at > $2`,
		tokenHash, now.UTC()).Scan(&l.SalonName, &l.ServiceName, &l.StartsAt, &l.Timezone)
	if err != nil {
		return ports.ReviewLink{}, pgstore.NotFound(err)
	}
	l.StartsAt = l.StartsAt.UTC()
	return l, nil
}

// reviewLeaseMinutes es el arriendo del reclamo, en minutos, tal como lo usa la
// SQL (make_interval). Sale del puerto para que el barrido y este adaptador midan
// el MISMO plazo.
var reviewLeaseMinutes = int(ports.ClaimLease / time.Minute)

// Due devuelve los turnos que ya pueden pedir reseña (D3): `done`, de un salón con
// las reseñas activas, con correo, terminados hace al menos delay_hours y como
// máximo 14 días, y SIN un pedido ENTREGADO o usado. Un pedido sin entregar del
// que NADIE tiene el arriendo (nunca se reclamó, o el arriendo venció porque el
// proceso murió entre el reclamo y el envío) vuelve a salir: es el reintento.
func (r Reviews) Due(now time.Time, limit int) ([]ports.DueReview, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT b.id, b.salon_id, sa.name, COALESCE(sv.name, ''), b.client_name, be.email,
		        b.starts_at, sa.timezone
		 FROM bookings b
		 JOIN review_settings rs ON rs.salon_id = b.salon_id AND rs.enabled
		 JOIN booking_emails be ON be.booking_id = b.id
		 JOIN salons sa ON sa.id = b.salon_id
		 LEFT JOIN services sv ON sv.id = b.service_id
		 WHERE b.status = 'done'
		   AND be.email <> ''
		   AND b.ends_at + make_interval(hours => rs.delay_hours) <= $1
		   AND b.ends_at > $1 - interval '14 days'
		   AND NOT EXISTS (
		     SELECT 1 FROM review_requests rr
		     WHERE rr.booking_id = b.id
		       AND (
		         -- Entregado o canjeado: no se pide más.
		         rr.delivered_at IS NOT NULL
		         OR rr.used_at IS NOT NULL
		         -- Un enlace que ya venció no se puede refrescar sin otro pedido.
		         OR rr.expires_at <= $1
		         -- Arriendo VIVO: otro barredor lo está mandando ahora mismo.
		         OR (rr.sent_at IS NOT NULL AND rr.sent_at > $1 - make_interval(mins => $3))
		       )
		   )
		 ORDER BY b.ends_at, b.id
		 LIMIT $2`, now.UTC(), limit, reviewLeaseMinutes)
	if err != nil {
		return nil, pgstore.PGError(err)
	}
	defer rows.Close()
	out := make([]ports.DueReview, 0, limit)
	for rows.Next() {
		var d ports.DueReview
		if err := rows.Scan(&d.BookingID, &d.SalonID, &d.SalonName, &d.ServiceName, &d.ClientName,
			&d.Email, &d.StartsAt, &d.Timezone); err != nil {
			return nil, err
		}
		d.StartsAt = d.StartsAt.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// Claim crea el pedido del turno —o reutiliza uno reclamable rotándole el token— y
// RENUEVA el arriendo del envío (sent_at) en UNA sola sentencia.
//
// Por qué un solo INSERT ... ON CONFLICT DO UPDATE con WHERE: la condición de
// «reclamable» (sin entregar, sin usar, sin vencer y con el arriendo vencido) se
// evalúa bajo el lock de la fila, así que dos barredores simultáneos no pueden
// ganar los dos: el segundo reevalúa el WHERE con la fila ya actualizada y no
// devuelve nada (claimed=false). El UNIQUE(booking_id) decide además quién CREA
// el pedido, y el token se rota en cada reclamo porque la base solo guarda su
// hash: el enlace del intento perdido deja de servir.
func (r Reviews) Claim(req ports.ReviewClaim) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	var id string
	err := r.Pool.QueryRow(ctx,
		`INSERT INTO review_requests(id,salon_id,booking_id,token_hash,created_at,expires_at,sent_at)
		 VALUES($1,$2,$3,$4,$5,$6,$5)
		 ON CONFLICT (booking_id) DO UPDATE SET
		   token_hash = EXCLUDED.token_hash,
		   expires_at = EXCLUDED.expires_at,
		   sent_at    = EXCLUDED.sent_at
		 WHERE review_requests.delivered_at IS NULL
		   AND review_requests.used_at IS NULL
		   AND review_requests.expires_at > EXCLUDED.sent_at
		   AND (
		     review_requests.sent_at IS NULL
		     OR review_requests.sent_at <= EXCLUDED.sent_at - make_interval(mins => $7)
		   )
		 RETURNING id`,
		req.ID, req.SalonID, req.BookingID, req.TokenHash, req.Now.UTC(), req.ExpiresAt.UTC(),
		reviewLeaseMinutes).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Otro barredor tiene el arriendo, o el pedido ya se entregó o se usó.
		return "", false, nil
	}
	if err != nil {
		return "", false, pgstore.PGError(err)
	}
	return id, true, nil
}

// Unclaim deshace el arriendo de ESTE proceso (sent_at = sentAt) cuando el correo
// no salió: el próximo tick lo reintenta sin esperar los 15 minutos. Si otro
// proceso ya lo reclamó, no se toca.
func (r Reviews) Unclaim(requestID string, sentAt time.Time) error {
	_, err := r.Pool.Exec(context.Background(),
		`UPDATE review_requests SET sent_at=NULL WHERE id=$1 AND sent_at=$2`, requestID, sentAt.UTC())
	return pgstore.PGError(err)
}

// MarkDelivered sella la entrega del correo. Condicional al arriendo que este
// proceso tiene: si otro barredor ya lo reclamó (sent_at distinto), no escribe.
func (r Reviews) MarkDelivered(requestID string, sentAt time.Time) error {
	_, err := r.Pool.Exec(context.Background(),
		`UPDATE review_requests SET delivered_at=$2 WHERE id=$1 AND sent_at=$2 AND delivered_at IS NULL`,
		requestID, sentAt.UTC())
	return pgstore.PGError(err)
}

// compile-time checks: un solo adaptador implementa las dos caras del módulo.
var (
	_ ports.ReviewRepository        = Reviews{}
	_ ports.ReviewRequestRepository = Reviews{}
	_ ports.PublicReviewReader      = reviews.Service{}
)
