package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Contrato HTTP de la nómina (F3.2 del contrato F3): las cuatro rutas, los
// permisos de D7 rol por rol, el aislamiento por tenant, la validación en
// español y la forma EXACTA de las respuestas.

// payrollNow es el «hoy» de los tests: marzo 2026 en curso, febrero cerrado.
var payrollNow = time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)

// payEnv es el router completo con la nómina cableada sobre los fakes.
type payEnv struct {
	Env    *httpkit.Env
	r      *gin.Engine
	Body   *httpkit.PayModels
	Sales  *httpkit.PayrollSales
	Salon  string
	otros  string
	Users  map[string]string // email -> userID
	tokens map[string]string // email -> token
}

func newPayEnv(t *testing.T) *payEnv {
	t.Helper()
	env := httpkit.NewPOSEnv()
	pay := &httpkit.PayModels{Mems: env.Mems, Users: env.Users, Rows: map[string]domain.MemberPay{}}
	sales := &httpkit.PayrollSales{}
	d := env.Deps
	d.Payroll = httpapi.PayrollHandler{
		PayModels: app.PayModelService{Pay: pay, Memberships: env.Mems},
		Payroll: app.PayrollService{
			Pay: pay, Sales: sales, Memberships: env.Mems, Settings: env.Settings,
			Now: func() time.Time { return payrollNow },
		},
		Memberships: env.Mems,
	}
	r := gin.New()
	httpapi.RegisterRoutes(r, d)

	e := &payEnv{Env: env, r: r, Body: pay, Sales: sales, Users: map[string]string{}, tokens: map[string]string{}}
	// Personas: dueña, administración, dos profesionales (una se dará de baja),
	// recepción y el dueño de OTRO salón con su propia profesional.
	for _, email := range []string{
		"duena@pay.com", "admin@pay.com", "ana@pay.com", "beto@pay.com",
		"recep@pay.com", "ajeno@pay.com", "carla@pay.com",
	} {
		tok, uid := env.Register(t, email)
		e.tokens[email], e.Users[email] = tok, uid
	}
	e.Salon = env.Salon(t, e.tokens["duena@pay.com"], "nomina")
	e.otros = env.Salon(t, e.tokens["ajeno@pay.com"], "nomina-otra")
	for _, m := range []domain.Membership{
		{UserID: e.Users["admin@pay.com"], SalonID: e.Salon, Role: domain.RoleAdmin},
		{UserID: e.Users["ana@pay.com"], SalonID: e.Salon, Role: domain.RoleProfessional, Attends: true},
		{UserID: e.Users["beto@pay.com"], SalonID: e.Salon, Role: domain.RoleProfessional, Attends: true},
		{UserID: e.Users["recep@pay.com"], SalonID: e.Salon, Role: domain.RoleReception},
		{UserID: e.Users["carla@pay.com"], SalonID: e.otros, Role: domain.RoleProfessional, Attends: true},
	} {
		if err := env.Mems.Save(m); err != nil {
			t.Fatalf("membership %s: %v", m.UserID, err)
		}
	}
	return e
}

// deactivate marca la baja de una persona en el fake (como ended_at).
func (e *payEnv) deactivate(t *testing.T, email string) {
	t.Helper()
	key := e.Users[email] + "|" + e.Salon
	m, ok := e.Env.Mems.ByKey[key]
	if !ok {
		t.Fatalf("%s no es miembro", email)
	}
	ended := payrollNow.Add(-48 * time.Hour)
	m.EndedAt = &ended
	e.Env.Mems.ByKey[key] = m
}

func (e *payEnv) url(salonID, path string) string {
	return "/api/v1/salons/" + salonID + path
}

func (e *payEnv) do(t *testing.T, method, path, body, email string) *httptest.ResponseRecorder {
	t.Helper()
	return httpkit.Do(e.r, method, path, body, e.tokens[email])
}

// keys devuelve el conjunto ordenado de claves de un objeto JSON: la forma de la
// respuesta se compara entera, no por partes.

// Matriz de permisos de D7 sobre las CUATRO rutas.
func TestHTTP_payroll_permisos_por_rol(t *testing.T) {
	e := newPayEnv(t)
	payModels := e.url(e.Salon, "/pay-models")
	payModel := e.url(e.Salon, "/members/"+e.Users["ana@pay.com"]+"/pay-model")
	preview := e.url(e.Salon, "/payroll/preview")
	me := e.url(e.Salon, "/payroll/me?kind=month&date=2026-03-10")
	putBody := `{"fixed_cents":1000,"fixed_period":"month","commission_enabled":true}`
	prevBody := `{"kind":"month","date":"2026-03-10"}`

	cases := []struct {
		rol   string
		memos func(path string) (method, body string)
		want  map[string]int
	}{
		{
			rol: "duena@pay.com",
			memos: func(path string) (string, string) {
				switch path {
				case payModels:
					return "GET", ""
				case payModel:
					return "PUT", putBody
				case preview:
					return "POST", prevBody
				default:
					return "GET", ""
				}
			},
			want: map[string]int{payModels: 200, payModel: 200, preview: 200, me: 200},
		},
		{
			rol: "admin@pay.com",
			memos: func(path string) (string, string) {
				switch path {
				case payModels:
					return "GET", ""
				case payModel:
					return "PUT", putBody
				case preview:
					return "POST", prevBody
				default:
					return "GET", ""
				}
			},
			want: map[string]int{payModels: 200, payModel: 200, preview: 200, me: 200},
		},
		{
			// Recepción: 403 en las cuatro (tampoco ve las suyas, D7).
			rol: "recep@pay.com",
			memos: func(path string) (string, string) {
				switch path {
				case payModels:
					return "GET", ""
				case payModel:
					return "PUT", putBody
				case preview:
					return "POST", prevBody
				default:
					return "GET", ""
				}
			},
			want: map[string]int{payModels: 403, payModel: 403, preview: 403, me: 403},
		},
		{
			// Profesional: 403 en todo menos «mis comisiones».
			rol: "ana@pay.com",
			memos: func(path string) (string, string) {
				switch path {
				case payModels:
					return "GET", ""
				case payModel:
					return "PUT", putBody
				case preview:
					return "POST", prevBody
				default:
					return "GET", ""
				}
			},
			want: map[string]int{payModels: 403, payModel: 403, preview: 403, me: 200},
		},
	}
	for _, c := range cases {
		for _, path := range []string{payModels, payModel, preview, me} {
			method, body := c.memos(path)
			w := e.do(t, method, path, body, c.rol)
			if w.Code != c.want[path] {
				t.Fatalf("%s %s %s: %d %s (esperaba %d)", c.rol, method, path, w.Code, w.Body.String(), c.want[path])
			}
			// Recepción y profesional reciben el mensaje exacto de D7 en las rutas
			// donde son miembros del salón.
			if c.want[path] == 403 && (c.rol == "recep@pay.com" || c.rol == "ana@pay.com") && path != me {
				var out map[string]string
				httpkit.MustJSON(t, w.Body.Bytes(), &out)
				if out["error"] != "sin permiso" {
					t.Fatalf("%s %s: mensaje = %q", c.rol, path, out["error"])
				}
			}
			if c.want[path] == 403 && path == me && c.rol == "recep@pay.com" {
				var out map[string]string
				httpkit.MustJSON(t, w.Body.Bytes(), &out)
				if out["error"] != "sin permiso" {
					t.Fatalf("recepción en /me: mensaje = %q", out["error"])
				}
			}
		}
	}

	// Sin sesión: 401 en las cuatro.
	for _, path := range []string{payModels, payModel, preview, me} {
		method := "GET"
		body := ""
		if path == payModel {
			method, body = "PUT", putBody
		}
		if path == preview {
			method, body = "POST", prevBody
		}
		if w := httpkit.Do(e.r, method, path, body, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("sin token %s %s: %d", method, path, w.Code)
		}
	}

	// Un miembro dado de baja: 403 en las cuatro.
	e.deactivate(t, "beto@pay.com")
	for _, path := range []string{payModels, payModel, preview, me} {
		method, body := "GET", ""
		if path == payModel {
			method, body = "PUT", putBody
		}
		if path == preview {
			method, body = "POST", prevBody
		}
		// Beto pide su propio modelo también: la baja no administra nada.
		if w := e.do(t, method, path, body, "beto@pay.com"); w.Code != http.StatusForbidden {
			t.Fatalf("dado de baja %s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	}

	// El dueño de OTRO salón sobre el salón ajeno: 403 en las cuatro.
	for _, path := range []string{payModels, payModel, preview, me} {
		method, body := "GET", ""
		if path == payModel {
			method, body = "PUT", putBody
		}
		if path == preview {
			method, body = "POST", prevBody
		}
		if w := e.do(t, method, path, body, "ajeno@pay.com"); w.Code != http.StatusForbidden {
			t.Fatalf("dueño ajeno %s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	}
}

// GET /pay-models: TODOS los miembros (activos y dados de baja) con la forma del
// contrato, el default de D3 y aislamiento por salón.
func TestHTTP_pay_models_listado(t *testing.T) {
	e := newPayEnv(t)
	e.deactivate(t, "beto@pay.com")

	w := e.do(t, "GET", e.url(e.Salon, "/pay-models"), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		PayModels []map[string]any `json:"pay_models"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if len(out.PayModels) != 5 {
		t.Fatalf("esperaba los 5 miembros del salón, dio %d: %s", len(out.PayModels), w.Body.String())
	}
	byUser := map[string]map[string]any{}
	for _, m := range out.PayModels {
		httpkit.SameKeys(t, "pay_model", m,
			"user_id", "name", "role", "active", "fixed_cents", "fixed_period", "commission_enabled")
		byUser[m["user_id"].(string)] = m
	}
	// La baja aparece con active=false y su nombre real.
	beto := byUser[e.Users["beto@pay.com"]]
	if beto == nil || beto["active"] != false || beto["name"] != "beto@pay.com" || beto["role"] != "professional" {
		t.Fatalf("la baja debe listarse: %+v", beto)
	}
	// Sin fila: 0, month, comisión encendida.
	ana := byUser[e.Users["ana@pay.com"]]
	if ana["fixed_cents"] != float64(0) || ana["fixed_period"] != "month" || ana["commission_enabled"] != true {
		t.Fatalf("default de D3: %+v", ana)
	}
	// Aislamiento: la profesional del otro salón NO está en la lista.
	if _, ok := byUser[e.Users["carla@pay.com"]]; ok {
		t.Fatal("la lista no puede traer miembros de otro salón")
	}

	// La otra dueña ve solo su salón (su propia membresía y la de Carla).
	wo := e.do(t, "GET", e.url(e.otros, "/pay-models"), "", "ajeno@pay.com")
	if wo.Code != http.StatusOK {
		t.Fatalf("listar el otro salón: %d %s", wo.Code, wo.Body.String())
	}
	var otro struct {
		PayModels []map[string]any `json:"pay_models"`
	}
	httpkit.MustJSON(t, wo.Body.Bytes(), &otro)
	if len(otro.PayModels) != 2 {
		t.Fatalf("el otro salón tiene 2 miembros, dio %d: %s", len(otro.PayModels), wo.Body.String())
	}
	for _, m := range otro.PayModels {
		if m["user_id"] == e.Users["ana@pay.com"] {
			t.Fatal("el otro salón no puede ver a Ana")
		}
	}
}

// PUT /pay-model: guarda y devuelve el mismo objeto; una persona de otro salón
// es 404 y no escribe nada.
func TestHTTP_pay_model_update(t *testing.T) {
	e := newPayEnv(t)
	ana := e.Users["ana@pay.com"]
	path := e.url(e.Salon, "/members/"+ana+"/pay-model")

	w := e.do(t, "PUT", path, `{"fixed_cents":150000,"fixed_period":"fortnight","commission_enabled":false}`, "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &got)
	httpkit.SameKeys(t, "pay_model", got,
		"user_id", "name", "role", "active", "fixed_cents", "fixed_period", "commission_enabled")
	if got["user_id"] != ana || got["fixed_cents"] != float64(150000) ||
		got["fixed_period"] != "fortnight" || got["commission_enabled"] != false || got["active"] != true {
		t.Fatalf("respuesta del update: %+v", got)
	}
	// Quedó guardado.
	w = e.do(t, "GET", e.url(e.Salon, "/pay-models"), "", "duena@pay.com")
	var list struct {
		PayModels []map[string]any `json:"pay_models"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	for _, m := range list.PayModels {
		if m["user_id"] == ana && m["fixed_cents"] != float64(150000) {
			t.Fatalf("no quedó guardado: %+v", m)
		}
	}

	// Una persona de OTRO salón: 404 (no existe para este salón).
	carla := e.Users["carla@pay.com"]
	if w := e.do(t, "PUT", e.url(e.Salon, "/members/"+carla+"/pay-model"),
		`{"fixed_cents":1000,"fixed_period":"month","commission_enabled":true}`, "duena@pay.com"); w.Code != http.StatusNotFound {
		t.Fatalf("persona de otro salón: %d %s", w.Code, w.Body.String())
	}
	// Y su modelo sigue siendo el default en SU salón.
	if v := e.Body.Rows[httpkit.PayRowKey(e.otros, carla)]; v.FixedCents != 0 {
		t.Fatalf("no debía escribirse nada: %+v", v)
	}
}

// Validación en español: monto negativo, monto arriba del máximo, período
// desconocido, número no entero y persona que no es del salón.
func TestHTTP_pay_model_errores(t *testing.T) {
	e := newPayEnv(t)
	ana := e.Users["ana@pay.com"]
	path := e.url(e.Salon, "/members/"+ana+"/pay-model")

	cases := []struct{ name, body, want string }{
		{"negativo", `{"fixed_cents":-1,"fixed_period":"month","commission_enabled":true}`,
			"El sueldo fijo no puede ser negativo"},
		{"arriba del máximo", `{"fixed_cents":2000000001,"fixed_period":"month","commission_enabled":true}`,
			"El sueldo fijo no puede superar los 2000000000 centavos"},
		{"período desconocido", `{"fixed_cents":1000,"fixed_period":"day","commission_enabled":true}`,
			"Período de pago desconocido: usá semana, quincena o mes"},
	}
	for _, c := range cases {
		w := e.do(t, "PUT", path, c.body, "duena@pay.com")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		var out map[string]string
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out["error"] != c.want {
			t.Fatalf("%s: mensaje = %q, esperaba %q", c.name, out["error"], c.want)
		}
	}

	// Un número no entero no es un modelo válido: 400 y nada escrito.
	w := e.do(t, "PUT", path, `{"fixed_cents":1500.5,"fixed_period":"month","commission_enabled":true}`, "duena@pay.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no entero: %d %s", w.Code, w.Body.String())
	}
	if v := e.Body.Rows[httpkit.PayRowKey(e.Salon, ana)]; v.FixedCents != 0 {
		t.Fatalf("nada debía escribirse: %+v", v)
	}

	// Una persona que no es del salón: 404.
	if w := e.do(t, "PUT", e.url(e.Salon, "/members/u-no-existe/pay-model"),
		`{"fixed_cents":1000,"fixed_period":"month","commission_enabled":true}`, "duena@pay.com"); w.Code != http.StatusNotFound {
		t.Fatalf("no miembro: %d %s", w.Code, w.Body.String())
	}
}

// POST /payroll/preview: la forma EXACTA del contrato, con el período local, las
// líneas (comisión, fijo, estimado) y los totales.
func TestHTTP_payroll_preview(t *testing.T) {
	e := newPayEnv(t)
	ana, beto := e.Users["ana@pay.com"], e.Users["beto@pay.com"]
	e.Sales.Add(e.Salon, domain.PayrollSalesTotal{
		UserID: ana, Name: "ana@pay.com", SalesCount: 3, SalesTotalCents: 3499,
		CommissionBase: 49990, Estimated: true,
	})
	e.Sales.Add(e.Salon, domain.PayrollSalesTotal{
		UserID: beto, Name: "beto@pay.com", SalesCount: 1, SalesTotalCents: 2000, CommissionBase: 20000,
	})
	// La dueña cobra un fijo mensual sin comisión.
	if w := e.do(t, "PUT", e.url(e.Salon, "/members/"+e.Users["duena@pay.com"]+"/pay-model"),
		`{"fixed_cents":100000,"fixed_period":"month","commission_enabled":false}`, "duena@pay.com"); w.Code != http.StatusOK {
		t.Fatalf("modelo de la dueña: %d %s", w.Code, w.Body.String())
	}

	w := e.do(t, "POST", e.url(e.Salon, "/payroll/preview"), `{"kind":"month","date":"2026-03-10"}`, "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	httpkit.SameKeys(t, "preview", out, "period", "lines", "totals")

	period := out["period"].(map[string]any)
	httpkit.SameKeys(t, "period", period, "kind", "from", "to", "closed")
	if period["kind"] != "month" || period["from"] != "2026-03-01" || period["to"] != "2026-03-31" || period["closed"] != false {
		t.Fatalf("período: %+v", period)
	}

	lines := out["lines"].([]any)
	if len(lines) != 3 {
		t.Fatalf("esperaba 3 líneas, dio %d: %s", len(lines), w.Body.String())
	}
	for _, raw := range lines {
		httpkit.SameKeys(t, "line", raw.(map[string]any),
			"user_id", "name", "active", "sales_count", "sales_total_cents",
			"commission_cents", "fixed_cents", "total_cents", "estimated")
	}
	// Orden: total desc y después nombre.
	first := lines[0].(map[string]any)
	if first["user_id"] != e.Users["duena@pay.com"] || first["fixed_cents"] != float64(100000) ||
		first["commission_cents"] != float64(0) || first["total_cents"] != float64(100000) {
		t.Fatalf("línea de la dueña: %+v", first)
	}
	second := lines[1].(map[string]any)
	if second["user_id"] != ana || second["commission_cents"] != float64(499) ||
		second["sales_count"] != float64(3) || second["sales_total_cents"] != float64(3499) ||
		second["estimated"] != true || second["active"] != true || second["total_cents"] != float64(499) {
		t.Fatalf("línea de Ana: %+v", second)
	}
	third := lines[2].(map[string]any)
	if third["user_id"] != beto || third["commission_cents"] != float64(200) || third["estimated"] != false {
		t.Fatalf("línea de Beto: %+v", third)
	}

	totals := out["totals"].(map[string]any)
	httpkit.SameKeys(t, "totals", totals, "commission_cents", "fixed_cents", "total_cents")
	if totals["commission_cents"] != float64(699) || totals["fixed_cents"] != float64(100000) ||
		totals["total_cents"] != float64(100699) {
		t.Fatalf("totales: %+v", totals)
	}

	// Un período terminado sale con closed:true (febrero con «hoy» 20/3).
	w = e.do(t, "POST", e.url(e.Salon, "/payroll/preview"), `{"kind":"month","date":"2026-02-10"}`, "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("preview de febrero: %d %s", w.Code, w.Body.String())
	}
	var feb map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &feb)
	if feb["period"].(map[string]any)["closed"] != true {
		t.Fatalf("febrero debe estar cerrado: %+v", feb["period"])
	}
}

// GET /payroll/me: solo lo del que llama, con la misma forma del período.
func TestHTTP_payroll_me(t *testing.T) {
	e := newPayEnv(t)
	ana, beto := e.Users["ana@pay.com"], e.Users["beto@pay.com"]
	e.Sales.Add(e.Salon, domain.PayrollSalesTotal{
		UserID: ana, Name: "ana@pay.com", SalesCount: 3, SalesTotalCents: 3499,
		CommissionBase: 49990, Estimated: true,
	})
	// Beto vendió mucho más: sus números no pueden aparecer en la respuesta de Ana.
	e.Sales.Add(e.Salon, domain.PayrollSalesTotal{
		UserID: beto, Name: "beto@pay.com", SalesCount: 9, SalesTotalCents: 900000, CommissionBase: 9000000,
	})
	// Y Ana tiene un modelo de pago suyo que tampoco se expone.
	if w := e.do(t, "PUT", e.url(e.Salon, "/members/"+ana+"/pay-model"),
		`{"fixed_cents":0,"fixed_period":"month","commission_enabled":true}`, "duena@pay.com"); w.Code != http.StatusOK {
		t.Fatalf("modelo de Ana: %d %s", w.Code, w.Body.String())
	}

	w := e.do(t, "GET", e.url(e.Salon, "/payroll/me?kind=month&date=2026-03-10"), "", "ana@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	httpkit.SameKeys(t, "me", out, "period", "sales_count", "sales_total_cents", "commission_cents", "estimated")
	period := out["period"].(map[string]any)
	httpkit.SameKeys(t, "period", period, "kind", "from", "to", "closed")
	if period["from"] != "2026-03-01" || period["to"] != "2026-03-31" {
		t.Fatalf("período: %+v", period)
	}
	if out["sales_count"] != float64(3) || out["sales_total_cents"] != float64(3499) ||
		out["commission_cents"] != float64(499) || out["estimated"] != true {
		t.Fatalf("mis comisiones: %+v", out)
	}

	// La administración también puede ver las suyas (0 ventas).
	w = e.do(t, "GET", e.url(e.Salon, "/payroll/me?kind=week&date=2026-03-10"), "", "admin@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("admin en /me: %d %s", w.Code, w.Body.String())
	}
	var adm map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &adm)
	if adm["sales_count"] != float64(0) || adm["commission_cents"] != float64(0) {
		t.Fatalf("admin sin ventas: %+v", adm)
	}
	if adm["period"].(map[string]any)["from"] != "2026-03-09" {
		t.Fatalf("la semana del 10/3 empieza el 9/3: %+v", adm["period"])
	}
}

// Período inválido: 400 con el mensaje exacto del contrato en las dos rutas.
func TestHTTP_payroll_periodo_invalido(t *testing.T) {
	e := newPayEnv(t)
	cases := []string{
		`{"kind":"day","date":"2026-03-10"}`,
		`{"kind":"","date":"2026-03-10"}`,
		`{"kind":"month","date":"2026-13-01"}`,
		`{"kind":"month","date":"10/03/2026"}`,
		`{"kind":"month","date":""}`,
	}
	for _, body := range cases {
		w := e.do(t, "POST", e.url(e.Salon, "/payroll/preview"), body, "duena@pay.com")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("preview %s: %d %s", body, w.Code, w.Body.String())
		}
		var out map[string]string
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out["error"] != "Período inválido" {
			t.Fatalf("preview %s: mensaje = %q", body, out["error"])
		}
	}
	// /me del profesional con un tipo inválido: 400, no 403 (el rol sí puede).
	w := e.do(t, "GET", e.url(e.Salon, "/payroll/me?kind=dia&date=2026-03-10"), "", "ana@pay.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("me con tipo inválido: %d %s", w.Code, w.Body.String())
	}
	var out map[string]string
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if out["error"] != "Período inválido" {
		t.Fatalf("me: mensaje = %q", out["error"])
	}
	// Y sin parámetros tampoco se inventa un período.
	if w := e.do(t, "GET", e.url(e.Salon, "/payroll/me"), "", "ana@pay.com"); w.Code != http.StatusBadRequest {
		t.Fatalf("me sin parámetros: %d %s", w.Code, w.Body.String())
	}
}

// PRESERVE: las rutas que comparten el router y los servicios de dinero
// (ventas, dashboard, cierre de caja y equipo) siguen respondiendo igual.
func TestHTTP_payroll_preserva_ventas_dashboard_caja_y_equipo(t *testing.T) {
	e := newPayEnv(t)

	// GET /sales: misma forma ({"sales":[...]}) y scope por salón.
	w := e.do(t, "GET", e.url(e.Salon, "/sales"), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("ventas: %d %s", w.Code, w.Body.String())
	}
	var salesOut map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &salesOut)
	httpkit.SameKeys(t, "sales", salesOut, "sales")
	if _, ok := salesOut["sales"].([]any); !ok {
		t.Fatalf("sales debe ser un array: %s", w.Body.String())
	}

	// GET /dashboard: mismas cuatro claves.
	w = e.do(t, "GET", e.url(e.Salon, "/dashboard"), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
	var dash map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &dash)
	httpkit.SameKeys(t, "dashboard", dash, "revenue_today", "appointments_today", "occupancy_rate", "top_services")

	// POST /cash-closes: 201 con la forma de siempre.
	w = e.do(t, "POST", e.url(e.Salon, "/cash-closes"), `{"counted_cents":0,"note":""}`, "duena@pay.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("cierre: %d %s", w.Code, w.Body.String())
	}
	var closeOut map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &closeOut)
	httpkit.SameKeys(t, "cash_close", closeOut, "close_id", "expected_cash")

	// GET /members: misma forma por miembro (la nómina no la cambió).
	w = e.do(t, "GET", e.url(e.Salon, "/members"), "", "duena@pay.com")
	if w.Code != http.StatusOK {
		t.Fatalf("equipo: %d %s", w.Code, w.Body.String())
	}
	var members struct {
		Members []map[string]any `json:"members"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &members)
	if len(members.Members) != 5 {
		t.Fatalf("esperaba 5 miembros, dio %d", len(members.Members))
	}
	for _, m := range members.Members {
		httpkit.SameKeys(t, "member", m, "id", "name", "email", "role", "attends",
			"trade", "service_ids", "active", "ended_at")
	}
}
