// BookingService: agenda del salón. La regla de negocio vive acá:
// quién puede reservar, y el filtro amable de solape. La garantía dura es la
// EXCLUDE de Postgres (migración 002): si dos requests corren a la vez, la DB
// deja pasar una y la otra vuelve como ErrAlreadyExists -> 409.
package app

import (
	"errors"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type BookingService struct {
	Bookings    ports.BookingRepository
	Memberships ports.MembershipRepository
	// Events y Notifier son opcionales (nil = no avisa): el caso de uso
	// publica "agendé un turno" sin saber que atrás hay websockets.
	Events   ports.EventPublisher
	Notifier Notifier
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
	return s.CreateBookingWithOp(actor, salonID, professionalID, clientName, clientPhone, startsAt, endsAt, "")
}

// CreateBookingWithOp agenda un turno con un operation id del cliente.
// Idempotente: si esa operación ya existe en este salón, devuelve la reserva
// original en vez de agendar un turno nuevo (misma lógica que las ventas).
func (s BookingService) CreateBookingWithOp(actor domain.Membership, salonID, professionalID, clientName, clientPhone string, startsAt, endsAt time.Time, clientOpID string) (domain.Booking, error) {
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
	b.ClientOpID = strings.TrimSpace(clientOpID)
	if b.ClientOpID != "" {
		if existing, err := s.Bookings.FindByClientOpID(salonID, b.ClientOpID); err == nil {
			return existing, nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Booking{}, err
		}
	}
	busy, err := s.Bookings.FindOverlapping(salonID, professionalID, startsAt, endsAt)
	if err != nil {
		return domain.Booking{}, err
	}
	if len(busy) > 0 {
		return domain.Booking{}, domain.ErrAlreadyExists
	}
	if err := s.Bookings.Save(b); err != nil {
		// Misma carrera que en ventas: el índice único decide y acá se resuelve
		// devolviendo la reserva original en vez de un 409 mentiroso.
		if b.ClientOpID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			if existing, rerr := s.Bookings.FindByClientOpID(salonID, b.ClientOpID); rerr == nil {
				return existing, nil
			}
		}
		return domain.Booking{}, err
	}
	// La agenda cambió: el calendario de los demás se actualiza solo.
	publish(s.Events, ports.Event{
		Type:    "booking.created",
		SalonID: salonID,
		Data: map[string]any{
			"id":              b.ID,
			"professional_id": b.ProfessionalID,
			"client_name":     b.ClientName,
			"starts_at":       b.StartsAt.Format(time.RFC3339),
			"ends_at":         b.EndsAt.Format(time.RFC3339),
			"status":          string(b.Status),
		},
		TS: b.CreatedAt,
	})
	notify(s.Notifier, salonID, "", "Nuevo turno",
		b.ClientName+" · "+b.StartsAt.Format("02/01 15:04"), domain.KindBooking)
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
