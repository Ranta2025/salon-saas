// Reglas de dominio de los gastos (F3b, D3/D4 del contrato F3b). Este archivo
// prueba lo que NO puede vivir en un handler: los límites del monto y de la
// fecha, la fecha local del salón (no la UTC) y los mensajes EXACTOS en español.
package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExpense_categorias(t *testing.T) {
	for _, c := range []ExpenseCategory{ExpenseRent, ExpenseSupplies, ExpenseUtilities, ExpenseOther} {
		if !c.Valid() {
			t.Fatalf("%q debería ser una categoría válida", c)
		}
	}
	for _, c := range []ExpenseCategory{"", "Rent", "renta", "otros", "salary"} {
		if c.Valid() {
			t.Fatalf("%q NO debería ser una categoría válida", c)
		}
	}
	for want, c := range map[string]ExpenseCategory{
		"Alquiler": ExpenseRent, "Insumos": ExpenseSupplies,
		"Servicios": ExpenseUtilities, "Otros": ExpenseOther,
	} {
		if got := c.Label(); got != want {
			t.Fatalf("Label() de %q = %q, esperaba %q", c, got, want)
		}
	}
}

// cdmx es la zona de la mayoría de los tests; el «hoy» es 20/3/2026 12:00 local.
var cdmx = mustLoc("America/Mexico_City")

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func TestExpense_validar_mensajes_exactos(t *testing.T) {
	now := time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC) // 12:00 en CDMX
	ok := ExpenseFields{Category: "rent", AmountCents: 150000, SpentOn: "2026-03-19", Description: "Alquiler de marzo"}
	if _, err := ok.Validate(now, cdmx); err != nil {
		t.Fatalf("un gasto válido no debería fallar: %v", err)
	}

	cases := []struct {
		name   string
		in     ExpenseFields
		want   string
		notErr bool
	}{
		{"monto cero", ExpenseFields{Category: "rent", AmountCents: 0, SpentOn: "2026-03-19"},
			"El monto debe ser mayor a cero", false},
		{"monto negativo", ExpenseFields{Category: "rent", AmountCents: -1, SpentOn: "2026-03-19"},
			"El monto debe ser mayor a cero", false},
		{"monto sobre el tope", ExpenseFields{Category: "rent", AmountCents: 2000000001, SpentOn: "2026-03-19"},
			"El monto no puede superar los 2000000000 centavos", false},
		{"fecha futura", ExpenseFields{Category: "rent", AmountCents: 1, SpentOn: "2026-03-21"},
			"La fecha del gasto no puede ser futura", false},
		{"fecha anterior a 2000", ExpenseFields{Category: "rent", AmountCents: 1, SpentOn: "1999-12-31"},
			"La fecha del gasto es demasiado antigua", false},
		{"fecha con barras", ExpenseFields{Category: "rent", AmountCents: 1, SpentOn: "19/03/2026"},
			"Fecha inválida: usá AAAA-MM-DD", false},
		{"fecha inexistente", ExpenseFields{Category: "rent", AmountCents: 1, SpentOn: "2026-02-30"},
			"Fecha inválida: usá AAAA-MM-DD", false},
		{"fecha vacía", ExpenseFields{Category: "rent", AmountCents: 1, SpentOn: ""},
			"Fecha inválida: usá AAAA-MM-DD", false},
		{"categoría desconocida", ExpenseFields{Category: "salary", AmountCents: 1, SpentOn: "2026-03-19"},
			"Categoría inválida: usá alquiler, insumos, servicios u otros", false},
		{"categoría vacía", ExpenseFields{Category: "", AmountCents: 1, SpentOn: "2026-03-19"},
			"Categoría inválida: usá alquiler, insumos, servicios u otros", false},
		{"descripción 201 runes", ExpenseFields{Category: "rent", AmountCents: 1, SpentOn: "2026-03-19",
			Description: strings.Repeat("ñ", 201)},
			"La descripción no puede superar los 200 caracteres", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.in.Validate(now, cdmx)
			if err == nil {
				t.Fatalf("esperaba error %q, no hubo error", tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("mensaje = %q, esperaba %q", err.Error(), tc.want)
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("el error tiene que ser de validación (400): %v", err)
			}
		})
	}
}

func TestExpense_bordes_validos(t *testing.T) {
	now := time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)
	cases := []ExpenseFields{
		{Category: "rent", AmountCents: 1, SpentOn: "2026-03-19"},
		{Category: "other", AmountCents: MaxExpenseCents, SpentOn: "2026-03-19"},
		// 200 runes multibyte: el tope cuenta caracteres, no bytes.
		{Category: "supplies", AmountCents: 500, SpentOn: "2026-03-19", Description: strings.Repeat("ñ", 200)},
		// «hoy» local del salón y el primer día aceptado.
		{Category: "utilities", AmountCents: 500, SpentOn: "2026-03-20"},
		{Category: "other", AmountCents: 500, SpentOn: "2000-01-01"},
		// Descripción vacía es válida.
		{Category: "other", AmountCents: 500, SpentOn: "2026-03-19", Description: ""},
	}
	for _, in := range cases {
		if _, err := in.Validate(now, cdmx); err != nil {
			t.Fatalf("%+v debería ser válido: %v", in, err)
		}
	}
}

// La fecha futura se mide en el día LOCAL del salón, no en UTC: a las 11:00Z del
// 10/3 en Auckland ya es 11/3, así que el 11/3 es HOY y se acepta (en UTC sería
// futuro).
func TestExpense_hoy_es_el_dia_local_del_salon(t *testing.T) {
	now := time.Date(2026, 3, 10, 11, 0, 0, 0, time.UTC)
	auckland := mustLoc("Pacific/Auckland")
	in := ExpenseFields{Category: "rent", AmountCents: 100, SpentOn: "2026-03-11"}
	if _, err := in.Validate(now, auckland); err != nil {
		t.Fatalf("en Auckland el 11/3 es hoy: %v", err)
	}
	if _, err := in.Validate(now, time.UTC); err == nil || err.Error() != "La fecha del gasto no puede ser futura" {
		t.Fatalf("en UTC el 11/3 es futuro: %v", err)
	}
}

func TestExpense_normaliza_categoria_y_descripcion(t *testing.T) {
	now := time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)
	in := ExpenseFields{Category: "  rent  ", AmountCents: 100, SpentOn: " 2026-03-19 ", Description: "  Insumos del mes  "}
	got, err := in.Validate(now, cdmx)
	if err != nil {
		t.Fatalf("validar: %v", err)
	}
	if got.Category != "rent" || got.SpentOn != "2026-03-19" || got.Description != "Insumos del mes" {
		t.Fatalf("normalización = %+v", got)
	}
}

func TestNewExpense_arma_el_gasto(t *testing.T) {
	now := time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)
	fields := ExpenseFields{Category: "utilities", AmountCents: 12345, SpentOn: "2026-03-19", Description: "Luz"}
	e := NewExpense("s1", fields, "u-owner", now)
	if e.ID == "" || e.SalonID != "s1" || e.Category != ExpenseUtilities ||
		e.AmountCents != 12345 || e.SpentOn != "2026-03-19" || e.Description != "Luz" ||
		e.CreatedBy != "u-owner" || !e.CreatedAt.Equal(now.UTC()) {
		t.Fatalf("gasto mal armado: %+v", e)
	}
	// El PUT reemplaza los cuatro campos y no toca identidad ni creador.
	got := e.WithFields(ExpenseFields{Category: "rent", AmountCents: 1, SpentOn: "2026-03-01", Description: ""})
	if got.ID != e.ID || got.SalonID != e.SalonID || got.CreatedBy != e.CreatedBy ||
		!got.CreatedAt.Equal(e.CreatedAt) {
		t.Fatalf("WithFields tocó la identidad: %+v vs %+v", got, e)
	}
	if got.Category != ExpenseRent || got.AmountCents != 1 || got.SpentOn != "2026-03-01" || got.Description != "" {
		t.Fatalf("WithFields no reemplazó los campos: %+v", got)
	}
}

func TestExpenseRange_validacion(t *testing.T) {
	if from, to, err := ExpenseRange("2026-03-01", "2026-03-31"); err != nil || from != "2026-03-01" || to != "2026-03-31" {
		t.Fatalf("rango válido: %q %q %v", from, to, err)
	}
	if _, _, err := ExpenseRange("2026-01-01", "2027-01-01"); err != nil {
		t.Fatalf("366 días inclusive debería ser válido: %v", err)
	}
	for _, tc := range []struct{ from, to string }{
		{"", "2026-03-31"},
		{"2026-03-01", ""},
		{"2026-03-31", "2026-03-01"},
		{"2026-01-01", "2027-01-02"}, // 367 días
		{"10/03/2026", "2026-03-31"},
		{"2026-03-01", "2026-13-01"},
	} {
		_, _, err := ExpenseRange(tc.from, tc.to)
		if err == nil || err.Error() != "Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días" {
			t.Fatalf("%q..%q: %v", tc.from, tc.to, err)
		}
	}
}
