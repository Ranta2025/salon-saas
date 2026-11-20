package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

func scanBooking(scan func(...interface{}) error) (domain.Booking, error) {
	var b domain.Booking
	var status string
	err := scan(&b.ID, &b.SalonID, &b.ProfessionalID, &b.ClientName, &b.ClientPhone,
		&b.StartsAt, &b.EndsAt, &status, &b.ClientOpID, &b.CreatedAt)
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

func (r Bookings) Save(b domain.Booking) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		b.ID, b.SalonID, b.ProfessionalID, b.ClientName, b.ClientPhone,
		b.StartsAt, b.EndsAt, string(b.Status), b.ClientOpID, b.CreatedAt)
	return pgError(err)
}

// FindByClientOpID busca una reserva ya registrada por su operation id.
// Base de la idempotencia de la cola offline: si dos reintentos llegan a la
// DB, el índice único deja pasar uno y el otro se resuelve por acá.
func (r Bookings) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at
		 FROM bookings WHERE salon_id=$1 AND client_op_id=$2 AND client_op_id <> ''`,
		salonID, clientOpID)
	b, err := scanBooking(row.Scan)
	if err != nil {
		return domain.Booking{}, notFound(err)
	}
	return b, nil
}

func (r Bookings) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at
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
	rows, err := r.Pool.Query(context.Background(),
		`SELECT id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,client_op_id,created_at
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
