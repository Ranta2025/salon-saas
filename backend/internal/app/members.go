// MemberService: la pantalla de equipo del salón. Solo owner/admin; el owner
// suma admins, el admin suma profesionales y recepción, y el salón nunca se
// queda sin owner.
package app

import (
	"errors"
	"fmt"
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
		return domain.Member{}, fmt.Errorf("%w: rol desconocido %q", domain.ErrValidation, role)
	}
	if !role.AddableBy(m.Role) {
		return domain.Member{}, domain.ErrForbidden
	}
	email = domain.NormalizeEmail(email)
	if email == "" {
		return domain.Member{}, fmt.Errorf("%w: el email es obligatorio", domain.ErrValidation)
	}
	user, err := s.Users.FindByEmail(email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Member{}, fmt.Errorf("%w: no existe una cuenta con el email %s (la persona debe registrarse primero)", domain.ErrValidation, email)
		}
		return domain.Member{}, err
	}
	membership, err := domain.NewMembership(user.ID, salonID, role)
	if err != nil {
		return domain.Member{}, err
	}
	if _, err := s.Memberships.Find(user.ID, salonID); err == nil {
		return domain.Member{}, fmt.Errorf("%w: esa persona ya es miembro del salón", domain.ErrAlreadyExists)
	} else if !errors.Is(err, domain.ErrNotFound) {
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
			return fmt.Errorf("%w: no se puede quitar al último owner del salón", domain.ErrValidation)
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
