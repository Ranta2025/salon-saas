package app

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de los repositorios de POS/catálogo: misma semántica que pgstore
// (scoping por salón, ErrNotFound, upsert por id) sin Postgres.
// Para unit tests de los casos de uso alcanzan: si el caso de uso dependiera
// de detalles de SQL, estos fakes no compilarían contra el puerto.

type fakeServices struct{ all []domain.Service }

func (f *fakeServices) Save(s domain.Service) error {
	for i := range f.all {
		if f.all[i].ID == s.ID {
			f.all[i] = s
			return nil
		}
	}
	f.all = append(f.all, s)
	return nil
}

func (f *fakeServices) FindByID(salonID, id string) (domain.Service, error) {
	for _, s := range f.all {
		if s.SalonID == salonID && s.ID == id {
			return s, nil
		}
	}
	return domain.Service{}, domain.ErrNotFound
}

func (f *fakeServices) List(salonID string, includeInactive bool) ([]domain.Service, error) {
	var out []domain.Service
	for _, s := range f.all {
		if s.SalonID != salonID {
			continue
		}
		if !includeInactive && !s.Active {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

var _ ports.ServiceRepository = (*fakeServices)(nil)

type fakeSales struct {
	all []domain.Sale
	top []domain.TopService
}

func (f *fakeSales) Save(s domain.Sale) error {
	f.all = append(f.all, s)
	return nil
}
func (f *fakeSales) FindByClientOpID(salonID, clientOpID string) (domain.Sale, error) {
	for _, s := range f.all {
		if s.SalonID == salonID && s.ClientOpID != "" && s.ClientOpID == clientOpID {
			return s, nil
		}
	}
	return domain.Sale{}, domain.ErrNotFound
}

func (f *fakeSales) ListRange(salonID string, from, to time.Time) ([]domain.Sale, error) {
	var out []domain.Sale
	for _, s := range f.all {
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
	for _, s := range f.all {
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
	return f.top, nil
}

var _ ports.SaleRepository = (*fakeSales)(nil)

type fakeCashCloses struct{ all []domain.CashClose }

func (f *fakeCashCloses) Save(c domain.CashClose) error {
	f.all = append(f.all, c)
	return nil
}

func (f *fakeCashCloses) Last(salonID string) (domain.CashClose, error) {
	var last domain.CashClose
	found := false
	for _, c := range f.all {
		if c.SalonID != salonID {
			continue
		}
		if !found || c.ClosedAt.After(last.ClosedAt) {
			last, found = c, true
		}
	}
	if !found {
		return domain.CashClose{}, domain.ErrNotFound
	}
	return last, nil
}

func (f *fakeCashCloses) List(salonID string) ([]domain.CashClose, error) {
	var out []domain.CashClose
	for _, c := range f.all {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

var _ ports.CashCloseRepository = (*fakeCashCloses)(nil)

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

// miembro siembra y devuelve la membership del actor (lo que el handler ya cargó).
func miembro(m *fakeMemberships, userID, salonID string, role domain.Role) domain.Membership {
	_ = m.Save(domain.Membership{UserID: userID, SalonID: salonID, Role: role})
	got, err := m.Find(userID, salonID)
	if err != nil {
		panic(err)
	}
	return got
}
