// PublicBookingService: la reserva pública (sin login) de la landing.
// La duración la pone el servicio, el profesional es opcional (se elige uno
// libre) y la garantía anti-solape sigue siendo la EXCLUDE de Postgres: dos
// personas reservando el mismo hueco a la vez terminan en 409 para una.
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

// PublicBookingInput es el body de POST /public/salons/:slug/bookings ya
// parseado (el handler traduce starts_at a time.Time).
type PublicBookingInput struct {
	ServiceID      string
	ProfessionalID string // vacío = el sistema elige uno libre
	ClientName     string
	ClientPhone    string
	StartsAt       time.Time
	ClientOpID     string // reintento del navegador: idempotencia
}

type PublicBookingService struct {
	Salons   ports.SalonRepository
	Services ports.ServiceRepository
	Settings ports.SalonSettingsRepository
	Staff    ports.StaffLister
	Bookings ports.BookingRepository
	// Clients deja la ficha del visitante cuando la reserva entra (E-03). Es
	// opcional como los avisos: nil = este entorno no lleva fichas.
	Clients ports.ClientRepository
	// Opcionales: nil = no avisa (tests sin infraestructura).
	Events   ports.EventPublisher
	Notifier Notifier
}

// Book agenda un turno público. Errores: 404 (slug/profesional inexistente),
// 400 (datos faltantes, servicio inactivo, fuera de horario, en el pasado) y
// 409 (hueco tomado, sin profesional libre).
func (s PublicBookingService) Book(slug string, in PublicBookingInput) (domain.Booking, error) {
	salon, err := s.Salons.FindBySlug(strings.TrimSpace(slug))
	if err != nil {
		return domain.Booking{}, err
	}
	if in.StartsAt.IsZero() {
		return domain.Booking{}, fmt.Errorf("%w: falta el horario del turno", domain.ErrValidation)
	}
	opID := strings.TrimSpace(in.ClientOpID)
	// Idempotencia antes de mirar la agenda: el reintento devuelve la reserva
	// original aunque su hueco ya esté ocupado (por ella misma).
	if opID != "" {
		existing, err := s.Bookings.FindByClientOpID(salon.ID, opID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return domain.Booking{}, err
		}
	}

	settings, err := loadSalonSettings(s.Settings, salon.ID)
	if err != nil {
		return domain.Booking{}, err
	}
	service, err := resolvePublicService(s.Services, salon.ID, in.ServiceID)
	if err != nil {
		return domain.Booking{}, err
	}
	staff, err := s.Staff.ListStaff(salon.ID)
	if err != nil {
		return domain.Booking{}, err
	}

	startsAt := in.StartsAt.UTC()
	endsAt := startsAt.Add(time.Duration(service.DurationMin) * time.Minute)
	// El turno tiene que entrar completo en el horario del salón (en su zona).
	open, closeAt, err := settings.Window(startsAt)
	if err != nil {
		return domain.Booking{}, err
	}
	if startsAt.Before(open) || endsAt.After(closeAt) {
		return domain.Booking{}, fmt.Errorf("%w: el horario elegido está fuera del horario del salón", domain.ErrValidation)
	}

	professionalID := strings.TrimSpace(in.ProfessionalID)
	if professionalID == "" {
		professionalID, err = s.freeProfessional(staff, salon.ID, startsAt, endsAt)
		if err != nil {
			return domain.Booking{}, err
		}
	} else {
		if !staffHas(staff, professionalID) {
			return domain.Booking{}, domain.ErrNotFound
		}
		busy, err := s.Bookings.FindOverlapping(salon.ID, professionalID, startsAt, endsAt)
		if err != nil {
			return domain.Booking{}, err
		}
		if len(busy) > 0 {
			return domain.Booking{}, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
		}
	}

	b, err := domain.NewBooking(salon.ID, professionalID, in.ClientName, in.ClientPhone, startsAt, endsAt)
	if err != nil {
		return domain.Booking{}, err
	}
	b.ClientOpID = opID
	if err := s.Bookings.Save(b); err != nil {
		// Carrera real: la EXCLUDE dejó pasar a otro. El reintento con op id
		// devuelve la reserva que sí entró.
		if opID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			if existing, rerr := s.Bookings.FindByClientOpID(salon.ID, opID); rerr == nil {
				return existing, nil
			}
		}
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.Booking{}, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
		}
		return domain.Booking{}, err
	}

	// La ficha del visitante se escribe DESPUÉS de la reserva y no puede
	// tumbarla: el turno es lo que la persona pidió (E-03). Si falla, queda el
	// detalle en el log y la ficha se puede crear a mano desde el mostrador.
	s.upsertClient(salon.ID, b.ClientName, b.ClientPhone)

	publish(s.Events, ports.Event{
		Type:    "booking.created",
		SalonID: salon.ID,
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
	notify(s.Notifier, salon.ID, "", "Nuevo turno",
		b.ClientName+" · "+FormatSalonTime(settings, b.StartsAt)+" (reserva web)", domain.KindBooking)
	return b, nil
}

// upsertClient deja la ficha del visitante en el salón de la reserva. Busca
// primero para NO duplicar: por teléfono normalizado (la misma persona suele
// escribirlo distinto) y, si no dejó teléfono, por nombre exacto.
//
// Si la ficha ya existe no se toca: la del mostrador puede tener notas, fotos y
// un nombre corregido a mano que una reserva desde la web no tiene por qué
// pisar. Best-effort a propósito: la reserva ya entró.
func (s PublicBookingService) upsertClient(salonID, name, phone string) {
	if s.Clients == nil {
		return
	}
	phoneNorm := domain.NormalizePhone(phone)
	var err error
	if phoneNorm != "" {
		_, err = s.Clients.FindByPhone(salonID, phoneNorm)
	} else {
		_, err = s.Clients.FindByName(salonID, strings.TrimSpace(name))
	}
	switch {
	case err == nil:
		return // ya tiene ficha
	case !errors.Is(err, domain.ErrNotFound):
		log.Printf("reserva pública: buscar ficha en el salón %s: %v", salonID, err)
		return
	}
	c, err := domain.NewClient(salonID, name, phone, "", "")
	if err != nil {
		log.Printf("reserva pública: ficha inválida en el salón %s: %v", salonID, err)
		return
	}
	if err := s.Clients.Save(c); err != nil {
		log.Printf("reserva pública: guardar ficha en el salón %s: %v", salonID, err)
	}
}

// freeProfessional elige el primer profesional del equipo sin turno en el
// hueco. Si todos están ocupados es un 409, no una reserva sin profesional.
func (s PublicBookingService) freeProfessional(staff []domain.StaffMember, salonID string, start, end time.Time) (string, error) {
	for _, m := range staff {
		if !m.Role.IsStaff() {
			continue
		}
		busy, err := s.Bookings.FindOverlapping(salonID, m.ID, start, end)
		if err != nil {
			return "", err
		}
		if len(busy) == 0 {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("%w: no hay profesional libre en ese horario", domain.ErrAlreadyExists)
}

// staffHas dice si userID atiende en el salón.
func staffHas(staff []domain.StaffMember, userID string) bool {
	for _, m := range staff {
		if m.ID == userID && m.Role.IsStaff() {
			return true
		}
	}
	return false
}

// resolvePublicService resuelve un servicio ACTIVO del salón. Para el público,
// un servicio inactivo o de otro tenant no existe en la carta (400).
func resolvePublicService(services ports.ServiceRepository, salonID, serviceID string) (domain.Service, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return domain.Service{}, fmt.Errorf("%w: falta el servicio", domain.ErrValidation)
	}
	service, err := services.FindByID(salonID, serviceID)
	if err != nil {
		return domain.Service{}, fmt.Errorf("%w: el servicio no está disponible en este salón", domain.ErrValidation)
	}
	if !service.Active {
		return domain.Service{}, fmt.Errorf("%w: el servicio no está disponible en este salón", domain.ErrValidation)
	}
	return service, nil
}
