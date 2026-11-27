package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sort"
	"time"
)

// Fakes de la nómina (F3.2) para el contrato HTTP: misma semántica que pgstore
// (scope por salón, default de D3 sin fila, ErrNotFound para quien no es del
// salón) y datos sembrables desde el test.

type PayModels struct {
	Mems  *Memberships
	Users *Users
	Rows  map[string]domain.MemberPay
}

func PayRowKey(salonID, userID string) string { return salonID + "|" + userID }

// view arma el modelo con la persona; ok=false si no es miembro del salón.
func (f *PayModels) view(salonID, userID string) (domain.MemberPayView, bool) {
	m, err := f.Mems.FindMember(userID, salonID)
	if err != nil {
		return domain.MemberPayView{}, false
	}
	name := userID
	if u, err := f.Users.FindByID(userID); err == nil {
		name = u.Name
	}
	model, ok := f.Rows[PayRowKey(salonID, userID)]
	if !ok {
		model = domain.DefaultMemberPay(salonID, userID)
	}
	return domain.MemberPayView{
		MemberPay: model, Name: name, Role: m.Role, Active: m.Active(),
	}, true
}

func (f *PayModels) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	out := []domain.MemberPayView{}
	for _, m := range f.Mems.ByKey {
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

func (f *PayModels) FindPayModel(salonID, userID string) (domain.MemberPayView, error) {
	v, ok := f.view(salonID, userID)
	if !ok {
		return domain.MemberPayView{}, domain.ErrNotFound
	}
	return v, nil
}

func (f *PayModels) SavePayModel(p domain.MemberPay) error {
	if _, ok := f.view(p.SalonID, p.UserID); !ok {
		return domain.ErrNotFound
	}
	if f.Rows == nil {
		f.Rows = map[string]domain.MemberPay{}
	}
	f.Rows[PayRowKey(p.SalonID, p.UserID)] = p
	return nil
}

var _ ports.MemberPayRepository = (*PayModels)(nil)

type PayrollSales struct {
	// bySalon guarda el agregado por persona, como lo devuelve la query.
	BySalon map[string][]domain.PayrollSalesTotal
}

func (f *PayrollSales) Add(salonID string, t domain.PayrollSalesTotal) {
	if f.BySalon == nil {
		f.BySalon = map[string][]domain.PayrollSalesTotal{}
	}
	f.BySalon[salonID] = append(f.BySalon[salonID], t)
}

func (f *PayrollSales) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	return f.BySalon[salonID], nil
}

func (f *PayrollSales) CommissionTotalFor(salonID, userID string, from, to time.Time) (domain.PayrollSalesTotal, error) {
	for _, t := range f.BySalon[salonID] {
		if t.UserID == userID {
			return t, nil
		}
	}
	return domain.PayrollSalesTotal{UserID: userID}, nil
}

var _ ports.PayrollSalesRepository = (*PayrollSales)(nil)
