package testutil

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeBookings es el repositorio de reservas en memoria (la copia exportada del
// `fb` de los tests del nudo de reservas).
type FakeBookings struct{ All []domain.Booking }

func (f *FakeBookings) Save(b domain.Booking) error {
	f.All = append(f.All, b)
	return nil
}
func (f *FakeBookings) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	for _, b := range f.All {
		if b.SalonID == salonID && b.ClientOpID != "" && b.ClientOpID == clientOpID {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}
func (f *FakeBookings) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	probe := domain.Booking{SalonID: salonID, ProfessionalID: professionalID, StartsAt: start, EndsAt: end, Status: domain.BookingBooked}
	for _, b := range f.All {
		if b.Status == domain.BookingBooked && b.Overlaps(probe) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *FakeBookings) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
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
func (f *FakeBookings) FindByID(salonID, id string) (domain.Booking, error) {
	for _, b := range f.All {
		if b.SalonID == salonID && b.ID == id {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}

// Update reemplaza la reserva por id (misma semántica que el UPDATE ... WHERE
// id AND salon_id de pgstore).
func (f *FakeBookings) Update(b domain.Booking) error {
	for i := range f.All {
		if f.All[i].ID == b.ID && f.All[i].SalonID == b.SalonID {
			f.All[i] = b
			return nil
		}
	}
	return domain.ErrNotFound
}

var _ ports.BookingRepository = (*FakeBookings)(nil)
