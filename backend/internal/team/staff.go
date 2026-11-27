// StaffService: el equipo que atiende en el salón. Lo consume el select de
// profesional de la agenda (el frontend no puede inventar IDs de profesionales).
package team

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type StaffService struct {
	Staff       ports.StaffLister
	Memberships ports.MembershipRepository
}

// List devuelve el personal del salón. Cualquier miembro puede leerlo (para
// agendar necesita saber quién atiende); un no-miembro recibe 403.
func (s StaffService) List(actor domain.Membership, salonID string) ([]domain.StaffMember, error) {
	if actor.SalonID != salonID {
		return nil, domain.ErrForbidden
	}
	if _, err := s.Memberships.Find(actor.UserID, salonID); err != nil {
		return nil, domain.ErrForbidden
	}
	list, err := s.Staff.ListStaff(salonID)
	if err != nil {
		return nil, err
	}
	// Solo quien atiende: el repo ya filtra por la misma regla del dominio y acá
	// se vuelve a aplicar (F2a). Antes bastaba el rol y el dueño que solo
	// administra aparecía en el select de profesional.
	out := make([]domain.StaffMember, 0, len(list))
	for _, m := range list {
		if m.Bookable() {
			out = append(out, m)
		}
	}
	return out, nil // el frontend recibe [] y no null
}
