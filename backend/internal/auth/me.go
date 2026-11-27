// MeService: "quién soy y en qué salones estoy". Es el endpoint que evita que
// el frontend tenga que pedirle un ID a la persona para pegarlo a mano.
package auth

import (
	"errors"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// MeSalon es un salón del usuario con su rol. Forma del contrato:
// {"id","slug","name","role"}.
type MeSalon struct {
	ID   string
	Slug string
	Name string
	Role domain.Role
}

// Me es la respuesta completa de GET /me.
type Me struct {
	User   domain.User
	Salons []MeSalon
}

type MeService struct {
	Users   ports.UserRepository
	Salons  ports.SalonRepository
	Members ports.MembershipLister
}

// Me resuelve el usuario del token y sus salones con nombre y rol.
//
// Un token válido de un usuario que ya no existe no es un 500: es un 401 (la
// credencial dejó de representar a nadie).
func (s MeService) Me(userID string) (Me, error) {
	if userID == "" {
		return Me{}, domain.ErrUnauthorized
	}
	u, err := s.Users.FindByID(userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return Me{}, domain.ErrUnauthorized
		}
		return Me{}, err
	}
	mems, err := s.Members.ListByUser(userID)
	if err != nil {
		return Me{}, err
	}
	// Siempre array (nunca null): el frontend itera sin chequear.
	salons := make([]MeSalon, 0, len(mems))
	for _, m := range mems {
		salon, err := s.Salons.FindByID(m.SalonID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue // membership huérfana: no rompe el /me de la persona
			}
			return Me{}, err
		}
		salons = append(salons, MeSalon{ID: salon.ID, Slug: salon.Slug, Name: salon.Name, Role: m.Role})
	}
	return Me{User: u, Salons: salons}, nil
}
