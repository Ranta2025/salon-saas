package bookings_test

import (
	"errors"
	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
	"testing"
	"time"
)

// FindByID: el scope por salón es la autorización del PATCH (turno de otro
// tenant = no existe).

// Update reemplaza la reserva por id (misma semántica que el UPDATE ... WHERE
// id AND salon_id de pgstore).

func TestBooking_create_ok_y_solape_409(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}
	rec := testutil.Recepcionista(mems)
	// B-12: solo agenda quien atiende en el salón.
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	testutil.Miembro(mems, "prof2", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	if _, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", day.Add(10*time.Hour), day.Add(11*time.Hour)); err != nil {
		t.Fatalf("primera reserva: %v", err)
	}
	// Mismo hueco -> 409 de dominio.
	_, err := svc.CreateBooking(rec, "s1", "prof1", "Ana", "", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("solape debe dar AlreadyExists, dio %v", err)
	}
	// Adyacente (11-12) sí entra.
	if _, err := svc.CreateBooking(rec, "s1", "prof1", "Ana", "", day.Add(11*time.Hour), day.Add(12*time.Hour)); err != nil {
		t.Fatalf("adyacente debe entrar: %v", err)
	}
	// Otro profesional, mismo hueco: entra.
	if _, err := svc.CreateBooking(rec, "s1", "prof2", "Eva", "", day.Add(10*time.Hour), day.Add(11*time.Hour)); err != nil {
		t.Fatalf("otro profesional debe entrar: %v", err)
	}
}

func TestBooking_permisos(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	// Profesional agenda para sí mismo: ok.
	_ = mems.Save(domain.Membership{UserID: "prof1", SalonID: "s1", Role: domain.RoleProfessional})
	barb, _ := mems.Find("prof1", "s1")
	if _, err := svc.CreateBooking(barb, "s1", "prof1", "Lía", "", day.Add(9*time.Hour), day.Add(10*time.Hour)); err != nil {
		t.Fatalf("profesional propio: %v", err)
	}
	// Profesional agenda para otro: 403.
	if _, err := svc.CreateBooking(barb, "s1", "prof2", "Lía", "", day.Add(9*time.Hour), day.Add(10*time.Hour)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional ajeno debe dar Forbidden, dio %v", err)
	}
	// Miembro de otro salón: 403.
	_ = mems.Save(domain.Membership{UserID: "otro", SalonID: "s2", Role: domain.RoleAdmin})
	otro, _ := mems.Find("otro", "s2")
	if _, err := svc.CreateBooking(otro, "s1", "prof1", "Lía", "", day.Add(9*time.Hour), day.Add(10*time.Hour)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
}

// TestBooking_idempotente_por_client_op: mismo contrato que las ventas.
func TestBooking_idempotente_por_client_op(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}
	rec := testutil.Recepcionista(mems)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC)
	start, end := day.Add(10*time.Hour), day.Add(11*time.Hour)

	first, err := svc.CreateBookingWithOp(rec, "s1", "prof1", "Lía", "", start, end, "op-b1")
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	second, err := svc.CreateBookingWithOp(rec, "s1", "prof1", "Lía", "", start, end, "op-b1")
	if err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("el reintento debe devolver la misma reserva: %q vs %q", first.ID, second.ID)
	}
	if len(bookings.All) != 1 {
		t.Fatalf("debe guardarse una sola vez, se guardó %d", len(bookings.All))
	}
}
