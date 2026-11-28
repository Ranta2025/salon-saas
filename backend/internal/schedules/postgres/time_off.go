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
)

// TimeOff: ausencias de las profesionales del salón (F2d2).
//
// Además del id, TODO va scopeado por salon_id: la ausencia de otro tenant no se
// lista, no se encuentra y no se borra (ErrNotFound -> 404, nunca un borrado
// silencioso ni un 500).
type TimeOff struct{ Pool *pgxpool.Pool }

// timeOffColumns es la proyección única de las tres lecturas: si mañana se agrega
// una columna, se agrega acá y no en tres SELECT distintos.
const timeOffColumns = `id, salon_id, user_id, starts_at, ends_at, reason,
	COALESCE(created_by,''), created_at`

// Save inserta la ausencia. La FK (user_id, salon_id) es la que impide dejar una
// ausencia colgada de alguien que no es del salón.
func (r TimeOff) Save(t domain.TimeOff) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at,reason,created_by,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8)`,
		t.ID, t.SalonID, t.UserID, t.StartsAt.UTC(), t.EndsAt.UTC(), t.Reason,
		t.CreatedBy, t.CreatedAt.UTC())
	// Traducido: la EXCLUDE de solape llega como 23P01 y tiene que salir como
	// ErrAlreadyExists, no como un error crudo de Postgres (que el HTTP
	// convertiría en un 500 en vez del 400 del contrato).
	return pgstore.PGError(err)
}

// ListRange trae las ausencias del salón que PISAN [from, to), ordenadas por
// inicio. El solape contra el rango se pregunta en SQL (starts_at < to AND
// ends_at > from): traer todo el salón para filtrar en memoria no escala.
func (r TimeOff) ListRange(salonID string, from, to time.Time) ([]domain.TimeOff, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+timeOffColumns+` FROM time_off
		 WHERE salon_id=$1 AND starts_at < $3 AND ends_at > $2
		 ORDER BY starts_at, id`, salonID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TimeOff, 0, 8)
	for rows.Next() {
		t, err := scanTimeOff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindByID resuelve una ausencia del salón. Otro salón es ErrNotFound.
func (r TimeOff) FindByID(salonID, id string) (domain.TimeOff, error) {
	t, err := scanTimeOff(r.Pool.QueryRow(context.Background(),
		`SELECT `+timeOffColumns+` FROM time_off WHERE salon_id=$1 AND id=$2`, salonID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TimeOff{}, domain.ErrNotFound
	}
	return t, err
}

// Delete borra la ausencia del salón. Sin filas afectadas es ErrNotFound: el id
// de otro tenant no existe acá.
func (r TimeOff) Delete(salonID, id string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`DELETE FROM time_off WHERE salon_id=$1 AND id=$2`, salonID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanTimeOff lee una fila con la proyección única (sirve para Query y QueryRow).
func scanTimeOff(row interface {
	Scan(dest ...any) error
}) (domain.TimeOff, error) {
	var t domain.TimeOff
	err := row.Scan(&t.ID, &t.SalonID, &t.UserID, &t.StartsAt, &t.EndsAt, &t.Reason,
		&t.CreatedBy, &t.CreatedAt)
	return t, err
}

var _ ports.TimeOffRepository = TimeOff{}
