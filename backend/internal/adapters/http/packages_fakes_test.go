package httpapi

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de paquetes (F8) del adaptador HTTP, con la misma semántica que pgstore: scope por salón,
// nombre activo único sin importar mayúsculas y compra/consumo/ajuste como UNA
// "transacción" (todo o nada) bajo un mutex.

type mpkgCatalog struct {
	all      []domain.ServicePackage
	services *msvc
}

func (f *mpkgCatalog) nameTaken(salonID, name, ignoreID string) bool {
	for _, p := range f.all {
		if p.SalonID == salonID && p.Active && p.ID != ignoreID && strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (f *mpkgCatalog) Create(p domain.ServicePackage) error {
	if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
		return domain.ErrAlreadyExists
	}
	f.all = append(f.all, p)
	return nil
}

func (f *mpkgCatalog) Save(p domain.ServicePackage) error {
	for i := range f.all {
		if f.all[i].ID == p.ID && f.all[i].SalonID == p.SalonID {
			if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
				return domain.ErrAlreadyExists
			}
			f.all[i] = p
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *mpkgCatalog) FindByID(salonID, id string) (domain.ServicePackage, error) {
	for _, p := range f.all {
		if p.SalonID == salonID && p.ID == id {
			return p, nil
		}
	}
	return domain.ServicePackage{}, domain.ErrNotFound
}

func (f *mpkgCatalog) List(salonID string) ([]domain.ServicePackage, error) {
	out := make([]domain.ServicePackage, 0, len(f.all))
	for _, p := range f.all {
		if p.SalonID != salonID {
			continue
		}
		if svc, err := f.services.FindByID(salonID, p.ServiceID); err == nil {
			p.ServiceName = svc.Name
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if li != lj {
			return li < lj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

var _ ports.PackageRepository = (*mpkgCatalog)(nil)

type mpkgOwned struct {
	mu        sync.Mutex
	services  *msvc
	sales     []domain.Sale
	packages  []domain.ClientPackage
	movements []domain.PackageMovement
	// failPurchase simula una DB caída: la compra no escribe NADA.
	failPurchase error
	// beforePurchase corre dentro de Purchase, antes de mirar el índice único:
	// simula a OTRO pedido con el mismo client_op_id que gana la carrera.
	beforePurchase func()
	purchaseCalls  int
	// mirror es la tabla de ventas COMPARTIDA con el POS del entorno HTTP: la
	// compra del paquete es una venta más y el índice único de client_op_id es el
	// mismo para las dos (nil = sin espejo).
	mirror *msale
}

func (f *mpkgOwned) saleIndex(salonID, op string) int {
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

// mirrorHasOp dice si una venta del POS (no de un paquete) ya usó ese op id.
func (f *mpkgOwned) mirrorHasOp(salonID, op string) bool {
	if f.mirror == nil || op == "" {
		return false
	}
	for _, s := range f.mirror.all {
		if s.SalonID == salonID && s.ClientOpID == op {
			return true
		}
	}
	return false
}

func (f *mpkgOwned) Purchase(o domain.PackagePurchaseOrder) error {
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
	if f.saleIndex(o.Sale.SalonID, o.Sale.ClientOpID) >= 0 || f.mirrorHasOp(o.Sale.SalonID, o.Sale.ClientOpID) {
		return domain.ErrAlreadyExists
	}
	f.sales = append(f.sales, o.Sale)
	if f.mirror != nil {
		f.mirror.all = append(f.mirror.all, o.Sale)
	}
	f.packages = append(f.packages, o.ClientPackage)
	f.movements = append(f.movements, o.Movement)
	return nil
}

func (f *mpkgOwned) named(cp domain.ClientPackage) domain.ClientPackage {
	if svc, err := f.services.FindByID(cp.SalonID, cp.ServiceID); err == nil {
		cp.ServiceName = svc.Name
	}
	return cp
}

func (f *mpkgOwned) FindPurchaseByOp(salonID, op string) (domain.ClientPackage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.saleIndex(salonID, op)
	if i < 0 {
		if f.mirrorHasOp(salonID, op) {
			return domain.ClientPackage{}, domain.ErrAlreadyExists
		}
		return domain.ClientPackage{}, domain.ErrNotFound
	}
	for _, cp := range f.packages {
		if cp.SaleID == f.sales[i].ID {
			return f.named(cp), nil
		}
	}
	return domain.ClientPackage{}, domain.ErrAlreadyExists
}

func (f *mpkgOwned) find(salonID, clientID, id string) int {
	for i, cp := range f.packages {
		if cp.SalonID == salonID && cp.ClientID == clientID && cp.ID == id {
			return i
		}
	}
	return -1
}

func (f *mpkgOwned) Redeem(r domain.PackageRedeemRequest) (domain.ClientPackage, error) {
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

func (f *mpkgOwned) Adjust(a domain.PackageAdjustRequest) (domain.ClientPackage, error) {
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

func (f *mpkgOwned) ListByClient(salonID, clientID string, now time.Time) ([]domain.ClientPackage, error) {
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

func (f *mpkgOwned) ListMovements(salonID, clientID, id string) ([]domain.PackageMovement, error) {
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
func (f *mpkgOwned) sumDelta(id string) int {
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

var _ ports.ClientPackageRepository = (*mpkgOwned)(nil)

// newPackagesEnv rearma el router con los paquetes habilitados (el entorno del
// POS no los conoce: no se tocan los tests existentes).
func newPackagesEnv() (*posEnv, *mpkgCatalog, *mpkgOwned) {
	env := newPOSEnv()
	catalog := &mpkgCatalog{services: env.services}
	owned := &mpkgOwned{services: env.services, mirror: env.sales}
	svc := app.PackageService{
		Packages: catalog, ClientPackages: owned, Services: env.services, Clients: env.clients,
		Memberships: env.mems, Settings: env.settings, Events: env.events,
	}
	env.d.Packages = PackageHandler{Packages: svc, Memberships: env.mems}
	env.r = gin.New()
	RegisterRoutes(env.r, env.d)
	return env, catalog, owned
}
