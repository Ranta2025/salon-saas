// Venta CON productos del POS (F6 del contrato de la fase 6, D3/D4/D5/D6/D8).
//
// Una venta SIN productos sigue por el camino de siempre: `domain.NewSale`, su
// validación y `Sales.Save` no cambian (byte por byte). Este camino es el nuevo:
//
//   - el pedido trae líneas opcionales `products: [{product_id, qty}]`;
//   - el precio sale SIEMPRE del catálogo del servidor y se congela en la
//     línea (un precio enviado se ignora);
//   - un producto repetido se SUMA (una sola línea en sale_products);
//   - tope de 50 líneas y cantidad 1..1000;
//   - producto inexistente, de otro salón o inactivo: 400 «Producto inválido»
//     y no se guarda nada;
//   - sin stock: 409 «No hay stock suficiente de «<nombre>»» y se deshace TODO;
//   - el repetido por client_op_id devuelve la venta original sin descontar dos
//     veces (la carrera real la resuelve el índice único dentro de la
//     transacción, D4).
//
// La comisión y la nómina NO cambian (D6): las líneas de producto no generan
// comisión; la base sale solo de `sale_items`.
package app

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// RegisterSaleWithProducts registra una venta con líneas de producto. `items`
// puede venir vacío (una venta solo de productos es válida). `clientID` y
// `clientOpID` tienen la misma semántica que en la venta clásica.
func (s SaleService) RegisterSaleWithProducts(actor domain.Membership, salonID, professionalID, clientName, clientID string, items []domain.SaleItem, products []domain.SaleProduct, method domain.PaymentMethod, clientOpID string) (domain.Sale, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Sale{}, err
	}
	if !puedeVender(m) {
		return domain.Sale{}, domain.ErrForbidden
	}
	// La ficha del POS (F5): tiene que existir EN ESTE salón; si el nombre vino
	// vacío se completa con el de la ficha, igual que en la venta clásica.
	clientID = strings.TrimSpace(clientID)
	if clientID != "" {
		client, err := s.lookupClient(salonID, clientID)
		if err != nil {
			return domain.Sale{}, err
		}
		if strings.TrimSpace(clientName) == "" {
			clientName = client.Name
		}
	}
	// (2) El pedido se valida TAL COMO LLEGÓ (suma de repetidos, topes y forma)
	// antes de tocar el catálogo: un qty absurdo es 400 aunque el producto no
	// exista. El precio que mande el cliente acá se ignora.
	lines, err := normalizeSaleProducts(products)
	if err != nil {
		return domain.Sale{}, err
	}
	if _, err := domain.NewSaleWithProducts(salonID, professionalID, clientName, items, lines, method); err != nil {
		return domain.Sale{}, err
	}
	opID := strings.TrimSpace(clientOpID)
	// (3) Replay idempotente: la venta original, con su total original.
	if opID != "" {
		if existing, err := s.Sales.FindByClientOpID(salonID, opID); err == nil {
			return existing, nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Sale{}, err
		}
	}
	if err := ensureProfessional(s.Memberships, salonID, professionalID); err != nil {
		return domain.Sale{}, err
	}
	// (4) Precio del catálogo del salón, nunca del cliente. Los servicios se
	// reprecian igual que en la venta clásica (con su foto de comisión, F3.1);
	// los productos tienen que estar ACTIVOS y ser del salón.
	pricedItems := make([]domain.SaleItem, len(items))
	for i, it := range items {
		svc, err := s.Services.FindByID(salonID, it.ServiceID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.Sale{}, fmt.Errorf("%w: el servicio no existe en el salón", domain.ErrValidation)
			}
			return domain.Sale{}, err
		}
		if !svc.Active {
			return domain.Sale{}, fmt.Errorf("%w: el servicio %q ya no está activo", domain.ErrValidation, svc.Name)
		}
		pct := svc.CommissionPct
		pricedItems[i] = domain.SaleItem{ServiceID: svc.ID, Qty: it.Qty, UnitPriceCents: svc.PriceCents, CommissionPct: &pct}
	}
	pricedProducts := make([]domain.SaleProduct, len(lines))
	for i, l := range lines {
		p, err := s.Products.FindByID(salonID, l.ProductID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.Sale{}, domain.NewMessage(domain.ErrValidation, domain.MsgProductInvalid)
			}
			return domain.Sale{}, err
		}
		if !p.Active {
			return domain.Sale{}, domain.NewMessage(domain.ErrValidation, domain.MsgProductInvalid)
		}
		pricedProducts[i] = domain.SaleProduct{ProductID: p.ID, Qty: l.Qty, UnitPriceCents: p.PriceCents}
	}
	sale, err := domain.NewSaleWithProducts(salonID, professionalID, clientName, pricedItems, pricedProducts, method)
	if err != nil {
		return domain.Sale{}, err
	}
	sale.ClientOpID = opID
	// (5) UNA transacción: venta + servicios + líneas de producto + descuento de
	// stock + un movimiento `sale` por descuento. Si algo falla no queda nada,
	// ni siquiera el descuento del primer producto.
	crossed, err := s.ProductSales.SaveWithProducts(sale)
	if err != nil {
		// La carrera de dos pedidos con el MISMO client_op_id la rechaza el
		// índice único (D4): el que pierde deshizo su descuento y acá se
		// devuelve la venta que ganó, con su total original.
		if opID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			if existing, rerr := s.Sales.FindByClientOpID(salonID, opID); rerr == nil {
				return existing, nil
			}
		}
		return domain.Sale{}, err
	}
	// D3-c: el vínculo con la ficha elegida, best-effort (igual que siempre).
	if clientID != "" && s.ClientLinks != nil {
		if err := s.ClientLinks.LinkSaleByID(salonID, sale.ID, clientID); err != nil {
			log.Printf("venta %s: vincular la ficha en el salón %s: %v", sale.ID, salonID, err)
		}
	}
	publish(s.Events, ports.Event{
		Type:    "sale.created",
		SalonID: salonID,
		Data: map[string]any{
			"id":              sale.ID,
			"total_cents":     sale.TotalCents,
			"method":          string(sale.Method),
			"client_name":     sale.ClientName,
			"professional_id": sale.ProfessionalID,
		},
		TS: sale.CreatedAt,
	})
	// D8: el aviso de stock bajo sale DESPUÉS del commit y solo para los
	// productos que cruzaron el umbral en esta venta. Un fallo no cambia la
	// respuesta (la venta ya está cobrada).
	for _, p := range crossed {
		announceProductLow(s.Events, s.Notifier, p)
	}
	return sale.Sale, nil
}

// normalizeSaleProducts suma los productos repetidos y valida la forma del
// pedido. El orden se conserva (el primero que apareció) y el adaptador ordena
// por id para bloquear filas sin formar interbloqueos (D3).
func normalizeSaleProducts(lines []domain.SaleProduct) ([]domain.SaleProduct, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	byID := make(map[string]int, len(lines))
	out := make([]domain.SaleProduct, 0, len(lines))
	for _, l := range lines {
		id := strings.TrimSpace(l.ProductID)
		if id == "" {
			return nil, domain.NewMessage(domain.ErrValidation, domain.MsgProductInvalid)
		}
		if l.Qty < 1 || l.Qty > domain.MaxProductQty {
			return nil, domain.NewMessage(domain.ErrValidation, domain.MsgProductQty)
		}
		if pos, ok := byID[id]; ok {
			out[pos].Qty += l.Qty
			if out[pos].Qty > domain.MaxProductQty {
				return nil, domain.NewMessage(domain.ErrValidation, domain.MsgProductQty)
			}
			continue
		}
		if len(out) >= domain.MaxProductLines {
			return nil, domain.NewMessage(domain.ErrValidation, domain.MsgProductLines)
		}
		byID[id] = len(out)
		// El precio unitario se descarta: lo pone el catálogo (D5).
		out = append(out, domain.SaleProduct{ProductID: id, Qty: l.Qty})
	}
	return out, nil
}
