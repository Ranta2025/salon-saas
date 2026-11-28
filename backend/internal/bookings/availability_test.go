package bookings_test

import (
	"errors"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/agendakit"
	"testing"
	"time"
)

// agendakit.PublicEnv: fixture de la agenda pública. Un salón en CDMX que abre
// 09:00-12:00 cada 30 minutos, un servicio ACTIVO de 45 minutos y dos
// profesionales. 09:00 local = 15:00Z (CDMX no tiene DST desde 2022).

// T6: GET /public/salons/:slug/availability. Huecos libres dentro del horario
// del salón, en la zona del salón, con la duración del servicio.
func TestAvailability_slots(t *testing.T) {
	env := agendakit.NewPublicEnv(t)

	day, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	if day.Date != "2026-12-01" {
		t.Fatalf("fecha: %q", day.Date)
	}
	// 5 huecos por profesional: 09:00, 09:30, 10:00, 10:30 y 11:00 local
	// (11:00+45 = 11:45 entra; 11:30+45 > 12:00 no). Dos profesionales.
	want := []struct {
		prof  string
		start string
		end   string
	}{
		{"prof1", "2026-12-01T15:00:00Z", "2026-12-01T15:45:00Z"},
		{"prof2", "2026-12-01T15:00:00Z", "2026-12-01T15:45:00Z"},
		{"prof1", "2026-12-01T15:30:00Z", "2026-12-01T16:15:00Z"},
		{"prof2", "2026-12-01T15:30:00Z", "2026-12-01T16:15:00Z"},
		{"prof1", "2026-12-01T16:00:00Z", "2026-12-01T16:45:00Z"},
		{"prof2", "2026-12-01T16:00:00Z", "2026-12-01T16:45:00Z"},
		{"prof1", "2026-12-01T16:30:00Z", "2026-12-01T17:15:00Z"},
		{"prof2", "2026-12-01T16:30:00Z", "2026-12-01T17:15:00Z"},
		{"prof1", "2026-12-01T17:00:00Z", "2026-12-01T17:45:00Z"},
		{"prof2", "2026-12-01T17:00:00Z", "2026-12-01T17:45:00Z"},
	}
	if len(day.Slots) != len(want) {
		t.Fatalf("esperaba %d huecos, dio %d: %+v", len(want), len(day.Slots), day.Slots)
	}
	for i, w := range want {
		got := day.Slots[i]
		if got.ProfessionalID != w.prof || got.StartsAt.Format(time.RFC3339) != w.start || got.EndsAt.Format(time.RFC3339) != w.end {
			t.Fatalf("hueco %d: %+v (esperaba %+v)", i, got, w)
		}
	}

	// Una reserva de 09:30-10:15 local saca los huecos que se pisan.
	b, _ := domain.NewBooking("s1", "prof1", "Lía", "", time.Date(2026, 12, 1, 15, 30, 0, 0, time.UTC), time.Date(2026, 12, 1, 16, 15, 0, 0, time.UTC))
	_ = env.Bookings.Save(b)
	day, err = env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof1")
	if err != nil {
		t.Fatalf("disponibilidad con reserva: %v", err)
	}
	if len(day.Slots) != 2 || day.Slots[0].StartsAt.Format(time.RFC3339) != "2026-12-01T16:30:00Z" ||
		day.Slots[1].StartsAt.Format(time.RFC3339) != "2026-12-01T17:00:00Z" {
		t.Fatalf("quedan 10:30 y 11:00 local, dio %+v", day.Slots)
	}
	// Una cancelada libera el hueco.
	env.Bookings.All[0], _ = env.Bookings.All[0].SetStatus(domain.BookingCancelled)
	day, _ = env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof1")
	if len(day.Slots) != 5 {
		t.Fatalf("la cancelada no debe bloquear: %+v", day.Slots)
	}
	// Filtrar por profesional devuelve solo el suyo.
	day, _ = env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof2")
	for _, s := range day.Slots {
		if s.ProfessionalID != "prof2" {
			t.Fatalf("filtro de profesional: %+v", day.Slots)
		}
	}
	// Una fecha pasada no tiene huecos (no se agenda hacia atrás).
	day, err = env.Avail.Day("navajas-oro", "2020-01-01", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("fecha pasada: %v", err)
	}
	if len(day.Slots) != 0 {
		t.Fatalf("fecha pasada debe dar [], dio %+v", day.Slots)
	}
}

func TestAvailability_errores(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	if _, err := env.Avail.Day("no-existe", "2026-12-01", env.Corte.ID, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("slug inexistente: %v", err)
	}
	if _, err := env.Avail.Day("navajas-oro", "01/12/2026", env.Corte.ID, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("fecha inválida: %v", err)
	}
	if _, err := env.Avail.Day("navajas-oro", "2026-12-01", "svc-inexistente", ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("servicio inexistente: %v", err)
	}
	// Servicio inactivo: no se ofrece al público.
	if _, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Viejo.ID, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("servicio inactivo: %v", err)
	}
	if _, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof-ajeno"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("profesional que no atiende: %v", err)
	}
	// Sin fila de settings: defaults de fábrica (09:00-19:00, paso 30).
	// Caben 19 arranques (09:00 local a 18:00 local).
	env.Settings.BySalon = nil
	day, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof1")
	if err != nil {
		t.Fatalf("sin settings: %v", err)
	}
	if len(day.Slots) != 19 {
		t.Fatalf("con defaults esperaba 19 huecos, dio %d: %+v", len(day.Slots), day.Slots)
	}
}
