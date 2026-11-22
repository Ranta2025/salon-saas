package pgstore

import (
	"context"
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
		&b.StartsAt, &b.EndsAt, &status, &b.ClientOpID, &b.CreatedAt, &b.ServiceID)
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

type Bookings struct{ Pool *pgxpool.Pool }

// bookingColumns es la lista única de columnas del turno. service_id va con
// COALESCE porque es NULL para los turnos del calendario: así el escaneo no
// necesita tipos anulables en Go.
const bookingColumns = `id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at,COALESCE(service_id,'')`

func (r Bookings) Save(b domain.Booking) error { return saveBooking(r.Pool, b) }

func saveBooking(q querier, b domain.Booking) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at,service_id)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''))`,
		b.ID, b.SalonID, b.ProfessionalID, b.ClientName, b.ClientPhone,
		b.StartsAt, b.EndsAt, string(b.Status), b.ClientOpID, b.CreatedAt, b.ServiceID)
	return pgError(err)
}

// FindByClientOpID busca una reserva ya registrada por su operation id.
// Base de la idempotencia de la cola offline: si dos reintentos llegan a la
// DB, el índice único deja pasar uno y el otro se resuelve por acá.
func (r Bookings) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	return findBookingByClientOpID(r.Pool, salonID, clientOpID)
}

func findBookingByClientOpID(q querier, salonID, clientOpID string) (domain.Booking, error) {
	row := q.QueryRow(context.Background(),
		`SELECT `+bookingColumns+`
		 FROM bookings WHERE salon_id=$1 AND client_op_id=$2 AND client_op_id <> ''`,
		salonID, clientOpID)
	b, err := scanBooking(row.Scan)
	if err != nil {
		return domain.Booking{}, notFound(err)
	}
	return b, nil
}

// FindByID resuelve un turno scopeado por salón: el id de otro tenant no
// existe (404). Es la autorización del PATCH.
func (r Bookings) FindByID(salonID, id string) (domain.Booking, error) {
	return findBooking(r.Pool, salonID, id)
}

func findBooking(q querier, salonID, id string) (domain.Booking, error) {
	row := q.QueryRow(context.Background(),
		`SELECT `+bookingColumns+`
		 FROM bookings WHERE salon_id=$1 AND id=$2`, salonID, id)
	b, err := scanBooking(row.Scan)
	if err != nil {
		return domain.Booking{}, notFound(err)
	}
	return b, nil
}

// Update persiste estado/horario de un turno existente. El WHERE lleva el
// salon_id (un id de otro tenant no se toca) y la EXCLUDE sigue siendo el
// árbitro del solape: 23P01 -> ErrAlreadyExists -> 409.
func (r Bookings) Update(b domain.Booking) error { return updateBooking(r.Pool, b) }

func updateBooking(q querier, b domain.Booking) error {
	tag, err := q.Exec(context.Background(),
		`UPDATE bookings
		 SET professional_id=$3, client_name=$4, client_phone=$5, starts_at=$6, ends_at=$7, status=$8, service_id=NULLIF($9,'')
		 WHERE salon_id=$1 AND id=$2`,
		b.SalonID, b.ID, b.ProfessionalID, b.ClientName, b.ClientPhone,
		b.StartsAt, b.EndsAt, string(b.Status), b.ServiceID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r Bookings) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	return findOverlapping(r.Pool, salonID, professionalID, start, end)
}

func findOverlapping(q querier, salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
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

func listBookings(q querier, salonID string, from, to time.Time) ([]domain.Booking, error) {
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
	ctx := context.Background()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op si ya commiteó

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
