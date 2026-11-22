// TenantService: crea salones y agrega miembros con RBAC mínimo.
package app

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type TenantService struct {
	Salons      ports.SalonRepository
	Memberships ports.MembershipRepository
}

// CreateSalon crea el salón y hace owner a quien lo creó.
// Orden: validar dominio -> chequear slug libre -> guardar salón -> guardar owner.
// Si falla el owner después del salón, el llamante decide (Fase 1: error; Fase 4: transacción real).
func (s TenantService) CreateSalon(ownerID, name, slug string) (domain.Salon, error) {
	if ownerID == "" {
		return domain.Salon{}, domain.ErrUnauthorized
	}
	salon, err := domain.NewSalon(name, slug)
	if err != nil {
		return domain.Salon{}, err
	}
	if _, err := s.Salons.FindBySlug(salon.Slug); err == nil {
		return domain.Salon{}, domain.ErrAlreadyExists
	}
	if err := s.Salons.Save(salon); err != nil {
		return domain.Salon{}, err
	}
	m, err := domain.NewMembership(ownerID, salon.ID, domain.RoleOwner)
	if err != nil {
		return domain.Salon{}, err
	}
	// El salón nace de una sola persona y esa persona atiende: el dueño-barbero
	// del día uno (S-F2a-4). La pantalla de equipo lo puede apagar después.
	m.Attends = true
	return salon, s.Memberships.Save(m)
}

// AddMember solo lo puede hacer quien configura el salón (owner/admin).
// Patrón Guard: primero permiso, después validación, último persistencia.
func (s TenantService) AddMember(actor domain.Membership, userID, salonID string, role domain.Role) error {
	if !actor.CanManage() {
		return domain.ErrForbidden
	}
	if actor.SalonID != salonID {
		return domain.ErrForbidden
	}
	m, err := domain.NewMembership(userID, salonID, role)
	if err != nil {
		return err
	}
	return s.Memberships.Save(m)
}
