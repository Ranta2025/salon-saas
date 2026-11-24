package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// SalonProfiles: el perfil público del salón (F16, D1). Toda query va scopeada
// por el id del salón y el slug no se escribe nunca.
type SalonProfiles struct{ Pool *pgxpool.Pool }

// Profile lee nombre, slug y los cinco datos opcionales (NULL = «sin dato», que
// el dominio ve como "").
func (r SalonProfiles) Profile(salonID string) (domain.SalonProfile, error) {
	p := domain.SalonProfile{SalonID: salonID}
	err := r.Pool.QueryRow(context.Background(),
		`SELECT slug, name, COALESCE(description,''), COALESCE(address,''), COALESCE(phone,''),
		        COALESCE(email,''), COALESCE(instagram,'')
		 FROM salons WHERE id=$1`, salonID,
	).Scan(&p.Slug, &p.Name, &p.Description, &p.Address, &p.Phone, &p.Email, &p.Instagram)
	if err != nil {
		return domain.SalonProfile{}, notFound(err)
	}
	return p, nil
}

// SaveProfile reemplaza nombre y los cinco datos en UN UPDATE: o cambian todos o
// ninguno. Un texto vacío se guarda como NULL. El slug no está en el SET. Sin
// filas afectadas el salón no existe (ErrNotFound, nunca un upsert).
func (r SalonProfiles) SaveProfile(p domain.SalonProfile) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE salons SET name=$2, description=NULLIF($3,''), address=NULLIF($4,''),
		        phone=NULLIF($5,''), email=NULLIF($6,''), instagram=NULLIF($7,'')
		 WHERE id=$1`,
		p.SalonID, p.Name, p.Description, p.Address, p.Phone, p.Email, p.Instagram)
	if err != nil {
		// Un CHECK de la migración 032 es 400 (dato fuera de rango), no un 500.
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var _ ports.SalonProfileRepository = SalonProfiles{}
