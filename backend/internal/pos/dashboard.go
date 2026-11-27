// DashboardService: el panel del día. Solo lee agregados; no escribe nada.
// Ocupación = minutos reservados / (profesionales * jornada de referencia).
package pos

import (
	"math"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// Dashboard es la foto del día que consume el panel interno.
type Dashboard struct {
	RevenueTodayCents int
	AppointmentsToday int
	OccupancyRate     float64
	TopServices       []domain.TopService
}

type DashboardService struct {
	Sales       ports.SaleRepository
	Agenda      ports.AgendaStats
	Staff       ports.StaffCounter
	Memberships ports.MembershipRepository
}

// Today arma el panel del día UTC en curso, scopeado al salón del actor.
func (s DashboardService) Today(actor domain.Membership, salonID string) (Dashboard, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return Dashboard{}, err
	}
	if !kernel.PuedeVerPlata(m) {
		return Dashboard{}, domain.ErrForbidden
	}
	from := kernel.StartOfUTCDay(time.Now().UTC())
	to := kernel.EndOfUTCDay(from)

	revenue, err := s.Sales.SumCents(salonID, from, to, "")
	if err != nil {
		return Dashboard{}, err
	}
	appointments, bookedMinutes, err := s.Agenda.StatsRange(salonID, from, to)
	if err != nil {
		return Dashboard{}, err
	}
	professionals, err := s.Staff.CountProfessionals(salonID)
	if err != nil {
		return Dashboard{}, err
	}
	top, err := s.Sales.TopServices(salonID, from, to, 5)
	if err != nil {
		return Dashboard{}, err
	}
	if top == nil {
		top = []domain.TopService{} // el frontend recibe [] y no null
	}
	return Dashboard{
		RevenueTodayCents: revenue,
		AppointmentsToday: appointments,
		OccupancyRate:     occupancyRate(bookedMinutes, professionals),
		TopServices:       top,
	}, nil
}

// occupancyRate devuelve 0..1 redondeado a 4 decimales. Sin profesionales no
// se divide por cero: un salón sin personal tiene ocupación 0.
func occupancyRate(bookedMinutes, professionals int) float64 {
	if professionals <= 0 || bookedMinutes <= 0 {
		return 0
	}
	capacity := professionals * kernel.OccupancyDayMinutes
	rate := math.Round(float64(bookedMinutes)/float64(capacity)*10000) / 10000
	if rate > 1 {
		return 1 // sobre-agenda: se reporta tope 100%, no 130%
	}
	return rate
}

func (s DashboardService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
