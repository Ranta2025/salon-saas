package app

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
)

// S-F2a-2: la disponibilidad pública solo ofrece a quien atiende. El dueño con
// attends=false no infla la agenda; el dueño-barbero (attends=true) sí aparece.
func TestAvailability_solo_ofrece_quien_atiende(t *testing.T) {
	env := newPublicEnv(t)
	env.staff.bySalon["s1"] = []domain.StaffMember{
		{ID: "prof1", Name: "Ana", Role: domain.RoleProfessional},
		{ID: "duenio", Name: "Dueña", Role: domain.RoleOwner},                // no atiende
		{ID: "barbero", Name: "Beto", Role: domain.RoleOwner, Attends: true}, // dueño-barbero
		{ID: "admin", Name: "Carla", Role: domain.RoleAdmin},                 // no atiende
		{ID: "recepcion", Name: "Dani", Role: domain.RoleReception, Attends: true},
	}

	day, err := env.avail.Day("navajas-oro", "2026-12-01", env.corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	if len(day.Slots) == 0 {
		t.Fatal("los que atienden tienen huecos")
	}
	for _, s := range day.Slots {
		switch s.ProfessionalID {
		case "prof1", "barbero":
		default:
			t.Fatalf("%s no atiende y no debe tener huecos: %+v", s.ProfessionalID, day.Slots)
		}
	}

	// El dueño apagado no existe como profesional del salón: 404.
	if _, err := env.avail.Day("navajas-oro", "2026-12-01", env.corte.ID, "duenio"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("dueño sin attends: %v", err)
	}
	// El dueño-barbero sí, y con sus huecos.
	day, err = env.avail.Day("navajas-oro", "2026-12-01", env.corte.ID, "barbero")
	if err != nil {
		t.Fatalf("dueño-barbero: %v", err)
	}
	if len(day.Slots) == 0 {
		t.Fatal("el dueño-barbero tiene que tener huecos")
	}
	for _, s := range day.Slots {
		if s.ProfessionalID != "barbero" {
			t.Fatalf("huecos de otro: %+v", day.Slots)
		}
	}
}

// Un salón donde nadie atiende no tiene huecos: [] y no un error (500).
func TestAvailability_nadie_atiende_no_hay_slots(t *testing.T) {
	env := newPublicEnv(t)
	env.staff.bySalon["s1"] = []domain.StaffMember{
		{ID: "duenio", Name: "Dueña", Role: domain.RoleOwner},
		{ID: "admin", Name: "Carla", Role: domain.RoleAdmin},
		{ID: "recepcion", Name: "Dani", Role: domain.RoleReception, Attends: true},
	}
	day, err := env.avail.Day("navajas-oro", "2026-12-01", env.corte.ID, "")
	if err != nil {
		t.Fatalf("sin gente que atienda la respuesta es vacía, no un error: %v", err)
	}
	if len(day.Slots) != 0 {
		t.Fatalf("esperaba [], dio %+v", day.Slots)
	}
	if day.Slots == nil {
		t.Fatal("slots tiene que ser [] y no null")
	}
}
