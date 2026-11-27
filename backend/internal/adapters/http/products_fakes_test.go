package httpapi

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/products"
)

// Fakes de productos/inventario (F6) con la misma semántica que pgstore:
// scope por salón, nombre activo único sin importar mayúsculas, stock con libro
// y venta con productos en una "transacción" simulada.

type mproducts struct {
	all       []domain.Product
	movements []domain.StockMovement
}

func (f *mproducts) nameTaken(salonID, name, ignoreID string) bool {
	for _, p := range f.all {
		if p.SalonID == salonID && p.Active && p.ID != ignoreID && strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (f *mproducts) Create(p domain.Product, initial *domain.StockMovement) error {
	if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
		return domain.ErrAlreadyExists
	}
	f.all = append(f.all, p)
	if initial != nil {
		f.movements = append(f.movements, *initial)
	}
	return nil
}

func (f *mproducts) Save(p domain.Product) error {
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

func (f *mproducts) FindByID(salonID, id string) (domain.Product, error) {
	for _, p := range f.all {
		if p.SalonID == salonID && p.ID == id {
			return p, nil
		}
	}
	return domain.Product{}, domain.ErrNotFound
}

func (f *mproducts) List(salonID string) ([]domain.Product, error) {
	out := make([]domain.Product, 0, len(f.all))
	for _, p := range f.all {
		if p.SalonID == salonID {
			out = append(out, p)
		}
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

func (f *mproducts) Adjust(salonID, productID string, delta int, reason domain.StockReason, note, createdBy string) (domain.Product, error) {
	for i := range f.all {
		if f.all[i].ID != productID || f.all[i].SalonID != salonID {
			continue
		}
		if f.all[i].Stock+delta < 0 {
			return domain.Product{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductNegativeStock)
		}
		f.all[i].Stock += delta
		f.movements = append(f.movements, domain.StockMovement{
			ID: domain.NewID(), SalonID: salonID, ProductID: productID,
			Delta: delta, Reason: reason, Note: note, CreatedBy: createdBy,
			CreatedAt: time.Now().UTC(),
		})
		return f.all[i], nil
	}
	return domain.Product{}, domain.ErrNotFound
}

func (f *mproducts) ListMovements(salonID, productID string, limit int) ([]domain.StockMovement, error) {
	out := make([]domain.StockMovement, 0, limit)
	for i := len(f.movements) - 1; i >= 0; i-- {
		m := f.movements[i]
		if m.SalonID != salonID || m.ProductID != productID {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

var _ ports.ProductRepository = (*mproducts)(nil)

// mproductSales: valida todo y recién después aplica (el rollback de Postgres).
type mproductSales struct {
	inv   *mproducts
	sales *msale
}

func (f *mproductSales) SaveWithProducts(s domain.SaleWithProducts) ([]domain.Product, error) {
	lines := append([]domain.SaleProduct(nil), s.Products...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].ProductID < lines[j].ProductID })
	next := make(map[string]domain.Product, len(lines))
	crossed := make([]domain.Product, 0, len(lines))
	for _, l := range lines {
		p, err := f.inv.FindByID(s.SalonID, l.ProductID)
		if err != nil || !p.Active {
			return nil, domain.NewMessage(domain.ErrValidation, domain.MsgProductInvalid)
		}
		if cur, ok := next[p.ID]; ok {
			p = cur
		}
		before := p
		if p.Stock < l.Qty {
			return nil, domain.NewMessage(domain.ErrAlreadyExists, domain.NoStockMessage(p.Name))
		}
		p.Stock -= l.Qty
		next[p.ID] = p
		if domain.CrossedLowStock(before, p) {
			crossed = append(crossed, p)
		}
	}
	for _, l := range lines {
		p := next[l.ProductID]
		for i := range f.inv.all {
			if f.inv.all[i].ID == p.ID && f.inv.all[i].SalonID == p.SalonID {
				f.inv.all[i].Stock = p.Stock
			}
		}
		f.inv.movements = append(f.inv.movements, domain.StockMovement{
			ID: domain.NewID(), SalonID: s.SalonID, ProductID: p.ID,
			Delta: -l.Qty, Reason: domain.StockSale, SaleID: s.ID,
			CreatedAt: time.Now().UTC(),
		})
	}
	_ = f.sales.Save(s.Sale)
	return crossed, nil
}

var _ ports.SaleProductsRepository = (*mproductSales)(nil)

// newProductsEnv rearma el router con productos habilitados (el POS de
// newPOSEnv no los conoce: no se tocan los tests existentes).
func newProductsEnv() (*posEnv, *mproducts) {
	env := newPOSEnv()
	inv := &mproducts{}
	notifSvc := app.NotificationService{Notifications: env.notifs, Salons: env.mems, Events: env.events}
	saleSvc := app.SaleService{
		Sales: env.sales, Services: env.services, Memberships: env.mems, Events: env.events,
		Products: inv, ProductSales: &mproductSales{inv: inv, sales: env.sales}, Notifier: notifSvc,
	}
	productSvc := products.ProductService{Products: inv, Memberships: env.mems, Events: env.events, Notifier: notifSvc}
	env.d.Sales = SaleHandler{Sales: saleSvc, Memberships: env.mems}
	env.d.Products = ProductHandler{Products: productSvc, Memberships: env.mems}
	env.r = gin.New()
	RegisterRoutes(env.r, env.d)
	return env, inv
}

// seedRole siembra una membresía del salón para probar los permisos rol por rol.
func seedRole(t *testing.T, env *posEnv, salonID, userID string, role domain.Role) {
	t.Helper()
	m := domain.Membership{UserID: userID, SalonID: salonID, Role: role}
	if role == domain.RoleProfessional {
		m.Attends = true
	}
	if err := env.mems.Save(m); err != nil {
		t.Fatalf("membership %s: %v", role, err)
	}
}
