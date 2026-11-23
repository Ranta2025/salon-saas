package domain

import (
	"sort"
	"strings"
	"time"
)

// Ganancia neta (F3b, D1, D2 y D7 del contrato F3b). Este archivo es PURO:
// stdlib y nada más. El servidor deriva el período en la zona del SALÓN (nunca
// UTC: no hereda H14) y el reporte rellena TODOS los días con ceros; los totales
// son la suma exacta de los días.

// ProfitKind es el período que el panel pide (día, semana o mes). "quincena" NO
// existe acá: es un tipo de la nómina, no del panel de ganancias.
type ProfitKind string

const (
	ProfitDay   ProfitKind = "day"
	ProfitWeek  ProfitKind = "week"
	ProfitMonth ProfitKind = "month"
)

// Valid dice si el tipo está en el catálogo del panel.
func (k ProfitKind) Valid() bool {
	switch k {
	case ProfitDay, ProfitWeek, ProfitMonth:
		return true
	default:
		return false
	}
}

// ProfitPeriod es el período ya derivado: From y To son la medianoche LOCAL del
// salón del primer día y del ÚLTIMO día INCLUSIVO. La ventana interna es
// [From, End()) y se arma con time.Date (vía derivePayPeriod en semana y mes):
// un día de cambio de horario de verano dura 23 o 25 horas y entra entero.
type ProfitPeriod struct {
	Kind ProfitKind
	From time.Time
	To   time.Time
}

// NewProfitPeriod deriva el período de `kind` que contiene `date` (YYYY-MM-DD)
// en la zona del salón; `date` vacío es HOY en esa zona. Semana y mes reutilizan
// derivePayPeriod (la MISMA regla de la nómina, D7: no se toca ni se duplica).
// Cualquier tipo o fecha desconocida es 400: "Período inválido".
func NewProfitPeriod(kind, date string, now time.Time, loc *time.Location) (ProfitPeriod, error) {
	invalid := NewMessage(ErrValidation, "Período inválido")
	if loc == nil {
		return ProfitPeriod{}, invalid
	}
	k := ProfitKind(strings.TrimSpace(kind))
	if !k.Valid() {
		return ProfitPeriod{}, invalid
	}
	day := strings.TrimSpace(date)
	if day == "" {
		day = now.In(loc).Format(dateLayout)
	}
	local, err := time.ParseInLocation(dateLayout, day, loc)
	if err != nil {
		return ProfitPeriod{}, invalid
	}
	y, m, d := local.Date()
	switch k {
	case ProfitDay:
		return ProfitPeriod{Kind: k, From: localDate(y, m, d, loc), To: localDate(y, m, d, loc)}, nil
	case ProfitWeek:
		p := derivePayPeriod(PayWeek, local, loc)
		return ProfitPeriod{Kind: k, From: p.From, To: p.To}, nil
	default:
		p := derivePayPeriod(PayMonth, local, loc)
		return ProfitPeriod{Kind: k, From: p.From, To: p.To}, nil
	}
}

// End es la medianoche local del día SIGUIENTE al último: el extremo abierto de
// la ventana. Se calcula con time.Date en la zona del salón, nunca sumando 24 h.
func (p ProfitPeriod) End() time.Time {
	y, m, d := p.To.Date()
	return localDate(y, m, d+1, p.To.Location())
}

// Window devuelve la ventana [inicio, fin) de instantes del período entero.
func (p ProfitPeriod) Window() (time.Time, time.Time) { return p.From, p.End() }

// FromDate y ToDate son las fechas locales INCLUSIVAS del contrato.
func (p ProfitPeriod) FromDate() string { return p.From.Format(dateLayout) }
func (p ProfitPeriod) ToDate() string   { return p.To.Format(dateLayout) }

// Days son TODOS los días locales del período, ascendentes, como YYYY-MM-DD.
func (p ProfitPeriod) Days() []string {
	loc := p.From.Location()
	out := make([]string, 0, 31)
	for y, m, d := p.From.Date(); ; {
		day := localDate(y, m, d, loc)
		out = append(out, day.Format(dateLayout))
		if !day.Before(p.To) {
			return out
		}
		y, m, d = day.AddDate(0, 0, 1).Date()
	}
}

// ProfitData es la foto CRUDA que devuelve el adaptador: montos por día local
// (clave YYYY-MM-DD) y el total por categoría. Un día sin movimientos no
// aparece; el reporte lo rellena con ceros.
type ProfitData struct {
	RevenueByDay       map[string]int
	ExpensesByDay      map[string]int
	PayrollByDay       map[string]int
	ExpensesByCategory []ExpenseCategoryTotal
}

// ExpenseCategoryTotal es el gasto sumado de una categoría en el período.
type ExpenseCategoryTotal struct {
	Category   ExpenseCategory
	TotalCents int
}

// ProfitDayTotal es una fila del reporte: los tres componentes y el neto del día.
type ProfitDayTotal struct {
	Date          string
	RevenueCents  int
	ExpensesCents int
	PayrollCents  int
	NetCents      int
}

// ProfitTotals son los totales del período. NetCents puede ser negativo.
type ProfitTotals struct {
	RevenueCents  int
	ExpensesCents int
	PayrollCents  int
	NetCents      int
}

// ProfitReport es la respuesta del panel (D2): el período, los totales, TODOS
// los días del período y el desglose de gastos por categoría.
type ProfitReport struct {
	Period             ProfitPeriod
	Totals             ProfitTotals
	Days               []ProfitDayTotal
	ExpensesByCategory []ExpenseCategoryTotal
}

// NewProfitReport arma el reporte del período con la foto del adaptador:
// rellena con ceros cada día local, calcula el neto del día
// (ingresos - gastos - nómina, puede ser negativo) y los totales como la suma
// EXACTA de los días. El desglose trae solo categorías con total > 0, por
// código.
func NewProfitReport(p ProfitPeriod, data ProfitData) ProfitReport {
	report := ProfitReport{
		Period:             p,
		Days:               make([]ProfitDayTotal, 0, 31),
		ExpensesByCategory: make([]ExpenseCategoryTotal, 0, 4),
	}
	for _, date := range p.Days() {
		day := ProfitDayTotal{
			Date:          date,
			RevenueCents:  data.RevenueByDay[date],
			ExpensesCents: data.ExpensesByDay[date],
			PayrollCents:  data.PayrollByDay[date],
		}
		day.NetCents = day.RevenueCents - day.ExpensesCents - day.PayrollCents
		report.Days = append(report.Days, day)
		report.Totals.RevenueCents += day.RevenueCents
		report.Totals.ExpensesCents += day.ExpensesCents
		report.Totals.PayrollCents += day.PayrollCents
	}
	report.Totals.NetCents = report.Totals.RevenueCents - report.Totals.ExpensesCents - report.Totals.PayrollCents

	for _, c := range data.ExpensesByCategory {
		if c.TotalCents > 0 {
			report.ExpensesByCategory = append(report.ExpensesByCategory, c)
		}
	}
	sort.Slice(report.ExpensesByCategory, func(i, j int) bool {
		return report.ExpensesByCategory[i].Category < report.ExpensesByCategory[j].Category
	})
	return report
}
