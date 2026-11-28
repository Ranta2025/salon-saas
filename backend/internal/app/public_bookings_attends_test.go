package app

import (
	"errors"
	"salon-saas/internal/testutil/agendakit"
	"testing"

	"salon-saas/internal/domain"
)

// S-F2a-2: la reserva pública asigna a quien atiende. Un dueño/administración
// sin attends no recibe turnos por asignación automática ni puede elegirse a
// mano como profesional.
func TestPublicBooking_no_asigna_quien_no_atiende(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Staff.BySalon["s1"] = []domain.StaffMember{
		{ID: "duenio", Name: "Dueña", Role: domain.RoleOwner},
		{ID: "admin", Name: "Carla", Role: domain.RoleAdmin},
		{ID: "recepcion", Name: "Dani", Role: domain.RoleReception, Attends: true},
	}
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID

	// Nadie atiende: 409 (no hay profesional libre), nunca un turno fantasma.
	if _, err := env.Book.Book("navajas-oro", in); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("sin gente que atienda: %v", err)
	}
	if len(env.Bookings.All) != 0 {
		t.Fatalf("no debe quedar ningún turno: %+v", env.Bookings.All)
	}
	// Elegido a mano: 404 (no existe como profesional del salón).
	for _, id := range []string{"duenio", "admin", "recepcion"} {
		in.ProfessionalID = id
		if _, err := env.Book.Book("navajas-oro", in); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s como profesional: %v", id, err)
		}
	}
}

// El dueño-barbero (attends=true) sí atiende: la reserva pública lo asigna y
// acepta su id explícito.
func TestPublicBooking_asigna_duenio_que_atiende(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Staff.BySalon["s1"] = []domain.StaffMember{
		{ID: "barbero", Name: "Beto", Role: domain.RoleOwner, Attends: true},
	}
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID

	b, err := env.Book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reserva sin profesional: %v", err)
	}
	if b.ProfessionalID != "barbero" {
		t.Fatalf("el dueño-barbero tiene que ser asignado: %+v", b)
	}
	// Y con el id explícito, en otro hueco.
	in.StartsAt = in.StartsAt.Add(45 * 60 * 1e9)
	in.ProfessionalID = "barbero"
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("dueño-barbero elegido a mano: %v", err)
	}
}
