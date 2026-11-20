// BookingService: agenda del salón. La regla de negocio vive acá:
// quién puede reservar, y el filtro amable de solape. La garantía dura es la
// EXCLUDE de Postgres (migración 002): si dos requests corren a la vez, la DB
// deja pasar una y la otra vuelve como ErrAlreadyExists -> 409.
package app

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type BookingService struct {
	Bookings    ports.BookingRepository
	Memberships ports.MembershipRepository
}

// puedeAgendar: recepción y superiores agendan a cualquiera; el profesional solo a sí mismo.
func puedeAgendar(actor domain.Membership, professionalID string) bool {
	if actor.Role == domain.RoleOwner || actor.Role == domain.RoleAdmin || actor.Role == domain.RoleReception {
		return true
	}
	return actor.Role == domain.RoleProfessional && actor.UserID == professionalID
}

// CreateBooking agenda un turno. actor debe ser miembro del salón.
func (s BookingService) CreateBooking(actor domain.Membership, salonID, professionalID, clientName, clientPhone string, startsAt, endsAt time.Time) (domain.Booking, error) {
	if actor.SalonID != salonID {
		return domain.Booking{}, domain.ErrForbidden
	}
	if _, err := s.Memberships.Find(actor.UserID, salonID); err != nil {
		return domain.Booking{}, domain.ErrForbidden
	}
	if !puedeAgendar(actor, professionalID) {
		return domain.Booking{}, domain.ErrForbidden
	}
	b, err := domain.NewBooking(salonID, professionalID, clientName, clientPhone, startsAt, endsAt)
	if err != nil {
		return domain.Booking{}, err
	}
	busy, err := s.Bookings.FindOverlapping(salonID, professionalID, startsAt, endsAt)
	if err != nil {
		return domain.Booking{}, err
	}
	if len(busy) > 0 {
		return domain.Booking{}, domain.ErrAlreadyExists
	}
	if err := s.Bookings.Save(b); err != nil {
		return domain.Booking{}, err
	}
	return b, nil
}

// List trae la agenda del salón en [from,to). Solo miembros del salón.
func (s BookingService) List(actor domain.Membership, salonID string, from, to time.Time) ([]domain.Booking, error) {
	if actor.SalonID != salonID {
		return nil, domain.ErrForbidden
	}
	if _, err := s.Memberships.Find(actor.UserID, salonID); err != nil {
		return nil, domain.ErrForbidden
	}
	return s.Bookings.ListRange(salonID, from, to)
}
