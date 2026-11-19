package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// pgErr traduce errores de Postgres a errores de dominio en el borde.
// 23P01 exclusión -> solape (409); 23503 FK -> referencia inexistente (400).
// Sin esto, el SQL crudo llegaría al cliente (fuga de internals + 500 mentiroso).
func exclusion(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23P01":
			return domain.ErrAlreadyExists
		case "23503":
			return domain.ErrValidation
		}
	}
	return err
}

func scanBooking(scan func(...interface{}) error) (domain.Booking, error) {
	var b domain.Booking
	var status string
	err := scan(&b.ID, &b.SalonID, &b.ProfessionalID, &b.ClientName, &b.ClientPhone,
		&b.StartsAt, &b.EndsAt, &status, &b.CreatedAt)
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
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		b.ID, b.SalonID, b.ProfessionalID, b.ClientName, b.ClientPhone,
		b.StartsAt, b.EndsAt, string(b.Status), b.CreatedAt)
	return exclusion(err)
}

func (r Bookings) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,created_at
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
		`SELECT id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,created_at
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
