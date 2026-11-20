package domain

import (
	"errors"
	"testing"
	"time"
)

// T6: PATCH de un turno. Cancelar, marcar no-show o hecho, y reprogramar.
// Las reglas viven en el dominio porque valen para cualquier camino que edite
// una reserva, no solo el handler HTTP.

func reservaFutura(t *testing.T) Booking {
	t.Helper()
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	b, err := NewBooking("s1", "prof1", "Lía", "555", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("reserva base: %v", err)
	}
	return b
}

func TestBooking_SetStatus(t *testing.T) {
	base := reservaFutura(t)
	for _, status := range []BookingStatus{BookingCancelled, BookingNoShow, BookingDone} {
		got, err := base.SetStatus(status)
		if err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		if got.Status != status {
			t.Fatalf("status: %q", got.Status)
		}
		// La identidad y los datos del turno no cambian.
		if got.ID != base.ID || got.SalonID != base.SalonID || got.ProfessionalID != base.ProfessionalID ||
			!got.StartsAt.Equal(base.StartsAt) || !got.EndsAt.Equal(base.EndsAt) {
			t.Fatalf("cambió datos al cambiar estado: %+v", got)
		}
	}
	// El estado original no se muta (value semantics).
	if base.Status != BookingBooked {
		t.Fatalf("la reserva original cambió: %q", base.Status)
	}
	for _, bad := range []BookingStatus{"", "booked", "cancelado", "DONE"} {
		if _, err := base.SetStatus(bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("estado %q debe ser ErrValidation, dio %v", bad, err)
		}
	}
}

func TestBooking_Reschedule(t *testing.T) {
	base := reservaFutura(t)
	newStart := base.StartsAt.Add(3 * time.Hour)
	newEnd := base.EndsAt.Add(3 * time.Hour)

	got, err := base.Reschedule("prof2", newStart, newEnd)
	if err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	if got.ProfessionalID != "prof2" || !got.StartsAt.Equal(newStart.UTC()) || !got.EndsAt.Equal(newEnd.UTC()) {
		t.Fatalf("no aplicó la reprogramación: %+v", got)
	}
	if got.ID != base.ID || got.ClientName != base.ClientName || got.Status != BookingBooked {
		t.Fatalf("la reprogramación no puede cambiar identidad ni estado: %+v", got)
	}
	// Mismo profesional, solo mover el horario: se conserva el profesional.
	got, err = base.Reschedule("", newStart, newEnd)
	if err != nil || got.ProfessionalID != base.ProfessionalID {
		t.Fatalf("profesional opcional: %+v %v", got, err)
	}
	// Fin antes que inicio, duración absurda y pasado: 400 de dominio.
	for _, c := range []struct {
		name     string
		prof     string
		start    time.Time
		end      time.Time
		expected error
	}{
		{"fin antes", "prof1", newEnd, newStart, ErrValidation},
		{"duración 13h", "prof1", newStart, newStart.Add(13 * time.Hour), ErrValidation},
		{"pasado", "prof1", time.Now().UTC().Add(-72 * time.Hour), time.Now().UTC().Add(-71 * time.Hour), ErrValidation},
	} {
		if _, err := base.Reschedule(c.prof, c.start, c.end); !errors.Is(err, c.expected) {
			t.Fatalf("%s debe ser ErrValidation, dio %v", c.name, err)
		}
	}
}
