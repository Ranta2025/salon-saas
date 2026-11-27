package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	pgstore "salon-saas/internal/adapters/postgres"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

func scanBooking(scan func(...interface{}) error) (domain.Booking, error) {
	var b domain.Booking
	var status string
	err := scan(&b.ID, &b.SalonID, &b.ProfessionalID, &b.ClientName, &b.ClientPhone,
		&b.StartsAt, &b.EndsAt, &status, &b.ClientOpID, &b.CreatedAt, &b.ServiceID, &b.CoveredFrom, &b.ResourceUnitID)
	if err != nil {
		return domain.Booking{}, err
	}
	b.Status = domain.BookingStatus(status)
	// Normalizar a UTC en el borde: la DB puede devolver su zona local y el
	// frontend no debería adivinar. El instante es el mismo, la forma es estable.
	b.StartsAt = b.StartsAt.UTC()
	b.EndsAt = b.EndsAt.UTC()
	b.CreatedAt = b.CreatedAt.UTC()
	return b, nil
}

type Bookings struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la espera del lock del salón y la transacción de
	// InSalonTx (H7). Cero = el valor por defecto (txDefaultTimeout).
	TxTimeout time.Duration
}

// bookingColumns es la lista única de columnas del turno. service_id va con
// COALESCE porque es NULL para los turnos del calendario, y covered_from porque
// es NULL en todo turno que nadie cubrió: así el escaneo no necesita tipos
// anulables en Go (S-F2d3-6).
const bookingColumns = `id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at,COALESCE(service_id,''),COALESCE(covered_from,''),COALESCE(resource_unit_id,'')`

func (r Bookings) Save(b domain.Booking) error {
	return r.inWriteLock(b.SalonID, func(q pgstore.Querier) error { return saveBooking(q, b) })
}

// inWriteLock serializa las ESCRITURAS de turnos de un mismo salón con un lock
// corto (xact lock: se libera solo al commit o rollback). Un turno tiene dos
// restricciones de solape (profesional y unidad de equipo); dos INSERT
// simultáneos del mismo salón se esperaban entre sí y Postgres abortaba a uno
// con un interbloqueo (40P01) tras un segundo de espera, de modo que una clienta
// recibía "sin lugar" aunque quedaran puestos libres. Con el lock, solo un
// INSERT está en vuelo por salón: no hay espera cruzada y las EXCLUDE quedan
// como la red de seguridad que rechaza el doble turno. La sección crítica es un
// solo INSERT/UPDATE; salones distintos no se esperan entre sí.
func (r Bookings) inWriteLock(salonID string, fn func(q pgstore.Querier) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "booking-write:"+salonID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func saveBooking(q pgstore.Querier, b domain.Booking) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at,service_id,covered_from,resource_unit_id)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),NULLIF($12,''),NULLIF($13,''))`,
		b.ID, b.SalonID, b.ProfessionalID, b.ClientName, b.ClientPhone,
		b.StartsAt, b.EndsAt, string(b.Status), b.ClientOpID, b.CreatedAt, b.ServiceID, b.CoveredFrom, b.ResourceUnitID)
	return bookingWriteError(err)
}

// bookingWriteError traduce el error de escribir un turno. Además de lo que ya
// traduce pgError (la EXCLUDE rechaza el doble turno: 23P01), un turno tiene
// DOS restricciones de solape (la de la profesional y la de la unidad de
// equipo): dos altas simultáneas que se pisan pueden esperarse la una a la otra
// y Postgres aborta a una con 40P01 (deadlock) o 40001. Eso NO es un error
// interno: es una carrera perdida por el mismo hueco, igual que un 23P01, y la
// transacción ya se deshizo, así que es seguro reintentar con otro par.
// Va acá y no en pgError porque en ventas o caja "ya existe" sería engañoso.
func bookingWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "40001") {
		return domain.ErrAlreadyExists
	}
	return pgstore.PGError(err)
}

// FindByClientOpID busca una reserva ya registrada por su operation id.
// Base de la idempotencia de la cola offline: si dos reintentos llegan a la
// DB, el índice único deja pasar uno y el otro se resuelve por acá.
func (r Bookings) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	return findBookingByClientOpID(r.Pool, salonID, clientOpID)
}

func findBookingByClientOpID(q pgstore.Querier, salonID, clientOpID string) (domain.Booking, error) {
	row := q.QueryRow(context.Background(),
		`SELECT `+bookingColumns+`
		 FROM bookings WHERE salon_id=$1 AND client_op_id=$2 AND client_op_id <> ''`,
		salonID, clientOpID)
	b, err := scanBooking(row.Scan)
	if err != nil {
		return domain.Booking{}, pgstore.NotFound(err)
	}
	return b, nil
}

// FindByID resuelve un turno scopeado por salón: el id de otro tenant no
// existe (404). Es la autorización del PATCH.
func (r Bookings) FindByID(salonID, id string) (domain.Booking, error) {
	return findBooking(r.Pool, salonID, id)
}

func findBooking(q pgstore.Querier, salonID, id string) (domain.Booking, error) {
	row := q.QueryRow(context.Background(),
		`SELECT `+bookingColumns+`
		 FROM bookings WHERE salon_id=$1 AND id=$2`, salonID, id)
	b, err := scanBooking(row.Scan)
	if err != nil {
		return domain.Booking{}, pgstore.NotFound(err)
	}
	return b, nil
}

// Update persiste estado/horario/profesional y quién tenía el turno de un turno
// existente (S-F2d3-6: covered_from se escribe en TODA escritura del turno). El
// WHERE lleva el salon_id (un id de otro tenant no se toca) y la EXCLUDE sigue
// siendo el árbitro del solape: 23P01 -> ErrAlreadyExists -> 409.
func (r Bookings) Update(b domain.Booking) error {
	return r.inWriteLock(b.SalonID, func(q pgstore.Querier) error { return updateBooking(q, b) })
}

func updateBooking(q pgstore.Querier, b domain.Booking) error {
	tag, err := q.Exec(context.Background(),
		`UPDATE bookings
		 SET professional_id=$3, client_name=$4, client_phone=$5, starts_at=$6, ends_at=$7, status=$8,
		     service_id=NULLIF($9,''), covered_from=NULLIF($10,''), resource_unit_id=NULLIF($11,'')
		 WHERE salon_id=$1 AND id=$2`,
		b.SalonID, b.ID, b.ProfessionalID, b.ClientName, b.ClientPhone,
		b.StartsAt, b.EndsAt, string(b.Status), b.ServiceID, b.CoveredFrom, b.ResourceUnitID)
	if err != nil {
		return bookingWriteError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CancelBookingReleasingClientOp cancels a BOOKED booking and frees its idempotency
// key in ONE statement, so a retry with the same key creates a fresh booking
// instead of getting the cancelled one back as a success (F11 native review
// R4-RETRY-CANCELLED, R4-COMPENSATION: either both happen or neither does). It is an
// optional capability used by the public booking flow when the deposit charge could
// not be created; a booking that is not `booked`, or another salon's, is never touched.
func (r Bookings) CancelBookingReleasingClientOp(salonID, bookingID string) error {
	return r.inWriteLock(salonID, func(q pgstore.Querier) error {
		_, err := q.Exec(context.Background(),
			`UPDATE bookings SET status='cancelled', client_op_id=''
			 WHERE salon_id=$1 AND id=$2 AND status='booked'`,
			salonID, bookingID)
		return bookingWriteError(err)
	})
}

func (r Bookings) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	return findOverlapping(r.Pool, salonID, professionalID, start, end)
}

func findOverlapping(q pgstore.Querier, salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	rows, err := q.Query(context.Background(),
		`SELECT `+bookingColumns+`
		 FROM bookings
		 WHERE salon_id=$1 AND professional_id=$2 AND status='booked'
		   AND tstzrange(starts_at,ends_at) && tstzrange($3,$4)`,
		salonID, professionalID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Booking
	for rows.Next() {
		b, err := scanBooking(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r Bookings) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	return listBookings(r.Pool, salonID, from, to)
}

func listBookings(q pgstore.Querier, salonID string, from, to time.Time) ([]domain.Booking, error) {
	rows, err := q.Query(context.Background(),
		`SELECT `+bookingColumns+`
		 FROM bookings
		 WHERE salon_id=$1 AND starts_at >= $2 AND starts_at < $3
		 ORDER BY starts_at`,
		salonID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Booking
	for rows.Next() {
		b, err := scanBooking(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// InSalonTx implementa ports.BookingTxRunner (S-F2d3-1/3): abre una transacción
// con un advisory lock del salón y commitea SOLO si fn termina bien. Cubrir una
// ausencia mueve todos sus turnos o ninguno; el lock evita que dos "Reasignar
// todo" del mismo salón se pisen y la EXCLUDE de bookings sigue siendo la que
// decide el solape (23P01 -> ErrAlreadyExists -> 409, nunca un 500).
//
// El lock se libera solo al terminar la transacción (commit o rollback), así que
// no puede quedar colgado por un error.
func (r Bookings) InSalonTx(salonID string, fn func(tx ports.BookingTx) error) error {
	// H7: la espera del lock y la transacción entera tienen un tope. Sin él, una
	// transacción trabada dejaba al pedido esperando para siempre con una
	// conexión del pool tomada, y los pedidos del mismo salón hacían cola detrás.
	// Al vencer, pgx cancela la consulta y el Rollback libera conexión y lock.
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx) // no-op si ya commiteó

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "booking-cover:"+salonID); err != nil {
		return err
	}
	if err := fn(bookingTx{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// bookingTx es la vista transaccional de la agenda: las mismas cinco operaciones
// que el repositorio, pero sobre la transacción abierta.
type bookingTx struct {
	tx  pgx.Tx
	ctx context.Context
}

func (b bookingTx) Save(booking domain.Booking) error { return saveBooking(b.tx, booking) }
func (b bookingTx) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	return findBookingByClientOpID(b.tx, salonID, clientOpID)
}
func (b bookingTx) FindByID(salonID, id string) (domain.Booking, error) {
	return findBooking(b.tx, salonID, id)
}
func (b bookingTx) Update(booking domain.Booking) error { return updateBooking(b.tx, booking) }
func (b bookingTx) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	return findOverlapping(b.tx, salonID, professionalID, start, end)
}
func (b bookingTx) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	return listBookings(b.tx, salonID, from, to)
}

var _ ports.BookingRepository = Bookings{}
var _ ports.BookingTxRunner = Bookings{}
var _ ports.BookingTx = bookingTx{}
