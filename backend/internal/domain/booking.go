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

// Booking es una reserva de un barbero en un salón para un rango de tiempo.
// La invariante "no solapar" la garantiza la DB (EXCLUDE); el dominio valida
// forma: tiempos coherentes y datos presentes.
type Booking struct {
	ID          string
	SalonID     string
	BarberID    string
	ClientName  string
	ClientPhone string
	StartsAt    time.Time
	EndsAt      time.Time
	Status      BookingStatus
	CreatedAt   time.Time
}

// NewBooking valida y crea una reserva activa.
func NewBooking(salonID, barberID, clientName, clientPhone string, startsAt, endsAt time.Time) (Booking, error) {
	clientName = strings.TrimSpace(clientName)
	if salonID == "" || barberID == "" {
		return Booking{}, fmt.Errorf("%w: salón y barbero obligatorios", ErrValidation)
	}
	if clientName == "" {
		return Booking{}, fmt.Errorf("%w: nombre del cliente vacío", ErrValidation)
	}
	if !endsAt.After(startsAt) {
		return Booking{}, fmt.Errorf("%w: el fin debe ser posterior al inicio", ErrValidation)
	}
	return Booking{
		ID:          NewID(),
		SalonID:     salonID,
		BarberID:    barberID,
		ClientName:  clientName,
		ClientPhone: strings.TrimSpace(clientPhone),
		StartsAt:    startsAt.UTC(),
		EndsAt:      endsAt.UTC(),
		Status:      BookingBooked,
		CreatedAt:   time.Now().UTC(),
	}, nil
}

// Overlaps dice si dos reservas del MISMO barbero se pisan.
// Se usa en tests y en el caso de uso (filtro rápido antes de la DB).
func (b Booking) Overlaps(o Booking) bool {
	if b.SalonID != o.SalonID || b.BarberID != o.BarberID {
		return false
	}
	return b.StartsAt.Before(o.EndsAt) && o.StartsAt.Before(b.EndsAt)
}
