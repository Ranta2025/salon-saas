// Productos e inventario del POS (F6 del contrato de la fase 6, D1/D2/D8).
//
// Un producto tiene precio, stock y umbral de stock bajo. El stock es un
// CONTADOR y cada cambio escribe un movimiento en el libro (stock_movements):
// en todo momento stock = Σ delta. El stock nunca queda negativo.
//
// El dominio no sabe de HTTP ni de SQL: solo valida y arma las entidades con
// los textos EXACTOS que ve la persona (los mensajes del contrato).
package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxProductPriceCents es el techo del precio de un producto (10.000.000
	// centavos), el mismo rango que una línea de venta.
	MaxProductPriceCents = 10_000_000
	// MaxProductStock es el techo del stock y del umbral de stock bajo.
	MaxProductStock = 1_000_000
	// MaxProductAdjust es el módulo máximo de un ajuste manual de stock.
	MaxProductAdjust = 1_000_000
	// MaxProductNoteRunes es el largo de la nota de un ajuste.
	MaxProductNoteRunes = 200
	// MaxProductQty es la cantidad máxima de una línea de producto.
	MaxProductQty = 1000
	// MaxProductLines es el tope de líneas de producto de una venta.
	MaxProductLines = 50
)

// Textos exactos del contrato F6 (los que ve la persona).
const (
	MsgProductNameRequired  = "El nombre del producto es obligatorio"
	MsgProductNameTooLong   = "El nombre del producto no puede superar los 120 caracteres"
	MsgProductPriceRange    = "El precio debe estar entre 0 y 10000000 centavos"
	MsgProductStockRange    = "El stock y el umbral deben estar entre 0 y 1000000"
	MsgProductInvalidChars  = "El texto tiene caracteres no válidos"
	MsgProductNameTaken     = "Ya hay un producto activo con ese nombre"
	MsgProductMissingFields = "Faltan datos del producto: nombre, precio, umbral de stock bajo y estado"
	MsgProductNotFound      = "Producto no encontrado"
	MsgProductInvalid       = "Producto inválido"
	MsgProductAdjustZero    = "El ajuste no puede ser cero"
	MsgProductAdjustReason  = "Motivo inválido: usá restock, loss o correction"
	MsgProductAdjustSign    = "El signo del ajuste no coincide con el motivo"
	MsgProductNegativeStock = "El stock no puede quedar negativo"
	MsgProductNoteTooLong   = "La nota no puede superar los 200 caracteres"
	MsgProductQty           = "La cantidad del producto debe estar entre 1 y 1000"
	MsgProductLines         = "La venta no puede tener más de 50 líneas de producto"
	MsgProductLimit         = "El límite debe estar entre 1 y 100"
	// MsgProductBusy es el 409 de una venta que chocó con otra transacción
	// (40P01/40001): la transacción ya se deshizo, reintentar es seguro.
	MsgProductBusy = "La venta chocó con otra operación: reintentá"
)

// NoStockMessage es el 409 de stock insuficiente, con el nombre del producto
// entre comillas angulares (texto del contrato).
func NoStockMessage(name string) string {
	return fmt.Sprintf("No hay stock suficiente de «%s»", name)
}

// Product es un producto del catálogo del salón.
type Product struct {
	ID                string
	SalonID           string
	Name              string
	PriceCents        int
	Stock             int
	LowStockThreshold int
	Active            bool
	CreatedAt         time.Time
}

// Low dice si el producto está «bajo»: ACTIVO, con umbral > 0 y stock <= umbral
// (D8). Un umbral 0 significa "sin alerta".
func (p Product) Low() bool {
	return p.Active && p.LowStockThreshold > 0 && p.Stock <= p.LowStockThreshold
}

// NewProduct valida y crea un producto. `stock` y `lowStockThreshold` son los
// valores ya resueltos por el caso de uso (0 por defecto).
func NewProduct(salonID, name string, priceCents, stock, lowStockThreshold int) (Product, error) {
	if strings.TrimSpace(salonID) == "" {
		return Product{}, fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	name, err := validateProductName(name)
	if err != nil {
		return Product{}, err
	}
	if err := validateProductNumbers(priceCents, stock, lowStockThreshold); err != nil {
		return Product{}, err
	}
	return Product{
		ID:                NewID(),
		SalonID:           salonID,
		Name:              name,
		PriceCents:        priceCents,
		Stock:             stock,
		LowStockThreshold: lowStockThreshold,
		Active:            true,
		CreatedAt:         time.Now().UTC(),
	}, nil
}

// WithDetails reemplaza nombre, precio, umbral y estado (el PUT del contrato
// exige las cuatro claves). El STOCK no se toca: se cambia solo con ajustes.
func (p Product) WithDetails(name string, priceCents, lowStockThreshold int, active bool) (Product, error) {
	name, err := validateProductName(name)
	if err != nil {
		return Product{}, err
	}
	if err := validateProductNumbers(priceCents, p.Stock, lowStockThreshold); err != nil {
		return Product{}, err
	}
	p.Name = name
	p.PriceCents = priceCents
	p.LowStockThreshold = lowStockThreshold
	p.Active = active
	return p, nil
}

func validateProductName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", NewMessage(ErrValidation, MsgProductNameRequired)
	}
	if hasNUL(name) {
		return "", NewMessage(ErrValidation, MsgProductInvalidChars)
	}
	if utf8.RuneCountInString(name) > MaxTextName {
		return "", NewMessage(ErrValidation, MsgProductNameTooLong)
	}
	return name, nil
}

func validateProductNumbers(priceCents, stock, lowStockThreshold int) error {
	if priceCents < 0 || priceCents > MaxProductPriceCents {
		return NewMessage(ErrValidation, MsgProductPriceRange)
	}
	if stock < 0 || stock > MaxProductStock || lowStockThreshold < 0 || lowStockThreshold > MaxProductStock {
		return NewMessage(ErrValidation, MsgProductStockRange)
	}
	return nil
}

// hasNUL detecta el carácter NUL (0x00), que no puede viajar a Postgres en un
// TEXT: sin este chequeo un nombre con NUL sería un 500.
func hasNUL(s string) bool { return strings.ContainsRune(s, 0) }

// StockReason cataloga por qué cambió el stock. Es cerrado: la DB lo repite con
// un CHECK.
type StockReason string

const (
	StockInitial    StockReason = "initial"    // alta del producto con stock > 0
	StockRestock    StockReason = "restock"    // reposición (delta > 0)
	StockLoss       StockReason = "loss"       // pérdida (delta < 0)
	StockCorrection StockReason = "correction" // corrección (delta de cualquier signo)
	StockSale       StockReason = "sale"       // venta (la escribe la transacción de la venta)
)

// Valid dice si el motivo está en el catálogo.
func (r StockReason) Valid() bool {
	switch r {
	case StockInitial, StockRestock, StockLoss, StockCorrection, StockSale:
		return true
	default:
		return false
	}
}

// StockMovement es una fila del libro de stock (append-only).
type StockMovement struct {
	ID        string
	SalonID   string
	ProductID string
	Delta     int
	Reason    StockReason
	Note      string
	SaleID    string
	CreatedBy string
	CreatedAt time.Time
}

// ApplyStockAdjustment valida un ajuste manual y devuelve el producto
// actualizado y su movimiento. NO persiste nada: la escritura la hace el
// adaptador en una transacción (stock + movimiento juntos).
func ApplyStockAdjustment(p Product, delta int, reason StockReason, note string) (Product, StockMovement, error) {
	switch {
	case reason != StockRestock && reason != StockLoss && reason != StockCorrection:
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductAdjustReason)
	case delta == 0:
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductAdjustZero)
	case delta > MaxProductAdjust || delta < -MaxProductAdjust:
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductStockRange)
	case reason == StockRestock && delta < 0:
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductAdjustSign)
	case reason == StockLoss && delta > 0:
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductAdjustSign)
	}
	note = strings.TrimSpace(note)
	if hasNUL(note) {
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductInvalidChars)
	}
	if utf8.RuneCountInString(note) > MaxProductNoteRunes {
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductNoteTooLong)
	}
	newStock := p.Stock + delta
	if newStock < 0 {
		// 409: el estado de la DB no permite el ajuste (contrato).
		return Product{}, StockMovement{}, NewMessage(ErrAlreadyExists, MsgProductNegativeStock)
	}
	if newStock > MaxProductStock {
		return Product{}, StockMovement{}, NewMessage(ErrValidation, MsgProductStockRange)
	}
	p.Stock = newStock
	return p, StockMovement{
		ID:        NewID(),
		SalonID:   p.SalonID,
		ProductID: p.ID,
		Delta:     delta,
		Reason:    reason,
		Note:      note,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// CrossedLowStock dice si el cambio de stock CRUZÓ el umbral (D8): antes estaba
// por encima y ahora está en/bajo el umbral, con un producto activo y umbral > 0.
// Si ya estaba bajo no hay aviso nuevo.
func CrossedLowStock(before, after Product) bool {
	if !after.Active || after.LowStockThreshold <= 0 {
		return false
	}
	return before.Stock > after.LowStockThreshold && after.Stock <= after.LowStockThreshold
}
