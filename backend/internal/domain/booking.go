package domain

import (
	"fmt"
	"strings"
	"time"
)

// BookingStatus: solo 'booked' bloquea hueco (ver migración 002).
type BookingStatus string

const (
	BookingBooked    BookingStatus = "booked"
	BookingCancelled BookingStatus = "cancelled"
	BookingDone      BookingStatus = "done"
	BookingNoShow    BookingStatus = "no_show"
)

// Booking es una reserva de un profesional en un salón para un rango de tiempo.
// La invariante "no solapar" la garantiza la DB (EXCLUDE); el dominio valida
// forma: tiempos coherentes y datos presentes.
type Booking struct {
	ID             string
	SalonID        string
	ProfessionalID string
	ClientName     string
	ClientPhone    string
	StartsAt       time.Time
	EndsAt         time.Time
	Status         BookingStatus
	// ServiceID es el servicio reservado. Opcional: la reserva pública lo deja
	// siempre (para saber qué se hace) y la reserva de staff puede no llevarlo
	// (el calendario agenda a mano un turno sin servicio) (S-F2c-2).
	ServiceID string
	// ResourceUnitID es la unidad de equipo que este turno ocupa durante toda su
	// duración (F2e, S-F2e-1). Vacío = el turno no ocupa equipo: la reserva de
	// staff desde el calendario no lo hace (S-F2e-4) y los servicios sin tipo
	// tampoco.
	ResourceUnitID string
	// CoveredFrom es la profesional que TENÍA el turno antes de que lo cubriera
	// otra persona (S-F2d3-6). Vacío = nadie lo cubre. Solo lo escribe la
	// reasignación; reprogramar, cambiar estado y las reservas lo dejan igual.
	CoveredFrom string
	// ClientOpID identifica la operación en el cliente (cola offline). Igual
	// que en las ventas: si viene repetido, el servidor devuelve la reserva YA
	// registrada en vez de agendar un turno nuevo.
	ClientOpID string
	CreatedAt  time.Time
}

// NewBooking valida y crea una reserva activa.
func NewBooking(salonID, professionalID, clientName, clientPhone string, startsAt, endsAt time.Time) (Booking, error) {
	clientName = strings.TrimSpace(clientName)
	if salonID == "" || professionalID == "" {
		return Booking{}, fmt.Errorf("%w: salón y profesional obligatorios", ErrValidation)
	}
	if clientName == "" {
		return Booking{}, fmt.Errorf("%w: nombre del cliente vacío", ErrValidation)
	}
	if err := validateText("el nombre del cliente", clientName, MaxTextName); err != nil {
		return Booking{}, err
	}
	if err := validateText("el teléfono", strings.TrimSpace(clientPhone), MaxTextPhone); err != nil {
		return Booking{}, err
	}
	if !endsAt.After(startsAt) {
		return Booking{}, fmt.Errorf("%w: el fin debe ser posterior al inicio", ErrValidation)
	}
	// B-18: un turno de más de medio día es un error de tipeo; y no se agenda
	// hacia atrás (el calendario no reescribe el pasado).
	if endsAt.Sub(startsAt) > MaxBookingDuration {
		return Booking{}, fmt.Errorf("%w: un turno no puede durar más de %d horas", ErrValidation, int(MaxBookingDuration.Hours()))
	}
	if startsAt.Before(time.Now().UTC().Add(-bookingClockSkew)) {
		return Booking{}, fmt.Errorf("%w: no se puede agendar un turno en el pasado", ErrValidation)
	}
	return Booking{
		ID:             NewID(),
		SalonID:        salonID,
		ProfessionalID: professionalID,
		ClientName:     clientName,
		ClientPhone:    strings.TrimSpace(clientPhone),
		StartsAt:       startsAt.UTC(),
		EndsAt:         endsAt.UTC(),
		Status:         BookingBooked,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

// bookingClockSkew: tolerancia para relojes desfasados entre el navegador y el
// servidor. Medio minuto no cambia nada y evita rechazar un turno "ahora".
const bookingClockSkew = 30 * time.Second

// SetStatus devuelve la reserva con el estado nuevo. Solo los estados de
// cierre de un turno se pueden fijar por la API: cancelada, no se presentó o
// hecha. Volver a 'booked' reabriría un hueco ya liberado sin pasar por la
// garantía anti-solape, así que no se admite.
func (b Booking) SetStatus(status BookingStatus) (Booking, error) {
	switch status {
	case BookingCancelled, BookingNoShow, BookingDone:
		b.Status = status
		return b, nil
	default:
		return Booking{}, fmt.Errorf("%w: estado inválido %q (usá cancelled, no_show o done)", ErrValidation, status)
	}
}

// Reschedule devuelve la reserva movida de horario y/o de profesional,
// revalidando las mismas reglas que una reserva nueva (fin posterior al inicio,
// duración máxima y no en el pasado). professionalID vacío = se queda con el
// actual. No cambia identidad, cliente ni estado: mover un turno no lo reabre.
func (b Booking) Reschedule(professionalID string, startsAt, endsAt time.Time) (Booking, error) {
	if p := strings.TrimSpace(professionalID); p != "" {
		b.ProfessionalID = p
	}
	if b.ProfessionalID == "" {
		return Booking{}, fmt.Errorf("%w: el profesional es obligatorio", ErrValidation)
	}
	if !endsAt.After(startsAt) {
		return Booking{}, fmt.Errorf("%w: el fin debe ser posterior al inicio", ErrValidation)
	}
	if endsAt.Sub(startsAt) > MaxBookingDuration {
		return Booking{}, fmt.Errorf("%w: un turno no puede durar más de %d horas", ErrValidation, int(MaxBookingDuration.Hours()))
	}
	if startsAt.Before(time.Now().UTC().Add(-bookingClockSkew)) {
		return Booking{}, fmt.Errorf("%w: no se puede agendar un turno en el pasado", ErrValidation)
	}
	b.StartsAt = startsAt.UTC()
	b.EndsAt = endsAt.UTC()
	return b, nil
}

// Overlaps dice si dos reservas del MISMO profesional se pisan.
// Se usa en tests y en el caso de uso (filtro rápido antes de la DB).
func (b Booking) Overlaps(o Booking) bool {
	if b.SalonID != o.SalonID || b.ProfessionalID != o.ProfessionalID {
		return false
	}
	return b.StartsAt.Before(o.EndsAt) && o.StartsAt.Before(b.EndsAt)
}
