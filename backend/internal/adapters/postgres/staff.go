package pgstore

import (
	"context"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
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

// ListStaff implementa ports.StaffLister: el personal que ATIENDE, con nombre y
// oficio (el oficio no es un permiso: la agenda lo muestra, no filtra por él).
// La regla es la del dominio (Membership.Bookable) traducida a SQL carácter por
// carácter: un profesional atiende siempre; owner/admin solo con attends = true;
// recepción y cliente NUNCA (aunque una fila vieja o manual tenga la marca
// encendida). El orden por nombre es el que espera el frontend.
func (r Memberships) ListStaff(salonID string) ([]domain.StaffMember, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT u.id, u.name, m.role, m.attends, m.trade
		 FROM memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.salon_id=$1
		   AND (m.role='professional' OR (m.attends AND m.role IN ('owner','admin')))
		 ORDER BY u.name, u.id`,
		salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.StaffMember, 0, 8)
	for rows.Next() {
		var role string
		var m domain.StaffMember
		if err := rows.Scan(&m.ID, &m.Name, &role, &m.Attends, &m.Trade); err != nil {
			return nil, err
		}
		m.Role = domain.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

var _ ports.StaffLister = Memberships{}
