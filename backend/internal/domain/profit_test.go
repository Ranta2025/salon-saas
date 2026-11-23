// Período y armado del reporte de ganancia neta (F3b, D1/D2/D7 del contrato).
// Este archivo prueba lo que NO puede vivir en el handler ni en SQL: el día es
// el LOCAL del salón (no UTC), los días de 23 y 25 horas entran enteros, el
// reporte rellena con ceros y los totales son la suma EXACTA de los días.
package domain

import (
	"testing"
	"time"
)

var (
	ny       = mustLoc("America/New_York")
	auckland = mustLoc("Pacific/Auckland")
)

// nowProfit es el «hoy» de estos tests: 20/3/2026 12:00 en Nueva York.
var nowProfit = time.Date(2026, 3, 20, 16, 0, 0, 0, time.UTC)

func TestProfitKind_valid(t *testing.T) {
	for _, k := range []ProfitKind{ProfitDay, ProfitWeek, ProfitMonth} {
		if !k.Valid() {
			t.Fatalf("%q debería ser válido", k)
		}
	}
	for _, k := range []ProfitKind{"", "Day", "fortnight", "year"} {
		if k.Valid() {
			t.Fatalf("%q no debería ser válido", k)
		}
	}
}

func TestNewProfitPeriod_dia_semana_mes(t *testing.T) {
	// Día: el 8/3/2026 en Nueva York (el día de 23 horas del cambio de horario).
	p, err := NewProfitPeriod("day", "2026-03-08", nowProfit, ny)
	if err != nil {
		t.Fatalf("día: %v", err)
	}
	if p.FromDate() != "2026-03-08" || p.ToDate() != "2026-03-08" {
		t.Fatalf("día = %s..%s", p.FromDate(), p.ToDate())
	}
	if p.From.Location() != ny {
		t.Fatalf("el período tiene que vivir en la zona del salón: %v", p.From.Location())
	}
	from, to := p.Window()
	if want := time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Fatalf("inicio = %s, esperaba %s (medianoche NY)", from.UTC(), want)
	}
	// 23 horas de día: el fin es la medianoche local del 9, no +24 h.
	if want := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC); !to.Equal(want) {
		t.Fatalf("fin = %s, esperaba %s (medianoche NY del día siguiente)", to.UTC(), want)
	}
	if got := to.Sub(from); got != 23*time.Hour {
		t.Fatalf("el 8/3/2026 en NY dura 23 horas, midió %s", got)
	}

	// Semana lunes-domingo (el 8/3 es domingo): 2/3..8/3. Es la misma derivación
	// de la nómina (derivePayPeriod), sin cambiarla.
	p, err = NewProfitPeriod("week", "2026-03-08", nowProfit, ny)
	if err != nil {
		t.Fatalf("semana: %v", err)
	}
	if p.FromDate() != "2026-03-02" || p.ToDate() != "2026-03-08" {
		t.Fatalf("semana = %s..%s", p.FromDate(), p.ToDate())
	}
	if days := p.Days(); len(days) != 7 || days[0] != "2026-03-02" || days[6] != "2026-03-08" {
		t.Fatalf("días de la semana = %v", days)
	}

	// Mes calendario: 1/3..31/3 (31 días ascendentes, con el día de 23 h dentro).
	p, err = NewProfitPeriod("month", "2026-03-08", nowProfit, ny)
	if err != nil {
		t.Fatalf("mes: %v", err)
	}
	if p.FromDate() != "2026-03-01" || p.ToDate() != "2026-03-31" {
		t.Fatalf("mes = %s..%s", p.FromDate(), p.ToDate())
	}
	days := p.Days()
	if len(days) != 31 || days[0] != "2026-03-01" || days[7] != "2026-03-08" || days[30] != "2026-03-31" {
		t.Fatalf("días del mes = %v", days)
	}
	for i := 1; i < len(days); i++ {
		if days[i] <= days[i-1] {
			t.Fatalf("los días tienen que ser ascendentes: %v", days)
		}
	}
}

// El día de 25 horas (1/11/2026 en Nueva York) también entra entero.
func TestNewProfitPeriod_dia_de_25_horas(t *testing.T) {
	p, err := NewProfitPeriod("day", "2026-11-01", nowProfit, ny)
	if err != nil {
		t.Fatalf("día: %v", err)
	}
	from, to := p.Window()
	if got := to.Sub(from); got != 25*time.Hour {
		t.Fatalf("el 1/11/2026 en NY dura 25 horas, midió %s", got)
	}
	if to.Sub(from) <= 24*time.Hour {
		t.Fatalf("el fin de un día de 25 h no puede ser +24 h: %s..%s", from.UTC(), to.UTC())
	}
}

// Sin `date` el período es HOY en la zona del salón. A las 11:00Z del 10/3 en
// Auckland ya es 11/3 (UTC+13), así que el día es el 11 y no el 10.
func TestNewProfitPeriod_sin_fecha_es_hoy_local(t *testing.T) {
	now := time.Date(2026, 3, 10, 11, 0, 0, 0, time.UTC)
	p, err := NewProfitPeriod("day", "", now, auckland)
	if err != nil {
		t.Fatalf("día sin fecha: %v", err)
	}
	if p.FromDate() != "2026-03-11" {
		t.Fatalf("hoy en Auckland = %s, esperaba 2026-03-11", p.FromDate())
	}
}

func TestNewProfitPeriod_invalido(t *testing.T) {
	for _, tc := range []struct{ kind, date string }{
		{"", "2026-03-10"},
		{"fortnight", "2026-03-10"},
		{"year", "2026-03-10"},
		{"day", "10/03/2026"},
		{"day", "2026-02-30"},
		{"week", "2026-13-01"},
		{"month", "no-es-fecha"},
	} {
		_, err := NewProfitPeriod(tc.kind, tc.date, nowProfit, ny)
		if err == nil || err.Error() != "Período inválido" {
			t.Fatalf("%q %q: %v", tc.kind, tc.date, err)
		}
	}
}

// El reporte rellena TODOS los días del período con ceros, el neto puede ser
// negativo y los totales son la suma exacta de los días (nunca la suma cruda de
// las consultas).
func TestNewProfitReport_ceros_neto_y_totales(t *testing.T) {
	p, err := NewProfitPeriod("week", "2026-03-08", nowProfit, ny)
	if err != nil {
		t.Fatalf("período: %v", err)
	}
	data := ProfitData{
		RevenueByDay:  map[string]int{"2026-03-02": 50000, "2026-03-08": 25000},
		ExpensesByDay: map[string]int{"2026-03-02": 10000, "2026-03-05": 400000},
		PayrollByDay:  map[string]int{"2026-03-08": 30000},
		ExpensesByCategory: []ExpenseCategoryTotal{
			{Category: ExpenseOther, TotalCents: 0},
			{Category: ExpenseRent, TotalCents: 400000},
			{Category: ExpenseSupplies, TotalCents: 10000},
		},
	}
	r := NewProfitReport(p, data)

	if len(r.Days) != 7 {
		t.Fatalf("días = %d, esperaba 7", len(r.Days))
	}
	if r.Days[0].Date != "2026-03-02" || r.Days[6].Date != "2026-03-08" {
		t.Fatalf("extremos = %s..%s", r.Days[0].Date, r.Days[6].Date)
	}
	// Un día sin movimientos sale en cero, no ausente.
	if d := r.Days[1]; d.Date != "2026-03-03" || d.RevenueCents != 0 || d.ExpensesCents != 0 ||
		d.PayrollCents != 0 || d.NetCents != 0 {
		t.Fatalf("día vacío = %+v", d)
	}
	if d := r.Days[0]; d.RevenueCents != 50000 || d.ExpensesCents != 10000 || d.NetCents != 40000 {
		t.Fatalf("día 2/3 = %+v", d)
	}
	// 5/3: solo gastos -> neto NEGATIVO.
	if d := r.Days[3]; d.NetCents != -400000 {
		t.Fatalf("el neto del 5/3 = %d, esperaba -400000", d.NetCents)
	}
	if d := r.Days[6]; d.RevenueCents != 25000 || d.PayrollCents != 30000 || d.NetCents != -5000 {
		t.Fatalf("día 8/3 = %+v", d)
	}

	if r.Totals.RevenueCents != 75000 || r.Totals.ExpensesCents != 410000 ||
		r.Totals.PayrollCents != 30000 || r.Totals.NetCents != -365000 {
		t.Fatalf("totales = %+v", r.Totals)
	}
	var revenue, expenses, payroll, net int
	for _, d := range r.Days {
		revenue += d.RevenueCents
		expenses += d.ExpensesCents
		payroll += d.PayrollCents
		net += d.NetCents
	}
	if revenue != r.Totals.RevenueCents || expenses != r.Totals.ExpensesCents ||
		payroll != r.Totals.PayrollCents || net != r.Totals.NetCents {
		t.Fatalf("los totales no son la suma de los días: %+v vs %d/%d/%d/%d",
			r.Totals, revenue, expenses, payroll, net)
	}

	// Categorías: solo las de total > 0, por código (rent < supplies < other).
	if len(r.ExpensesByCategory) != 2 ||
		r.ExpensesByCategory[0] != (ExpenseCategoryTotal{Category: ExpenseRent, TotalCents: 400000}) ||
		r.ExpensesByCategory[1] != (ExpenseCategoryTotal{Category: ExpenseSupplies, TotalCents: 10000}) {
		t.Fatalf("categorías = %+v", r.ExpensesByCategory)
	}
}

// Un período sin movimientos es un reporte de ceros, no un error ni un nil.
func TestNewProfitReport_salon_vacio(t *testing.T) {
	p, err := NewProfitPeriod("day", "2026-03-08", nowProfit, ny)
	if err != nil {
		t.Fatalf("período: %v", err)
	}
	r := NewProfitReport(p, ProfitData{})
	if len(r.Days) != 1 || r.Days[0].NetCents != 0 {
		t.Fatalf("días = %+v", r.Days)
	}
	if r.Totals != (ProfitTotals{}) {
		t.Fatalf("totales = %+v", r.Totals)
	}
	if r.ExpensesByCategory == nil || len(r.ExpensesByCategory) != 0 {
		t.Fatalf("categorías = %#v (tiene que ser [])", r.ExpensesByCategory)
	}
}
