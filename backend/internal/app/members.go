// MemberService: la pantalla de equipo del salón. Solo owner/admin; el owner
// suma admins, el admin suma profesionales y recepción, y el salón nunca se
// queda sin owner.
package app

import (
	"errors"
	"sort"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type MemberService struct {
	Members     ports.MemberAdminRepository
	Memberships ports.MembershipRepository
	Users       ports.UserRepository
	// Professionals lleva los servicios de cada quien y Services valida que el
	// id asignado sea un servicio de ESTE salón (F2c).
	Professionals ports.ProfessionalServiceRepository
	Services      ports.ServiceRepository
}

// MemberPatch es el cuerpo del PATCH del equipo: los dos campos son opcionales
// y al menos uno tiene que venir (F2c). nil = el campo no vino.
type MemberPatch struct {
	Attends *bool
	Trade   *string
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
	// Los servicios se completan acá y no en el repo del equipo: quien no
	// atiende no tiene lista (ni una query de más).
	for i := range list {
		withServices, err := s.withServices(salonID, list[i])
		if err != nil {
			return nil, err
		}
		list[i] = withServices
	}
	return list, nil
}

// withServices devuelve el miembro con su lista de servicios (siempre un array,
// nunca nil) para el JSON de la pantalla de equipo.
func (s MemberService) withServices(salonID string, m domain.Member) (domain.Member, error) {
	m.ServiceIDs = []string{}
	if !m.Bookable() || s.Professionals == nil {
		return m, nil
	}
	// El salón va explícito: la lista de servicios de un miembro nunca se lee
	// sin el tenant que la contiene.
	ids, err := s.Professionals.ListServiceIDs(salonID, m.ID)
	if err != nil {
		return domain.Member{}, err
	}
	if ids != nil {
		m.ServiceIDs = ids
	}
	return m, nil
}

// Add suma a una persona que YA tiene cuenta. No crea usuarios: el salón no
// inventa cuentas ni claves por terceros.
//
// `attends` es opcional (nil = el default del rol según S-F2a-4): un
// profesional atiende siempre y la administración solo si el alta lo pide (el
// dueño-barbero que además administra).
func (s MemberService) Add(actor domain.Membership, salonID, email string, role domain.Role, attends *bool) (domain.Member, error) {
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
	if attends != nil {
		// La misma regla que el PATCH: un rol que nunca atiende no se puede
		// encender y un profesional no se puede apagar.
		if membership, err = membership.SetAttends(*attends); err != nil {
			return domain.Member{}, err
		}
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
	// El alta devuelve el mismo objeto que el listado: oficio vacío y lista de
	// servicios vacía (un profesional nuevo no hace nada hasta que se le asigne).
	return s.withServices(salonID, domain.Member{
		ID: user.ID, Name: user.Name, Email: user.Email, Role: role, Attends: membership.Attends,
	})
}

// Update cambia "atiende clientes" y/o el oficio de un miembro (PATCH
// /salons/:id/members/:uid, S-F2c-3). Solo owner/admin; el miembro tiene que ser
// de ESTE salón (404 si es de otro); al menos un campo tiene que venir y el rol
// decide qué se puede cambiar (ver domain.Membership.SetAttends / SetTrade).
//
// Las dos validaciones corren ANTES de escribir: si el oficio es inválido, la
// marca tampoco se guarda.
func (s MemberService) Update(actor domain.Membership, salonID, userID string, patch MemberPatch) (domain.Member, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.Member{}, err
	}
	if patch.Attends == nil && patch.Trade == nil {
		return domain.Member{}, domain.NewMessage(domain.ErrValidation, "Indicá qué cambiar")
	}
	target, err := s.Memberships.Find(userID, salonID)
	if err != nil {
		// No distingue "no existe" de "es de otro salón": las dos son 404.
		return domain.Member{}, domain.ErrNotFound
	}
	updated := target
	if patch.Attends != nil {
		if updated, err = updated.SetAttends(*patch.Attends); err != nil {
			return domain.Member{}, err
		}
	}
	if patch.Trade != nil {
		// El oficio solo existe en quien atiende: con la marca ya aplicada, la
		// regla mira el estado FINAL del pedido (encender y poner oficio a la
		// vez es válido).
		if updated, err = updated.SetTrade(*patch.Trade); err != nil {
			return domain.Member{}, err
		}
	}
	if updated.Attends != target.Attends {
		if err := s.Members.SetAttends(userID, salonID, updated.Attends); err != nil {
			return domain.Member{}, err
		}
	}
	if updated.Trade != target.Trade {
		if err := s.Members.SetTrade(userID, salonID, updated.Trade); err != nil {
			return domain.Member{}, err
		}
	}
	user, err := s.Users.FindByID(userID)
	if err != nil {
		return domain.Member{}, err
	}
	member := domain.Member{
		ID: user.ID, Name: user.Name, Email: user.Email, Role: target.Role,
		Attends: updated.Attends, Trade: updated.Trade,
	}
	return s.withServices(salonID, member)
}

// SetServices reemplaza la lista de servicios que hace un miembro
// (PUT /salons/:id/members/:uid/services, S-F2c-3). Solo owner/admin; solo
// quien atiende; cada id tiene que ser un servicio de ESTE salón. La lista
// vacía es válida (esa persona no hace ninguno más).
func (s MemberService) SetServices(actor domain.Membership, salonID, userID string, serviceIDs []string) ([]string, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return nil, err
	}
	target, err := s.Memberships.Find(userID, salonID)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	if !target.Bookable() {
		return nil, domain.NewMessage(domain.ErrValidation, "Ese rol no hace servicios")
	}
	ids, err := s.validServiceIDs(salonID, serviceIDs)
	if err != nil {
		return nil, err
	}
	if err := s.Professionals.SetServices(salonID, userID, ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// validServiceIDs normaliza la lista (sin repetidos, ordenada) y rechaza
// cualquier id que no sea un servicio del salón. Un servicio de otro tenant
// tampoco existe acá: mismo mensaje, sin confirmar que existe en otra parte.
func (s MemberService) validServiceIDs(salonID string, serviceIDs []string) ([]string, error) {
	seen := map[string]bool{}
	ids := make([]string, 0, len(serviceIDs))
	for _, id := range serviceIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, err := s.Services.FindByID(salonID, id); err != nil {
			return nil, domain.NewMessage(domain.ErrValidation, "Servicio inválido")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
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
	return manageActor(s.Memberships, actor, salonID)
}

// manageActor es la autorización compartida de la pantalla de equipo y de las
// invitaciones (F2g1): el actor tiene que estar en ESE salón y ser owner/admin.
// Vive una sola vez para que el alta directa (`POST /members`) y la invitación
// no puedan divergir en quién puede sumar gente.
func manageActor(memberships ports.MembershipRepository, actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
