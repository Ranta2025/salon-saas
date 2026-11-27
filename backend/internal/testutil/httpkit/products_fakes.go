package httpkit

import (
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/notifications"
	"salon-saas/internal/ports"
	"salon-saas/internal/products"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Fakes de productos/inventario (F6) con la misma semántica que pgstore:
// scope por salón, nombre activo único sin importar mayúsculas, stock con libro
// y venta con productos en una "transacción" simulada.

type Products struct {
	All       []domain.Product
	Movements []domain.StockMovement
}

func (f *Products) nameTaken(salonID, name, ignoreID string) bool {
	for _, p := range f.All {
		if p.SalonID == salonID && p.Active && p.ID != ignoreID && strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (f *Products) Create(p domain.Product, initial *domain.StockMovement) error {
	if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
		return domain.ErrAlreadyExists
	}
	f.All = append(f.All, p)
	if initial != nil {
		f.Movements = append(f.Movements, *initial)
	}
	return nil
}

func (f *Products) Save(p domain.Product) error {
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

func (f *Products) FindByID(salonID, id string) (domain.Product, error) {
	for _, p := range f.All {
		if p.SalonID == salonID && p.ID == id {
			return p, nil
		}
	}
	return domain.Product{}, domain.ErrNotFound
}

func (f *Products) List(salonID string) ([]domain.Product, error) {
	out := make([]domain.Product, 0, len(f.All))
	for _, p := range f.All {
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

func (f *Products) Adjust(salonID, productID string, delta int, reason domain.StockReason, note, createdBy string) (domain.Product, error) {
	for i := range f.All {
		if f.All[i].ID != productID || f.All[i].SalonID != salonID {
			continue
		}
		if f.All[i].Stock+delta < 0 {
			return domain.Product{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductNegativeStock)
		}
		f.All[i].Stock += delta
		f.Movements = append(f.Movements, domain.StockMovement{
			ID: domain.NewID(), SalonID: salonID, ProductID: productID,
			Delta: delta, Reason: reason, Note: note, CreatedBy: createdBy,
			CreatedAt: time.Now().UTC(),
		})
		return f.All[i], nil
	}
	return domain.Product{}, domain.ErrNotFound
}

func (f *Products) ListMovements(salonID, productID string, limit int) ([]domain.StockMovement, error) {
	out := make([]domain.StockMovement, 0, limit)
	for i := len(f.Movements) - 1; i >= 0; i-- {
		m := f.Movements[i]
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

var _ ports.ProductRepository = (*Products)(nil)

// ProductSales: valida todo y recién después aplica (el rollback de Postgres).
type ProductSales struct {
	Invitations *Products
	Sales       *Sales
}

func (f *ProductSales) SaveWithProducts(s domain.SaleWithProducts) ([]domain.Product, error) {
	lines := append([]domain.SaleProduct(nil), s.Products...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].ProductID < lines[j].ProductID })
	next := make(map[string]domain.Product, len(lines))
	crossed := make([]domain.Product, 0, len(lines))
	for _, l := range lines {
		p, err := f.Invitations.FindByID(s.SalonID, l.ProductID)
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
		for i := range f.Invitations.All {
			if f.Invitations.All[i].ID == p.ID && f.Invitations.All[i].SalonID == p.SalonID {
				f.Invitations.All[i].Stock = p.Stock
			}
		}
		f.Invitations.Movements = append(f.Invitations.Movements, domain.StockMovement{
			ID: domain.NewID(), SalonID: s.SalonID, ProductID: p.ID,
			Delta: -l.Qty, Reason: domain.StockSale, SaleID: s.ID,
			CreatedAt: time.Now().UTC(),
		})
	}
	_ = f.Sales.Save(s.Sale)
	return crossed, nil
}

var _ ports.SaleProductsRepository = (*ProductSales)(nil)

// NewProductsEnv rearma el router con productos habilitados (el POS de
// NewPOSEnv no los conoce: no se tocan los tests existentes).
func NewProductsEnv() (*Env, *Products) {
	env := NewPOSEnv()
	inv := &Products{}
	notifSvc := notifications.NotificationService{Notifications: env.Notifications, Salons: env.Mems, Events: env.Events}
	saleSvc := app.SaleService{
		Sales: env.Sales, Services: env.Services, Memberships: env.Mems, Events: env.Events,
		Products: inv, ProductSales: &ProductSales{Invitations: inv, Sales: env.Sales}, Notifier: notifSvc,
	}
	productSvc := products.ProductService{Products: inv, Memberships: env.Mems, Events: env.Events, Notifier: notifSvc}
	env.Deps.Sales = httpapi.SaleHandler{Sales: saleSvc, Memberships: env.Mems}
	env.Deps.Products = httpapi.ProductHandler{Products: productSvc, Memberships: env.Mems}
	env.Router = gin.New()
	httpapi.RegisterRoutes(env.Router, env.Deps)
	return env, inv
}

// SeedRole siembra una membresía del salón para probar los permisos rol por rol.
func SeedRole(t *testing.T, env *Env, salonID, userID string, role domain.Role) {
	t.Helper()
	m := domain.Membership{UserID: userID, SalonID: salonID, Role: role}
	if role == domain.RoleProfessional {
		m.Attends = true
	}
	if err := env.Mems.Save(m); err != nil {
		t.Fatalf("membership %s: %v", role, err)
	}
}
