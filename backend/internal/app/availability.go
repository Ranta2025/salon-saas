// AvailabilityService: los huecos libres que ofrece la reserva pública.
// El cálculo depende del salón (zona horaria, apertura/cierre y paso) y del
// servicio (duración): por eso no es una query SQL sino un caso de uso.
package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Slot es un hueco concreto: quién lo atiende y en qué instante (UTC).
type Slot struct {
	ProfessionalID string
	StartsAt       time.Time
	EndsAt         time.Time
}

// DayAvailability es la respuesta de GET /public/salons/:slug/availability.
type DayAvailability struct {
	Date  string
	Slots []Slot
}

// dateLayout es el único formato aceptado por la disponibilidad pública.
const dateLayout = "2006-01-02"

type AvailabilityService struct {
	Salons   ports.SalonRepository
	Services ports.ServiceRepository
	Settings ports.SalonSettingsRepository
	Staff    ports.StaffLister
	Bookings ports.BookingRepository
	// Professionals dice quién HACE el servicio (F2c): la disponibilidad es
	// saber hacerlo ∩ estar libre, no "todo el equipo hace todo".
	Professionals ports.ProfessionalServiceRepository
	// Hours es el horario semanal del salón y de cada profesional (F2d1). nil o
	// sin filas = la ventana única de /settings para todo el equipo (hoy).
	Hours ports.WeeklyHoursRepository
	// TimeOff son las ausencias de las profesionales (F2d2): quien está de
	// vacaciones no se ofrece ese día.
	TimeOff ports.TimeOffRepository
	// Resources es el equipamiento con capacidad (F2e): si el servicio exige un
	// tipo de equipo, un hueco se ofrece solo si queda una unidad ACTIVA libre en
	// todo el intervalo. nil o servicio sin tipo = comportamiento de antes.
	Resources ports.ResourceRepository
}

// Day calcula los huecos libres de una fecha local del salón, para un servicio
// y opcionalmente para un profesional. Solo devuelve arranques en el futuro
// (no se agenda hacia atrás) que entren completos dentro del horario.
func (s AvailabilityService) Day(slug, date, serviceID, professionalID string) (DayAvailability, error) {
	salon, err := s.Salons.FindBySlug(strings.TrimSpace(slug))
	if err != nil {
		return DayAvailability{}, err
	}
	settings, err := loadSalonSettings(s.Settings, salon.ID)
	if err != nil {
		return DayAvailability{}, err
	}
	loc, err := settings.Location()
	if err != nil {
		return DayAvailability{}, err
	}
	day, err := time.ParseInLocation(dateLayout, strings.TrimSpace(date), loc)
	if err != nil {
		return DayAvailability{}, fmt.Errorf("%w: fecha inválida (usá YYYY-MM-DD)", domain.ErrValidation)
	}
	service, err := resolvePublicService(s.Services, salon.ID, serviceID)
	if err != nil {
		return DayAvailability{}, err
	}
	professionals, err := s.professionals(salon.ID, service.ID, professionalID)
	if err != nil {
		return DayAvailability{}, err
	}
	open, closeAt, err := settings.Window(day)
	if err != nil {
		return DayAvailability{}, err
	}

	// Horario semanal (F2d1): sin filas todo esto es la ventana única de antes.
	single, err := singleWindow(settings)
	if err != nil {
		return DayAvailability{}, err
	}
	weekly, err := salonWeeklyHours(s.Hours, salon.ID)
	if err != nil {
		return DayAvailability{}, err
	}
	segments, err := memberHours(s.Hours, salon.ID)
	if err != nil {
		return DayAvailability{}, err
	}
	// El día de la semana es el del SALÓN: un domingo 23:30 en CDMX ya es lunes
	// en UTC y tiene que ofrecerse como domingo.
	weekday := domain.IsoWeekday(day, loc)

	// Se traen las reservas de una ventana más ancha que el día: un turno que
	// arrancó la noche anterior también ocupa la mañana.
	booked, err := s.Bookings.ListRange(salon.ID, open.UTC().Add(-24*time.Hour), closeAt.UTC().Add(24*time.Hour))
	if err != nil {
		return DayAvailability{}, err
	}
	// Ausencias (F2d2): quien está de vacaciones o de día libre no se ofrece ese
	// día. Una sola query para todo el equipo, con la misma ventana ancha que los
	// turnos: una ausencia que empezó ayer también cubre la mañana de hoy.
	absences, err := timeOffRange(s.TimeOff, salon.ID, open.UTC().Add(-24*time.Hour), closeAt.UTC().Add(24*time.Hour))
	if err != nil {
		return DayAvailability{}, err
	}
	now := time.Now().UTC()
	durationMin := service.DurationMin
	stepMin := settings.SlotStepMin

	slots := make([]Slot, 0, 32)
	for _, p := range professionals {
		// Los tramos en los que ESTA persona puede recibir un turno ese día:
		// ventana del salón ∩ su horario (vacío = día libre o salón cerrado).
		plan := domain.DayPlan{Single: single, Weekly: weekly, Segments: segments[p.ID]}
		windows, err := plan.Windows(weekday)
		if err != nil {
			return DayAvailability{}, err
		}
		for _, w := range windows {
			// Se arranca en cada tramo: un turno nunca cruza un descanso ni el
			// cierre, por eso no hay un único grillo para todo el día.
			for startMin := w.Start; startMin+durationMin <= w.End; startMin += stepMin {
				start := salonTimeAt(day, startMin, loc)
				if !start.After(now) {
					continue // ya pasó (o es ahora): no es un hueco reservable
				}
				end := start.Add(time.Duration(durationMin) * time.Minute)
				if overlapsAny(booked, p.ID, start, end) {
					continue
				}
				// La ausencia manda aunque el hueco esté libre: no está para
				// atender. El período es semiabierto, así que un turno que
				// arranca justo cuando termina la ausencia SÍ se ofrece.
				if awayAny(absences, p.ID, start, end) {
					continue
				}
				slots = append(slots, Slot{ProfessionalID: p.ID, StartsAt: start.UTC(), EndsAt: end.UTC()})
			}
		}
	}
	// Orden estable: primero el horario y después el profesional.
	sort.SliceStable(slots, func(i, j int) bool {
		if !slots[i].StartsAt.Equal(slots[j].StartsAt) {
			return slots[i].StartsAt.Before(slots[j].StartsAt)
		}
		return slots[i].ProfessionalID < slots[j].ProfessionalID
	})
	// Equipo (F2e, S-F2e-4): si el servicio ocupa una unidad, un horario se
	// ofrece solo si queda una unidad ACTIVA libre en TODO el intervalo. Así 5
	// profesionales con 3 puestos no ofrecen 5 turnos a la misma hora: el tope
	// lo pone el equipamiento (la EXCLUDE por unidad lo garantiza al reservar).
	if service.ResourceTypeID != "" {
		slots, err = s.capByEquipment(salon.ID, service.ResourceTypeID, slots, open, closeAt)
		if err != nil {
			return DayAvailability{}, err
		}
	}
	return DayAvailability{Date: day.Format(dateLayout), Slots: slots}, nil
}

// capByEquipment limita la oferta de cada horario a las unidades activas libres
// del tipo. Sin unidades activas no se ofrece nada ({"slots":[]}), nunca un
// error: el salón configuró un servicio que hoy no puede atender.
func (s AvailabilityService) capByEquipment(salonID, typeID string, slots []Slot, from, to time.Time) ([]Slot, error) {
	out := make([]Slot, 0, len(slots))
	if len(slots) == 0 || s.Resources == nil {
		return out, nil
	}
	units, err := s.Resources.ActiveUnits(salonID, typeID)
	if err != nil {
		return nil, err
	}
	busy, err := s.Resources.OccupiedUnits(salonID, from.UTC().Add(-24*time.Hour), to.UTC().Add(24*time.Hour), "")
	if err != nil {
		return nil, err
	}
	// Los huecos del mismo arranque comparten duración: el tope es cuántas
	// unidades quedan libres en ese intervalo.
	for i := 0; i < len(slots); {
		j := i
		for j < len(slots) && slots[j].StartsAt.Equal(slots[i].StartsAt) {
			j++
		}
		free := len(domain.FreeUnits(units, busy, slots[i].StartsAt, slots[i].EndsAt))
		for _, slot := range slots[i:j] {
			if free == 0 {
				break
			}
			out = append(out, slot)
			free--
		}
		i = j
	}
	return out, nil
}

// professionals filtra el personal que ATIENDE y HACE ese servicio (F2c); un
// id que no es del equipo del salón, o que no hace el servicio, simplemente no
// existe acá (404).
//
// La regla de quién atiende es la del dominio (professional siempre, dueño y
// administración solo con la marca attends). El repo ya devuelve solo esa gente
// y acá se vuelve a aplicar: si el filtro del adaptador se rompiera, el dueño
// que no atiende volvería a inflar la disponibilidad, que es el bug de F2a.
//
// Si NADIE hace el servicio la lista vuelve vacía y los huecos también: la
// respuesta es {"slots":[]}, nunca un error.
func (s AvailabilityService) professionals(salonID, serviceID, professionalID string) ([]domain.StaffMember, error) {
	staff, err := s.Staff.ListStaff(salonID)
	if err != nil {
		return nil, err
	}
	eligible, err := s.Professionals.ListProfessionalsFor(salonID, serviceID)
	if err != nil {
		return nil, err
	}
	does := make(map[string]bool, len(eligible))
	for _, m := range eligible {
		does[m.ID] = true
	}
	bookable := make([]domain.StaffMember, 0, len(staff))
	for _, m := range staff {
		if m.Bookable() && does[m.ID] {
			bookable = append(bookable, m)
		}
	}
	want := strings.TrimSpace(professionalID)
	if want == "" {
		return bookable, nil
	}
	for _, m := range bookable {
		if m.ID == want {
			return []domain.StaffMember{m}, nil
		}
	}
	return nil, domain.ErrNotFound
}

// overlapsAny dice si el profesional ya tiene un turno activo en [start,end).
func overlapsAny(bookings []domain.Booking, professionalID string, start, end time.Time) bool {
	for _, b := range bookings {
		if b.ProfessionalID != professionalID || b.Status != domain.BookingBooked {
			continue
		}
		if b.StartsAt.Before(end) && start.Before(b.EndsAt) {
			return true
		}
	}
	return false
}
