// F5 (D4/D5/D6/D7): alergias de la ficha (dato de salud), la ficha editable y
// el resumen del historial con el día LOCAL del salón.
package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func clienteBase(t *testing.T) Client {
	t.Helper()
	c, err := NewClient("s1", "Lía Fernández", "11 5555-1234", "lia@mail.com", "prefiere la mañana")
	if err != nil {
		t.Fatalf("cliente base: %v", err)
	}
	return c
}

// D4: la alergia es texto libre de hasta 500 caracteres (se mide en runes) y un
// NUL es 400 con el texto exacto, nunca un 500 de Postgres.
func TestClientAllergies_limites_y_NUL(t *testing.T) {
	c := clienteBase(t)

	ok, err := c.WithAllergies(strings.Repeat("a", 500))
	if err != nil {
		t.Fatalf("500 caracteres debe ser válido: %v", err)
	}
	if len(ok.Allergies) != 500 || ok.ID != c.ID || ok.Name != c.Name {
		t.Fatalf("las alergias no se guardaron o pisaron otros campos: %+v", ok)
	}
	// 500 runes multibyte también entran (el tope es por caracteres, no bytes).
	if _, err := c.WithAllergies(strings.Repeat("á", 500)); err != nil {
		t.Fatalf("500 caracteres multibyte debe ser válido: %v", err)
	}

	_, err = c.WithAllergies(strings.Repeat("a", 501))
	if err == nil || err.Error() != "Las alergias no pueden superar los 500 caracteres" {
		t.Fatalf("501 caracteres: %v", err)
	}
	if _, err := c.WithAllergies(strings.Repeat("á", 501)); err == nil ||
		err.Error() != "Las alergias no pueden superar los 500 caracteres" {
		t.Fatalf("501 caracteres multibyte: %v", err)
	}

	if _, err := c.WithAllergies("maní\x00y nuez"); err == nil ||
		err.Error() != "La ficha tiene caracteres no válidos" {
		t.Fatalf("un NUL debe dar el 400 exacto, dio %v", err)
	}
	// Vacío es válido: "sin alergias" no es un dato faltante.
	empty, err := c.WithAllergies("   ")
	if err != nil || empty.Allergies != "" {
		t.Fatalf("vacío debe quedar en \"\": %q %v", empty.Allergies, err)
	}
}

// El alta con alergias es ADITIVA: NewClient mantiene su firma y su resultado.
func TestNewClientWithAllergies(t *testing.T) {
	c, err := NewClientWithAllergies("s1", "Lía", "555", "", "", "  nickel  ")
	if err != nil {
		t.Fatalf("alta con alergias: %v", err)
	}
	if c.Allergies != "nickel" {
		t.Fatalf("las alergias se recortan: %q", c.Allergies)
	}
	if _, err := NewClientWithAllergies("s1", "Lía", "555", "", "", strings.Repeat("x", 501)); err == nil {
		t.Fatal("501 caracteres en el alta debe fallar")
	}
	// El alta original sigue dejando las alergias vacías.
	base := clienteBase(t)
	if base.Allergies != "" {
		t.Fatalf("NewClient no debe cambiar: %q", base.Allergies)
	}
}

// D5: PUT REEMPLAZA los cinco campos. Valida igual que el alta y un NUL en
// CUALQUIERA de los cinco es el 400 exacto (lección H20/H21: si llega a
// Postgres, es un 500).
func TestClientUpdated_reemplaza_y_valida(t *testing.T) {
	c := clienteBase(t)
	created := c.CreatedAt

	up, err := c.Updated("  Lía G.  ", "  11 4444-0000 ", " LIA@Mail.com ", " sin notas ", " nickel ")
	if err != nil {
		t.Fatalf("update válido: %v", err)
	}
	if up.ID != c.ID || up.SalonID != c.SalonID || !up.CreatedAt.Equal(created) {
		t.Fatalf("el update no debe tocar identidad: %+v", up)
	}
	if up.Name != "Lía G." || up.Phone != "11 4444-0000" || up.Email != "lia@mail.com" ||
		up.Notes != "sin notas" || up.Allergies != "nickel" {
		t.Fatalf("campos mal reemplazados: %+v", up)
	}
	// Email, notas y alergias vacíos son válidos.
	if _, err := c.Updated("Lía", "555", "", "", ""); err != nil {
		t.Fatalf("vacíos opcionales: %v", err)
	}

	nul := "a\x00b"
	cases := []struct{ name, phone, email, notes, alrg string }{
		{nul, "555", "", "", ""},
		{"Lía", nul, "", "", ""},
		{"Lía", "555", nul, "", ""},
		{"Lía", "555", "", nul, ""},
		{"Lía", "555", "", "", nul},
	}
	for i, tc := range cases {
		_, err := c.Updated(tc.name, tc.phone, tc.email, tc.notes, tc.alrg)
		if err == nil || err.Error() != "La ficha tiene caracteres no válidos" {
			t.Fatalf("caso %d con NUL: %v", i, err)
		}
	}

	// Las validaciones del alta se repiten: nombre obligatorio y topes.
	if _, err := c.Updated("", "555", "", "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre vacío: %v", err)
	}
	if _, err := c.Updated(strings.Repeat("n", 121), "555", "", "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre largo: %v", err)
	}
	if _, err := c.Updated("Lía", strings.Repeat("1", 41), "", "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("teléfono largo: %v", err)
	}
	if _, err := c.Updated("Lía", "555", "no-es-email", "", ""); err == nil || err.Error() != "Email inválido" {
		t.Fatalf("email inválido: %v", err)
	}
	if _, err := c.Updated("Lía", "555", "", strings.Repeat("n", 2001), ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("notas largas: %v", err)
	}
	if _, err := c.Updated("Lía", "555", "", "", strings.Repeat("a", 501)); err == nil ||
		err.Error() != "Las alergias no pueden superar los 500 caracteres" {
		t.Fatalf("alergias largas: %v", err)
	}
}

// D7: last_visit es el día LOCAL del salón del turno `done` más reciente, y
// next_visit es el próximo `booked` que ya calculó la lectura. Los bordes de
// 23:59:59 y 00:00:00 locales tienen que caer en días distintos, con y sin
// horario de verano.
func TestClientHistorySummary_dia_local(t *testing.T) {
	done := func(s string) *time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("fecha de prueba %q: %v", s, err)
		}
		return &ts
	}
	next := done("2026-06-02T16:00:00Z")
	data := ClientHistoryData{
		Visits: 3, NoShows: 1, Cancelled: 2, Upcoming: 1,
		LastDoneAt:   done("2026-01-01T00:00:00Z"),
		NextBookedAt: next,
		Bookings:     []ClientHistoryBooking{{ID: "b1"}},
		Spend:        ClientSpend{SalesCount: 2, TotalCents: 1500},
		Sales:        []ClientHistorySale{{ID: "s1", TotalCents: 1500}},
	}
	cases := []struct {
		tz         string
		lastDone   string
		wantDay    string
		wantNextOK bool
	}{
		// America/Mexico_City no tiene horario de verano desde 2022 (UTC-6).
		{"America/Mexico_City", "2026-03-08T23:59:59-06:00", "2026-03-08", true},
		{"America/Mexico_City", "2026-03-09T00:00:00-06:00", "2026-03-09", true},
		// America/New_York: 2026-03-08 adelanta el reloj (día de 23 h).
		{"America/New_York", "2026-03-08T04:59:59Z", "2026-03-07", true},
		{"America/New_York", "2026-03-08T05:00:00Z", "2026-03-08", true},
		// 2026-11-01 atrasa el reloj (día de 25 h): las 23:59:59 locales siguen
		// siendo el 1 de noviembre, aunque en UTC ya sea el día 2.
		{"America/New_York", "2026-11-02T04:59:59Z", "2026-11-01", true},
		{"America/New_York", "2026-11-02T05:00:00Z", "2026-11-02", true},
		// Pacific/Auckland (UTC+13 en enero): medianoche local = 11:00Z del día anterior.
		{"Pacific/Auckland", "2026-01-15T23:59:59+13:00", "2026-01-15", true},
		{"Pacific/Auckland", "2026-01-15T00:00:00+13:00", "2026-01-15", true},
	}
	for _, tc := range cases {
		loc, err := time.LoadLocation(tc.tz)
		if err != nil {
			t.Fatalf("zona %s: %v", tc.tz, err)
		}
		copy := data
		copy.LastDoneAt = done(tc.lastDone)
		sum := NewClientHistorySummary(copy, loc)
		if sum.LastVisit != tc.wantDay {
			t.Fatalf("%s: turno %s -> last_visit %q, esperaba %q", tc.tz, tc.lastDone, sum.LastVisit, tc.wantDay)
		}
		if sum.Timezone != tc.tz {
			t.Fatalf("timezone: %q, esperaba %q", sum.Timezone, tc.tz)
		}
		if sum.Visits != 3 || sum.NoShows != 1 || sum.Cancelled != 2 || sum.Upcoming != 1 {
			t.Fatalf("los conteos deben copiarse: %+v", sum)
		}
		if sum.NextVisit == nil || !sum.NextVisit.Equal(*next) {
			t.Fatalf("next_visit: %v, esperaba %v", sum.NextVisit, next)
		}
	}

	// Sin turnos hechos ni próximos: null (no una fecha inventada).
	empty := NewClientHistorySummary(ClientHistoryData{}, time.UTC)
	if empty.LastVisit != "" || empty.NextVisit != nil {
		t.Fatalf("sin historial debe ser vacío: %+v", empty)
	}
}
