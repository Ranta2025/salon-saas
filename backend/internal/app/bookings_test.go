package app

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type fb struct{ all []domain.Booking }

func (f *fb) Save(b domain.Booking) error {
	f.all = append(f.all, b)
	return nil
}
func (f *fb) FindOverlapping(salonID, barberID string, start, end time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	probe := domain.Booking{SalonID: salonID, BarberID: barberID, StartsAt: start, EndsAt: end, Status: domain.BookingBooked}
	for _, b := range f.all {
		if b.Status == domain.BookingBooked && b.Overlaps(probe) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fb) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	for _, b := range f.all {
		if b.SalonID == salonID && !b.StartsAt.Before(from) && b.StartsAt.Before(to) {
			out = append(out, b)
		}
	}
	return out, nil
}

var _ ports.BookingRepository = (*fb)(nil)

func recepcionista(m *fakeMemberships) domain.Membership {
	_ = m.Save(domain.Membership{UserID: "recep", SalonID: "s1", Role: domain.RoleReception})
	mm, _ := m.Find("recep", "s1")
	return mm
}

func TestBooking_create_ok_y_solape_409(t *testing.T) {
	bookings := &fb{}
	mems := &fakeMemberships{}
	svc := BookingService{Bookings: bookings, Memberships: mems}
	rec := recepcionista(mems)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	if _, err := svc.CreateBooking(rec, "s1", "barb1", "Lía", "555", day.Add(10*time.Hour), day.Add(11*time.Hour)); err != nil {
		t.Fatalf("primera reserva: %v", err)
	}
	// Mismo hueco -> 409 de dominio.
	_, err := svc.CreateBooking(rec, "s1", "barb1", "Ana", "", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("solape debe dar AlreadyExists, dio %v", err)
	}
	// Adyacente (11-12) sí entra.
	if _, err := svc.CreateBooking(rec, "s1", "barb1", "Ana", "", day.Add(11*time.Hour), day.Add(12*time.Hour)); err != nil {
		t.Fatalf("adyacente debe entrar: %v", err)
	}
	// Otro barbero, mismo hueco: entra.
	if _, err := svc.CreateBooking(rec, "s1", "barb2", "Eva", "", day.Add(10*time.Hour), day.Add(11*time.Hour)); err != nil {
		t.Fatalf("otro barbero debe entrar: %v", err)
	}
}

func TestBooking_permisos(t *testing.T) {
	bookings := &fb{}
	mems := &fakeMemberships{}
	svc := BookingService{Bookings: bookings, Memberships: mems}
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	// Barbero agenda para sí mismo: ok.
	_ = mems.Save(domain.Membership{UserID: "barb1", SalonID: "s1", Role: domain.RoleBarber})
	barb, _ := mems.Find("barb1", "s1")
	if _, err := svc.CreateBooking(barb, "s1", "barb1", "Lía", "", day.Add(9*time.Hour), day.Add(10*time.Hour)); err != nil {
		t.Fatalf("barbero propio: %v", err)
	}
	// Barbero agenda para otro: 403.
	if _, err := svc.CreateBooking(barb, "s1", "barb2", "Lía", "", day.Add(9*time.Hour), day.Add(10*time.Hour)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("barbero ajeno debe dar Forbidden, dio %v", err)
	}
	// Miembro de otro salón: 403.
	_ = mems.Save(domain.Membership{UserID: "otro", SalonID: "s2", Role: domain.RoleAdmin})
	otro, _ := mems.Find("otro", "s2")
	if _, err := svc.CreateBooking(otro, "s1", "barb1", "Lía", "", day.Add(9*time.Hour), day.Add(10*time.Hour)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
}
