package testutil

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeSales es el repositorio de ventas en memoria: índice por client_op_id
// (idempotencia), rango por fecha de creación, suma por método y el top de
// servicios (que el test fija a mano).
type FakeSales struct {
	All []domain.Sale
	Top []domain.TopService
}

func (f *FakeSales) Save(s domain.Sale) error { f.All = append(f.All, s); return nil }

func (f *FakeSales) FindByClientOpID(salonID, clientOpID string) (domain.Sale, error) {
	for _, s := range f.All {
		if s.SalonID == salonID && s.ClientOpID != "" && s.ClientOpID == clientOpID {
			return s, nil
		}
	}
	return domain.Sale{}, domain.ErrNotFound
}

func (f *FakeSales) ListRange(salonID string, from, to time.Time) ([]domain.Sale, error) {
	var out []domain.Sale
	for _, s := range f.All {
		if s.SalonID != salonID || s.CreatedAt.Before(from) || !s.CreatedAt.Before(to) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *FakeSales) SumCents(salonID string, from, to time.Time, method domain.PaymentMethod) (int, error) {
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

func (f *FakeSales) TopServices(string, time.Time, time.Time, int) ([]domain.TopService, error) {
	return f.Top, nil
}

var _ ports.SaleRepository = (*FakeSales)(nil)

// FakeCashCloses es el libro de cierres de caja en memoria: el último por fecha
// y el listado del salón.
type FakeCashCloses struct{ All []domain.CashClose }

func (f *FakeCashCloses) Save(c domain.CashClose) error { f.All = append(f.All, c); return nil }

func (f *FakeCashCloses) Last(salonID string) (domain.CashClose, error) {
	var last domain.CashClose
	found := false
	for _, c := range f.All {
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

func (f *FakeCashCloses) List(salonID string) ([]domain.CashClose, error) {
	var out []domain.CashClose
	for _, c := range f.All {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

var _ ports.CashCloseRepository = (*FakeCashCloses)(nil)
