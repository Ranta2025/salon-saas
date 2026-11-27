package testutil

import (
	"sort"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// SaleSaver es lo mínimo que necesita la venta con productos de un repositorio de
// ventas: guardar la venta. El resto de las lecturas son del POS.
type SaleSaver interface {
	Save(s domain.Sale) error
}

// FakeProducts: misma semántica que pgstore (scope por salón, unicidad del
// nombre ACTIVO sin importar mayúsculas, stock + movimiento en la misma
// "transacción", libro más reciente primero) sin Postgres.
type FakeProducts struct {
	All       []domain.Product
	Movements []domain.StockMovement
}

func (f *FakeProducts) Create(p domain.Product, initial *domain.StockMovement) error {
	if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
		return domain.ErrAlreadyExists
	}
	f.All = append(f.All, p)
	if initial != nil {
		f.Movements = append(f.Movements, *initial)
	}
	return nil
}

// nameTaken: un nombre activo no se repite en el salón (case-insensitive); los
// inactivos no bloquean.
func (f *FakeProducts) nameTaken(salonID, name, ignoreID string) bool {
	for _, p := range f.All {
		if p.SalonID == salonID && p.Active && p.ID != ignoreID && strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (f *FakeProducts) Save(p domain.Product) error {
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

func (f *FakeProducts) FindByID(salonID, id string) (domain.Product, error) {
	for _, p := range f.All {
		if p.SalonID == salonID && p.ID == id {
			return p, nil
		}
	}
	return domain.Product{}, domain.ErrNotFound
}

func (f *FakeProducts) List(salonID string) ([]domain.Product, error) {
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

func (f *FakeProducts) Adjust(salonID, productID string, delta int, reason domain.StockReason, note, createdBy string) (domain.Product, error) {
	for i := range f.All {
		if f.All[i].ID != productID || f.All[i].SalonID != salonID {
			continue
		}
		after := f.All[i].Stock + delta
		if after < 0 {
			return domain.Product{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductNegativeStock)
		}
		f.All[i].Stock = after
		f.Movements = append(f.Movements, domain.StockMovement{
			ID: domain.NewID(), SalonID: salonID, ProductID: productID,
			Delta: delta, Reason: reason, Note: note, CreatedBy: createdBy,
			CreatedAt: time.Now().UTC(),
		})
		return f.All[i], nil
	}
	return domain.Product{}, domain.ErrNotFound
}

func (f *FakeProducts) ListMovements(salonID, productID string, limit int) ([]domain.StockMovement, error) {
	var out []domain.StockMovement
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

var _ ports.ProductRepository = (*FakeProducts)(nil)

// FakeProductSales: la venta con productos en UNA transacción simulada. Primero
// valida TODAS las líneas y recién después aplica, así un pedido que falla no
// deja el primer producto descontado (lo mismo que el rollback de Postgres).
type FakeProductSales struct {
	Inv   *FakeProducts
	Sales SaleSaver
	All   []domain.SaleWithProducts
}

func (f *FakeProductSales) SaveWithProducts(s domain.SaleWithProducts) ([]domain.Product, error) {
	// Ordenado por id, como el adaptador (evita interbloqueos).
	lines := append([]domain.SaleProduct(nil), s.Products...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].ProductID < lines[j].ProductID })

	next := make(map[string]domain.Product, len(lines))
	crossed := make([]domain.Product, 0, len(lines))
	for _, l := range lines {
		p, err := f.Inv.FindByID(s.SalonID, l.ProductID)
		if err != nil || !p.Active {
			return nil, domain.NewMessage(domain.ErrValidation, domain.MsgProductInvalid)
		}
		stock := p.Stock
		if cur, ok := next[p.ID]; ok {
			stock = cur.Stock
		}
		if stock < l.Qty {
			return nil, domain.NewMessage(domain.ErrAlreadyExists, domain.NoStockMessage(p.Name))
		}
		before := p
		before.Stock = stock
		p.Stock = stock - l.Qty
		next[p.ID] = p
		if domain.CrossedLowStock(before, p) {
			crossed = append(crossed, p)
		}
	}

	// Aplicar: stock, movimientos, venta y líneas.
	for _, l := range lines {
		p := next[l.ProductID]
		for i := range f.Inv.All {
			if f.Inv.All[i].ID == p.ID && f.Inv.All[i].SalonID == p.SalonID {
				f.Inv.All[i].Stock = p.Stock
			}
		}
		f.Inv.Movements = append(f.Inv.Movements, domain.StockMovement{
			ID: domain.NewID(), SalonID: s.SalonID, ProductID: p.ID,
			Delta: -l.Qty, Reason: domain.StockSale, SaleID: s.ID,
			CreatedAt: time.Now().UTC(),
		})
	}
	f.All = append(f.All, s)
	_ = f.Sales.Save(s.Sale)
	return crossed, nil
}

var _ ports.SaleProductsRepository = (*FakeProductSales)(nil)

// FakeStockNotifier: guarda los avisos del caso de uso (stock bajo) con su
// título. Va aparte del fakeNotifier de los tests de avisos (no se toca un
// archivo de test existente).
type FakeStockNotifier struct{ All []domain.Notification }

func (f *FakeStockNotifier) Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error) {
	n, err := domain.NewNotification(salonID, userID, title, body, kind)
	if err != nil {
		return domain.Notification{}, err
	}
	f.All = append(f.All, n)
	return n, nil
}

var _ kernel.Notifier = (*FakeStockNotifier)(nil)
