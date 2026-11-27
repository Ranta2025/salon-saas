package pos

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de los repositorios de POS/catálogo: misma semántica que pgstore
// (scoping por salón, ErrNotFound, upsert por id) sin Postgres.
// Para unit tests de los casos de uso alcanzan: si el caso de uso dependiera
// de detalles de SQL, estos fakes no compilarían contra el puerto.

type fakeSales struct {
	All []domain.Sale
	Top []domain.TopService
}

func (f *fakeSales) Save(s domain.Sale) error {
	f.All = append(f.All, s)
	return nil
}
func (f *fakeSales) FindByClientOpID(salonID, clientOpID string) (domain.Sale, error) {
	for _, s := range f.All {
		if s.SalonID == salonID && s.ClientOpID != "" && s.ClientOpID == clientOpID {
			return s, nil
		}
	}
	return domain.Sale{}, domain.ErrNotFound
}

func (f *fakeSales) ListRange(salonID string, from, to time.Time) ([]domain.Sale, error) {
	var out []domain.Sale
	for _, s := range f.All {
		if s.SalonID != salonID || s.CreatedAt.Before(from) || !s.CreatedAt.Before(to) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// SumCents con method == "" suma todos los métodos.
func (f *fakeSales) SumCents(salonID string, from, to time.Time, method domain.PaymentMethod) (int, error) {
	total := 0
	for _, s := range f.All {
		if s.SalonID != salonID || s.CreatedAt.Before(from) || !s.CreatedAt.Before(to) {
			continue
		}
		if method != "" && s.Method != method {
			continue
		}
		total += s.TotalCents
	}
	return total, nil
}

func (f *fakeSales) TopServices(string, time.Time, time.Time, int) ([]domain.TopService, error) {
	return f.Top, nil
}

var _ ports.SaleRepository = (*fakeSales)(nil)

type fakeAgenda struct {
	bookings []domain.Booking
	lastFrom time.Time
	lastTo   time.Time
}

func (f *fakeAgenda) StatsRange(salonID string, from, to time.Time) (int, int, error) {
	f.lastFrom, f.lastTo = from, to
	count, minutes := 0, 0
	for _, b := range f.bookings {
		if b.SalonID != salonID || b.StartsAt.Before(from) || !b.StartsAt.Before(to) {
			continue
		}
		if b.Status != domain.BookingBooked && b.Status != domain.BookingDone {
			continue
		}
		count++
		minutes += int(b.EndsAt.Sub(b.StartsAt).Minutes())
	}
	return count, minutes, nil
}

var _ ports.AgendaStats = (*fakeAgenda)(nil)

type fakeStaff struct{ count int }

func (f fakeStaff) CountProfessionals(string) (int, error) { return f.count, nil }

var _ ports.StaffCounter = fakeStaff{}
