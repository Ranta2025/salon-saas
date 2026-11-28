package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ProfessionalServices: qué servicios hace cada profesional (F2c). Todo va
// scopeado por salon_id: el id de otro tenant no existe para estas queries.
type ProfessionalServices struct{ Pool *pgxpool.Pool }

// ListServiceIDs trae los servicios de un miembro del salón, ordenados por id
// para que la respuesta sea estable.
func (r ProfessionalServices) ListServiceIDs(salonID, userID string) ([]string, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT service_id FROM professional_services
		 WHERE salon_id=$1 AND user_id=$2
		 ORDER BY service_id`, salonID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, 8)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetServices REEMPLAZA la lista del miembro dentro de una transacción: o queda
// la lista nueva entera o la anterior. Un id inexistente rompe la FK y el
// rollback deja la lista intacta (la atomicidad no la da el orden del código).
func (r ProfessionalServices) SetServices(salonID, userID string, serviceIDs []string) error {
	ctx := context.Background()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM professional_services WHERE salon_id=$1 AND user_id=$2`,
		salonID, userID); err != nil {
		return err
	}
	seen := make(map[string]bool, len(serviceIDs))
	for _, id := range serviceIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := tx.Exec(ctx,
			`INSERT INTO professional_services(salon_id,user_id,service_id) VALUES($1,$2,$3)`,
			salonID, userID, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ListProfessionalsFor trae quien ATIENDE (la regla del dominio traducida a SQL:
// profesional siempre, owner/admin con attends, y nunca quien está dado de baja)
// y HACE ese servicio. Es la lista de elegibles de la disponibilidad y de la
// reserva pública: una baja no se ofrece (S-F2g2-3).
func (r ProfessionalServices) ListProfessionalsFor(salonID, serviceID string) ([]domain.StaffMember, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT u.id, u.name, m.role, m.attends, m.trade
		 FROM professional_services ps
		 JOIN memberships m ON m.user_id = ps.user_id AND m.salon_id = ps.salon_id
		 JOIN users u ON u.id = m.user_id
		 WHERE ps.salon_id=$1 AND ps.service_id=$2 AND m.ended_at IS NULL
		   AND (m.role='professional' OR (m.attends AND m.role IN ('owner','admin')))
		 ORDER BY u.name, u.id`, salonID, serviceID)
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

var _ ports.ProfessionalServiceRepository = ProfessionalServices{}
