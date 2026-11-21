package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// B-03: límites de dominio en ventas. Un monto absurdo (2_000_000_000
// centavos) tiene que rechazarse en el dominio: si entra, el SUM::int de
// Postgres revienta con 22003 y el dashboard/cierre quedan rotos para siempre.
func TestSale_rechaza_montos_fuera_de_rango(t *testing.T) {
	big := 2_000_000_000 // 20 millones de pesos en centavos
	if _, err := NewSale("s1", "p1", "Lía", []SaleItem{
		{ServiceID: "svc1", Qty: 1, UnitPriceCents: big},
	}, MethodCash); !errors.Is(err, ErrValidation) {
		t.Fatalf("precio unitario %d debe dar ErrValidation, dio %v", big, err)
	}

	// Precio por encima del tope permitido (10_000_000 centavos).
	if _, err := NewSale("s1", "p1", "Lía", []SaleItem{
		{ServiceID: "svc1", Qty: 1, UnitPriceCents: MaxSaleUnitPriceCents + 1},
	}, MethodCash); !errors.Is(err, ErrValidation) {
		t.Fatalf("precio > %d debe dar ErrValidation, dio %v", MaxSaleUnitPriceCents, err)
	}
	// El tope exacto sí pasa (borde cerrado).
	if _, err := NewSale("s1", "p1", "Lía", []SaleItem{
		{ServiceID: "svc1", Qty: 1, UnitPriceCents: MaxSaleUnitPriceCents},
	}, MethodCash); err != nil {
		t.Fatalf("el tope exacto debe aceptarse: %v", err)
	}

	// Cantidad por ítem.
	if _, err := NewSale("s1", "p1", "Lía", []SaleItem{
		{ServiceID: "svc1", Qty: MaxSaleItemQty + 1, UnitPriceCents: 100},
	}, MethodCash); !errors.Is(err, ErrValidation) {
		t.Fatalf("qty > %d debe dar ErrValidation, dio %v", MaxSaleItemQty, err)
	}

	// Cantidad de ítems.
	items := make([]SaleItem, MaxSaleItems+1)
	for i := range items {
		items[i] = SaleItem{ServiceID: "svc1", Qty: 1, UnitPriceCents: 100}
	}
	if _, err := NewSale("s1", "p1", "Lía", items, MethodCash); !errors.Is(err, ErrValidation) {
		t.Fatalf("más de %d ítems debe dar ErrValidation, dio %v", MaxSaleItems, err)
	}

	// Overflow del total: cada línea dentro de rango, el total no cabe.
	overflow := []SaleItem{
		{ServiceID: "svc1", Qty: MaxSaleItemQty, UnitPriceCents: MaxSaleUnitPriceCents},
	}
	if _, err := NewSale("s1", "p1", "Lía", overflow, MethodCash); !errors.Is(err, ErrValidation) {
		t.Fatalf("el total no cabe en el límite: esperaba ErrValidation, dio %v", err)
	}
}

// ValidateSaleItems valida el pedido crudo (el que manda el cliente) sin
// construir la venta: es lo que permite rechazar un precio manipulado fuera de
// rango ANTES de reemplazarlo por el del catálogo.
func TestValidateSaleItems_rechaza_el_pedido_crudo(t *testing.T) {
	if err := ValidateSaleItems([]SaleItem{{ServiceID: "svc1", Qty: 1, UnitPriceCents: 2_000_000_000}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("pedido crudo con 2e9 debe dar ErrValidation, dio %v", err)
	}
	if err := ValidateSaleItems([]SaleItem{{ServiceID: "svc1", Qty: 1, UnitPriceCents: 150000}}); err != nil {
		t.Fatalf("pedido válido rechazado: %v", err)
	}
}

// B-18: una reserva no dura más de 12 h ni arranca en el pasado.
func TestBooking_duracion_y_pasado(t *testing.T) {
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	start := day.Add(9 * time.Hour)
	if _, err := NewBooking("s", "p1", "Lía", "", start, start.Add(MaxBookingDuration+time.Minute)); !errors.Is(err, ErrValidation) {
		t.Fatalf("más de %v debe dar ErrValidation, dio %v", MaxBookingDuration, err)
	}
	if _, err := NewBooking("s", "p1", "Lía", "", start, start.Add(MaxBookingDuration)); err != nil {
		t.Fatalf("el tope exacto debe aceptarse: %v", err)
	}
	past := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := NewBooking("s", "p1", "Lía", "", past, past.Add(time.Hour)); !errors.Is(err, ErrValidation) {
		t.Fatalf("reserva en el pasado debe dar ErrValidation, dio %v", err)
	}
}

// B-16: los campos de texto tienen tope. Sin esto un body de 10 MB entra a la
// DB y después rompe listados y pantallas.
func TestText_limites(t *testing.T) {
	long := strings.Repeat("a", MaxTextName+1)
	if _, err := NewSalon(long, "salon-largo"); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre de salón largo debe dar ErrValidation, dio %v", err)
	}
	if _, err := NewService("s1", long, 30, 1000, 10); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre de servicio largo debe dar ErrValidation, dio %v", err)
	}
	if _, err := NewClient("s1", long, "", "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre de cliente largo debe dar ErrValidation, dio %v", err)
	}
	if _, err := NewClient("s1", "Lía", strings.Repeat("1", MaxTextPhone+1), "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("teléfono largo debe dar ErrValidation, dio %v", err)
	}
	if _, err := NewClient("s1", "Lía", "", "", strings.Repeat("n", MaxTextNotes+1)); !errors.Is(err, ErrValidation) {
		t.Fatalf("notas largas deben dar ErrValidation, dio %v", err)
	}
	if _, err := NewUser(long, "ana@mail.com", "h"); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre de usuario largo debe dar ErrValidation, dio %v", err)
	}
	if _, err := NewNotification("s1", "", strings.Repeat("t", MaxTextTitle+1), "", KindInfo); !errors.Is(err, ErrValidation) {
		t.Fatalf("título largo debe dar ErrValidation, dio %v", err)
	}
	if _, err := NewNotification("s1", "", "ok", strings.Repeat("b", MaxTextBody+1), KindInfo); !errors.Is(err, ErrValidation) {
		t.Fatalf("cuerpo largo debe dar ErrValidation, dio %v", err)
	}
	if _, err := NewBooking("s", "p1", strings.Repeat("c", MaxTextName+1), "", time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour)); !errors.Is(err, ErrValidation) {
		t.Fatalf("cliente de reserva largo debe dar ErrValidation, dio %v", err)
	}
	// Los valores válidos siguen entrando.
	if _, err := NewClient("s1", strings.Repeat("a", MaxTextName), "", "", strings.Repeat("n", MaxTextNotes)); err != nil {
		t.Fatalf("el tope exacto debe aceptarse: %v", err)
	}
}

// B-06: la clave mide entre 8 y 72 bytes (bcrypt trunca en 72).
func TestValidatePassword(t *testing.T) {
	for _, bad := range []string{"", "corta", strings.Repeat("x", MinPasswordBytes-1), strings.Repeat("x", MaxPasswordBytes+1)} {
		if err := ValidatePassword(bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("clave de %d bytes debe dar ErrValidation, dio %v", len(bad), err)
		}
	}
	for _, ok := range []string{strings.Repeat("x", MinPasswordBytes), strings.Repeat("x", MaxPasswordBytes)} {
		if err := ValidatePassword(ok); err != nil {
			t.Fatalf("clave de %d bytes debe aceptarse: %v", len(ok), err)
		}
	}
	// El tope es en BYTES, no en runes: "ñ" ocupa 2 bytes.
	accented := strings.Repeat("ñ", 37) // 74 bytes
	if err := ValidatePassword(accented); !errors.Is(err, ErrValidation) {
		t.Fatalf("clave de %d bytes debe dar ErrValidation, dio %v", len(accented), err)
	}
}

// NormalizeEmail es la única forma de comparar emails: minúsculas y sin
// espacios. Sin esto "Ana@Mail.com" y "ana@mail.com" son usuarios distintos y
// el login falla según cómo se tipeó.
func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Ana@Mail.COM "); got != "ana@mail.com" {
		t.Fatalf("NormalizeEmail = %q", got)
	}
}
