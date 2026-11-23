package httpapi

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de la nómina (F3.2) para el contrato HTTP: misma semántica que pgstore
// (scope por salón, default de D3 sin fila, ErrNotFound para quien no es del
// salón) y datos sembrables desde el test.

type mPayModels struct {
	mems  *mm
	users *mu
	rows  map[string]domain.MemberPay
}

func payRowKey(salonID, userID string) string { return salonID + "|" + userID }

// view arma el modelo con la persona; ok=false si no es miembro del salón.
func (f *mPayModels) view(salonID, userID string) (domain.MemberPayView, bool) {
	m, err := f.mems.FindMember(userID, salonID)
	if err != nil {
		return domain.MemberPayView{}, false
	}
	name := userID
	if u, err := f.users.FindByID(userID); err == nil {
		name = u.Name
	}
	model, ok := f.rows[payRowKey(salonID, userID)]
	if !ok {
		model = domain.DefaultMemberPay(salonID, userID)
	}
	return domain.MemberPayView{
		MemberPay: model, Name: name, Role: m.Role, Active: m.Active(),
	}, true
}

func (f *mPayModels) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	out := []domain.MemberPayView{}
	for _, m := range f.mems.byKey {
		if m.SalonID != salonID {
			continue
		}
		if v, ok := f.view(salonID, m.UserID); ok {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UserID < out[j].UserID
	})
	return out, nil
}

func (f *mPayModels) FindPayModel(salonID, userID string) (domain.MemberPayView, error) {
	v, ok := f.view(salonID, userID)
	if !ok {
		return domain.MemberPayView{}, domain.ErrNotFound
	}
	return v, nil
}

func (f *mPayModels) SavePayModel(p domain.MemberPay) error {
	if _, ok := f.view(p.SalonID, p.UserID); !ok {
		return domain.ErrNotFound
	}
	if f.rows == nil {
		f.rows = map[string]domain.MemberPay{}
	}
	f.rows[payRowKey(p.SalonID, p.UserID)] = p
	return nil
}

var _ ports.MemberPayRepository = (*mPayModels)(nil)

type mPayrollSales struct {
	// bySalon guarda el agregado por persona, como lo devuelve la query.
	bySalon map[string][]domain.PayrollSalesTotal
}

func (f *mPayrollSales) add(salonID string, t domain.PayrollSalesTotal) {
	if f.bySalon == nil {
		f.bySalon = map[string][]domain.PayrollSalesTotal{}
	}
	f.bySalon[salonID] = append(f.bySalon[salonID], t)
}

func (f *mPayrollSales) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	return f.bySalon[salonID], nil
}

func (f *mPayrollSales) CommissionTotalFor(salonID, userID string, from, to time.Time) (domain.PayrollSalesTotal, error) {
	for _, t := range f.bySalon[salonID] {
		if t.UserID == userID {
			return t, nil
		}
	}
	return domain.PayrollSalesTotal{UserID: userID}, nil
}

var _ ports.PayrollSalesRepository = (*mPayrollSales)(nil)
