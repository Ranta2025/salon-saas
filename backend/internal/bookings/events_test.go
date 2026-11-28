package bookings_test

import (
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// Los casos de uso que ya existían (reserva, venta, cierre) publican su evento
// en el bus del salón: el frontend se entera sin polling. Además dejan un aviso
// persistido en los casos que valen la pena (turno nuevo, cierre de caja).

func TestBookingService_publica_booking_created(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	notifier := &testutil.FakeNotifier{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems, Events: events, Notifier: notifier}
	rec := testutil.Recepcionista(mems)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("reserva: %v", err)
	}
	ev, count := events.Typed("booking.created")
	if count != 1 {
		t.Fatalf("esperaba 1 booking.created, dio %d (%+v)", count, events.All)
	}
	if ev.SalonID != "s1" {
		t.Fatalf("el evento debe ir al salón de la reserva, dio %q", ev.SalonID)
	}
	data, ok := ev.Data.(map[string]any)
	if !ok || data["id"] != b.ID {
		t.Fatalf("el evento debe llevar la reserva creada, dio %+v", ev.Data)
	}
	// Aviso persistido para el salón (bandeja del equipo).
	if notifier.Calls != 1 {
		t.Fatalf("esperaba 1 aviso por la reserva nueva, dio %d", notifier.Calls)
	}
}

func TestBookingService_solape_no_publica_evento(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems, Events: events}
	rec := testutil.Recepcionista(mems)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	_, _ = svc.CreateBooking(rec, "s1", "prof1", "Lía", "", day.Add(10*time.Hour), day.Add(11*time.Hour))
	before, _ := events.Typed("booking.created")
	if _, err := svc.CreateBooking(rec, "s1", "prof1", "Ana", "", day.Add(10*time.Hour), day.Add(11*time.Hour)); err == nil {
		t.Fatal("el solape debe fallar")
	}
	after, count := events.Typed("booking.created")
	if count != 1 || after.Type != before.Type {
		t.Fatalf("un alta rechazada no publica evento, dio %d (%+v)", count, events.All)
	}
}
