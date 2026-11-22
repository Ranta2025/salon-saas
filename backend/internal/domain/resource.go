package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Límites del equipamiento (S-F2e-2). Valen para cualquier camino de escritura
// (HTTP hoy, importador mañana): el tope vive en el dominio, no en el handler.
const (
	MaxResourceName = 60 // nombre de un tipo o de una unidad
	// MaxResourceTypes es cuántos TIPOS puede definir un salón.
	MaxResourceTypes = 20
	// MaxResourceUnitsPerType es cuántas UNIDADES admite un tipo: es la
	// capacidad instalada ("3 puestos de peluquería").
	MaxResourceUnitsPerType = 50
)

// Mensajes exactos del contrato (S-F2e-3): se devuelven verbatim a la pantalla.
const (
	MsgResourceNameRequired = "El nombre del equipo es obligatorio"
	MsgResourceNameTooLong  = "El nombre no puede superar los 60 caracteres"
	MsgResourceNameNewline  = "El nombre del equipo no puede tener saltos de línea"
	MsgResourceNameTaken    = "Ya existe un equipo con ese nombre"
	MsgResourceTypeLimit    = "Máximo 20 tipos de equipo"
	MsgResourceUnitLimit    = "Máximo 50 unidades por tipo"
	MsgResourceDeleteUsed   = "Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo"
	MsgResourceUnitCount    = "La cantidad de unidades debe estar entre 1 y 50"
	MsgServiceResourceBad   = "Equipo inválido"
)

// ResourceType es un TIPO de equipo del salón: "Puesto de peluquería", "Silla
// de barbería", "Lavacabezas". No guarda cantidad: las unidades son FILAS con
// nombre y su propia EXCLUDE (S-F2e-1), no un contador.
type ResourceType struct {
	ID        string
	SalonID   string
	Name      string
	CreatedAt time.Time
}

// ResourceUnit es una unidad concreta del tipo ("Puesto 1"). Un turno ocupa UNA
// unidad durante toda su duración; desactivarla la saca de la disponibilidad
// pero los turnos ya agendados la conservan.
type ResourceUnit struct {
	ID      string
	SalonID string
	TypeID  string
	Name    string
	Active  bool
}

// UnitOccupancy es un intervalo ya tomado por un turno en una unidad. La
// disponibilidad lo usa para contar cuántas unidades quedan libres; la garantía
// dura sigue siendo la EXCLUDE de Postgres (S-F2e-1).
type UnitOccupancy struct {
	UnitID   string
	StartsAt time.Time
	EndsAt   time.Time
}

// NewResourceType valida y crea un tipo de equipo.
func NewResourceType(salonID, name string) (ResourceType, error) {
	clean, err := validateResourceName(name)
	if err != nil {
		return ResourceType{}, err
	}
	if strings.TrimSpace(salonID) == "" {
		return ResourceType{}, fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	return ResourceType{ID: NewID(), SalonID: salonID, Name: clean, CreatedAt: time.Now().UTC()}, nil
}

// SetName devuelve el tipo renombrado, revalidando las mismas reglas del alta
// (crear y renombrar no pueden divergir).
func (t ResourceType) SetName(name string) (ResourceType, error) {
	clean, err := validateResourceName(name)
	if err != nil {
		return ResourceType{}, err
	}
	t.Name = clean
	return t, nil
}

// UnitName arma el nombre automático de la unidad N del tipo ("Puesto 1",
// "Puesto 2"...). Es el nombre que se guarda al crear el tipo con `count`.
func (t ResourceType) UnitName(n int) string { return fmt.Sprintf("%s %d", t.Name, n) }

// NewResourceUnit valida una unidad. El nombre es obligatorio y de hasta 60
// caracteres, igual que el del tipo.
func NewResourceUnit(salonID, typeID, name string) (ResourceUnit, error) {
	clean, err := validateResourceName(name)
	if err != nil {
		return ResourceUnit{}, err
	}
	if strings.TrimSpace(salonID) == "" || strings.TrimSpace(typeID) == "" {
		return ResourceUnit{}, fmt.Errorf("%w: el salón y el tipo son obligatorios", ErrValidation)
	}
	return ResourceUnit{ID: NewID(), SalonID: salonID, TypeID: typeID, Name: clean, Active: true}, nil
}

// SetDetails cambia el nombre y/o el estado de la unidad. name vacío = no se
// toca. Desactivar es cómo se "quita" equipo: los turnos ya agendados conservan
// su unidad (S-F2e-3).
func (u ResourceUnit) SetDetails(name string, active bool) (ResourceUnit, error) {
	if strings.TrimSpace(name) != "" {
		clean, err := validateResourceName(name)
		if err != nil {
			return ResourceUnit{}, err
		}
		u.Name = clean
	}
	u.Active = active
	return u, nil
}

// EnsureResourceTypeBudget valida que quepa un tipo más: existing es cuántos
// tipos tiene hoy el salón.
func EnsureResourceTypeBudget(existing int) error {
	if existing >= MaxResourceTypes {
		return NewMessage(ErrValidation, MsgResourceTypeLimit)
	}
	return nil
}

// EnsureResourceUnitBudget valida que quepan `adding` unidades más en un tipo
// que ya tiene `existing`.
func EnsureResourceUnitBudget(existing, adding int) error {
	if adding < 1 || existing+adding > MaxResourceUnitsPerType {
		return NewMessage(ErrValidation, MsgResourceUnitLimit)
	}
	return nil
}

// FreeUnits devuelve las unidades ACTIVAS que ningún intervalo ocupado pisa en
// [start, end). El orden es el de entrada (el de los nombres).
func FreeUnits(units []ResourceUnit, busy []UnitOccupancy, start, end time.Time) []ResourceUnit {
	free := make([]ResourceUnit, 0, len(units))
	for _, u := range units {
		if !u.Active {
			continue
		}
		if UnitFree(busy, u.ID, start, end) {
			free = append(free, u)
		}
	}
	return free
}

// UnitFree dice si la unidad no tiene ningún intervalo ocupado que pise
// [start, end). Los extremos que se tocan no se pisan: un turno que empieza
// cuando el otro termina entra, igual que en la EXCLUDE de Postgres.
func UnitFree(busy []UnitOccupancy, unitID string, start, end time.Time) bool {
	for _, o := range busy {
		if o.UnitID != unitID {
			continue
		}
		if o.StartsAt.Before(end) && start.Before(o.EndsAt) {
			return false
		}
	}
	return true
}

// validateResourceName concentra las reglas del nombre de un tipo o de una
// unidad: trim, obligatorio, sin saltos de línea y hasta 60 caracteres (en
// runes: "ñ" cuenta uno).
func validateResourceName(name string) (string, error) {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return "", NewMessage(ErrValidation, MsgResourceNameRequired)
	}
	if strings.ContainsAny(clean, "\r\n") {
		return "", NewMessage(ErrValidation, MsgResourceNameNewline)
	}
	if utf8.RuneCountInString(clean) > MaxResourceName {
		return "", NewMessage(ErrValidation, MsgResourceNameTooLong)
	}
	return clean, nil
}
