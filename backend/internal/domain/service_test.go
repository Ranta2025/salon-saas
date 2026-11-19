package domain

import (
	"errors"
	"testing"
)

func TestNewService_ok(t *testing.T) {
	s, err := NewService("s1", "  Corte de dama  ", 45, 120000, 30)
	if err != nil {
		t.Fatalf("servicio válido: %v", err)
	}
	if s.ID == "" {
		t.Fatal("el servicio debe nacer con id")
	}
	if s.SalonID != "s1" {
		t.Fatalf("salon_id: %q", s.SalonID)
	}
	if s.Name != "Corte de dama" {
		t.Fatalf("el nombre se recorta, dio %q", s.Name)
	}
	if s.DurationMin != 45 || s.PriceCents != 120000 || s.CommissionPct != 30 {
		t.Fatalf("campos mal copiados: %+v", s)
	}
	if !s.Active {
		t.Fatal("un servicio nuevo nace activo")
	}
}

func TestNewService_rechaza_invalidos(t *testing.T) {
	cases := []struct {
		name       string
		salonID    string
		n          string
		dur, price int
		comm       int
	}{
		{"nombre vacío", "s1", "   ", 30, 1000, 0},
		{"sin salón", "", "Corte", 30, 1000, 0},
		{"duración cero", "s1", "Corte", 0, 1000, 0},
		{"duración negativa", "s1", "Corte", -15, 1000, 0},
		{"precio negativo", "s1", "Corte", 30, -1, 0},
		{"comisión negativa", "s1", "Corte", 30, 1000, -1},
		{"comisión arriba de 100", "s1", "Corte", 30, 1000, 101},
	}
	for _, c := range cases {
		if _, err := NewService(c.salonID, c.n, c.dur, c.price, c.comm); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: esperaba ErrValidation, dio %v", c.name, err)
		}
	}
	// Fronteras válidas: precio 0 (cortesía) y comisión 100 / 0.
	if _, err := NewService("s1", "Cortesía", 15, 0, 100); err != nil {
		t.Fatalf("precio 0 y comisión 100 deben ser válidos: %v", err)
	}
	if _, err := NewService("s1", "Sin comisión", 15, 1000, 0); err != nil {
		t.Fatalf("comisión 0 debe ser válida: %v", err)
	}
}

func TestService_WithDetails(t *testing.T) {
	s, err := NewService("s1", "Corte", 30, 1000, 10)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	up, err := s.WithDetails("Color", 90, 250000, 25, false)
	if err != nil {
		t.Fatalf("actualizar: %v", err)
	}
	if up.ID != s.ID || up.SalonID != s.SalonID {
		t.Fatal("la identidad no cambia al actualizar")
	}
	if up.Name != "Color" || up.DurationMin != 90 || up.PriceCents != 250000 || up.CommissionPct != 25 {
		t.Fatalf("no aplicó los cambios: %+v", up)
	}
	if up.Active {
		t.Fatal("active=false debe desactivar")
	}
	// Mismas reglas que al crear.
	if _, err := s.WithDetails(" ", 90, 100, 0, true); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre vacío debe dar ErrValidation, dio %v", err)
	}
	if _, err := s.WithDetails("Color", 0, 100, 0, true); !errors.Is(err, ErrValidation) {
		t.Fatalf("duración 0 debe dar ErrValidation, dio %v", err)
	}
}
