package app

import (
	"errors"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type fb struct{ All []domain.Booking }

func (f *fb) Save(b domain.Booking) error {
	f.All = append(f.All, b)
	return nil
}
func (f *fb) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	for _, b := range f.All {
		if b.SalonID == salonID && b.ClientOpID != "" && b.ClientOpID == clientOpID {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}
func (f *fb) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	probe := domain.Booking{SalonID: salonID, ProfessionalID: professionalID, StartsAt: start, EndsAt: end, Status: domain.BookingBooked}
	for _, b := range f.All {
		if b.Status == domain.BookingBooked && b.Overlaps(probe) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fb) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	for _, b := range f.All {
		if b.SalonID == salonID && !b.StartsAt.Before(from) && b.StartsAt.Before(to) {
			out = append(out, b)
		}
	}
	return out, nil
}

// FindByID: el scope por salón es la autorización del PATCH (turno de otro
// tenant = no existe).
func (f *fb) FindByID(salonID, id string) (domain.Booking, error) {
	for _, b := range f.All {
		if b.SalonID == salonID && b.ID == id {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}

// Update reemplaza la reserva por id (misma semántica que el UPDATE ... WHERE
// id AND salon_id de pgstore).
func (f *fb) Update(b domain.Booking) error {
	for i := range f.All {
		if f.All[i].ID == b.ID && f.All[i].SalonID == b.SalonID {
			f.All[i] = b
			return nil
		}
	}
	return domain.ErrNotFound
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
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}
	rec := recepcionista(mems)
	// B-12: solo agenda quien atiende en el salón.
	miembro(mems, "prof1", "s1", domain.RoleProfessional)
	miembro(mems, "prof2", "s1", domain.RoleProfessional)
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
	bookings := &fb{}
	mems := &fakeMemberships{}
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
	bookings := &fb{}
	mems := &fakeMemberships{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}
	rec := recepcionista(mems)
	miembro(mems, "prof1", "s1", domain.RoleProfessional)
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
