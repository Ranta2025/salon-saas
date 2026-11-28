package bookings_test

import (
	"errors"
	"salon-saas/internal/testutil"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/team"
)

// S-F2a-2: el alta de un turno desde el panel valida el professional_id con la
// misma regla que la disponibilidad. Antes, dueño y administración pasaban
// siempre (Role.IsStaff) aunque no atendieran.
func TestBooking_staff_solo_agenda_para_quien_atiende(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}
	rec := testutil.Recepcionista(mems)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	start, end := day.Add(10*time.Hour), day.Add(11*time.Hour)

	// Dueño y administración que no atienden: 400.
	_ = mems.Save(domain.Membership{UserID: "duenio", SalonID: "s1", Role: domain.RoleOwner})
	_ = mems.Save(domain.Membership{UserID: "admin", SalonID: "s1", Role: domain.RoleAdmin})
	for _, id := range []string{"duenio", "admin"} {
		if _, err := svc.CreateBooking(rec, "s1", id, "Lía", "", start, end); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%s como profesional: %v", id, err)
		}
	}
	// Recepción nunca atiende, ni con la marca encendida.
	_ = mems.Save(domain.Membership{UserID: "recep2", SalonID: "s1", Role: domain.RoleReception, Attends: true})
	if _, err := svc.CreateBooking(rec, "s1", "recep2", "Lía", "", start, end); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("recepción como profesional: %v", err)
	}
	if len(bookings.All) != 0 {
		t.Fatalf("no debe quedar ningún turno: %+v", bookings.All)
	}
	// El dueño-barbero sí entra.
	_ = mems.Save(domain.Membership{UserID: "barbero", SalonID: "s1", Role: domain.RoleOwner, Attends: true})
	b, err := svc.CreateBooking(rec, "s1", "barbero", "Lía", "", start, end)
	if err != nil {
		t.Fatalf("dueño-barbero: %v", err)
	}
	if b.ProfessionalID != "barbero" {
		t.Fatalf("turno del dueño-barbero: %+v", b)
	}
}

// S-F2a-4: el owner nace atendiendo (salón de una persona) y el resto del
// equipo nace sin atender salvo el profesional, que atiende por rol.
func TestTenants_CreateSalon_ownerAttends(t *testing.T) {
	salons, mems := &testutil.FakeSalons{}, &testutil.FakeMemberships{}
	svc := team.TenantService{Salons: salons, Memberships: mems}
	s, err := svc.CreateSalon("u-owner", "Navajas de Oro", "navajas-oro")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	owner, err := mems.Find("u-owner", s.ID)
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	if !owner.Attends || !owner.Bookable() {
		t.Fatalf("el owner de un salón nuevo atiende: %+v", owner)
	}
	if err := svc.AddMember(owner, "u-prof", s.ID, domain.RoleProfessional); err != nil {
		t.Fatalf("owner agrega profesional: %v", err)
	}
	if prof, _ := mems.Find("u-prof", s.ID); !prof.Attends || !prof.Bookable() {
		t.Fatalf("el profesional agregado atiende: %+v", prof)
	}
	if err := svc.AddMember(owner, "u-recep", s.ID, domain.RoleReception); err != nil {
		t.Fatalf("owner agrega recepción: %v", err)
	}
	if recep, _ := mems.Find("u-recep", s.ID); recep.Attends || recep.Bookable() {
		t.Fatalf("recepción no atiende: %+v", recep)
	}
}
