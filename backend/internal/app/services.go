// ServiceService: catálogo de servicios del salón. Configurar precios es
// privilegio de quien configura el salón (owner/admin); el resto solo mira.
package app

import (
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type ServiceService struct {
	Services    ports.ServiceRepository
	Memberships ports.MembershipRepository
	// Professionals: quién hace el servicio. El alta no asigna a nadie salvo
	// que el body traiga professional_ids (S-F2c-5).
	Professionals ports.ProfessionalServiceRepository
}

// maxProfessionalIDs es el tope de professional_ids del alta (S-F2c-5): un body
// enorme no puede recorrer todo el equipo.
const maxProfessionalIDs = 50

// Create agrega un servicio al catálogo. Solo owner/admin del salón.
//
// professionalIDs es OPCIONAL (S-F2c-5): vacío = el servicio nace sin nadie que
// lo haga y la pantalla de servicios lo avisa; con ids, se asigna a esos
// profesionales, que tienen que ser reservables de ESTE salón. Todos los ids se
// validan antes de guardar nada: un alta rechazada no deja el servicio creado.
func (s ServiceService) Create(actor domain.Membership, salonID, name string, durationMin, priceCents, commissionPct int, professionalIDs []string) (domain.Service, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.Service{}, err
	}
	svc, err := domain.NewService(salonID, name, durationMin, priceCents, commissionPct)
	if err != nil {
		return domain.Service{}, err
	}
	if err := s.ensureFreeName(salonID, svc.Name, ""); err != nil {
		return domain.Service{}, err
	}
	assignees, err := s.bookableProfessionals(salonID, professionalIDs)
	if err != nil {
		return domain.Service{}, err
	}
	if err := s.Services.Save(svc); err != nil {
		// El índice único de la DB es la garantía dura: si dos requests
		// concurrentes pasan el chequeo amable, una vuelve como AlreadyExists.
		return domain.Service{}, err
	}
	for _, userID := range assignees {
		current, err := s.Professionals.ListServiceIDs(salonID, userID)
		if err != nil {
			return domain.Service{}, err
		}
		if err := s.Professionals.SetServices(salonID, userID, append(current, svc.ID)); err != nil {
			return domain.Service{}, err
		}
	}
	return svc, nil
}

// bookableProfessionals valida la lista opcional del alta: tope de 50, sin
// repetidos y cada id tiene que ser alguien que ATIENDE en este salón. Un id de
// otro tenant tampoco existe acá (mismo mensaje).
func (s ServiceService) bookableProfessionals(salonID string, professionalIDs []string) ([]string, error) {
	// El tope se mide sobre lo que vino en el body: 51 ids repetidos también
	// son un body que no se acepta.
	if len(professionalIDs) > maxProfessionalIDs {
		return nil, domain.NewMessage(domain.ErrValidation, "No se pueden asignar más de 50 profesionales")
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(professionalIDs))
	for _, id := range professionalIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		m, err := s.Memberships.Find(id, salonID)
		if err != nil || !m.Bookable() {
			return nil, domain.NewMessage(domain.ErrValidation, "Profesional inválido")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// Update cambia nombre, duración, precio, comisión y estado. Solo owner/admin.
func (s ServiceService) Update(actor domain.Membership, salonID, serviceID, name string, durationMin, priceCents, commissionPct int, active bool) (domain.Service, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.Service{}, err
	}
	current, err := s.Services.FindByID(salonID, serviceID)
	if err != nil {
		return domain.Service{}, err
	}
	updated, err := current.WithDetails(name, durationMin, priceCents, commissionPct, active)
	if err != nil {
		return domain.Service{}, err
	}
	if err := s.ensureFreeName(salonID, updated.Name, updated.ID); err != nil {
		return domain.Service{}, err
	}
	if err := s.Services.Save(updated); err != nil {
		return domain.Service{}, err
	}
	return updated, nil
}

// List trae el catálogo. Cualquier miembro del salón puede leerlo.
func (s ServiceService) List(actor domain.Membership, salonID string, includeInactive bool) ([]domain.Service, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return nil, err
	}
	return s.Services.List(salonID, includeInactive)
}

// manager exige membership real (no confía en el rol que trae el actor) y
// permiso de configuración.
func (s ServiceService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// member valida que el actor sea miembro del salón en la DB.
func (s ServiceService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// ensureFreeName rechaza un nombre que ya usa un servicio ACTIVO del salón.
// Desactivar libera el nombre (el índice de la DB es WHERE active).
// ignoreID permite que un servicio se renombre a sí mismo (mismas mayúsculas).
func (s ServiceService) ensureFreeName(salonID, name, ignoreID string) error {
	list, err := s.Services.List(salonID, true)
	if err != nil {
		return err
	}
	for _, other := range list {
		if other.ID == ignoreID || !other.Active {
			continue
		}
		if strings.EqualFold(other.Name, name) {
			return domain.ErrAlreadyExists
		}
	}
	return nil
}
