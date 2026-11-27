// ProductService: catálogo de productos e inventario del POS (F6 del contrato
// de la fase 6). Decisiones D1/D2/D7/D8:
//
//   - Leer el catálogo y el stock: owner/admin/recepción (puedeVerPlata).
//   - Crear, editar el catálogo, AJUSTAR el stock y ver los movimientos: solo
//     owner/admin (CanManage). Profesional y cliente no ven nada.
//   - El stock nunca se edita directo: cada cambio es un movimiento del libro
//     (stock = Σ delta) en la MISMA transacción del adaptador.
//   - Cuando un ajuste CRUZA el umbral hacia abajo se deja UN aviso «Stock bajo»
//     y se publica `product.updated`, después de escribir. Si ya estaba bajo no
//     hay aviso nuevo.
//
// Toda consulta va scopeada por salon_id: el producto de otro salón es 404.
package app

import (
	"errors"
	"fmt"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

type ProductService struct {
	Products    ports.ProductRepository
	Memberships ports.MembershipRepository
	// Events y Notifier son opcionales (nil = no avisa): el stock bajo avisa
	// después de escribir y un fallo nunca cambia la respuesta.
	Events   ports.EventPublisher
	Notifier Notifier
}

const (
	defaultProductMovements = 50
	maxProductMovements     = 100
)

// List trae los productos del salón (activos e inactivos) por nombre.
func (s ProductService) List(actor domain.Membership, salonID string) ([]domain.Product, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !kernel.PuedeVerPlata(m) {
		return nil, domain.ErrForbidden
	}
	return s.Products.List(salonID)
}

// Create da de alta un producto. `stock` y `lowStockThreshold` son opcionales
// (nil = 0); un stock inicial mayor que cero escribe su movimiento `initial`.
func (s ProductService) Create(actor domain.Membership, salonID, name string, priceCents int, stock, lowStockThreshold *int) (domain.Product, error) {
	m, err := s.manager(actor, salonID)
	if err != nil {
		return domain.Product{}, err
	}
	initialStock, threshold := 0, 0
	if stock != nil {
		initialStock = *stock
	}
	if lowStockThreshold != nil {
		threshold = *lowStockThreshold
	}
	p, err := domain.NewProduct(salonID, name, priceCents, initialStock, threshold)
	if err != nil {
		return domain.Product{}, err
	}
	if err := s.ensureFreeName(salonID, p.Name, ""); err != nil {
		return domain.Product{}, err
	}
	var initial *domain.StockMovement
	if p.Stock > 0 {
		initial = &domain.StockMovement{
			ID:        domain.NewID(),
			SalonID:   salonID,
			ProductID: p.ID,
			Delta:     p.Stock,
			Reason:    domain.StockInitial,
			CreatedBy: m.UserID,
		}
	}
	if err := s.Products.Create(p, initial); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.Product{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductNameTaken)
		}
		return domain.Product{}, err
	}
	return p, nil
}

// Update reemplaza nombre, precio, umbral y estado (las CUATRO claves las exige
// el handler) y NO cambia el stock.
func (s ProductService) Update(actor domain.Membership, salonID, productID, name string, priceCents, lowStockThreshold int, active bool) (domain.Product, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.Product{}, err
	}
	current, err := s.Products.FindByID(salonID, strings.TrimSpace(productID))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Product{}, domain.NewMessage(domain.ErrNotFound, domain.MsgProductNotFound)
		}
		return domain.Product{}, err
	}
	updated, err := current.WithDetails(name, priceCents, lowStockThreshold, active)
	if err != nil {
		return domain.Product{}, err
	}
	if updated.Active {
		if err := s.ensureFreeName(salonID, updated.Name, updated.ID); err != nil {
			return domain.Product{}, err
		}
	}
	if err := s.Products.Save(updated); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Product{}, domain.NewMessage(domain.ErrNotFound, domain.MsgProductNotFound)
		}
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.Product{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductNameTaken)
		}
		return domain.Product{}, err
	}
	return updated, nil
}

// Adjust mueve el stock con un motivo del catálogo y deja su movimiento.
func (s ProductService) Adjust(actor domain.Membership, salonID, productID string, delta int, reason, note string) (domain.Product, error) {
	m, err := s.manager(actor, salonID)
	if err != nil {
		return domain.Product{}, err
	}
	current, err := s.Products.FindByID(salonID, strings.TrimSpace(productID))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Product{}, domain.NewMessage(domain.ErrNotFound, domain.MsgProductNotFound)
		}
		return domain.Product{}, err
	}
	_, movement, err := domain.ApplyStockAdjustment(current, delta, domain.StockReason(reason), note)
	if err != nil {
		return domain.Product{}, err
	}
	saved, err := s.Products.Adjust(salonID, current.ID, delta, movement.Reason, movement.Note, m.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Product{}, domain.NewMessage(domain.ErrNotFound, domain.MsgProductNotFound)
		}
		return domain.Product{}, err
	}
	// El aviso sale solo si el ajuste CRUZÓ el umbral (D8). El stock anterior se
	// deriva del que quedó y el delta: es el valor real bajo el lock de la fila.
	before := saved
	before.Stock = saved.Stock - delta
	if domain.CrossedLowStock(before, saved) {
		announceProductLow(s.Events, s.Notifier, saved)
	}
	return saved, nil
}

// Movements: libro de un producto, más reciente primero.
func (s ProductService) Movements(actor domain.Membership, salonID, productID string, limit int) ([]domain.StockMovement, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = defaultProductMovements
	}
	if limit < 1 || limit > maxProductMovements {
		return nil, domain.NewMessage(domain.ErrValidation, domain.MsgProductLimit)
	}
	if _, err := s.Products.FindByID(salonID, strings.TrimSpace(productID)); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.NewMessage(domain.ErrNotFound, domain.MsgProductNotFound)
		}
		return nil, err
	}
	return s.Products.ListMovements(salonID, strings.TrimSpace(productID), limit)
}

// ensureFreeName rechaza un nombre que ya usa un producto ACTIVO del salón, sin
// importar mayúsculas (el índice de la DB es sobre lower(name) WHERE active).
func (s ProductService) ensureFreeName(salonID, name, ignoreID string) error {
	list, err := s.Products.List(salonID)
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.ID == ignoreID || !p.Active {
			continue
		}
		if strings.EqualFold(p.Name, name) {
			return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductNameTaken)
		}
	}
	return nil
}

// member valida que el actor sea miembro ACTIVO del salón (no confía en el rol
// que traiga el token).
func (s ProductService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// manager exige membership activa y permiso de configuración del salón.
func (s ProductService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// announceProductLow deja el aviso interno y el evento del stock bajo (D8).
// Best-effort: si el aviso falla, la venta/ajuste ya está escrito y la respuesta
// no cambia.
func announceProductLow(events ports.EventPublisher, notifier Notifier, p domain.Product) {
	kernel.Notify(notifier, p.SalonID, "", "Stock bajo",
		fmt.Sprintf("%s: quedan %d unidades", p.Name, p.Stock), domain.KindInfo)
	kernel.Publish(events, ports.Event{
		Type:    "product.updated",
		SalonID: p.SalonID,
		Data: map[string]any{
			"id":    p.ID,
			"name":  p.Name,
			"stock": p.Stock,
			"low":   p.Low(),
		},
	})
}
