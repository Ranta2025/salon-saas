package domain

import (
	"fmt"
	"strings"
	"time"
)

// PaymentMethod: cómo pagó el cliente. Importa porque el cierre de caja solo
// cuenta efectivo; tarjeta/transferencia no están en el cajón.
type PaymentMethod string

const (
	MethodCash     PaymentMethod = "cash"
	MethodCard     PaymentMethod = "card"
	MethodTransfer PaymentMethod = "transfer"
)

// Valid dice si el método está en el catálogo.
func (m PaymentMethod) Valid() bool {
	switch m {
	case MethodCash, MethodCard, MethodTransfer:
		return true
	default:
		return false
	}
}

// SaleItem es una línea de la venta: qué servicio, cuántas veces y a qué precio
// unitario en centavos. El precio unitario se congela al vender: si mañana el
// catálogo cambia, la venta histórica no se mueve.
type SaleItem struct {
	ServiceID      string
	Qty            int
	UnitPriceCents int
	// CommissionPct es la FOTO del % de comisión del servicio en el momento de
	// la venta (F3.1, D1 del contrato F3). nil = la línea es anterior a F3.1 y
	// no tiene foto (NULL en la DB): su comisión se estima después con la
	// tarifa vigente. Congelarla al vender es lo que impide que una liquidación
	// ya pagada cambie si el dueño edita el porcentaje del catálogo.
	CommissionPct *int
}

// SubtotalCents es qty * precio unitario, en centavos enteros.
func (i SaleItem) SubtotalCents() int { return i.Qty * i.UnitPriceCents }

// Sale es una venta del POS. TotalCents NO viene del cliente: lo calcula el
// dominio a partir de los ítems (ver NewSale). Esa es la razón por la que
// NewSale no acepta un total como parámetro: no hay dónde inyectarlo.
type Sale struct {
	ID             string
	SalonID        string
	ProfessionalID string
	ClientName     string
	Items          []SaleItem
	TotalCents     int
	Method         PaymentMethod
	// ClientOpID identifica la operación en el cliente (cola offline). Vacío
	// significa "operación en línea". Si viene repetido, el servidor devuelve la
	// venta YA registrada en vez de cobrarla de nuevo: sin esto, una venta que
	// llegó al servidor pero perdió la respuesta se cobraría dos veces.
	ClientOpID string
	CreatedAt  time.Time
}

// NewSale valida y crea la venta, calculando el total en el servidor.
func NewSale(salonID, professionalID, clientName string, items []SaleItem, method PaymentMethod) (Sale, error) {
	clientName = strings.TrimSpace(clientName)
	if salonID == "" {
		return Sale{}, fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	if professionalID == "" {
		return Sale{}, fmt.Errorf("%w: el profesional es obligatorio", ErrValidation)
	}
	if clientName == "" {
		return Sale{}, fmt.Errorf("%w: el nombre del cliente es obligatorio", ErrValidation)
	}
	if err := validateText("el nombre del cliente", clientName, MaxTextName); err != nil {
		return Sale{}, err
	}
	if !method.Valid() {
		return Sale{}, fmt.Errorf("%w: método de pago desconocido %q", ErrValidation, method)
	}
	if err := ValidateSaleItems(items); err != nil {
		return Sale{}, err
	}
	total, err := totalCents(items)
	if err != nil {
		return Sale{}, err
	}
	return Sale{
		ID:             NewID(),
		SalonID:        salonID,
		ProfessionalID: professionalID,
		ClientName:     clientName,
		Items:          items,
		TotalCents:     total,
		Method:         method,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

// ValidateSaleItems valida la FORMA y los límites de las líneas, sin construir
// la entidad. Se usa para rechazar el pedido crudo del cliente (precio
// manipulado, cantidades absurdas) antes de reemplazar los precios por los del
// catálogo: el pedido se valida tal como llegó, y el cobro se hace con el precio
// del salón.
func ValidateSaleItems(items []SaleItem) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: la venta necesita al menos un ítem", ErrValidation)
	}
	if len(items) > MaxSaleItems {
		return fmt.Errorf("%w: la venta no puede tener más de %d ítems", ErrValidation, MaxSaleItems)
	}
	for _, it := range items {
		if strings.TrimSpace(it.ServiceID) == "" {
			return fmt.Errorf("%w: cada ítem necesita service_id", ErrValidation)
		}
		if it.Qty < 1 {
			return fmt.Errorf("%w: la cantidad debe ser al menos 1", ErrValidation)
		}
		if it.Qty > MaxSaleItemQty {
			return fmt.Errorf("%w: la cantidad no puede superar %d", ErrValidation, MaxSaleItemQty)
		}
		if it.UnitPriceCents < 0 {
			return fmt.Errorf("%w: el precio unitario no puede ser negativo", ErrValidation)
		}
		if it.UnitPriceCents > MaxSaleUnitPriceCents {
			return fmt.Errorf("%w: el precio unitario no puede superar %d centavos", ErrValidation, MaxSaleUnitPriceCents)
		}
		// La foto de comisión (F3.1) es opcional (nil = sin foto), pero si está
		// tiene que ser un porcentaje real: espejo del CHECK de la DB (0..100).
		if it.CommissionPct != nil && (*it.CommissionPct < 0 || *it.CommissionPct > 100) {
			return fmt.Errorf("%w: el porcentaje de comisión del ítem debe estar entre 0 y 100", ErrValidation)
		}
	}
	return nil
}

// totalCents suma los subtotales en centavos (sin floats, sin redondeos) con
// chequeo de overflow: el total tiene que caber en el límite que soportan la DB
// y las sumas del dashboard/cierre. Un total gigante no es una venta grande: es
// un pedido roto, y se rechaza antes de escribir.
func totalCents(items []SaleItem) (int, error) {
	total := int64(0)
	for _, it := range items {
		total += int64(it.Qty) * int64(it.UnitPriceCents)
		if total > MaxSaleTotalCents {
			return 0, fmt.Errorf("%w: el total de la venta supera el máximo permitido (%d centavos)", ErrValidation, MaxSaleTotalCents)
		}
	}
	return int(total), nil
}

// TopService es un agregado de lectura del dashboard: el ranking de servicios
// más vendidos del día. No es una entidad persistida (no tiene tabla propia).
type TopService struct {
	ServiceID    string
	Name         string
	Qty          int
	RevenueCents int
}
