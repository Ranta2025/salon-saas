// ResourceService: puestos y equipamiento con capacidad (F2e).
//
//	GET    /salons/:id/resources
//	POST   /salons/:id/resources                 {name, count}
//	PATCH  /salons/:id/resources/:tid            {name}
//	POST   /salons/:id/resources/:tid/units      {count}
//	PATCH  /salons/:id/resource-units/:uid       {name?, active?}
//	DELETE /salons/:id/resources/:tid
//
// Decisiones del contrato (S-F2e-3):
//   - La capacidad son FILAS con nombre, no un contador: el salón define "3
//     puestos" y cada puesto tiene su propia EXCLUDE en la DB.
//   - Quitar equipo es DESACTIVARLO: los turnos ya agendados conservan su
//     unidad y el PATCH informa cuántos turnos futuros la usan.
//   - Borrar un tipo se rechaza si un servicio lo usa o si alguna unidad tiene
//     turnos: el historial no se puede quedar huérfano.
//   - Leer: cualquier miembro. Escribir: solo dueño/administración.
package staffing

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ResourceTypeView es un tipo de equipo con sus unidades y cuántos servicios
// del catálogo lo usan (S-F2e-3).
type ResourceTypeView struct {
	Type     domain.ResourceType
	Units    []domain.ResourceUnit
	Services int
}

// ResourceUnitUpdate es la respuesta del PATCH de una unidad: la unidad y
// cuántos turnos futuros la usan (informativo, los conservan).
type ResourceUnitUpdate struct {
	Unit           domain.ResourceUnit
	FutureBookings int
}

type ResourceService struct {
	Resources   ports.ResourceRepository
	Services    ports.ServiceRepository
	Memberships ports.MembershipRepository
}

// List trae los tipos del salón con sus unidades y el uso de cada tipo.
func (s ResourceService) List(actor domain.Membership, salonID string) ([]ResourceTypeView, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return nil, err
	}
	types, err := s.Resources.ListTypes(salonID)
	if err != nil {
		return nil, err
	}
	services, err := s.Services.List(salonID, true)
	if err != nil {
		return nil, err
	}
	used := make(map[string]int, len(types))
	for _, svc := range services {
		if svc.ResourceTypeID != "" {
			used[svc.ResourceTypeID]++
		}
	}
	out := make([]ResourceTypeView, 0, len(types))
	for _, t := range types {
		units, err := s.Resources.ListUnits(salonID, t.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ResourceTypeView{Type: t, Units: units, Services: used[t.ID]})
	}
	return out, nil
}

// Create da de alta un tipo con sus unidades ("Puesto 1", "Puesto 2"...).
func (s ResourceService) Create(actor domain.Membership, salonID, name string, count int) (ResourceTypeView, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return ResourceTypeView{}, err
	}
	rt, err := domain.NewResourceType(salonID, name)
	if err != nil {
		return ResourceTypeView{}, err
	}
	if err := validateUnitCount(count); err != nil {
		return ResourceTypeView{}, err
	}
	types, err := s.Resources.ListTypes(salonID)
	if err != nil {
		return ResourceTypeView{}, err
	}
	if err := domain.EnsureResourceTypeBudget(len(types)); err != nil {
		return ResourceTypeView{}, err
	}
	if err := s.ensureFreeName(salonID, rt.Name, ""); err != nil {
		return ResourceTypeView{}, err
	}
	units := make([]domain.ResourceUnit, 0, count)
	for i := 1; i <= count; i++ {
		u, err := domain.NewResourceUnit(salonID, rt.ID, rt.UnitName(i))
		if err != nil {
			return ResourceTypeView{}, err
		}
		units = append(units, u)
	}
	if err := s.Resources.CreateType(rt, units); err != nil {
		// El índice único (salon_id, lower(name)) es la garantía dura: dos altas
		// concurrentes con el mismo nombre terminan en el mismo 409.
		if errors.Is(err, domain.ErrAlreadyExists) {
			return ResourceTypeView{}, domain.NewMessage(domain.ErrValidation, domain.MsgResourceNameTaken)
		}
		return ResourceTypeView{}, err
	}
	return ResourceTypeView{Type: rt, Units: units}, nil
}

// Rename cambia el nombre de un tipo del salón.
func (s ResourceService) Rename(actor domain.Membership, salonID, typeID, name string) (domain.ResourceType, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.ResourceType{}, err
	}
	current, err := s.Resources.FindType(salonID, strings.TrimSpace(typeID))
	if err != nil {
		return domain.ResourceType{}, err
	}
	renamed, err := current.SetName(name)
	if err != nil {
		return domain.ResourceType{}, err
	}
	if err := s.ensureFreeName(salonID, renamed.Name, renamed.ID); err != nil {
		return domain.ResourceType{}, err
	}
	if err := s.Resources.SaveType(renamed); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.ResourceType{}, domain.NewMessage(domain.ErrValidation, domain.MsgResourceNameTaken)
		}
		return domain.ResourceType{}, err
	}
	return renamed, nil
}

// AddUnits agrega `count` unidades al tipo con el SIGUIENTE número libre: si la
// "3" se renombró o se borró, se vuelve a usar ese número.
func (s ResourceService) AddUnits(actor domain.Membership, salonID, typeID string, count int) ([]domain.ResourceUnit, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return nil, err
	}
	rt, err := s.Resources.FindType(salonID, strings.TrimSpace(typeID))
	if err != nil {
		return nil, err
	}
	if err := validateUnitCount(count); err != nil {
		return nil, err
	}
	units, err := s.Resources.ListUnits(salonID, rt.ID)
	if err != nil {
		return nil, err
	}
	if err := domain.EnsureResourceUnitBudget(len(units), count); err != nil {
		return nil, err
	}
	added := make([]domain.ResourceUnit, 0, count)
	for _, n := range nextFreeNumbers(rt, units, count) {
		u, err := domain.NewResourceUnit(salonID, rt.ID, rt.UnitName(n))
		if err != nil {
			return nil, err
		}
		if err := s.Resources.SaveUnit(u); err != nil {
			if errors.Is(err, domain.ErrAlreadyExists) {
				return nil, domain.NewMessage(domain.ErrValidation, domain.MsgResourceNameTaken)
			}
			return nil, err
		}
		added = append(added, u)
	}
	return added, nil
}

// UpdateUnit renombra y/o activa/desactiva una unidad. Desactivar la saca de la
// disponibilidad; los turnos agendados la conservan (S-F2e-3).
func (s ResourceService) UpdateUnit(actor domain.Membership, salonID, unitID, name string, active *bool) (ResourceUnitUpdate, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return ResourceUnitUpdate{}, err
	}
	current, err := s.Resources.FindUnit(salonID, strings.TrimSpace(unitID))
	if err != nil {
		return ResourceUnitUpdate{}, err
	}
	wantActive := current.Active
	if active != nil {
		wantActive = *active
	}
	updated, err := current.SetDetails(name, wantActive)
	if err != nil {
		return ResourceUnitUpdate{}, err
	}
	if !strings.EqualFold(updated.Name, current.Name) {
		if err := s.ensureFreeUnitName(salonID, updated.TypeID, updated.Name, updated.ID); err != nil {
			return ResourceUnitUpdate{}, err
		}
	}
	if err := s.Resources.SaveUnit(updated); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return ResourceUnitUpdate{}, domain.NewMessage(domain.ErrValidation, domain.MsgResourceNameTaken)
		}
		return ResourceUnitUpdate{}, err
	}
	future, err := s.Resources.UnitFutureBookings(salonID, updated.ID, time.Now().UTC())
	if err != nil {
		return ResourceUnitUpdate{}, err
	}
	return ResourceUnitUpdate{Unit: updated, FutureBookings: future}, nil
}

// DeleteType borra un tipo SOLO si ningún servicio lo usa y ninguna unidad
// tiene turnos (S-F2e-3). Si lo usan, el mensaje pide desactivar.
func (s ResourceService) DeleteType(actor domain.Membership, salonID, typeID string) error {
	if _, err := s.manager(actor, salonID); err != nil {
		return err
	}
	rt, err := s.Resources.FindType(salonID, strings.TrimSpace(typeID))
	if err != nil {
		return err
	}
	services, err := s.Services.List(salonID, true)
	if err != nil {
		return err
	}
	for _, svc := range services {
		if svc.ResourceTypeID == rt.ID {
			return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgResourceDeleteUsed)
		}
	}
	withBookings, err := s.Resources.UnitsWithBookings(salonID, rt.ID)
	if err != nil {
		return err
	}
	if withBookings > 0 {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgResourceDeleteUsed)
	}
	return s.Resources.DeleteType(salonID, rt.ID)
}

// validateUnitCount: la cantidad de un alta o de "agregar unidades" es 1..50.
func validateUnitCount(count int) error {
	if count < 1 || count > domain.MaxResourceUnitsPerType {
		return domain.NewMessage(domain.ErrValidation, domain.MsgResourceUnitCount)
	}
	return nil
}

// nextFreeNumbers devuelve los `count` primeros números que ningún nombre
// "<tipo> <n>" del tipo está usando.
func nextFreeNumbers(rt domain.ResourceType, units []domain.ResourceUnit, count int) []int {
	used := make(map[int]bool, len(units))
	prefix := rt.Name + " "
	for _, u := range units {
		if !strings.HasPrefix(u.Name, prefix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(u.Name, prefix))); err == nil {
			used[n] = true
		}
	}
	out := make([]int, 0, count)
	for n := 1; len(out) < count; n++ {
		if !used[n] {
			out = append(out, n)
		}
	}
	return out
}

// ensureFreeName rechaza un nombre de tipo que ya usa otro del salón, sin
// importar mayúsculas (el índice de la DB es sobre lower(name)).
func (s ResourceService) ensureFreeName(salonID, name, ignoreID string) error {
	types, err := s.Resources.ListTypes(salonID)
	if err != nil {
		return err
	}
	for _, t := range types {
		if t.ID == ignoreID {
			continue
		}
		if strings.EqualFold(t.Name, name) {
			return domain.NewMessage(domain.ErrValidation, domain.MsgResourceNameTaken)
		}
	}
	return nil
}

// ensureFreeUnitName: mismo criterio dentro de un tipo (el índice es
// (salon_id, type_id, lower(name))).
func (s ResourceService) ensureFreeUnitName(salonID, typeID, name, ignoreID string) error {
	units, err := s.Resources.ListUnits(salonID, typeID)
	if err != nil {
		return err
	}
	for _, u := range units {
		if u.ID == ignoreID {
			continue
		}
		if strings.EqualFold(u.Name, name) {
			return domain.NewMessage(domain.ErrValidation, domain.MsgResourceNameTaken)
		}
	}
	return nil
}

// member valida que el actor sea miembro real del salón (no confía en el rol
// que traiga el token).
func (s ResourceService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// manager exige membership real y permiso de configuración del salón.
func (s ResourceService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
