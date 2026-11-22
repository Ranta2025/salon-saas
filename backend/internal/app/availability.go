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
	professionals, err := s.professionals(salon.ID, professionalID)
	if err != nil {
		return DayAvailability{}, err
	}
	open, closeAt, err := settings.Window(day)
	if err != nil {
		return DayAvailability{}, err
	}

	// Se traen las reservas de una ventana más ancha que el día: un turno que
	// arrancó la noche anterior también ocupa la mañana.
	booked, err := s.Bookings.ListRange(salon.ID, open.UTC().Add(-24*time.Hour), closeAt.UTC().Add(24*time.Hour))
	if err != nil {
		return DayAvailability{}, err
	}
	now := time.Now().UTC()
	duration := time.Duration(service.DurationMin) * time.Minute
	step := time.Duration(settings.SlotStepMin) * time.Minute

	slots := make([]Slot, 0, 32)
	for start := open; !start.Add(duration).After(closeAt); start = start.Add(step) {
		if !start.After(now) {
			continue // ya pasó (o es ahora): no es un hueco reservable
		}
		for _, p := range professionals {
			if overlapsAny(booked, p.ID, start, start.Add(duration)) {
				continue
			}
			slots = append(slots, Slot{ProfessionalID: p.ID, StartsAt: start.UTC(), EndsAt: start.UTC().Add(duration)})
		}
	}
	// Orden estable: primero el horario y después el profesional.
	sort.SliceStable(slots, func(i, j int) bool {
		if !slots[i].StartsAt.Equal(slots[j].StartsAt) {
			return slots[i].StartsAt.Before(slots[j].StartsAt)
		}
		return slots[i].ProfessionalID < slots[j].ProfessionalID
	})
	return DayAvailability{Date: day.Format(dateLayout), Slots: slots}, nil
}

// professionals filtra el personal que atiende en el salón; un id que no es
// del equipo del salón simplemente no existe acá (404).
//
// La regla de quién atiende es la del dominio (professional siempre, dueño y
// administración solo con la marca attends). El repo ya devuelve solo esa gente
// y acá se vuelve a aplicar: si el filtro del adaptador se rompiera, el dueño
// que no atiende volvería a inflar la disponibilidad, que es el bug de F2a.
func (s AvailabilityService) professionals(salonID, professionalID string) ([]domain.StaffMember, error) {
	staff, err := s.Staff.ListStaff(salonID)
	if err != nil {
		return nil, err
	}
	bookable := make([]domain.StaffMember, 0, len(staff))
	for _, m := range staff {
		if m.Bookable() {
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
