package httpapi

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de gastos y ganancia neta (F3b) para el contrato HTTP: misma semántica
// que pgstore (scope por salón, orden descendente, tope del listado) y datos
// sembrables desde el test.

type mExpenses struct {
	byID map[string]domain.Expense
}

func newMExpenses() *mExpenses { return &mExpenses{byID: map[string]domain.Expense{}} }

func (f *mExpenses) Save(e domain.Expense) error {
	f.byID[e.ID] = e
	return nil
}

func (f *mExpenses) ListRange(salonID, from, to string, limit int) ([]domain.Expense, bool, error) {
	out := []domain.Expense{}
	for _, e := range f.byID {
		if e.SalonID != salonID || e.SpentOn < from || e.SpentOn > to {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SpentOn != out[j].SpentOn {
			return out[i].SpentOn > out[j].SpentOn
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (f *mExpenses) FindByID(salonID, id string) (domain.Expense, error) {
	e, ok := f.byID[id]
	if !ok || e.SalonID != salonID {
		return domain.Expense{}, domain.ErrNotFound
	}
	return e, nil
}

func (f *mExpenses) Update(e domain.Expense) error {
	cur, ok := f.byID[e.ID]
	if !ok || cur.SalonID != e.SalonID {
		return domain.ErrNotFound
	}
	f.byID[e.ID] = e
	return nil
}

func (f *mExpenses) Delete(salonID, id string) error {
	e, ok := f.byID[id]
	if !ok || e.SalonID != salonID {
		return domain.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

var _ ports.ExpenseRepository = (*mExpenses)(nil)

// mProfit devuelve la foto sembrada y no filtra: el recorte por día del período
// lo hace el dominio (que es lo que se prueba en el handler).
type mProfit struct {
	data domain.ProfitData
}

func (f *mProfit) DailyProfit(salonID, timezone string, from, to time.Time) (domain.ProfitData, error) {
	return f.data, nil
}

var _ ports.ProfitRepository = (*mProfit)(nil)
