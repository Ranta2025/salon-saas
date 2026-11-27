package packages

import (
	"sort"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil"
)

// Fakes de paquetes (F8) con la misma semántica que pgstore: scope por salón,
// nombre activo único sin importar mayúsculas y compra/consumo/ajuste como UNA
// "transacción" (todo o nada) bajo un mutex.

type fakeClientPackages struct {
	mu        sync.Mutex
	services  *testutil.FakeServices
	sales     []domain.Sale
	packages  []domain.ClientPackage
	movements []domain.PackageMovement
	// failPurchase simula una DB caída: la compra no escribe NADA.
	failPurchase error
	// beforePurchase corre dentro de Purchase, antes de mirar el índice único:
	// simula a OTRO pedido con el mismo client_op_id que gana la carrera.
	beforePurchase func()
	purchaseCalls  int
}

func (f *fakeClientPackages) saleIndex(salonID, op string) int {
	if op == "" {
		return -1
	}
	for i, s := range f.sales {
		if s.SalonID == salonID && s.ClientOpID == op {
			return i
		}
	}
	return -1
}

func (f *fakeClientPackages) Purchase(o domain.PackagePurchaseOrder) error {
	f.purchaseCalls++
	if f.beforePurchase != nil {
		hook := f.beforePurchase
		f.beforePurchase = nil
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPurchase != nil {
		return f.failPurchase
	}
	if f.saleIndex(o.Sale.SalonID, o.Sale.ClientOpID) >= 0 {
		return domain.ErrAlreadyExists
	}
	f.sales = append(f.sales, o.Sale)
	f.packages = append(f.packages, o.ClientPackage)
	f.movements = append(f.movements, o.Movement)
	return nil
}

func (f *fakeClientPackages) named(cp domain.ClientPackage) domain.ClientPackage {
	if svc, err := f.services.FindByID(cp.SalonID, cp.ServiceID); err == nil {
		cp.ServiceName = svc.Name
	}
	return cp
}

func (f *fakeClientPackages) FindPurchaseByOp(salonID, op string) (domain.ClientPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.saleIndex(salonID, op)
	if i < 0 {
		return domain.ClientPackage{}, domain.ErrNotFound
	}
	for _, cp := range f.packages {
		if cp.SaleID == f.sales[i].ID {
			return f.named(cp), nil
		}
	}
	return domain.ClientPackage{}, domain.ErrAlreadyExists
}

func (f *fakeClientPackages) find(salonID, clientID, id string) int {
	for i, cp := range f.packages {
		if cp.SalonID == salonID && cp.ClientID == clientID && cp.ID == id {
			return i
		}
	}
	return -1
}

func (f *fakeClientPackages) Redeem(r domain.PackageRedeemRequest) (domain.ClientPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.find(r.SalonID, r.ClientID, r.ClientPackageID)
	if r.ClientOpID != "" {
		for _, m := range f.movements {
			if m.SalonID == r.SalonID && m.ClientOpID == r.ClientOpID {
				if i < 0 || m.ClientPackageID != f.packages[i].ID {
					return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageOpReused)
				}
				return f.named(f.packages[i]), nil
			}
		}
	}
	if i < 0 {
		return domain.ClientPackage{}, domain.ErrNotFound
	}
	cp := &f.packages[i]
	if cp.SessionsLeft < 1 {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageNoSessions)
	}
	if cp.ExpiredAt(r.Now) {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageExpired)
	}
	cp.SessionsLeft--
	f.movements = append(f.movements, domain.PackageMovement{
		ID: domain.NewID(), SalonID: r.SalonID, ClientPackageID: cp.ID, Delta: -1,
		Reason: domain.PackageRedeem, Note: r.Note, ProfessionalID: r.ProfessionalID,
		ClientOpID: r.ClientOpID, Actor: r.Actor, CreatedAt: r.Now,
	})
	return f.named(*cp), nil
}

func (f *fakeClientPackages) Adjust(a domain.PackageAdjustRequest) (domain.ClientPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.find(a.SalonID, a.ClientID, a.ClientPackageID)
	if i < 0 {
		return domain.ClientPackage{}, domain.ErrNotFound
	}
	cp := &f.packages[i]
	after := cp.SessionsLeft + a.Delta
	if after < 0 || after > cp.SessionsTotal {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageAdjustRange)
	}
	cp.SessionsLeft = after
	f.movements = append(f.movements, domain.PackageMovement{
		ID: domain.NewID(), SalonID: a.SalonID, ClientPackageID: cp.ID, Delta: a.Delta,
		Reason: domain.PackageAdjust, Note: a.Note, Actor: a.Actor, CreatedAt: a.Now,
	})
	return f.named(*cp), nil
}

func (f *fakeClientPackages) ListByClient(salonID, clientID string, now time.Time) ([]domain.ClientPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ClientPackage
	for _, cp := range f.packages {
		if cp.SalonID == salonID && cp.ClientID == clientID {
			out = append(out, f.named(cp))
		}
	}
	live := func(c domain.ClientPackage) bool { return c.SessionsLeft > 0 && !c.ExpiredAt(now) }
	sort.SliceStable(out, func(i, j int) bool {
		if live(out[i]) != live(out[j]) {
			return live(out[i])
		}
		return out[i].PurchasedAt.After(out[j].PurchasedAt)
	})
	if len(out) > domain.MaxPackageListed {
		out = out[:domain.MaxPackageListed]
	}
	return out, nil
}

func (f *fakeClientPackages) ListMovements(salonID, clientID, id string) ([]domain.PackageMovement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.find(salonID, clientID, id) < 0 {
		return nil, domain.ErrNotFound
	}
	var out []domain.PackageMovement
	for i := len(f.movements) - 1; i >= 0; i-- {
		if f.movements[i].ClientPackageID == id && f.movements[i].SalonID == salonID {
			out = append(out, f.movements[i])
		}
	}
	return out, nil
}

// sumDelta es el Σ delta del libro de un paquete (el invariante D4).
func (f *fakeClientPackages) sumDelta(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.movements {
		if m.ClientPackageID == id {
			n += m.Delta
		}
	}
	return n
}

var _ ports.ClientPackageRepository = (*fakeClientPackages)(nil)

// pint arma el puntero de una vigencia opcional.
func pint(v int) *int { return &v }
