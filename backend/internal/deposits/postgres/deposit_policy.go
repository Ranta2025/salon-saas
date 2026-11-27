package postgres

import (
	"context"
	pgstore "salon-saas/internal/adapters/postgres"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// DepositPolicies implementa ports.DepositPolicyRepository: la política de señas
// del salón (F11, D2/D3; migración 037). A lo sumo UNA fila por salón y toda
// consulta va scopeada por salon_id.
type DepositPolicies struct {
	Pool *pgxpool.Pool
}

const depositPolicySelect = `SELECT salon_id,enabled,percent,hold_minutes,refund_hours,updated_at
	FROM deposit_policies`

func scanDepositPolicy(scan func(dest ...any) error) (domain.DepositPolicy, error) {
	var p domain.DepositPolicy
	if err := scan(&p.SalonID, &p.Enabled, &p.Percent, &p.HoldMinutes, &p.RefundHours, &p.UpdatedAt); err != nil {
		return domain.DepositPolicy{}, err
	}
	p.Saved = true
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, nil
}

// Find lee la política del salón. Sin fila = ErrNotFound (el caso de uso lo
// traduce a Saved=false: enabled false y el resto null, D3).
func (r DepositPolicies) Find(salonID string) (domain.DepositPolicy, error) {
	row := r.Pool.QueryRow(context.Background(), depositPolicySelect+` WHERE salon_id=$1`, salonID)
	p, err := scanDepositPolicy(row.Scan)
	if err != nil {
		return domain.DepositPolicy{}, pgstore.NotFound(err)
	}
	return p, nil
}

// Save es UN upsert que reemplaza los cuatro valores y updated_at, y devuelve la
// fila tal como quedó guardada (D3). No toca ninguna otra tabla: el salón de la
// FK es la autorización (un salón inexistente es 23503 -> 400 «referencia»).
func (r DepositPolicies) Save(p domain.DepositPolicy) (domain.DepositPolicy, error) {
	row := r.Pool.QueryRow(context.Background(),
		`INSERT INTO deposit_policies(salon_id,enabled,percent,hold_minutes,refund_hours,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (salon_id) DO UPDATE SET
		   enabled=EXCLUDED.enabled, percent=EXCLUDED.percent,
		   hold_minutes=EXCLUDED.hold_minutes, refund_hours=EXCLUDED.refund_hours,
		   updated_at=EXCLUDED.updated_at
		 RETURNING `+depositPolicyColumns,
		p.SalonID, p.Enabled, p.Percent, p.HoldMinutes, p.RefundHours, updatedAtOrNow(p.UpdatedAt))
	saved, err := scanDepositPolicy(row.Scan)
	if err != nil {
		return domain.DepositPolicy{}, pgstore.PGError(err)
	}
	return saved, nil
}

// depositPolicyColumns es la lista del RETURNING (mismo orden que el SELECT).
const depositPolicyColumns = `salon_id,enabled,percent,hold_minutes,refund_hours,updated_at`

// updatedAtOrNow evita guardar el cero de time.Time si el caso de uso no fijó el
// reloj: la columna tiene DEFAULT now() pero un INSERT explícito con el cero
// guardaría el año 1 y el orden de la pantalla quedaría mal.
func updatedAtOrNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

var _ ports.DepositPolicyRepository = DepositPolicies{}
