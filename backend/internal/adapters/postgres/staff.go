package pgstore

import (
	"context"

	"salon-saas/internal/domain"
)

// CountProfessionals: capacidad instalada del salón (ports.StaffCounter).
// Mismo tipo Memberships, en su propio archivo: el repo de membresías no
// engorda y el dashboard no depende de métodos que nadie más usa.
func (r Memberships) CountProfessionals(salonID string) (int, error) {
	var n int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM memberships WHERE salon_id=$1 AND role=$2`,
		salonID, string(domain.RoleProfessional)).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
