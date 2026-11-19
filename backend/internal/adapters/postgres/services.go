package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// Services: catálogo del salón. Toda query va scopeada por salon_id.
type Services struct{ Pool *pgxpool.Pool }

func scanService(scan func(...interface{}) error) (domain.Service, error) {
	var s domain.Service
	if err := scan(&s.ID, &s.SalonID, &s.Name, &s.DurationMin, &s.PriceCents, &s.CommissionPct, &s.Active); err != nil {
		return domain.Service{}, err
	}
	return s, nil
}

const serviceColumns = `id,salon_id,name,duration_min,price_cents,commission_pct,active`

func (r Services) Save(s domain.Service) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO services(id,salon_id,name,duration_min,price_cents,commission_pct,active)
		 VALUES($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (id) DO UPDATE SET
		   name=EXCLUDED.name,
		   duration_min=EXCLUDED.duration_min,
		   price_cents=EXCLUDED.price_cents,
		   commission_pct=EXCLUDED.commission_pct,
		   active=EXCLUDED.active`,
		s.ID, s.SalonID, s.Name, s.DurationMin, s.PriceCents, s.CommissionPct, s.Active)
	// Nombre activo duplicado -> índice único -> ErrAlreadyExists (409).
	return pgError(err)
}

func (r Services) FindByID(salonID, id string) (domain.Service, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+serviceColumns+` FROM services WHERE salon_id=$1 AND id=$2`, salonID, id)
	s, err := scanService(row.Scan)
	if err != nil {
		return domain.Service{}, notFound(err)
	}
	return s, nil
}

func (r Services) List(salonID string, includeInactive bool) ([]domain.Service, error) {
	q := `SELECT ` + serviceColumns + ` FROM services WHERE salon_id=$1`
	if !includeInactive {
		q += ` AND active`
	}
	q += ` ORDER BY name`
	rows, err := r.Pool.Query(context.Background(), q, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Service
	for rows.Next() {
		s, err := scanService(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
