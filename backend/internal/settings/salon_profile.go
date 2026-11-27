// SalonProfileService: el perfil público del salón (F16, D1). Cualquier miembro
// activo lo lee; solo owner/admin lo edita.
package settings

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type SalonProfileService struct {
	Profiles    ports.SalonProfileRepository
	Memberships ports.MembershipRepository
}

// Get devuelve el perfil. Cualquier miembro activo del salón.
func (s SalonProfileService) Get(actor domain.Membership, salonID string) (domain.SalonProfile, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return domain.SalonProfile{}, err
	}
	return s.Profiles.Profile(salonID)
}

// RequireManager es el permiso SOLO (sin tocar datos): el handler lo llama ANTES
// de leer el cuerpo, así quien no puede editar recibe 403 aunque el cuerpo esté
// mal formado (lección de F8).
func (s SalonProfileService) RequireManager(actor domain.Membership, salonID string) error {
	_, err := s.manager(actor, salonID)
	return err
}

// Update REEMPLAZA nombre y los cinco datos. Solo owner/admin. Valida TODO antes
// de escribir: un 400 no cambia nada.
func (s SalonProfileService) Update(actor domain.Membership, salonID string, in domain.SalonProfileInput) (domain.SalonProfile, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.SalonProfile{}, err
	}
	current, err := s.Profiles.Profile(salonID)
	if err != nil {
		return domain.SalonProfile{}, err
	}
	updated, err := current.Updated(in)
	if err != nil {
		return domain.SalonProfile{}, err
	}
	if err := s.Profiles.SaveProfile(updated); err != nil {
		return domain.SalonProfile{}, err
	}
	return updated, nil
}

// manager exige membership real (no confía en el rol que trae el actor) y
// permiso de configuración.
func (s SalonProfileService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// member valida que el actor sea miembro ACTIVO del salón en la DB: una baja
// pierde el acceso en su próximo pedido.
func (s SalonProfileService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
