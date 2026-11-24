// Venta con líneas de producto (F6 del contrato de la fase 6, D1/D3/D5).
//
// `sale_items` y `domain.Sale` siguen intactas: un producto NO es un servicio
// (no tiene comisión ni duración) y una venta solo de productos no tendría
// ninguna línea de servicio. Por eso hay un constructor NUEVO que arma la
// misma entidad Sale con las líneas de producto aparte y el total de
// servicios + productos.
package domain

import (
	"fmt"
	"strings"
	"time"
)

// SaleProduct es una línea de producto de una venta: qué producto, cuántas
// unidades y a qué precio unitario en centavos. El precio se congela al vender
// (sale_products.unit_price_cents), igual que en `sale_items`.
type SaleProduct struct {
	ProductID      string
	Qty            int
	UnitPriceCents int
}

// SubtotalCents es qty * precio unitario, en centavos enteros.
func (l SaleProduct) SubtotalCents() int { return l.Qty * l.UnitPriceCents }

// SaleWithProducts es una venta del POS con líneas de producto. La venta (Sale)
// es la misma entidad de siempre; acá se le agregan las líneas y la parte de
// producto del total. `ProductsCents` es la suma de las líneas de producto y
// `Sale.TotalCents` es servicios + productos (D1).
type SaleWithProducts struct {
	Sale
	Products      []SaleProduct
	ProductsCents int
}

// ValidateSaleProducts valida la FORMA de las líneas de producto: tope de 50,
// cantidad 1..1000, producto obligatorio y precio unitario dentro de rango
// (espejo de los CHECK de sale_products). El precio real lo pone el catálogo
// del servidor: esto valida lo que llegó, antes de repreciar.
func ValidateSaleProducts(lines []SaleProduct) error {
	if len(lines) > MaxProductLines {
		return NewMessage(ErrValidation, MsgProductLines)
	}
	for _, l := range lines {
		if strings.TrimSpace(l.ProductID) == "" {
			return NewMessage(ErrValidation, MsgProductInvalid)
		}
		if l.Qty < 1 || l.Qty > MaxProductQty {
			return NewMessage(ErrValidation, MsgProductQty)
		}
		if l.UnitPriceCents < 0 || l.UnitPriceCents > MaxProductPriceCents {
			return NewMessage(ErrValidation, MsgProductPriceRange)
		}
	}
	return nil
}

// NewSaleWithProducts valida y crea la venta con líneas de producto. El total
// se calcula SIEMPRE acá (nunca se acepta del cliente) y una venta necesita al
// menos una línea, de servicio o de producto (D5).
func NewSaleWithProducts(salonID, professionalID, clientName string, items []SaleItem, products []SaleProduct, method PaymentMethod) (SaleWithProducts, error) {
	clientName = strings.TrimSpace(clientName)
	if salonID == "" {
		return SaleWithProducts{}, fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	if professionalID == "" {
		return SaleWithProducts{}, fmt.Errorf("%w: el profesional es obligatorio", ErrValidation)
	}
	if clientName == "" {
		return SaleWithProducts{}, fmt.Errorf("%w: el nombre del cliente es obligatorio", ErrValidation)
	}
	if err := validateText("el nombre del cliente", clientName, MaxTextName); err != nil {
		return SaleWithProducts{}, err
	}
	if !method.Valid() {
		return SaleWithProducts{}, fmt.Errorf("%w: método de pago desconocido %q", ErrValidation, method)
	}
	if len(items) == 0 && len(products) == 0 {
		return SaleWithProducts{}, fmt.Errorf("%w: la venta necesita al menos un ítem", ErrValidation)
	}
	if len(items) > 0 {
		if err := ValidateSaleItems(items); err != nil {
			return SaleWithProducts{}, err
		}
	}
	if err := ValidateSaleProducts(products); err != nil {
		return SaleWithProducts{}, err
	}
	servicesTotal, err := totalCents(items)
	if err != nil {
		return SaleWithProducts{}, err
	}
	productsTotal, err := productsTotalCents(products)
	if err != nil {
		return SaleWithProducts{}, err
	}
	total := int64(servicesTotal) + int64(productsTotal)
	if total > MaxSaleTotalCents {
		return SaleWithProducts{}, fmt.Errorf("%w: el total de la venta supera el máximo permitido (%d centavos)", ErrValidation, MaxSaleTotalCents)
	}
	if items == nil {
		items = []SaleItem{}
	}
	return SaleWithProducts{
		Sale: Sale{
			ID:             NewID(),
			SalonID:        salonID,
			ProfessionalID: professionalID,
			ClientName:     clientName,
			Items:          items,
			TotalCents:     int(total),
			Method:         method,
			CreatedAt:      time.Now().UTC(),
		},
		Products:      products,
		ProductsCents: int(productsTotal),
	}, nil
}

func productsTotalCents(products []SaleProduct) (int64, error) {
	total := int64(0)
	for _, l := range products {
		total += int64(l.Qty) * int64(l.UnitPriceCents)
		if total > MaxSaleTotalCents {
			return 0, fmt.Errorf("%w: el total de la venta supera el máximo permitido (%d centavos)", ErrValidation, MaxSaleTotalCents)
		}
	}
	return total, nil
}
