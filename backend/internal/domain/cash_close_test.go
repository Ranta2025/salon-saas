package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewCashClose_diferencia(t *testing.T) {
	open := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	closed := open.Add(10 * time.Hour)

	c, err := NewCashClose("s1", open, closed, 250000, 248000, "  faltó vuelto  ")
	if err != nil {
		t.Fatalf("cierre válido: %v", err)
	}
	if c.DifferenceCents != -2000 {
		t.Fatalf("diferencia = contado - sistema = 248000-250000 = -2000, dio %d", c.DifferenceCents)
	}
	if c.Note != "faltó vuelto" {
		t.Fatalf("la nota se recorta, dio %q", c.Note)
	}
	if c.ID == "" || c.SalonID != "s1" {
		t.Fatalf("identidad: %+v", c)
	}
	if !c.OpenedAt.Equal(open) || !c.ClosedAt.Equal(closed) {
		t.Fatalf("ventana: %v - %v", c.OpenedAt, c.ClosedAt)
	}

	// Sobrante: el signo es la info contable que se mira al cerrar.
	up, err := NewCashClose("s1", open, closed, 100000, 101500, "")
	if err != nil {
		t.Fatalf("sobrante: %v", err)
	}
	if up.DifferenceCents != 1500 {
		t.Fatalf("sobrante: esperaba +1500, dio %d", up.DifferenceCents)
	}
	// Caja cuadrada.
	even, err := NewCashClose("s1", open, closed, 100000, 100000, "")
	if err != nil || even.DifferenceCents != 0 {
		t.Fatalf("cuadre exacto: %v / %d", err, even.DifferenceCents)
	}
}

func TestNewCashClose_rechaza_invalidos(t *testing.T) {
	open := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		salonID string
		opened  time.Time
		closed  time.Time
		system  int
		counted int
	}{
		{"sin salón", "", open, open.Add(time.Hour), 100, 100},
		{"contado negativo", "s1", open, open.Add(time.Hour), 100, -1},
		{"sistema negativo", "s1", open, open.Add(time.Hour), -1, 100},
		{"cierre antes de apertura", "s1", open, open.Add(-time.Minute), 100, 100},
	}
	for _, c := range cases {
		if _, err := NewCashClose(c.salonID, c.opened, c.closed, c.system, c.counted, ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: esperaba ErrValidation, dio %v", c.name, err)
		}
	}
}
