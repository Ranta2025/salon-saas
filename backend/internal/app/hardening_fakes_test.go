package app

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// countingHasher cuenta los compares reales y los falsos (B-05: sin usuario en
// la DB, igual se gasta el trabajo de bcrypt).
type countingHasher struct {
	fakeHasher
	dummyCalls int
}

func (h *countingHasher) DummyCompare(string) bool {
	h.dummyCalls++
	return false
}

// fakeSalonTx simula la transacción con lock por salón: delega en los mismos
// fakes que el camino sin transacción y anota el lock tomado.
type fakeSalonTx struct {
	closes      *fakeCashCloses
	sales       *fakeSales
	locks       int
	lastSalonID string
}

type fakeCashTx struct{ parent *fakeSalonTx }

func (t fakeCashTx) Save(c domain.CashClose) error { return t.parent.closes.Save(c) }
func (t fakeCashTx) Last(salonID string) (domain.CashClose, error) {
	return t.parent.closes.Last(salonID)
}
func (t fakeCashTx) List(salonID string) ([]domain.CashClose, error) {
	return t.parent.closes.List(salonID)
}
func (t fakeCashTx) SumCashCents(salonID string, from, to time.Time) (int, error) {
	return t.parent.sales.SumCents(salonID, from, to, domain.MethodCash)
}

func (t *fakeSalonTx) InSalonTx(salonID string, fn func(ports.CashCloseTx) error) error {
	t.locks++
	t.lastSalonID = salonID
	return fn(fakeCashTx{parent: t})
}

var _ ports.SalonTxRunner = (*fakeSalonTx)(nil)

// fakeStaffLister: personal por salón en memoria.
type fakeStaffLister struct {
	BySalon map[string][]domain.StaffMember
}

func (f *fakeStaffLister) ListStaff(salonID string) ([]domain.StaffMember, error) {
	return f.BySalon[salonID], nil
}

var _ ports.StaffLister = (*fakeStaffLister)(nil)
