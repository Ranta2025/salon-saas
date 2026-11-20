package pgstore

import (
	"context"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// MemberAdminRepository: la pantalla de equipo del salón. Todo va scopeado por
// salon_id: un membership de otro tenant no existe para estas queries.

// ListMembers devuelve el equipo con nombre y email (join con users), ordenado
// por nombre como lo espera la pantalla.
func (r Memberships) ListMembers(salonID string) ([]domain.Member, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT u.id, u.name, u.email, m.role
		 FROM memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.salon_id=$1
		 ORDER BY u.name, u.id`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Member, 0, 8)
	for rows.Next() {
		var role string
		var m domain.Member
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &role); err != nil {
			return nil, err
		}
		m.Role = domain.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMember quita a alguien del salón. Si no era miembro de ESTE salón no
// afecta filas: ErrNotFound (no distingue "no existe" de "es de otro salón").
func (r Memberships) DeleteMember(userID, salonID string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`DELETE FROM memberships WHERE user_id=$1 AND salon_id=$2`, userID, salonID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountRole cuenta miembros con un rol dentro del salón (protección del último
// owner).
func (r Memberships) CountRole(salonID string, role domain.Role) (int, error) {
	var n int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM memberships WHERE salon_id=$1 AND role=$2`,
		salonID, string(role)).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

var _ ports.MemberAdminRepository = Memberships{}
var _ ports.SalonSettingsRepository = Salons{}
