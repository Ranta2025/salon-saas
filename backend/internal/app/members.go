// MemberService: la pantalla de equipo del salón. Solo owner/admin; el owner
// suma admins, el admin suma profesionales y recepción, y el salón nunca se
// queda sin owner.
package app

import (
	"errors"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type MemberService struct {
	Members     ports.MemberAdminRepository
	Memberships ports.MembershipRepository
	Users       ports.UserRepository
}

// List devuelve el equipo con nombre y email. Solo owner/admin: es la pantalla
// donde se dan y quitan permisos.
func (s MemberService) List(actor domain.Membership, salonID string) ([]domain.Member, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return nil, err
	}
	list, err := s.Members.ListMembers(salonID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.Member{}
	}
	return list, nil
}

// Add suma a una persona que YA tiene cuenta. No crea usuarios: el salón no
// inventa cuentas ni claves por terceros.
func (s MemberService) Add(actor domain.Membership, salonID, email string, role domain.Role) (domain.Member, error) {
	m, err := s.manager(actor, salonID)
	if err != nil {
		return domain.Member{}, err
	}
	if !role.Valid() {
		// Los roles válidos se nombran en español: el valor crudo de la API
		// ("professional") no le dice nada a quien administra el salón (E-07).
		return domain.Member{}, domain.NewMessage(domain.ErrValidation,
			"Rol desconocido: usá dueño, administración, profesional, recepción o cliente")
	}
	if !role.AddableBy(m.Role) {
		return domain.Member{}, domain.ErrForbidden
	}
	email = domain.NormalizeEmail(email)
	if email == "" {
		return domain.Member{}, domain.NewMessage(domain.ErrValidation, "El email es obligatorio")
	}
	user, err := s.Users.FindByEmail(email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Member{}, domain.NewMessage(domain.ErrValidation, "La persona todavía no tiene cuenta")
		}
		return domain.Member{}, err
	}
	membership, err := domain.NewMembership(user.ID, salonID, role)
	if err != nil {
		return domain.Member{}, err
	}
	existing, err := s.Memberships.Find(user.ID, salonID)
	switch {
	case err == nil && existing.Role == role:
		return domain.Member{}, domain.NewMessage(domain.ErrAlreadyExists, "Ya tiene ese rol")
	case err == nil:
		return domain.Member{}, domain.NewMessage(domain.ErrAlreadyExists,
			"Ya es parte del equipo como "+existing.Role.Label())
	case !errors.Is(err, domain.ErrNotFound):
		return domain.Member{}, err
	}
	if err := s.Memberships.Save(membership); err != nil {
		return domain.Member{}, err
	}
	return domain.Member{ID: user.ID, Name: user.Name, Email: user.Email, Role: role}, nil
}

// Remove saca a alguien del salón. El último owner no se va: sin dueño nadie
// puede volver a administrar el negocio.
func (s MemberService) Remove(actor domain.Membership, salonID, userID string) error {
	if _, err := s.manager(actor, salonID); err != nil {
		return err
	}
	if strings.TrimSpace(userID) == "" {
		return domain.ErrNotFound
	}
	target, err := s.Memberships.Find(userID, salonID)
	if err != nil {
		return domain.ErrNotFound
	}
	if target.Role == domain.RoleOwner {
		owners, err := s.Members.CountRole(salonID, domain.RoleOwner)
		if err != nil {
			return err
		}
		if owners <= 1 {
			return domain.NewMessage(domain.ErrValidation, "No se puede quitar al último dueño")
		}
	}
	return s.Members.DeleteMember(userID, salonID)
}

// manager exige membership real y permiso de gestión (owner/admin).
func (s MemberService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
