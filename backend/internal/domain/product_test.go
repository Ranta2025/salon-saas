package domain

import (
	"errors"
	"strings"
	"testing"
)

// F6/D1: el producto valida nombre, precio, stock y umbral con los textos
// EXACTOS del contrato. Estos son los únicos textos que ve la persona.
func TestNewProduct_validation(t *testing.T) {
	p, err := NewProduct("s1", "  Shampoo  ", 150000, 3, 1)
	if err != nil {
		t.Fatalf("producto válido: %v", err)
	}
	if p.ID == "" || p.SalonID != "s1" || p.Name != "Shampoo" {
		t.Fatalf("producto mal construido: %+v", p)
	}
	if p.PriceCents != 150000 || p.Stock != 3 || p.LowStockThreshold != 1 || !p.Active {
		t.Fatalf("defaults/valores: %+v", p)
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("created_at tiene que quedar fijado al crear")
	}

	cases := []struct {
		name    string
		salonID string
		pname   string
		price   int
		stock   int
		low     int
		want    string
	}{
		{"salón vacío", "", "Shampoo", 1, 0, 0, ""},
		{"nombre vacío", "s1", "   ", 1, 0, 0, MsgProductNameRequired},
		{"nombre 121", "s1", strings.Repeat("a", 121), 1, 0, 0, MsgProductNameTooLong},
		{"nombre NUL", "s1", "Sha\x00mpoo", 1, 0, 0, MsgProductInvalidChars},
		{"precio negativo", "s1", "Shampoo", -1, 0, 0, MsgProductPriceRange},
		{"precio alto", "s1", "Shampoo", 10_000_001, 0, 0, MsgProductPriceRange},
		{"stock negativo", "s1", "Shampoo", 1, -1, 0, MsgProductStockRange},
		{"stock alto", "s1", "Shampoo", 1, 1_000_001, 0, MsgProductStockRange},
		{"umbral negativo", "s1", "Shampoo", 1, 0, -1, MsgProductStockRange},
		{"umbral alto", "s1", "Shampoo", 1, 0, 1_000_001, MsgProductStockRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProduct(tc.salonID, tc.pname, tc.price, tc.stock, tc.low)
			if err == nil {
				t.Fatal("esperaba error")
			}
			if tc.want == "" {
				if !errors.Is(err, ErrValidation) {
					t.Fatalf("salón vacío debe ser validación, dio %v", err)
				}
				return
			}
			if got := messageOf(err); got != tc.want {
				t.Fatalf("mensaje = %q, quería %q", got, tc.want)
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("tiene que ser ErrValidation (400), dio %v", err)
			}
		})
	}
}

// D8: está «bajo» solo si está activo, el umbral > 0 y stock <= umbral.
func TestProduct_Low(t *testing.T) {
	p := Product{Name: "Shampoo", Stock: 2, LowStockThreshold: 2, Active: true}
	if !p.Low() {
		t.Fatal("stock == umbral es bajo")
	}
	p.Stock = 3
	if p.Low() {
		t.Fatal("stock > umbral no es bajo")
	}
	p.Stock, p.LowStockThreshold = 0, 0
	if p.Low() {
		t.Fatal("umbral 0 = sin alerta")
	}
	p.Stock, p.LowStockThreshold, p.Active = 1, 5, false
	if p.Low() {
		t.Fatal("un producto inactivo no está bajo")
	}
}

// PUT reemplaza nombre, precio, umbral y estado; el stock NO cambia.
func TestProduct_WithDetails(t *testing.T) {
	p := Product{ID: "p1", SalonID: "s1", Name: "Shampoo", PriceCents: 100, Stock: 7, LowStockThreshold: 1, Active: true}
	up, err := p.WithDetails(" Acondicionador ", 200, 4, false)
	if err != nil {
		t.Fatalf("update válido: %v", err)
	}
	if up.Name != "Acondicionador" || up.PriceCents != 200 || up.LowStockThreshold != 4 || up.Active {
		t.Fatalf("no reemplazó los campos: %+v", up)
	}
	if up.Stock != 7 {
		t.Fatalf("el PUT no puede tocar el stock: %d", up.Stock)
	}
	for _, tc := range []struct {
		name  string
		price int
		low   int
		want  string
	}{
		{"", 1, 0, MsgProductNameRequired},
		{"x\x00", 1, 0, MsgProductInvalidChars},
		{"Ok", -1, 0, MsgProductPriceRange},
		{"Ok", 1, 1_000_001, MsgProductStockRange},
	} {
		if _, err := p.WithDetails(tc.name, tc.price, tc.low, true); err == nil || messageOf(err) != tc.want {
			t.Fatalf("%q: err = %v, quería %q", tc.name, err, tc.want)
		}
	}
}

// S-F6 (ajustes): delta distinto de cero, motivo del catálogo, signo coherente
// con restock/loss, nota de hasta 200 y stock nunca negativo.
func TestApplyStockAdjustment(t *testing.T) {
	base := Product{ID: "p1", SalonID: "s1", Name: "Shampoo", Stock: 5, Active: true}

	up, mv, err := ApplyStockAdjustment(base, 4, StockRestock, "  compra  ")
	if err != nil {
		t.Fatalf("restock: %v", err)
	}
	if up.Stock != 9 {
		t.Fatalf("stock = 9, dio %d", up.Stock)
	}
	if mv.ID == "" || mv.ProductID != "p1" || mv.SalonID != "s1" || mv.Delta != 4 ||
		mv.Reason != StockRestock || mv.Note != "compra" || mv.CreatedAt.IsZero() {
		t.Fatalf("movimiento mal armado: %+v", mv)
	}
	if mv.SaleID != "" || mv.CreatedBy != "" {
		t.Fatalf("un ajuste no tiene venta: %+v", mv)
	}

	if up, _, err := ApplyStockAdjustment(base, -2, StockLoss, ""); err != nil || up.Stock != 3 {
		t.Fatalf("loss: %v %+v", err, up)
	}
	if up, _, err := ApplyStockAdjustment(base, -1, StockCorrection, "recuento"); err != nil || up.Stock != 4 {
		t.Fatalf("correction negativa: %v %+v", err, up)
	}
	if up, _, err := ApplyStockAdjustment(base, 2, StockCorrection, ""); err != nil || up.Stock != 7 {
		t.Fatalf("correction positiva: %v %+v", err, up)
	}

	cases := []struct {
		name   string
		delta  int
		reason StockReason
		note   string
		kind   error
		want   string
	}{
		{"cero", 0, StockRestock, "", ErrValidation, MsgProductAdjustZero},
		{"motivo initial", 1, StockInitial, "", ErrValidation, MsgProductAdjustReason},
		{"motivo sale", -1, StockSale, "", ErrValidation, MsgProductAdjustReason},
		{"motivo desconocido", 1, StockReason("otro"), "", ErrValidation, MsgProductAdjustReason},
		{"restock negativo", -1, StockRestock, "", ErrValidation, MsgProductAdjustSign},
		{"loss positivo", 1, StockLoss, "", ErrValidation, MsgProductAdjustSign},
		{"delta grande", 1_000_001, StockCorrection, "", ErrValidation, MsgProductStockRange},
		{"nota larga", 1, StockCorrection, strings.Repeat("n", 201), ErrValidation, MsgProductNoteTooLong},
		{"nota NUL", 1, StockCorrection, "a\x00b", ErrValidation, MsgProductInvalidChars},
		{"stock negativo", -6, StockLoss, "", ErrAlreadyExists, MsgProductNegativeStock},
		{"stock sobre el tope", 999_996, StockRestock, "", ErrValidation, MsgProductStockRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ApplyStockAdjustment(base, tc.delta, tc.reason, tc.note)
			if err == nil {
				t.Fatal("esperaba error")
			}
			if got := messageOf(err); got != tc.want {
				t.Fatalf("mensaje = %q, quería %q", got, tc.want)
			}
			if !errors.Is(err, tc.kind) {
				t.Fatalf("tipo = %v, quería %v", err, tc.kind)
			}
		})
	}
}

// D8: solo avisa si CRUZA de arriba del umbral a <= umbral. Si ya estaba bajo,
// no hay aviso nuevo.
func TestCrossedLowStock(t *testing.T) {
	before := Product{Stock: 6, LowStockThreshold: 5, Active: true}
	after := before
	after.Stock = 5
	if !CrossedLowStock(before, after) {
		t.Fatal("6 -> 5 con umbral 5 cruza")
	}
	// Ya estaba bajo: no vuelve a avisar.
	if CrossedLowStock(after, Product{Stock: 4, LowStockThreshold: 5, Active: true}) {
		t.Fatal("ya estaba bajo: no hay aviso")
	}
	// No llegó al umbral.
	if CrossedLowStock(before, Product{Stock: 6, LowStockThreshold: 5, Active: true}) {
		t.Fatal("no cruzó")
	}
	// Sin umbral o inactivo: nunca.
	if CrossedLowStock(Product{Stock: 3, Active: true}, Product{Stock: 0, Active: true}) {
		t.Fatal("umbral 0 no avisa")
	}
	if CrossedLowStock(Product{Stock: 3, LowStockThreshold: 2, Active: false}, Product{Stock: 1, LowStockThreshold: 2, Active: false}) {
		t.Fatal("inactivo no avisa")
	}
}

func TestStockReason_Valid(t *testing.T) {
	for _, r := range []StockReason{StockInitial, StockRestock, StockLoss, StockCorrection, StockSale} {
		if !r.Valid() {
			t.Fatalf("%q debería ser válido", r)
		}
	}
	if StockReason("").Valid() || StockReason("otros").Valid() {
		t.Fatal("motivo fuera del catálogo no es válido")
	}
}
