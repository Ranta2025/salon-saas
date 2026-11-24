// BookingService: agenda del salón. La regla de negocio vive acá:
// quién puede reservar, y el filtro amable de solape. La garantía dura es la
// EXCLUDE de Postgres (migración 002): si dos requests corren a la vez, la DB
// deja pasar una y la otra vuelve como ErrAlreadyExists -> 409.
package app

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type BookingService struct {
	Bookings    ports.BookingRepository
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón para el TEXTO de los avisos (el
	// evento del websocket sigue en UTC). nil = defaults de fábrica.
	Settings ports.SalonSettingsRepository
	// Events y Notifier son opcionales (nil = no avisa): el caso de uso
	// publica "agendé un turno" sin saber que atrás hay websockets.
	Events   ports.EventPublisher
	Notifier Notifier
	// Services y Resources (F2e): un turno que ocupa una unidad de equipo no
	// puede reprogramarse a un horario donde no hay ninguna libre. Opcionales:
	// sin servicio o sin unidad el turno se mueve como siempre.
	Services  ports.ServiceRepository
	Resources ports.ResourceRepository
	// ClientLinks vincula el turno con la ficha por id o por teléfono (F5, D3-b).
	// Best-effort: nil o un fallo del UPDATE no tumba el turno.
	ClientLinks ports.ClientLinkRepository
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
	// B-12: el turno tiene que ser para alguien que atiende en ESTE salón.
	if err := ensureProfessional(s.Memberships, salonID, professionalID); err != nil {
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
		// Misma carrera que en ventas: el índice único decide y acá se resuelve
		// devolviendo la reserva original en vez de un 409 mentiroso.
		if b.ClientOpID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			if existing, rerr := s.Bookings.FindByClientOpID(salonID, b.ClientOpID); rerr == nil {
				return existing, nil
			}
		}
		return domain.Booking{}, err
	}
	// D3-b: el turno se vincula a la ÚNICA ficha del salón cuyo teléfono
	// normalizado coincide (nunca se crea una ficha desde acá). Best-effort: un
	// fallo del UPDATE se loguea y el turno queda como está.
	if s.ClientLinks != nil && strings.TrimSpace(b.ClientPhone) != "" {
		if err := s.ClientLinks.LinkBookingByPhone(salonID, b.ID); err != nil {
			log.Printf("turno %s: vincular la ficha en el salón %s: %v", b.ID, salonID, err)
		}
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
		b.ClientName+" · "+salonTimeLabel(s.Settings, salonID, b.StartsAt), domain.KindBooking)
	return b, nil
}

// BookingPatch es el body de PATCH /salons/:id/bookings/:bid: estado y/o
// reprogramación. nil = el campo no vino.
type BookingPatch struct {
	Status         *domain.BookingStatus
	StartsAt       *time.Time
	EndsAt         *time.Time
	ProfessionalID *string
}

// reschedules dice si el patch mueve el turno (horario y/o profesional).
func (p BookingPatch) reschedules() bool {
	if p.StartsAt != nil || p.EndsAt != nil {
		return true
	}
	return p.ProfessionalID != nil && strings.TrimSpace(*p.ProfessionalID) != ""
}

// puedeEditarTurno: recepción y superiores tocan cualquier turno; el
// profesional solo los suyos (no reprograma la agenda ajena).
func puedeEditarTurno(actor domain.Membership, b domain.Booking) bool {
	switch actor.Role {
	case domain.RoleOwner, domain.RoleAdmin, domain.RoleReception:
		return true
	case domain.RoleProfessional:
		return actor.UserID == b.ProfessionalID
	default:
		return false
	}
}

// Update aplica cancelación / no-show / hecho y/o reprogramación, y publica
// booking.updated para que los calendarios abiertos se refresquen solos.
func (s BookingService) Update(actor domain.Membership, salonID, bookingID string, patch BookingPatch) (domain.Booking, error) {
	if actor.SalonID != salonID {
		return domain.Booking{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Booking{}, domain.ErrForbidden
	}
	// Scope por salón: el turno de otro tenant no existe (404).
	b, err := s.Bookings.FindByID(salonID, bookingID)
	if err != nil {
		return domain.Booking{}, err
	}
	if !puedeEditarTurno(m, b) {
		return domain.Booking{}, domain.ErrForbidden
	}

	rescheduled := false
	if patch.reschedules() {
		// Un turno cerrado liberó su hueco: reprogramarlo lo reabriría sin
		// pasar por la decisión de estado. Se rechaza.
		if b.Status != domain.BookingBooked {
			return domain.Booking{}, fmt.Errorf("%w: no se puede reprogramar un turno cancelado, hecho o no-show", domain.ErrValidation)
		}
		startsAt, endsAt, professionalID := b.StartsAt, b.EndsAt, b.ProfessionalID
		if patch.StartsAt != nil {
			startsAt = *patch.StartsAt
		}
		if patch.EndsAt != nil {
			endsAt = *patch.EndsAt
		}
		if patch.ProfessionalID != nil {
			professionalID = *patch.ProfessionalID
		}
		moved, err := b.Reschedule(professionalID, startsAt, endsAt)
		if err != nil {
			return domain.Booking{}, err
		}
		if moved.ProfessionalID != b.ProfessionalID {
			if err := ensureProfessional(s.Memberships, salonID, moved.ProfessionalID); err != nil {
				return domain.Booking{}, err
			}
		}
		// Equipo (F2e, S-F2e-4): un turno que ocupa una unidad exige que SU
		// unidad (o otra ACTIVA libre del mismo tipo) esté libre en el horario
		// nuevo. Si no queda ninguna, es 409: nunca dos turnos en una unidad.
		moved, err = s.moveUnit(salonID, b, moved)
		if err != nil {
			return domain.Booking{}, err
		}
		busy, err := s.Bookings.FindOverlapping(salonID, moved.ProfessionalID, moved.StartsAt, moved.EndsAt)
		if err != nil {
			return domain.Booking{}, err
		}
		for _, other := range busy {
			if other.ID != b.ID {
				return domain.Booking{}, fmt.Errorf("%w: el horario ya está ocupado", domain.ErrAlreadyExists)
			}
		}
		b = moved
		rescheduled = true
	}
	if patch.Status != nil {
		updated, err := b.SetStatus(*patch.Status)
		if err != nil {
			return domain.Booking{}, err
		}
		b = updated
	}

	if err := s.Bookings.Update(b); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.Booking{}, fmt.Errorf("%w: el horario ya está ocupado", domain.ErrAlreadyExists)
		}
		return domain.Booking{}, err
	}
	publish(s.Events, ports.Event{
		Type:    "booking.updated",
		SalonID: salonID,
		Data: map[string]any{
			"id":              b.ID,
			"professional_id": b.ProfessionalID,
			"client_name":     b.ClientName,
			"starts_at":       b.StartsAt.Format(time.RFC3339),
			"ends_at":         b.EndsAt.Format(time.RFC3339),
			"status":          string(b.Status),
		},
		TS: time.Now().UTC(),
	})
	if patch.Status != nil {
		notify(s.Notifier, salonID, "", bookingChangeTitle(b.Status),
			b.ClientName+" · "+salonTimeLabel(s.Settings, salonID, b.StartsAt), domain.KindBooking)
	} else if rescheduled {
		notify(s.Notifier, salonID, "", "Turno reprogramado",
			b.ClientName+" · "+salonTimeLabel(s.Settings, salonID, b.StartsAt), domain.KindBooking)
	}
	return b, nil
}

// bookingChangeTitle es el título del aviso de un cambio de estado.
func bookingChangeTitle(status domain.BookingStatus) string {
	switch status {
	case domain.BookingCancelled:
		return "Turno cancelado"
	case domain.BookingNoShow:
		return "Cliente no se presentó"
	default:
		return "Turno atendido"
	}
}

// moveUnit deja el turno reprogramado con una unidad libre en el horario nuevo:
// conserva la suya si sigue libre y, si no, toma otra unidad ACTIVA del tipo.
// Un turno sin unidad (reserva de staff sin servicio) no consulta el equipo y se
// reprograma como siempre.
func (s BookingService) moveUnit(salonID string, before, moved domain.Booking) (domain.Booking, error) {
	if before.ResourceUnitID == "" {
		return moved, nil
	}
	if s.Resources == nil || s.Services == nil {
		return moved, nil
	}
	busy, err := s.Resources.OccupiedUnits(salonID, moved.StartsAt, moved.EndsAt, before.ID)
	if err != nil {
		return domain.Booking{}, err
	}
	if domain.UnitFree(busy, before.ResourceUnitID, moved.StartsAt, moved.EndsAt) {
		return moved, nil
	}
	service, err := s.Services.FindByID(salonID, before.ServiceID)
	if err != nil || service.ResourceTypeID == "" {
		return domain.Booking{}, fmt.Errorf("%w: no hay equipo libre en ese horario", domain.ErrAlreadyExists)
	}
	units, err := s.Resources.ActiveUnits(salonID, service.ResourceTypeID)
	if err != nil {
		return domain.Booking{}, err
	}
	free := domain.FreeUnits(units, busy, moved.StartsAt, moved.EndsAt)
	if len(free) == 0 {
		return domain.Booking{}, fmt.Errorf("%w: no hay equipo libre en ese horario", domain.ErrAlreadyExists)
	}
	moved.ResourceUnitID = free[0].ID
	return moved, nil
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
