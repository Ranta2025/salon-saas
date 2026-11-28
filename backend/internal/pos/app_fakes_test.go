package pos_test

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil"
)

// fakeSalonTx simula la transacción con lock por salón: delega en los mismos
// fakes que el camino sin transacción y anota el lock tomado.
type fakeSalonTx struct {
	closes      *testutil.FakeCashCloses
	sales       *testutil.FakeSales
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
