package httpkit

import (
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/packages"
	"salon-saas/internal/ports"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Fakes de paquetes (F8) del adaptador HTTP, con la misma semántica que pgstore: scope por salón,
// nombre activo único sin importar mayúsculas y compra/consumo/ajuste como UNA
// "transacción" (todo o nada) bajo un mutex.

type PkgCatalog struct {
	All      []domain.ServicePackage
	Services *Services
}

func (f *PkgCatalog) nameTaken(salonID, name, ignoreID string) bool {
	for _, p := range f.All {
		if p.SalonID == salonID && p.Active && p.ID != ignoreID && strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (f *PkgCatalog) Create(p domain.ServicePackage) error {
	if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
		return domain.ErrAlreadyExists
	}
	f.All = append(f.All, p)
	return nil
}

func (f *PkgCatalog) Save(p domain.ServicePackage) error {
	for i := range f.All {
		if f.All[i].ID == p.ID && f.All[i].SalonID == p.SalonID {
			if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
				return domain.ErrAlreadyExists
			}
			f.All[i] = p
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *PkgCatalog) FindByID(salonID, id string) (domain.ServicePackage, error) {
	for _, p := range f.All {
		if p.SalonID == salonID && p.ID == id {
			return p, nil
		}
	}
	return domain.ServicePackage{}, domain.ErrNotFound
}

func (f *PkgCatalog) List(salonID string) ([]domain.ServicePackage, error) {
	out := make([]domain.ServicePackage, 0, len(f.All))
	for _, p := range f.All {
		if p.SalonID != salonID {
			continue
		}
		if svc, err := f.Services.FindByID(salonID, p.ServiceID); err == nil {
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

var _ ports.PackageRepository = (*PkgCatalog)(nil)

type PkgOwned struct {
	Mu        sync.Mutex
	Services  *Services
	Sales     []domain.Sale
	Packages  []domain.ClientPackage
	Movements []domain.PackageMovement
	// failPurchase simula una DB caída: la compra no escribe NADA.
	FailPurchase error
	// beforePurchase corre dentro de Purchase, antes de mirar el índice único:
	// simula a OTRO pedido con el mismo client_op_id que gana la carrera.
	BeforePurchase func()
	PurchaseCalls  int
	// mirror es la tabla de ventas COMPARTIDA con el POS del entorno HTTP: la
	// compra del paquete es una venta más y el índice único de client_op_id es el
	// mismo para las dos (nil = sin espejo).
	Mirror *Sales
}

func (f *PkgOwned) saleIndex(salonID, op string) int {
	if op == "" {
		return -1
	}
	for i, s := range f.Sales {
		if s.SalonID == salonID && s.ClientOpID == op {
			return i
		}
	}
	return -1
}

// mirrorHasOp dice si una venta del POS (no de un paquete) ya usó ese op id.
func (f *PkgOwned) mirrorHasOp(salonID, op string) bool {
	if f.Mirror == nil || op == "" {
		return false
	}
	for _, s := range f.Mirror.All {
		if s.SalonID == salonID && s.ClientOpID == op {
			return true
		}
	}
	return false
}

func (f *PkgOwned) Purchase(o domain.PackagePurchaseOrder) error {
	f.PurchaseCalls++
	if f.BeforePurchase != nil {
		hook := f.BeforePurchase
		f.BeforePurchase = nil
		hook()
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.FailPurchase != nil {
		return f.FailPurchase
	}
	if f.saleIndex(o.Sale.SalonID, o.Sale.ClientOpID) >= 0 || f.mirrorHasOp(o.Sale.SalonID, o.Sale.ClientOpID) {
		return domain.ErrAlreadyExists
	}
	f.Sales = append(f.Sales, o.Sale)
	if f.Mirror != nil {
		f.Mirror.All = append(f.Mirror.All, o.Sale)
	}
	f.Packages = append(f.Packages, o.ClientPackage)
	f.Movements = append(f.Movements, o.Movement)
	return nil
}

func (f *PkgOwned) named(cp domain.ClientPackage) domain.ClientPackage {
	if svc, err := f.Services.FindByID(cp.SalonID, cp.ServiceID); err == nil {
		cp.ServiceName = svc.Name
	}
	return cp
}

func (f *PkgOwned) FindPurchaseByOp(salonID, op string) (domain.ClientPackage, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	i := f.saleIndex(salonID, op)
	if i < 0 {
		if f.mirrorHasOp(salonID, op) {
			return domain.ClientPackage{}, domain.ErrAlreadyExists
		}
		return domain.ClientPackage{}, domain.ErrNotFound
	}
	for _, cp := range f.Packages {
		if cp.SaleID == f.Sales[i].ID {
			return f.named(cp), nil
		}
	}
	return domain.ClientPackage{}, domain.ErrAlreadyExists
}

func (f *PkgOwned) find(salonID, clientID, id string) int {
	for i, cp := range f.Packages {
		if cp.SalonID == salonID && cp.ClientID == clientID && cp.ID == id {
			return i
		}
	}
	return -1
}

func (f *PkgOwned) Redeem(r domain.PackageRedeemRequest) (domain.ClientPackage, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	i := f.find(r.SalonID, r.ClientID, r.ClientPackageID)
	if r.ClientOpID != "" {
		for _, m := range f.Movements {
			if m.SalonID == r.SalonID && m.ClientOpID == r.ClientOpID {
				if i < 0 || m.ClientPackageID != f.Packages[i].ID {
					return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageOpReused)
				}
				return f.named(f.Packages[i]), nil
			}
		}
	}
	if i < 0 {
		return domain.ClientPackage{}, domain.ErrNotFound
	}
	cp := &f.Packages[i]
	if cp.SessionsLeft < 1 {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageNoSessions)
	}
	if cp.ExpiredAt(r.Now) {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageExpired)
	}
	cp.SessionsLeft--
	f.Movements = append(f.Movements, domain.PackageMovement{
		ID: domain.NewID(), SalonID: r.SalonID, ClientPackageID: cp.ID, Delta: -1,
		Reason: domain.PackageRedeem, Note: r.Note, ProfessionalID: r.ProfessionalID,
		ClientOpID: r.ClientOpID, Actor: r.Actor, CreatedAt: r.Now,
	})
	return f.named(*cp), nil
}

func (f *PkgOwned) Adjust(a domain.PackageAdjustRequest) (domain.ClientPackage, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	i := f.find(a.SalonID, a.ClientID, a.ClientPackageID)
	if i < 0 {
		return domain.ClientPackage{}, domain.ErrNotFound
	}
	cp := &f.Packages[i]
	after := cp.SessionsLeft + a.Delta
	if after < 0 || after > cp.SessionsTotal {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageAdjustRange)
	}
	cp.SessionsLeft = after
	f.Movements = append(f.Movements, domain.PackageMovement{
		ID: domain.NewID(), SalonID: a.SalonID, ClientPackageID: cp.ID, Delta: a.Delta,
		Reason: domain.PackageAdjust, Note: a.Note, Actor: a.Actor, CreatedAt: a.Now,
	})
	return f.named(*cp), nil
}

func (f *PkgOwned) ListByClient(salonID, clientID string, now time.Time) ([]domain.ClientPackage, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []domain.ClientPackage
	for _, cp := range f.Packages {
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

func (f *PkgOwned) ListMovements(salonID, clientID, id string) ([]domain.PackageMovement, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.find(salonID, clientID, id) < 0 {
		return nil, domain.ErrNotFound
	}
	var out []domain.PackageMovement
	for i := len(f.Movements) - 1; i >= 0; i-- {
		if f.Movements[i].ClientPackageID == id && f.Movements[i].SalonID == salonID {
			out = append(out, f.Movements[i])
		}
	}
	return out, nil
}

// sumDelta es el Σ delta del libro de un paquete (el invariante D4).
func (f *PkgOwned) SumDelta(id string) int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	n := 0
	for _, m := range f.Movements {
		if m.ClientPackageID == id {
			n += m.Delta
		}
	}
	return n
}

var _ ports.ClientPackageRepository = (*PkgOwned)(nil)

// NewPackagesEnv rearma el router con los paquetes habilitados (el entorno del
// POS no los conoce: no se tocan los tests existentes).
func NewPackagesEnv() (*Env, *PkgCatalog, *PkgOwned) {
	env := NewPOSEnv()
	catalog := &PkgCatalog{Services: env.Services}
	owned := &PkgOwned{Services: env.Services, Mirror: env.Sales}
	svc := packages.PackageService{
		Packages: catalog, ClientPackages: owned, Services: env.Services, Clients: env.Clients,
		Memberships: env.Mems, Settings: env.Settings, Events: env.Events,
	}
	env.Deps.Packages = httpapi.PackageHandler{Packages: svc, Memberships: env.Mems}
	env.Router = gin.New()
	httpapi.RegisterRoutes(env.Router, env.Deps)
	return env, catalog, owned
}
