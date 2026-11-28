package bookings_test

import (
	"errors"
	"salon-saas/internal/testutil"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
)

// S-F2d1-5 (PRESERVE): la reserva de staff desde el calendario NO se restringe
// por el horario semanal. Agenda a mano un turno fuera de todo horario (sábado a
// las 21:00) y solo el choque lo frena, como antes de F2d1.
func TestStaffBooking_noSeRestringePorHorarioSemanal(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	actor := testutil.Recepcionista(mems)
	_ = mems.Save(domain.Membership{UserID: "prof1", SalonID: "s1", Role: domain.RoleProfessional, Attends: true})
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}

	start := time.Date(2026, 12, 5, 21, 0, 0, 0, time.UTC)
	end := start.Add(45 * time.Minute)
	b, err := svc.CreateBooking(actor, "s1", "prof1", "Lía", "", start, end)
	if err != nil {
		t.Fatalf("el calendario agenda fuera de horario: %v", err)
	}
	if b.ProfessionalID != "prof1" {
		t.Fatalf("profesional del turno: %+v", b)
	}

	// El choque sigue siendo el único límite: el mismo hueco es 409.
	if _, err := svc.CreateBooking(actor, "s1", "prof1", "Otra", "", start, end); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("choque de turnos: %v", err)
	}
}
