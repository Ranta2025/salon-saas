package ports

import (
	"salon-saas/internal/domain"
	"time"
)

type SalonSettingsRepository interface {
	Settings(salonID string) (domain.SalonSettings, error)
	SaveSettings(s domain.SalonSettings) error
	// SaveSettingsGuarded es el ÚNICO camino de escritura de la API: corre en
	// UNA transacción que toma el MISMO advisory lock por salón que la creación
	// de liquidaciones ("payroll-write:<salon_id>") y, si la zona horaria CAMBIA
	// y el salón ya tiene liquidaciones (abiertas o pagadas), no escribe NADA
	// (ni los otros campos) y devuelve ErrAlreadyExists -> 409. Una corrida
	// guarda fechas LOCALES y la EXCLUDE compara fechas: cambiar la zona después
	// haría que las mismas ventas cuenten en dos liquidaciones (se pagan dos
	// veces) o en ninguna. Es EXISTS, no una marca: borrar la última corrida
	// abierta descongela la zona.
	SaveSettingsGuarded(s domain.SalonSettings) error
}

// WeeklyHoursRepository: horario semanal del salón y de cada profesional (F2d1).
//
// La ausencia de filas es comportamiento, no dato faltante: un salón sin filas
// usa la ventana única de /settings todos los días y una profesional sin filas
// sigue el horario del salón. Por eso no hay backfill en la migración 017.
// Todas las queries van scopeadas por salon_id: el horario de otro tenant no
// existe acá.
type WeeklyHoursRepository interface {
	// SalonHours trae las ventanas por día del salón (vacío = sin horario
	// propio). El día sin fila, cuando hay filas, está cerrado.
	SalonHours(salonID string) ([]domain.WeekdayHours, error)
	// SaveSalonHours REEMPLAZA el horario en una transacción: o queda el nuevo
	// entero o queda el anterior. Lista vacía = volver al horario único.
	SaveSalonHours(salonID string, days []domain.WeekdayHours) error
	// MemberHours trae los tramos de un miembro de ESE salón (vacío = sigue al
	// salón).
	MemberHours(salonID, userID string) ([]domain.HoursSegment, error)
	// SaveMemberHours REEMPLAZA los tramos del miembro en una transacción.
	// Lista vacía = vuelve a seguir el horario del salón.
	SaveMemberHours(salonID, userID string, segments []domain.HoursSegment) error
	// AllMemberHours trae los tramos de todo el salón indexados por persona: la
	// disponibilidad pregunta una vez por día, no una query por profesional.
	AllMemberHours(salonID string) (map[string][]domain.HoursSegment, error)
}

// TimeOffRepository: ausencias (vacaciones y días libres puntuales) de las
// profesionales de un salón (F2d2).
//
// Una ausencia es un período [starts_at, ends_at) en UTC de UNA persona
// reservable. Todas las queries van scopeadas por salon_id: la ausencia de otro
// tenant no se lista, no se encuentra y no se borra (ErrNotFound -> 404).
//
// No hay unicidad de período en la DB a propósito: "dos ausencias de la misma
// persona no se pisan" es una regla de negocio que el caso de uso verifica
// contra las filas existentes (el CHECKing de rangos en SQL necesitaría una
// EXCLUDE y una migración de extensión; el mensaje tiene que ser en español).
type TimeOffRepository interface {
	// Save inserta la ausencia. La FK (user_id, salon_id) impide dejar una
	// ausencia colgada de alguien que no es del salón.
	Save(t domain.TimeOff) error
	// ListRange trae las ausencias del salón que PISAN [from, to) ordenadas por
	// inicio: es la query de la pantalla de equipo y de la disponibilidad.
	ListRange(salonID string, from, to time.Time) ([]domain.TimeOff, error)
	// FindByID resuelve una ausencia scopeada por salón (ErrNotFound si es de
	// otro tenant).
	FindByID(salonID, id string) (domain.TimeOff, error)
	// Delete borra la ausencia del salón; otra id o de otro salón es ErrNotFound
	// (nunca un borrado silencioso).
	Delete(salonID, id string) error
}

// ResourceRepository: puestos y equipamiento con capacidad (F2e).
//
// Un TIPO agrupa unidades ("Puesto de peluquería" x 3) y un SERVICIO puede
// exigir un tipo; cada TURNO ocupa una UNIDAD concreta. La capacidad no es un
// contador: es una fila por unidad con su propia EXCLUDE en la DB (migración
// 020), así dos reservas simultáneas nunca se llevan la última.
//
// Todas las queries van scopeadas por salon_id: el equipo de otro tenant no se
// lista, no se encuentra y no se borra (ErrNotFound -> 404).
//
// Sin backfill: un servicio sin tipo de equipo (y un turno sin unidad) se
// comporta exactamente como antes.
type ResourceRepository interface {
	// CreateType crea el tipo y sus unidades en UNA transacción (el alta con
	// `count` no puede quedar a medias).
	CreateType(t domain.ResourceType, units []domain.ResourceUnit) error
	// SaveType actualiza el nombre (sin tocar identidad ni salón).
	SaveType(t domain.ResourceType) error
	// DeleteType borra el tipo y sus unidades en UNA transacción, scopeado por
	// salón; ErrNotFound si es de otro tenant. La FK de servicios (RESTRICT) y
	// una unidad con turnos lo frenan con ErrAlreadyExists: el caso de uso ya lo
	// verificó, el adaptador es la garantía dura.
	DeleteType(salonID, id string) error
	FindType(salonID, id string) (domain.ResourceType, error)
	ListTypes(salonID string) ([]domain.ResourceType, error)

	// SaveUnit inserta o actualiza una unidad (nombre y active).
	SaveUnit(u domain.ResourceUnit) error
	FindUnit(salonID, id string) (domain.ResourceUnit, error)
	// ListUnits trae las unidades de un tipo, incluidas las desactivadas.
	ListUnits(salonID, typeID string) ([]domain.ResourceUnit, error)
	// ActiveUnits son las que la disponibilidad puede ofrecer.
	ActiveUnits(salonID, typeID string) ([]domain.ResourceUnit, error)
	// UnitsWithBookings cuenta unidades del tipo con algún turno `booked`: es lo
	// que impide borrar el tipo.
	UnitsWithBookings(salonID, typeID string) (int, error)
	// UnitFutureBookings cuenta los turnos `booked` que empiezan después de now
	// y usan la unidad (informativo al desactivarla).
	UnitFutureBookings(salonID, unitID string, now time.Time) (int, error)
	// OccupiedUnits trae los intervalos tomados por turnos `booked` con unidad
	// en [from,to). excludeBookingID permite reprogramar un turno sin que su
	// propio intervalo cuente como ocupado.
	OccupiedUnits(salonID string, from, to time.Time, excludeBookingID string) ([]domain.UnitOccupancy, error)
}

// BookingTx es la vista transaccional de la agenda: cubrir una ausencia mueve
// TODOS sus turnos o ninguno (S-F2d3-3). Es el mismo repositorio de reservas que
// fuera de la transacción, para que el caso de uso no tenga dos formas de leer
// un turno.
type BookingTx interface {
	BookingRepository
}

// BookingTxRunner abre una transacción con un lock por salón (el mismo patrón que
// el cierre de caja): dos "cubrir la ausencia" simultáneos sobre el mismo salón
// no se pisan y la EXCLUDE de bookings sigue siendo la que decide el solape.
type BookingTxRunner interface {
	InSalonTx(salonID string, fn func(tx BookingTx) error) error
}

// BookingEmailFinder resuelve la dirección con la que se avisa un cambio de
// turno (cubrir una ausencia o cancelar, F2d3). Va aparte de
// BookingEmailRepository (ISP): quien solo avisa no carga con la cola de
// recordatorios.
type BookingEmailFinder interface {
	// FindEmail devuelve la dirección guardada del turno del salón. Sin fila es
	// ErrNotFound (el turno se agendó sin email: no hay a quién escribir).
	FindEmail(salonID, bookingID string) (string, error)
}
