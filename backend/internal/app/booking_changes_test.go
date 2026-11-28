package app

import (
	"errors"
	"salon-saas/internal/testutil"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
)

// T6: PATCH /salons/:id/bookings/:bid. Cancelar / no-show / hecho y
// reprogramar, con permisos: recepción y superiores sobre cualquiera, el
// profesional solo los suyos.

func bookingFixture(t *testing.T) (*bkg.BookingService, *testutil.FakeBookings, *testutil.FakeMemberships, *testutil.FakeEvents, *testutil.FakeNotifier, domain.Booking) {
	t.Helper()
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	notifier := &testutil.FakeNotifier{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems, Events: events, Notifier: notifier}
	testutil.Miembro(mems, "recep", "s1", domain.RoleReception)
	testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	testutil.Miembro(mems, "prof2", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	rec, _ := mems.Find("recep", "s1")
	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("reserva base: %v", err)
	}
	events.All = nil // el alta ya publicó; acá empieza a contarse el PATCH
	notifier.Calls = 0
	return &svc, bookings, mems, events, notifier, b
}

func TestBookingUpdate_cancelar(t *testing.T) {
	svc, bookings, mems, events, notifier, b := bookingFixture(t)
	actor, _ := mems.Find("recep", "s1")
	status := domain.BookingCancelled

	got, err := svc.Update(actor, "s1", b.ID, bkg.BookingPatch{Status: &status})
	if err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	if got.Status != domain.BookingCancelled || got.ID != b.ID {
		t.Fatalf("reserva cancelada: %+v", got)
	}
	// El evento de la agenda sale con el estado nuevo.
	ev, n := events.Typed("booking.updated")
	if n != 1 {
		t.Fatalf("esperaba un booking.updated, hubo %d", n)
	}
	if data, ok := ev.Data.(map[string]any); !ok || data["id"] != b.ID || data["status"] != "cancelled" {
		t.Fatalf("payload de booking.updated: %+v", ev.Data)
	}
	if notifier.Calls != 1 {
		t.Fatalf("cancelar deja un aviso, hubo %d", notifier.Calls)
	}
	// El hueco queda libre: la DB también lo ve (status cancelled).
	var stored domain.Booking
	for _, x := range bookings.All {
		if x.ID == b.ID {
			stored = x
		}
	}
	if stored.Status != domain.BookingCancelled {
		t.Fatalf("no persistió la cancelación: %+v", stored)
	}
}

func TestBookingUpdate_permisos(t *testing.T) {
	svc, _, mems, _, _, b := bookingFixture(t)
	status := domain.BookingDone

	// Profesional ajeno al turno: 403. Profesional dueño del turno: puede.
	prof2, _ := mems.Find("prof2", "s1")
	if _, err := svc.Update(prof2, "s1", b.ID, bkg.BookingPatch{Status: &status}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional ajeno debe dar Forbidden, dio %v", err)
	}
	prof1, _ := mems.Find("prof1", "s1")
	if _, err := svc.Update(prof1, "s1", b.ID, bkg.BookingPatch{Status: &status}); err != nil {
		t.Fatalf("profesional dueño del turno: %v", err)
	}
	// Admin: manda sobre cualquier turno.
	admin := testutil.Miembro(mems, "admin", "s1", domain.RoleAdmin)
	noShow := domain.BookingNoShow
	if _, err := svc.Update(admin, "s1", b.ID, bkg.BookingPatch{Status: &noShow}); err != nil {
		t.Fatalf("admin: %v", err)
	}
	// Profesional de otro salón / miembro ajeno: 403.
	otro := testutil.Miembro(mems, "ajeno", "s2", domain.RoleOwner)
	if _, err := svc.Update(otro, "s1", b.ID, bkg.BookingPatch{Status: &status}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón: %v", err)
	}
	// Recepción de otro salón: 403. Recepción propia: puede (ya probado arriba).
	// Turno inexistente: 404. Turno de otro salón: 404 (no se filtra).
	owner, _ := mems.Find("owner", "s1")
	if _, err := svc.Update(owner, "s1", "no-existe", bkg.BookingPatch{Status: &status}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("turno inexistente: %v", err)
	}
	ajena, _ := domain.NewBooking("s2", "prof1", "Otra", "", time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 11, 0, 0, 0, time.UTC))
	_ = svc.Bookings.Save(ajena)
	if _, err := svc.Update(owner, "s1", ajena.ID, bkg.BookingPatch{Status: &status}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("turno de otro salón: %v", err)
	}
	// Estado inválido: 400 de dominio.
	bad := domain.BookingStatus("cancelado")
	if _, err := svc.Update(owner, "s1", b.ID, bkg.BookingPatch{Status: &bad}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("estado inválido: %v", err)
	}
}

func TestBookingUpdate_reprogramar(t *testing.T) {
	svc, _, mems, events, _, b := bookingFixture(t)
	owner, _ := mems.Find("owner", "s1")
	start := b.StartsAt.Add(3 * time.Hour)
	end := b.EndsAt.Add(3 * time.Hour)

	got, err := svc.Update(owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &start, EndsAt: &end})
	if err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	if !got.StartsAt.Equal(start) || !got.EndsAt.Equal(end) || got.Status != domain.BookingBooked {
		t.Fatalf("reprogramado: %+v", got)
	}
	if _, n := events.Typed("booking.updated"); n != 1 {
		t.Fatalf("esperaba booking.updated, hubo %d", n)
	}

	// Mover a un hueco ocupado: 409 (anti-solape), sin tocar la reserva.
	ocupada, _ := domain.NewBooking("s1", "prof1", "Ocupa", "", start.Add(5*time.Hour), start.Add(6*time.Hour))
	_ = svc.Bookings.Save(ocupada)
	badStart, badEnd := ocupada.StartsAt, ocupada.EndsAt
	if _, err := svc.Update(owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &badStart, EndsAt: &badEnd}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("hueco ocupado debe dar AlreadyExists, dio %v", err)
	}
	// Cambiar de profesional a uno libre, mismo horario: ok.
	other := "prof2"
	got, err = svc.Update(owner, "s1", b.ID, bkg.BookingPatch{ProfessionalID: &other})
	if err != nil {
		t.Fatalf("cambiar profesional: %v", err)
	}
	if got.ProfessionalID != "prof2" {
		t.Fatalf("profesional: %q", got.ProfessionalID)
	}
	// Respetar el mismo hueco (idempotente) no es un auto-solape.
	got, err = svc.Update(owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &got.StartsAt, EndsAt: &got.EndsAt})
	if err != nil {
		t.Fatalf("mismo horario debe ser idempotente: %v", err)
	}
	// Un turno cerrado no se reprograma (el hueco ya se liberó).
	cancelStatus := domain.BookingCancelled
	cancelled, _ := svc.Update(owner, "s1", b.ID, bkg.BookingPatch{Status: &cancelStatus})
	movedStart, movedEnd := cancelled.StartsAt.Add(time.Hour), cancelled.EndsAt.Add(time.Hour)
	if _, err := svc.Update(owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &movedStart, EndsAt: &movedEnd}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("reprogramar cancelado: %v", err)
	}
	// Reprogramar a un profesional que no atiende: 400.
	ajeno := "prof-ajeno"
	if _, err := svc.Update(owner, "s1", b.ID, bkg.BookingPatch{ProfessionalID: &ajeno}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional ajeno: %v", err)
	}
}

func TestBookingUpdate_estado_y_horario_juntos(t *testing.T) {
	svc, _, mems, events, _, b := bookingFixture(t)
	owner, _ := mems.Find("owner", "s1")
	start := b.StartsAt.Add(4 * time.Hour)
	end := b.EndsAt.Add(4 * time.Hour)
	status := domain.BookingDone

	got, err := svc.Update(owner, "s1", b.ID, bkg.BookingPatch{Status: &status, StartsAt: &start, EndsAt: &end})
	if err != nil {
		t.Fatalf("patch combinado: %v", err)
	}
	if got.Status != domain.BookingDone || !got.StartsAt.Equal(start) {
		t.Fatalf("patch combinado: %+v", got)
	}
	if ev, n := events.Typed("booking.updated"); n != 1 || ev.Data.(map[string]any)["status"] != "done" {
		t.Fatalf("evento: %+v", events.All)
	}
}
