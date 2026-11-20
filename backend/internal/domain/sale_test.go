package domain

import (
	"errors"
	"testing"
)

func TestNewSale_total_en_servidor(t *testing.T) {
	// El dominio NO recibe total: lo calcula de los ítems. Es el candado
	// estructural contra un cliente que mande su propio total.
	s, err := NewSale("s1", "prof1", "Lía", []SaleItem{
		{ServiceID: "svc-corte", Qty: 2, UnitPriceCents: 150000},
		{ServiceID: "svc-brushing", Qty: 1, UnitPriceCents: 80000},
	}, MethodCard)
	if err != nil {
		t.Fatalf("venta válida: %v", err)
	}
	if s.TotalCents != 380000 {
		t.Fatalf("total = 2*150000 + 1*80000 = 380000, dio %d", s.TotalCents)
	}
	if s.ID == "" || s.SalonID != "s1" || s.ProfessionalID != "prof1" {
		t.Fatalf("campos de identidad: %+v", s)
	}
	if s.ClientName != "Lía" || s.Method != MethodCard {
		t.Fatalf("cliente/método: %+v", s)
	}
	if s.CreatedAt.IsZero() || s.CreatedAt.Location() == nil || s.CreatedAt.UTC() != s.CreatedAt {
		t.Fatalf("created_at debe nacer en UTC: %v", s.CreatedAt)
	}
	// Precio 0 es válido (cortesía) y suma 0.
	z, err := NewSale("s1", "prof1", "Ana", []SaleItem{{ServiceID: "svc-x", Qty: 3, UnitPriceCents: 0}}, MethodCash)
	if err != nil {
		t.Fatalf("cortesía: %v", err)
	}
	if z.TotalCents != 0 {
		t.Fatalf("cortesía debe dar 0, dio %d", z.TotalCents)
	}
}

func TestNewSale_rechaza_invalidos(t *testing.T) {
	okItem := SaleItem{ServiceID: "svc-corte", Qty: 1, UnitPriceCents: 100000}
	cases := []struct {
		name        string
		salon, prof string
		client      string
		items       []SaleItem
		method      PaymentMethod
	}{
		{"sin ítems", "s1", "prof1", "Lía", nil, MethodCash},
		{"ítems vacíos", "s1", "prof1", "Lía", []SaleItem{}, MethodCash},
		{"qty 0", "s1", "prof1", "Lía", []SaleItem{{ServiceID: "svc-corte", Qty: 0, UnitPriceCents: 100}}, MethodCash},
		{"qty negativa", "s1", "prof1", "Lía", []SaleItem{{ServiceID: "svc-corte", Qty: -2, UnitPriceCents: 100}}, MethodCash},
		{"precio unitario negativo", "s1", "prof1", "Lía", []SaleItem{{ServiceID: "svc-corte", Qty: 1, UnitPriceCents: -1}}, MethodCash},
		{"sin service_id", "s1", "prof1", "Lía", []SaleItem{{ServiceID: " ", Qty: 1, UnitPriceCents: 100}}, MethodCash},
		{"cliente vacío", "s1", "prof1", "  ", []SaleItem{okItem}, MethodCash},
		{"sin salón", "", "prof1", "Lía", []SaleItem{okItem}, MethodCash},
		{"sin profesional", "s1", "", "Lía", []SaleItem{okItem}, MethodCash},
		{"método vacío", "s1", "prof1", "Lía", []SaleItem{okItem}, ""},
		{"método desconocido", "s1", "prof1", "Lía", []SaleItem{okItem}, PaymentMethod("bitcoin")},
	}
	for _, c := range cases {
		if _, err := NewSale(c.salon, c.prof, c.client, c.items, c.method); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: esperaba ErrValidation, dio %v", c.name, err)
		}
	}
	// Un ítem inválido no puede esconderse detrás de uno válido.
	if _, err := NewSale("s1", "prof1", "Lía", []SaleItem{okItem, {ServiceID: "svc-y", Qty: 0, UnitPriceCents: 100}}, MethodCash); !errors.Is(err, ErrValidation) {
		t.Fatalf("ítem inválido dentro de la lista: %v", err)
	}
}

func TestPaymentMethod_valido(t *testing.T) {
	for _, m := range []PaymentMethod{MethodCash, MethodCard, MethodTransfer} {
		if !m.Valid() {
			t.Fatalf("%q debería ser válido", m)
		}
	}
	if PaymentMethod("").Valid() || PaymentMethod("efectivo").Valid() {
		t.Fatal("métodos fuera del catálogo deben ser inválidos")
	}
}
