package app

// E-04 del hallazgo de la auditoría: los avisos de turnos del equipo mostraban
// la hora en UTC sin decirlo ("26/11 17:00" para un turno de las 11:00 en CDMX).
// El texto ahora va en la zona del salón y nombra la zona. El evento websocket
// no cambia: `starts_at` sigue siendo UTC RFC3339.

import (
	"strings"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
)

// bookingTimeEnv: agenda del salón s1 en CDMX (UTC-6, sin DST).
func bookingTimeEnv(t *testing.T) (*bkg.BookingService, *fb, *fakeMemberships, *fakeEvents, *fakeNotifier, domain.Membership) {
	t.Helper()
	bookings := &fb{}
	mems := &fakeMemberships{}
	notifier := &fakeNotifier{}
	settings := &fakeSettings{BySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}}}
	events := &fakeEvents{}
	svc := &bkg.BookingService{Bookings: bookings, Memberships: mems, Settings: settings, Events: events, Notifier: notifier}
	miembro(mems, "prof1", "s1", domain.RoleProfessional)
	rec := recepcionista(mems)
	return svc, bookings, mems, events, notifier, rec
}

func TestBookingService_aviso_de_alta_en_hora_del_salon(t *testing.T) {
	svc, _, mems, events, notifier, rec := bookingTimeEnv(t)
	// 17:00Z = 11:00 en CDMX: el caso del hallazgo.
	starts := time.Date(2036, 11, 26, 17, 0, 0, 0, time.UTC)
	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", starts, starts.Add(time.Hour))
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	body := notifier.lastBody()
	if !strings.Contains(body, "26/11 11:00 (America/Mexico_City)") {
		t.Fatalf("el aviso tiene que decir la hora local y la zona: %q", body)
	}
	if strings.Contains(body, "17:00") {
		t.Fatalf("el aviso no puede mostrar la hora UTC: %q", body)
	}
	// El evento no cambia: UTC RFC3339.
	ev, n := events.typed("booking.created")
	if n != 1 {
		t.Fatalf("esperaba un booking.created, hubo %d", n)
	}
	data, _ := ev.Data.(map[string]any)
	if data["starts_at"] != "2036-11-26T17:00:00Z" {
		t.Fatalf("starts_at del evento: %+v", data["starts_at"])
	}
	if data["id"] != b.ID {
		t.Fatalf("el evento debe ser el turno creado: %+v", data)
	}
	_ = mems
}

func TestBookingService_avisos_de_patch_en_hora_del_salon(t *testing.T) {
	svc, _, _, _, notifier, rec := bookingTimeEnv(t)
	starts := time.Date(2036, 11, 26, 17, 0, 0, 0, time.UTC)
	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", starts, starts.Add(time.Hour))
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}

	// Cancelar: "Turno cancelado" con la hora local.
	cancelled := domain.BookingCancelled
	if _, err := svc.Update(rec, "s1", b.ID, bkg.BookingPatch{Status: &cancelled}); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	if body := notifier.lastBody(); !strings.Contains(body, "26/11 11:00 (America/Mexico_City)") {
		t.Fatalf("el aviso de cancelación: %q", body)
	}
}

func TestBookingService_aviso_reprogramado_en_hora_del_salon(t *testing.T) {
	svc, _, _, _, notifier, rec := bookingTimeEnv(t)
	starts := time.Date(2036, 11, 26, 17, 0, 0, 0, time.UTC)
	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", starts, starts.Add(time.Hour))
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	moved := time.Date(2036, 11, 27, 18, 30, 0, 0, time.UTC) // 12:30 CDMX
	movedEnd := moved.Add(time.Hour)
	if _, err := svc.Update(rec, "s1", b.ID, bkg.BookingPatch{StartsAt: &moved, EndsAt: &movedEnd}); err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	body := notifier.lastBody()
	if !strings.Contains(body, "27/11 12:30 (America/Mexico_City)") {
		t.Fatalf("el aviso de reprogramación: %q", body)
	}
	if strings.Contains(body, "18:30") {
		t.Fatalf("el aviso no puede mostrar la hora UTC: %q", body)
	}
}
