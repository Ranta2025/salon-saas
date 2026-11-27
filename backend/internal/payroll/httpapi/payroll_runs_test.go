package httpapi_test

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/payroll"
	payrollhttpapi "salon-saas/internal/payroll/httpapi"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Contrato HTTP de las liquidaciones (F3.3): las siete rutas, la forma EXACTA
// de las respuestas, los permisos de D7 rol por rol, el aislamiento por tenant,
// la validación en español y el CSV de D8.

type runHTTPEnv struct {
	*payEnv
	Runs *httpkit.PayrollRuns
}

func newRunHTTPEnv(t *testing.T) *runHTTPEnv {
	t.Helper()
	base := newPayEnv(t)
	runs := httpkit.NewPayrollRuns(func() time.Time { return payrollNow })
	runs.SettingsRepo = base.Env.Settings
	runs.Body, runs.Sales = base.Body, base.Sales
	d := base.Env.Deps
	d.PayrollRuns = payrollhttpapi.PayrollRunHandler{
		Runs: payroll.PayrollRunService{
			Runs: runs, Tx: runs,
			PayModels: base.Body, Sales: base.Sales,
			Memberships: base.Env.Mems, Settings: base.Env.Settings,
			Now: func() time.Time { return payrollNow },
		},
		Memberships: base.Env.Mems,
	}
	r := gin.New()
	httpapi.RegisterRoutes(r, d)
	base.r = r
	return &runHTTPEnv{payEnv: base, Runs: runs}
}

// febRunData deja a Ana con una venta cerrada de febrero (comisión 200).
func (e *runHTTPEnv) febRunData() {
	e.Sales.Add(e.Salon, domain.PayrollSalesTotal{
		UserID: e.Users["ana@pay.com"], Name: "ana@pay.com",
		SalesCount: 2, SalesTotalCents: 2000, CommissionBase: 20000,
	})
}

func (e *runHTTPEnv) runsPath(path string) string { return e.url(e.Salon, "/payroll/runs"+path) }

// runJSONKeys es la forma EXACTA del detalle congelada en el contrato.
func runJSONKeys(t *testing.T, what string, run map[string]any) {
	t.Helper()
	httpkit.SameKeys(t, what, run, "id", "kind", "from", "to", "status", "created_at", "paid_at", "totals", "lines")
	totals, _ := run["totals"].(map[string]any)
	httpkit.SameKeys(t, what+" totals", totals,
		"commission_cents", "fixed_cents", "adjustment_cents", "total_cents")
	lines, _ := run["lines"].([]any)
	for _, l := range lines {
		line, _ := l.(map[string]any)
		httpkit.SameKeys(t, what+" line", line,
			"user_id", "name", "active", "sales_count", "sales_total_cents",
			"commission_cents", "fixed_cents", "adjustment_cents", "adjustment_note",
			"total_cents", "estimated")
	}
}

// Ciclo completo: crear 201, listar, leer, ajustar, pagar y borrar; más las
// frases del contrato para período en curso, vacío, solape y corrida pagada.
func TestHTTP_payroll_runs_ciclo(t *testing.T) {
	e := newRunHTTPEnv(t)
	e.febRunData()

	w := e.do(t, "POST", e.runsPath(""), `{"kind":"month","date":"2026-02-10"}`, "duena@pay.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("crear: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	runJSONKeys(t, "run creado", created)
	if created["kind"] != "month" || created["from"] != "2026-02-01" ||
		created["to"] != "2026-02-28" || created["status"] != "open" {
		t.Fatalf("cabecera: %+v", created)
	}
	if created["created_at"] != "2026-03-20T18:00:00Z" {
		t.Fatalf("created_at = %v", created["created_at"])
	}
	if v, ok := created["paid_at"]; !ok || v != nil {
		t.Fatalf("paid_at de una abierta = %v (presente %v)", v, ok)
	}
	lines := created["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("líneas: %+v", lines)
	}
	line := lines[0].(map[string]any)
	if line["user_id"] != e.Users["ana@pay.com"] || line["name"] != "ana@pay.com" ||
		line["active"] != true || line["sales_count"] != float64(2) ||
		line["sales_total_cents"] != float64(2000) || line["commission_cents"] != float64(200) ||
		line["adjustment_cents"] != float64(0) || line["adjustment_note"] != "" ||
		line["total_cents"] != float64(200) {
		t.Fatalf("línea: %+v", line)
	}
	runID := created["id"].(string)

	// Listado: la forma de la fila con lines_count.
	w = e.do(t, "GET", e.runsPath(""), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Runs []map[string]any `json:"runs"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Runs) != 1 {
		t.Fatalf("listado: %+v", list.Runs)
	}
	httpkit.SameKeys(t, "resumen", list.Runs[0],
		"id", "kind", "from", "to", "status", "created_at", "paid_at", "lines_count", "totals")
	if list.Runs[0]["lines_count"] != float64(1) || list.Runs[0]["status"] != "open" {
		t.Fatalf("resumen: %+v", list.Runs[0])
	}

	// Detalle.
	w = e.do(t, "GET", e.runsPath("/"+runID), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("detalle: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &got)
	if got["id"] != runID {
		t.Fatalf("detalle: %+v", got)
	}

	// Ajuste: total = comisión + fijo + ajuste.
	w = e.do(t, "PATCH", e.runsPath("/"+runID+"/lines/"+e.Users["ana@pay.com"]),
		`{"adjustment_cents":-200,"adjustment_note":"venta duplicada"}`, "admin@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("ajuste: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &got)
	runJSONKeys(t, "run ajustado", got)
	totals := got["totals"].(map[string]any)
	if totals["commission_cents"] != float64(200) || totals["adjustment_cents"] != float64(-200) ||
		totals["total_cents"] != float64(0) {
		t.Fatalf("totales ajustados: %+v", totals)
	}
	line = got["lines"].([]any)[0].(map[string]any)
	if line["adjustment_cents"] != float64(-200) || line["adjustment_note"] != "venta duplicada" ||
		line["total_cents"] != float64(0) {
		t.Fatalf("línea ajustada: %+v", line)
	}

	// Exportar una abierta también se puede (la foto ya está congelada).
	w = e.do(t, "GET", e.runsPath("/"+runID+"/export.csv"), "", "duena@pay.com")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "\xef\xbb\xbf") {
		t.Fatalf("export abierta: %d %q", w.Code, w.Body.String())
	}

	// Pagar.
	w = e.do(t, "POST", e.runsPath("/"+runID+"/pay"), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("pagar: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &got)
	if got["status"] != "paid" {
		t.Fatalf("estado tras pagar: %+v", got)
	}
	paidAt, ok := got["paid_at"].(string)
	if !ok {
		t.Fatalf("paid_at = %v", got["paid_at"])
	}
	if _, err := time.Parse(time.RFC3339, paidAt); err != nil {
		t.Fatalf("paid_at no es RFC3339: %q", paidAt)
	}

	// Desde acá, TODO es 409 con la frase exacta.
	for _, c := range []struct{ method, path, body string }{
		{"PATCH", e.runsPath("/" + runID + "/lines/" + e.Users["ana@pay.com"]), `{"adjustment_cents":1,"adjustment_note":"x"}`},
		{"POST", e.runsPath("/" + runID + "/pay"), ""},
		{"DELETE", e.runsPath("/" + runID), ""},
	} {
		w := e.do(t, c.method, c.path, c.body, "duena@pay.com")
		if w.Code != http.StatusConflict {
			t.Fatalf("%s %s sobre pagada: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
		var out map[string]string
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out["error"] != domain.MsgPayrollRunPaid {
			t.Fatalf("%s %s: mensaje = %q", c.method, c.path, out["error"])
		}
	}

	// Otra corrida abierta se borra (204) y libera los días.
	e.Sales.Add(e.Salon, domain.PayrollSalesTotal{
		UserID: e.Users["ana@pay.com"], Name: "ana@pay.com",
		SalesCount: 1, SalesTotalCents: 1000, CommissionBase: 10000,
	})
	w = e.do(t, "POST", e.runsPath(""), `{"kind":"month","date":"2026-01-10"}`, "duena@pay.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("crear enero: %d %s", w.Code, w.Body.String())
	}
	var jan map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &jan)
	if w := e.do(t, "DELETE", e.runsPath("/"+jan["id"].(string)), "", "duena@pay.com"); w.Code != http.StatusNoContent {
		t.Fatalf("borrar abierta: %d %s", w.Code, w.Body.String())
	}
	if w := e.do(t, "GET", e.runsPath("/"+jan["id"].(string)), "", "duena@pay.com"); w.Code != http.StatusNotFound {
		t.Fatalf("la borrada: %d", w.Code)
	}
	if w := e.do(t, "DELETE", e.runsPath("/no-existe"), "", "duena@pay.com"); w.Code != http.StatusNotFound {
		t.Fatalf("borrar inexistente: %d", w.Code)
	}
}

// Las reglas de creación y de ajuste, con sus frases exactas.
func TestHTTP_payroll_runs_reglas_y_validacion(t *testing.T) {
	e := newRunHTTPEnv(t)
	duena := "duena@pay.com"

	// Período en curso: 400.
	if w := e.do(t, "POST", e.runsPath(""), `{"kind":"month","date":"2026-03-10"}`, duena); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), domain.MsgPayrollPeriodNotClosed) {
		t.Fatalf("período en curso: %d %s", w.Code, w.Body.String())
	}
	// Período vacío: 400.
	if w := e.do(t, "POST", e.runsPath(""), `{"kind":"month","date":"2026-01-10"}`, duena); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), domain.MsgPayrollNothingToPay) {
		t.Fatalf("período vacío: %d %s", w.Code, w.Body.String())
	}
	// kind/date inválidos: 400 "Período inválido".
	if w := e.do(t, "POST", e.runsPath(""), `{"kind":"day","date":"2026-02-10"}`, duena); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "Período inválido") {
		t.Fatalf("período inválido: %d %s", w.Code, w.Body.String())
	}
	// JSON roto: 400 "body inválido".
	if w := e.do(t, "POST", e.runsPath(""), `{"kind":`, duena); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "body inválido") {
		t.Fatalf("body roto: %d %s", w.Code, w.Body.String())
	}

	e.febRunData()
	w := e.do(t, "POST", e.runsPath(""), `{"kind":"month","date":"2026-02-10"}`, duena)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear: %d %s", w.Code, w.Body.String())
	}
	var run map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &run)
	runID := run["id"].(string)
	linePath := e.runsPath("/" + runID + "/lines/" + e.Users["ana@pay.com"])

	// Cuerpo parcial: campo ausente o null -> 400 (lección de F3.2), nunca 0 en
	// silencio, y nada cambia.
	for _, body := range []string{`{}`, `{"adjustment_note":"x"}`, `{"adjustment_cents":null}`} {
		w := e.do(t, "PATCH", linePath, body, duena)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("cuerpo parcial %s: %d %s", body, w.Code, w.Body.String())
		}
		var out map[string]string
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out["error"] != domain.MsgPayrollAdjustmentMissing {
			t.Fatalf("cuerpo parcial %s: mensaje = %q", body, out["error"])
		}
	}
	// Nota obligatoria si el ajuste no es 0.
	w = e.do(t, "PATCH", linePath, `{"adjustment_cents":500}`, duena)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), domain.MsgPayrollNoteRequired) {
		t.Fatalf("sin nota: %d %s", w.Code, w.Body.String())
	}
	// Nota de 201 caracteres y con salto de línea: 400.
	long, _ := json.Marshal(strings.Repeat("ñ", 201))
	if w := e.do(t, "PATCH", linePath, `{"adjustment_cents":500,"adjustment_note":`+string(long)+`}`, duena); w.Code != http.StatusBadRequest {
		t.Fatalf("nota de 201: %d", w.Code)
	}
	if w := e.do(t, "PATCH", linePath, `{"adjustment_cents":500,"adjustment_note":"a\nb"}`, duena); w.Code != http.StatusBadRequest {
		t.Fatalf("nota con salto: %d %s", w.Code, w.Body.String())
	}
	// Total negativo: 400 con la frase.
	w = e.do(t, "PATCH", linePath, `{"adjustment_cents":-201,"adjustment_note":"de más"}`, duena)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), domain.MsgPayrollNegativeTotal) {
		t.Fatalf("total negativo: %d %s", w.Code, w.Body.String())
	}
	// Magnitud fuera de ±MaxPayCents: 400.
	if w := e.do(t, "PATCH", linePath, `{"adjustment_cents":2000000001,"adjustment_note":"tope"}`, duena); w.Code != http.StatusBadRequest {
		t.Fatalf("ajuste fuera del máximo: %d %s", w.Code, w.Body.String())
	}
	// Persona que no está en la corrida: 404.
	if w := e.do(t, "PATCH", e.runsPath("/"+runID+"/lines/"+e.Users["recep@pay.com"]),
		`{"adjustment_cents":1,"adjustment_note":"x"}`, duena); w.Code != http.StatusNotFound {
		t.Fatalf("persona fuera: %d %s", w.Code, w.Body.String())
	}
	// Corrida que no existe: 404.
	if w := e.do(t, "PATCH", e.runsPath("/no-existe/lines/"+e.Users["ana@pay.com"]),
		`{"adjustment_cents":0}`, duena); w.Code != http.StatusNotFound {
		t.Fatalf("corrida inexistente: %d %s", w.Code, w.Body.String())
	}
	// Ajuste válido en 0 con nota vacía.
	if w := e.do(t, "PATCH", linePath, `{"adjustment_cents":0}`, duena); w.Code != http.StatusOK {
		t.Fatalf("ajuste 0: %d %s", w.Code, w.Body.String())
	}
	// Solape: crear otra que pise febrero es 409 con la frase.
	if w := e.do(t, "POST", e.runsPath(""), `{"kind":"fortnight","date":"2026-02-10"}`, duena); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), domain.MsgPayrollRunOverlaps) {
		t.Fatalf("solape: %d %s", w.Code, w.Body.String())
	}
}

// Matriz de D7 sobre TODAS las rutas: owner/admin sí; recepción, profesional,
// miembro dado de baja y dueño de otro salón 403; sin token 401.
func TestHTTP_payroll_runs_permisos_por_rol(t *testing.T) {
	e := newRunHTTPEnv(t)
	e.febRunData()
	e.Sales.Add(e.Salon, domain.PayrollSalesTotal{
		UserID: e.Users["ana@pay.com"], Name: "ana@pay.com",
		SalesCount: 1, SalesTotalCents: 1000, CommissionBase: 10000,
	})

	// Un run de referencia para las rutas con :rid.
	w := e.do(t, "POST", e.runsPath(""), `{"kind":"month","date":"2026-02-10"}`, "duena@pay.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("run de referencia: %d %s", w.Code, w.Body.String())
	}
	var ref map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &ref)
	rid := ref["id"].(string)

	routes := []struct{ name, method, path, body string }{
		{"listar", "GET", e.runsPath(""), ""},
		{"crear", "POST", e.runsPath(""), `{"kind":"month","date":"2026-01-10"}`},
		{"detalle", "GET", e.runsPath("/" + rid), ""},
		{"ajustar", "PATCH", e.runsPath("/" + rid + "/lines/" + e.Users["ana@pay.com"]), `{"adjustment_cents":0}`},
		{"pagar", "POST", e.runsPath("/" + rid + "/pay"), ""},
		{"borrar", "DELETE", e.runsPath("/" + rid), ""},
		{"exportar", "GET", e.runsPath("/" + rid + "/export.csv"), ""},
	}

	// owner y admin: ninguna de las siete da 403/401.
	for _, email := range []string{"duena@pay.com", "admin@pay.com"} {
		// El "crear" de enero puede dar 201 la primera vez y 409 la segunda: no
		// importa, lo que se mira es que no sea 403.
		for _, r := range routes {
			w := e.do(t, r.method, r.path, r.body, email)
			if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
				t.Fatalf("%s %s con %s: %d %s", r.method, r.name, email, w.Code, w.Body.String())
			}
		}
	}

	// owner y admin CREAN de verdad (201) en períodos distintos.
	if w := e.do(t, "POST", e.runsPath(""), `{"kind":"month","date":"2025-12-10"}`, "admin@pay.com"); w.Code != http.StatusCreated {
		t.Fatalf("admin crea: %d %s", w.Code, w.Body.String())
	}
	// Y BORRAN de verdad (204) una corrida abierta cada uno.
	for i, email := range []string{"duena@pay.com", "admin@pay.com"} {
		id := "run-del-" + email
		e.Runs.Seed(domain.PayrollRun{
			ID: id, SalonID: e.Salon, Kind: domain.PayMonth,
			From: "2025-0" + string(rune('1'+i)) + "-01", To: "2025-0" + string(rune('1'+i)) + "-28",
			Status: domain.PayrollRunOpen, CreatedAt: payrollNow,
			Lines: []domain.PayrollLine{{UserID: e.Users["ana@pay.com"], Name: "ana@pay.com", TotalCents: 0}},
		})
		if w := e.do(t, "DELETE", e.runsPath("/"+id), "", email); w.Code != http.StatusNoContent {
			t.Fatalf("%s borra: %d %s", email, w.Code, w.Body.String())
		}
	}

	// Recepción, profesional y dueño de otro salón: 403 en las siete.
	for _, email := range []string{"recep@pay.com", "ana@pay.com", "ajeno@pay.com"} {
		for _, r := range routes {
			w := e.do(t, r.method, r.path, r.body, email)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s %s con %s: %d %s (esperaba 403)", r.method, r.name, email, w.Code, w.Body.String())
			}
		}
	}
	// Una persona dada de baja: 403 aunque haya sido profesional.
	e.deactivate(t, "beto@pay.com")
	for _, r := range routes {
		w := e.do(t, r.method, r.path, r.body, "beto@pay.com")
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s con la baja: %d %s", r.method, r.name, w.Code, w.Body.String())
		}
	}
	// Sin token: 401 en las siete.
	for _, r := range routes {
		w := httpkit.Do(e.r, r.method, r.path, r.body, "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s sin token: %d", r.method, r.name, w.Code)
		}
	}
}

// La liquidación de OTRO salón no se lee, no se ajusta, no se paga, no se borra
// ni se exporta: 404 siempre.
func TestHTTP_payroll_runs_tenant(t *testing.T) {
	e := newRunHTTPEnv(t)
	now := payrollNow
	otro := domain.PayrollRun{
		ID: "run-otro", SalonID: e.otros, Kind: domain.PayMonth,
		From: "2026-02-01", To: "2026-02-28", Status: domain.PayrollRunOpen,
		CreatedBy: e.Users["ajeno@pay.com"], CreatedAt: now,
		Lines: []domain.PayrollLine{{UserID: e.Users["carla@pay.com"], Name: "Carla", Active: true, TotalCents: 0}},
	}
	e.Runs.Seed(otro)

	for _, r := range []struct{ method, path, body string }{
		{"GET", "/run-otro", ""},
		{"PATCH", "/run-otro/lines/" + e.Users["carla@pay.com"], `{"adjustment_cents":0}`},
		{"POST", "/run-otro/pay", ""},
		{"DELETE", "/run-otro", ""},
		{"GET", "/run-otro/export.csv", ""},
	} {
		w := e.do(t, r.method, e.runsPath(r.path), r.body, "duena@pay.com")
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s del otro salón: %d %s", r.method, r.path, w.Code, w.Body.String())
		}
	}
	// La dueña del otro salón sí la ve.
	if w := e.do(t, "GET", e.url(e.otros, "/payroll/runs/run-otro"), "", "ajeno@pay.com"); w.Code != http.StatusOK {
		t.Fatalf("la dueña del otro salón: %d %s", w.Code, w.Body.String())
	}
}

// D8: BOM, encabezado exacto, decimales desde centavos, comillas RFC 4180 y
// neutralización de fórmulas en nombres y notas; nombre de archivo seguro.
func TestHTTP_payroll_export_csv(t *testing.T) {
	e := newRunHTTPEnv(t)
	run := domain.PayrollRun{
		ID: "run-csv", SalonID: e.Salon, Kind: domain.PayMonth,
		From: "2026-02-01", To: "2026-02-28", Status: domain.PayrollRunPaid,
		CreatedBy: e.Users["duena@pay.com"], CreatedAt: payrollNow,
		PaidAt: &payrollNow, PaidBy: e.Users["duena@pay.com"],
		Lines: []domain.PayrollLine{
			{UserID: "u-1", Name: "=SUM(A1:A9)", Active: true, TotalCents: 0},
			{UserID: "u-2", Name: "+Ana", Active: true, SalesTotalCents: 5, CommissionCents: 5, TotalCents: 5},
			{UserID: "u-3", Name: "-Beto", Active: false, SalesTotalCents: 99, CommissionCents: 99, TotalCents: 99},
			{UserID: "u-4", Name: "@Cmd", Active: true, SalesCount: 100, SalesTotalCents: 100, CommissionCents: 100, FixedCents: 100000, TotalCents: 100100},
			{UserID: "u-5", Name: "Ruiz, Ana", Active: true, SalesTotalCents: 123456789, CommissionCents: 123456789, TotalCents: 123456789},
			{UserID: "u-6", Name: "Dijo \"hola\"", Active: true, CommissionCents: 1250, AdjustmentCents: -1250, AdjustmentNote: "=1+1", TotalCents: 0},
			{UserID: "u-7", Name: "\tTab", Active: true, TotalCents: 0},
			{UserID: "u-8", Name: "\rCR", Active: true, TotalCents: 0},
			{UserID: "u-9", Name: "Ñandú", Active: true, TotalCents: 0},
		},
	}
	e.Runs.Seed(run)

	w := e.do(t, "GET", e.runsPath("/run-csv/export.csv"), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="nomina-month-2026-02-01-2026-02-28.csv"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf"+domain.PayrollCSVHeader+"\r\n") {
		t.Fatalf("BOM/encabezado: %q", body[:40])
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatalf("CSV inválido: %v", err)
	}
	if len(records) != 10 {
		t.Fatalf("filas = %d, esperaba 10", len(records))
	}
	for _, row := range records {
		if len(row) != 9 {
			t.Fatalf("fila con %d columnas: %q", len(row), row)
		}
	}
	// Nombres neutralizados (comillas y comas incluidas).
	wantNames := []string{
		"'=SUM(A1:A9)", "'+Ana", "'-Beto", "'@Cmd", "Ruiz, Ana",
		"Dijo \"hola\"", "'\tTab", "'\rCR", "Ñandú",
	}
	for i, want := range wantNames {
		if got := records[i+1][0]; got != want {
			t.Fatalf("fila %d: nombre = %q, esperaba %q", i+1, got, want)
		}
	}
	// Estados.
	if records[1][1] != "Activo" || records[3][1] != "Dado de baja" {
		t.Fatalf("estados: %q / %q", records[1][1], records[3][1])
	}
	// Decimales desde centavos: 0, 5, 99, 100, 123456789 y un ajuste negativo.
	if records[1][3] != "0.00" || records[2][3] != "0.05" || records[3][3] != "0.99" ||
		records[4][3] != "1.00" || records[5][3] != "1234567.89" {
		t.Fatalf("decimales: %q %q %q %q %q",
			records[1][3], records[2][3], records[3][3], records[4][3], records[5][3])
	}
	if records[4][5] != "1000.00" || records[4][8] != "1001.00" {
		t.Fatalf("fijo/total: %q / %q", records[4][5], records[4][8])
	}
	if records[6][6] != "-12.50" || records[6][7] != "'=1+1" || records[6][8] != "0.00" {
		t.Fatalf("ajuste/nota/total: %q %q %q", records[6][6], records[6][7], records[6][8])
	}
	// Ventas como entero.
	if records[4][2] != "100" {
		t.Fatalf("ventas = %q", records[4][2])
	}
}
