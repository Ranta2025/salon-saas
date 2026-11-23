// Contrato HTTP del reporte de ganancia neta (F3b, D1/D2/D5): la forma EXACTA,
// los permisos, el período derivado en la zona del salón, el salón sin
// movimientos (ceros, no 500) y el 400 de «Período inválido».
package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"salon-saas/internal/domain"
)

func (e *expHTTPEnv) profitPath(q string) string { return e.url(e.salon, "/profit"+q) }

func TestHTTP_profit_forma_y_totales(t *testing.T) {
	e := newExpHTTPEnv(t)
	e.profit.data = domain.ProfitData{
		RevenueByDay:  map[string]int{"2026-03-02": 50000, "2026-03-08": 25000},
		ExpensesByDay: map[string]int{"2026-03-02": 10000, "2026-03-05": 400000},
		PayrollByDay:  map[string]int{"2026-03-08": 30000},
		ExpensesByCategory: []domain.ExpenseCategoryTotal{
			{Category: domain.ExpenseOther, TotalCents: 400000},
			{Category: domain.ExpenseSupplies, TotalCents: 10000},
		},
	}
	w := e.do(t, "GET", e.profitPath("?kind=week&date=2026-03-08"), "", "duena@exp.com")
	if w.Code != http.StatusOK {
		t.Fatalf("ganancias: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	mustJSON(t, w.Body.Bytes(), &got)
	sameKeys(t, "reporte", got, "period", "totals", "days", "expenses_by_category")

	period := got["period"].(map[string]any)
	sameKeys(t, "period", period, "kind", "from", "to")
	if period["kind"] != "week" || period["from"] != "2026-03-02" || period["to"] != "2026-03-08" {
		t.Fatalf("period = %v", period)
	}

	totals := got["totals"].(map[string]any)
	sameKeys(t, "totals", totals, "revenue_cents", "expenses_cents", "payroll_cents", "net_cents")
	if totals["revenue_cents"] != float64(75000) || totals["expenses_cents"] != float64(410000) ||
		totals["payroll_cents"] != float64(30000) || totals["net_cents"] != float64(-365000) {
		t.Fatalf("totals = %v", totals)
	}

	days := got["days"].([]any)
	if len(days) != 7 {
		t.Fatalf("días = %d, esperaba 7 (todos, con ceros)", len(days))
	}
	first := days[0].(map[string]any)
	sameKeys(t, "day", first, "date", "revenue_cents", "expenses_cents", "payroll_cents", "net_cents")
	if first["date"] != "2026-03-02" || first["revenue_cents"] != float64(50000) ||
		first["expenses_cents"] != float64(10000) || first["net_cents"] != float64(40000) {
		t.Fatalf("primer día = %v", first)
	}
	empty := days[1].(map[string]any)
	if empty["date"] != "2026-03-03" || empty["net_cents"] != float64(0) {
		t.Fatalf("día sin movimientos = %v", empty)
	}
	// El neto del día con gastos grandes es NEGATIVO y la suma de días da los
	// totales.
	var net, revenue, expenses, payroll int
	for _, d := range days {
		day := d.(map[string]any)
		revenue += int(day["revenue_cents"].(float64))
		expenses += int(day["expenses_cents"].(float64))
		payroll += int(day["payroll_cents"].(float64))
		net += int(day["net_cents"].(float64))
		if int(day["revenue_cents"].(float64))-int(day["expenses_cents"].(float64))-int(day["payroll_cents"].(float64)) != int(day["net_cents"].(float64)) {
			t.Fatalf("neto del día inconsistente: %v", day)
		}
	}
	if revenue != int(totals["revenue_cents"].(float64)) || expenses != int(totals["expenses_cents"].(float64)) ||
		payroll != int(totals["payroll_cents"].(float64)) || net != int(totals["net_cents"].(float64)) {
		t.Fatalf("la suma de días no da los totales: %v", totals)
	}

	cats := got["expenses_by_category"].([]any)
	if len(cats) != 2 {
		t.Fatalf("categorías = %v", cats)
	}
	c0 := cats[0].(map[string]any)
	sameKeys(t, "categoría", c0, "category", "total_cents")
	if c0["category"] != "other" || c0["total_cents"] != float64(400000) {
		t.Fatalf("categorías = %v", cats)
	}
	if c1 := cats[1].(map[string]any); c1["category"] != "supplies" || c1["total_cents"] != float64(10000) {
		t.Fatalf("categorías = %v", cats)
	}
}

// Un salón sin movimientos devuelve ceros (no 500 y no null) y sin `date` el día
// es HOY en la zona del salón (20/3/2026 en CDMX).
func TestHTTP_profit_salon_vacio_y_hoy_local(t *testing.T) {
	e := newExpHTTPEnv(t)
	e.profit.data = domain.ProfitData{}
	w := e.do(t, "GET", e.profitPath("?kind=day"), "", "duena@exp.com")
	if w.Code != http.StatusOK {
		t.Fatalf("ganancias vacías: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	mustJSON(t, w.Body.Bytes(), &got)
	period := got["period"].(map[string]any)
	if period["kind"] != "day" || period["from"] != "2026-03-20" || period["to"] != "2026-03-20" {
		t.Fatalf("period = %v", period)
	}
	days := got["days"].([]any)
	if len(days) != 1 || days[0].(map[string]any)["net_cents"] != float64(0) {
		t.Fatalf("días = %v", days)
	}
	cats, ok := got["expenses_by_category"].([]any)
	if !ok || len(cats) != 0 {
		t.Fatalf("categorías = %#v (tiene que ser [])", got["expenses_by_category"])
	}
}

// `kind` o `date` inválidos son 400 con la frase del contrato; sin token, 401.
func TestHTTP_profit_periodo_invalido(t *testing.T) {
	e := newExpHTTPEnv(t)
	for _, q := range []string{
		"", "?date=2026-03-10", "?kind=fortnight&date=2026-03-10",
		"?kind=year&date=2026-03-10", "?kind=month&date=10/03/2026",
		"?kind=week&date=2026-13-01",
	} {
		w := e.do(t, "GET", e.profitPath(q), "", "duena@exp.com")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"Período inválido"`) {
			t.Fatalf("%q: %d %s", q, w.Code, w.Body.String())
		}
	}
	if w := doReq(e.r, "GET", e.profitPath("?kind=day"), "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d %s", w.Code, w.Body.String())
	}
}
