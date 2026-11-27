package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sort"
	"time"
)

// Fakes de gastos y ganancia neta (F3b) para el contrato HTTP: misma semántica
// que pgstore (scope por salón, orden descendente, tope del listado) y datos
// sembrables desde el test.

type Expenses struct {
	ByID map[string]domain.Expense
}

func NewExpenses() *Expenses { return &Expenses{ByID: map[string]domain.Expense{}} }

func (f *Expenses) Save(e domain.Expense) error {
	f.ByID[e.ID] = e
	return nil
}

func (f *Expenses) ListRange(salonID, from, to string, limit int) ([]domain.Expense, bool, error) {
	out := []domain.Expense{}
	for _, e := range f.ByID {
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

func (f *Expenses) FindByID(salonID, id string) (domain.Expense, error) {
	e, ok := f.ByID[id]
	if !ok || e.SalonID != salonID {
		return domain.Expense{}, domain.ErrNotFound
	}
	return e, nil
}

func (f *Expenses) Update(e domain.Expense) error {
	cur, ok := f.ByID[e.ID]
	if !ok || cur.SalonID != e.SalonID {
		return domain.ErrNotFound
	}
	f.ByID[e.ID] = e
	return nil
}

func (f *Expenses) Delete(salonID, id string) error {
	e, ok := f.ByID[id]
	if !ok || e.SalonID != salonID {
		return domain.ErrNotFound
	}
	delete(f.ByID, id)
	return nil
}

var _ ports.ExpenseRepository = (*Expenses)(nil)

// Profit devuelve la foto sembrada y no filtra: el recorte por día del período
// lo hace el dominio (que es lo que se prueba en el handler).
type Profit struct {
	Data domain.ProfitData
}

func (f *Profit) DailyProfit(salonID, timezone string, from, to time.Time) (domain.ProfitData, error) {
	return f.Data, nil
}

var _ ports.ProfitRepository = (*Profit)(nil)
