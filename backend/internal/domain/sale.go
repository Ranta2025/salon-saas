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
	CreatedAt      time.Time
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
	if len(items) == 0 {
		return Sale{}, fmt.Errorf("%w: la venta necesita al menos un ítem", ErrValidation)
	}
	if !method.Valid() {
		return Sale{}, fmt.Errorf("%w: método de pago desconocido %q", ErrValidation, method)
	}
	for _, it := range items {
		if strings.TrimSpace(it.ServiceID) == "" {
			return Sale{}, fmt.Errorf("%w: cada ítem necesita service_id", ErrValidation)
		}
		if it.Qty < 1 {
			return Sale{}, fmt.Errorf("%w: la cantidad debe ser al menos 1", ErrValidation)
		}
		if it.UnitPriceCents < 0 {
			return Sale{}, fmt.Errorf("%w: el precio unitario no puede ser negativo", ErrValidation)
		}
	}
	return Sale{
		ID:             NewID(),
		SalonID:        salonID,
		ProfessionalID: professionalID,
		ClientName:     clientName,
		Items:          items,
		TotalCents:     totalCents(items),
		Method:         method,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

// totalCents suma los subtotales en centavos (sin floats, sin redondeos).
func totalCents(items []SaleItem) int {
	total := 0
	for _, it := range items {
		total += it.SubtotalCents()
	}
	return total
}

// TopService es un agregado de lectura del dashboard: el ranking de servicios
// más vendidos del día. No es una entidad persistida (no tiene tabla propia).
type TopService struct {
	ServiceID    string
	Name         string
	Qty          int
	RevenueCents int
}
