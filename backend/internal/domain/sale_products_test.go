package domain

import (
	"strings"
	"testing"
)

// D5: una venta solo de productos es válida y su total sale de las líneas del
// catálogo; una mixta suma servicios + productos.
func TestNewSaleWithProducts_totals(t *testing.T) {
	only, err := NewSaleWithProducts("s1", "prof1", "Lía", nil, []SaleProduct{
		{ProductID: "p1", Qty: 2, UnitPriceCents: 50000},
		{ProductID: "p2", Qty: 1, UnitPriceCents: 30000},
	}, MethodCard)
	if err != nil {
		t.Fatalf("venta solo de productos: %v", err)
	}
	if only.ID == "" || only.SalonID != "s1" || only.ProfessionalID != "prof1" || only.ClientName != "Lía" {
		t.Fatalf("venta mal construida: %+v", only.Sale)
	}
	if only.TotalCents != 130000 || only.ProductsCents != 130000 {
		t.Fatalf("total/productos = %d/%d, quería 130000/130000", only.TotalCents, only.ProductsCents)
	}
	if len(only.Items) != 0 {
		t.Fatalf("no hay líneas de servicio: %+v", only.Items)
	}
	if len(only.Products) != 2 || only.Products[0].SubtotalCents() != 100000 {
		t.Fatalf("líneas de producto: %+v", only.Products)
	}
	if only.CreatedAt.IsZero() || only.Method != MethodCard {
		t.Fatalf("created_at/method: %+v", only.Sale)
	}

	mixed, err := NewSaleWithProducts("s1", "prof1", "Lía", []SaleItem{
		{ServiceID: "svc1", Qty: 1, UnitPriceCents: 150000},
	}, []SaleProduct{{ProductID: "p1", Qty: 1, UnitPriceCents: 50000}}, MethodCash)
	if err != nil {
		t.Fatalf("venta mixta: %v", err)
	}
	if mixed.TotalCents != 200000 || mixed.ProductsCents != 50000 {
		t.Fatalf("mixta: total=%d productos=%d", mixed.TotalCents, mixed.ProductsCents)
	}
	if len(mixed.Items) != 1 || mixed.Items[0].ServiceID != "svc1" {
		t.Fatalf("ítems de servicio: %+v", mixed.Items)
	}
}

// Sin ninguna línea la venta no existe (misma regla que NewSale).
func TestNewSaleWithProducts_needs_a_line(t *testing.T) {
	_, err := NewSaleWithProducts("s1", "prof1", "Lía", nil, nil, MethodCash)
	if err == nil || !strings.Contains(messageOf(err), "la venta necesita al menos un ítem") {
		t.Fatalf("sin líneas: %v", err)
	}
}

// D5: tope de 50 líneas, cantidad 1..1000, producto obligatorio y precio de
// catálogo dentro de rango (la DB repite los CHECK).
func TestValidateSaleProducts(t *testing.T) {
	ok := func(n int) []SaleProduct {
		out := make([]SaleProduct, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, SaleProduct{ProductID: "p" + string(rune('a'+i%26)), Qty: 1, UnitPriceCents: 100})
		}
		return out
	}
	if err := ValidateSaleProducts(ok(50)); err != nil {
		t.Fatalf("50 líneas: %v", err)
	}
	if err := ValidateSaleProducts(ok(51)); err == nil || messageOf(err) != MsgProductLines {
		t.Fatalf("51 líneas: %v", err)
	}

	cases := []struct {
		name string
		line SaleProduct
		want string
	}{
		{"sin producto", SaleProduct{Qty: 1}, MsgProductInvalid},
		{"producto en blanco", SaleProduct{ProductID: "  ", Qty: 1}, MsgProductInvalid},
		{"qty 0", SaleProduct{ProductID: "p1", Qty: 0}, MsgProductQty},
		{"qty negativa", SaleProduct{ProductID: "p1", Qty: -1}, MsgProductQty},
		{"qty 1001", SaleProduct{ProductID: "p1", Qty: 1001}, MsgProductQty},
		{"precio negativo", SaleProduct{ProductID: "p1", Qty: 1, UnitPriceCents: -1}, MsgProductPriceRange},
		{"precio alto", SaleProduct{ProductID: "p1", Qty: 1, UnitPriceCents: 10_000_001}, MsgProductPriceRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSaleProducts([]SaleProduct{tc.line})
			if err == nil || messageOf(err) != tc.want {
				t.Fatalf("err = %v, quería %q", err, tc.want)
			}
		})
	}

	// El total no puede pasar el tope de la DB (INTEGER de Postgres).
	if _, err := NewSaleWithProducts("s1", "p", "X", nil, []SaleProduct{
		{ProductID: "p1", Qty: 1000, UnitPriceCents: 10_000_000},
	}, MethodCash); err == nil || !strings.Contains(messageOf(err), "total de la venta supera el máximo") {
		t.Fatalf("total fuera de rango: %v", err)
	}
}

// PRESERVE: la venta clásica (sin productos) sigue rechazando el pedido vacío
// y no conoce productos: NewSale no cambia.
func TestNewSale_unchangedWithProducts(t *testing.T) {
	if _, err := NewSale("s1", "prof1", "Lía", nil, MethodCash); err == nil {
		t.Fatal("NewSale sin ítems debe seguir rechazando")
	}
}
