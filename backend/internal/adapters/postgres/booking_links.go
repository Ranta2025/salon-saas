package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// BookingLinks es el adaptador del enlace firmado de un turno (F1, migración
// 027).
//
// Dos garantías duras viven acá y no en el caso de uso:
//   - el token en claro no aparece en ninguna query: se guarda y se busca su
//     SHA-256;
//   - el reclamo es un UPDATE CONDICIONAL (`used_at IS NULL`) hecho DENTRO de la
//     transacción del cambio y DESPUÉS del advisory lock de la agenda del salón:
//     dos pedidos simultáneos con el mismo token se serializan en el lock y el
//     segundo ve 0 filas (ErrNotFound -> el 404 genérico del contrato).
type BookingLinks struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la transacción del enlace: sin tope, una transacción
	// trabada (esperando el lock de un salón ocupado) deja el pedido esperando
	// para siempre con una conexión del pool tomada (mismo criterio que H7).
	TxTimeout time.Duration
}

// bookingLinkColumns es la lista única de columnas. `used_at` se escanea en un
// puntero porque NULL significa "todavía sirve".
const bookingLinkColumns = `id,salon_id,booking_id,token_hash,created_at,used_at`

func scanBookingLink(scan func(...any) error) (domain.BookingLink, error) {
	var l domain.BookingLink
	if err := scan(&l.ID, &l.SalonID, &l.BookingID, &l.TokenHash, &l.CreatedAt, &l.UsedAt); err != nil {
		return domain.BookingLink{}, err
	}
	// Normalizar a UTC en el borde (el instante es el mismo, la forma es estable).
	l.CreatedAt = l.CreatedAt.UTC()
	if l.UsedAt != nil {
		used := l.UsedAt.UTC()
		l.UsedAt = &used
	}
	return l, nil
}

// Save inserta un token nuevo. No hay upsert: cada email lleva su propio enlace y
// el índice único de token_hash solo rechazaría un token repetido (imposible con
// 256 bits de entropía, pero la DB no confía en eso).
func (r BookingLinks) Save(l domain.BookingLink) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO booking_links(id,salon_id,booking_id,token_hash,created_at,used_at)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		l.ID, l.SalonID, l.BookingID, l.TokenHash, l.CreatedAt, l.UsedAt)
	return pgError(err)
}

// FindByTokenHash es la ÚNICA query sin salon_id: el hash del token es la
// capability que resuelve el turno antes de saber de qué salón es (quien abre el
// enlace no tiene sesión). Sin fila es ErrNotFound.
func (r BookingLinks) FindByTokenHash(tokenHash string) (domain.BookingLink, error) {
	link, err := scanBookingLink(r.Pool.QueryRow(context.Background(),
		`SELECT `+bookingLinkColumns+` FROM booking_links WHERE token_hash=$1`, tokenHash).Scan)
	if err != nil {
		return domain.BookingLink{}, notFound(err)
	}
	return link, nil
}

// InBookingLinkTx abre la transacción del enlace y commitea SOLO si fn termina
// bien. NO toma ningún lock por su cuenta: el único advisory lock lo pide el caso
// de uso (`LockWrites`) después de resolver el enlace, que es lo único que dice de
// qué salón es. Una sola conexión del pool para toda la operación.
func (r BookingLinks) InBookingLinkTx(fn func(tx ports.BookingLinkTx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx) // no-op si ya commiteó
	if err := fn(bookingLinkTx{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// bookingLinkTx es la vista transaccional del enlace.
type bookingLinkTx struct {
	tx  pgx.Tx
	ctx context.Context
}

// FindLink resuelve el enlace por su hash SIN reclamarlo.
func (t bookingLinkTx) FindLink(tokenHash string) (domain.BookingLink, error) {
	link, err := scanBookingLink(t.tx.QueryRow(t.ctx,
		`SELECT `+bookingLinkColumns+` FROM booking_links WHERE token_hash=$1`, tokenHash).Scan)
	if err != nil {
		return domain.BookingLink{}, notFound(err)
	}
	return link, nil
}

// LockWrites toma el advisory lock de ESCRITURAS del salón: la MISMA clave
// (`booking-write:<salón>`) que usan Book, Update y el alta de una reserva
// pública. Es el único lock de esta transacción y se libera solo al terminar.
func (t bookingLinkTx) LockWrites(salonID string) error {
	_, err := t.tx.Exec(t.ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "booking-write:"+salonID)
	return err
}

// ClaimLink es el reclamo atómico (D2): se lleva el enlace SOLO si sigue sin usar
// y lo devuelve ya usado. Sin filas es ErrNotFound (el segundo pedido simultáneo,
// o un token ya gastado).
func (t bookingLinkTx) ClaimLink(id string, now time.Time) (domain.BookingLink, error) {
	link, err := scanBookingLink(t.tx.QueryRow(t.ctx,
		`UPDATE booking_links SET used_at=$2
		 WHERE id=$1 AND used_at IS NULL
		 RETURNING `+bookingLinkColumns, id, now.UTC()).Scan)
	if err != nil {
		return domain.BookingLink{}, notFound(err)
	}
	return link, nil
}

// SaveLink emite el token nuevo de una reprogramación en la MISMA transacción del
// cambio: si el cambio no commitea, el token no existe.
func (t bookingLinkTx) SaveLink(l domain.BookingLink) error {
	_, err := t.tx.Exec(t.ctx,
		`INSERT INTO booking_links(id,salon_id,booking_id,token_hash,created_at,used_at)
		 VALUES($1,$2,$3,$4,$5,$6)`,
		l.ID, l.SalonID, l.BookingID, l.TokenHash, l.CreatedAt, l.UsedAt)
	return pgError(err)
}

// FindBooking es la lectura del turno dentro de la transacción, con el MISMO
// scope por salón que el repositorio: el turno de otro tenant no existe.
func (t bookingLinkTx) FindBooking(salonID, id string) (domain.Booking, error) {
	return findBooking(t.tx, salonID, id)
}

// UpdateBooking persiste el cambio del turno. La EXCLUDE de Postgres sigue siendo
// el árbitro del solape: 23P01 -> ErrAlreadyExists -> 409 y la transacción se
// deshace con el token ya reclamado (el token no se consume).
func (t bookingLinkTx) UpdateBooking(b domain.Booking) error {
	return updateBooking(t.tx, b)
}

var (
	_ ports.BookingLinkRepository = BookingLinks{}
	_ ports.BookingLinkTxRunner   = BookingLinks{}
	_ ports.BookingLinkTx         = bookingLinkTx{}
)
