// Contrato HTTP de los gastos (F3b, D3/D4/D5): las cuatro rutas, la forma EXACTA
// de la respuesta, los permisos rol por rol, el aislamiento por tenant, la
// validación en español y el PUT que REEMPLAZA sin escribir nada si falta una
// clave.
package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/finance"
	financehttpapi "salon-saas/internal/finance/httpapi"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// expNow es el «hoy» de estos tests: 20/3/2026 12:00 en CDMX.
var expNow = time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)

type expHTTPEnv struct {
	*httpkit.Env
	r        *gin.Engine
	Expenses *httpkit.Expenses
	Profit   *httpkit.Profit
	Salon    string
	otros    string
	Users    map[string]string
	tokens   map[string]string
}

func newExpHTTPEnv(t *testing.T) *expHTTPEnv {
	t.Helper()
	env := httpkit.NewPOSEnv()
	expenses, profit := httpkit.NewExpenses(), &httpkit.Profit{}
	d := env.Deps
	d.Expenses = financehttpapi.ExpenseHandler{
		Expenses: finance.ExpenseService{
			Expenses: expenses, Memberships: env.Mems, Settings: env.Settings,
			Now: func() time.Time { return expNow },
		},
		Memberships: env.Mems,
	}
	d.Profit = financehttpapi.ProfitHandler{
		Profit: finance.ProfitService{
			Profit: profit, Memberships: env.Mems, Settings: env.Settings,
			Now: func() time.Time { return expNow },
		},
		Memberships: env.Mems,
	}
	r := gin.New()
	httpapi.RegisterRoutes(r, d)

	e := &expHTTPEnv{
		Env: env, r: r, Expenses: expenses, Profit: profit,
		Users: map[string]string{}, tokens: map[string]string{},
	}
	for _, email := range []string{
		"duena@exp.com", "admin@exp.com", "recep@exp.com", "ana@exp.com",
		"cli@exp.com", "ajeno@exp.com",
	} {
		tok, uid := env.Register(t, email)
		e.tokens[email], e.Users[email] = tok, uid
	}
	e.Salon = env.Salon(t, e.tokens["duena@exp.com"], "gastos")
	e.otros = env.Salon(t, e.tokens["ajeno@exp.com"], "gastos-otros")
	for _, m := range []domain.Membership{
		{UserID: e.Users["admin@exp.com"], SalonID: e.Salon, Role: domain.RoleAdmin},
		{UserID: e.Users["recep@exp.com"], SalonID: e.Salon, Role: domain.RoleReception},
		{UserID: e.Users["ana@exp.com"], SalonID: e.Salon, Role: domain.RoleProfessional, Attends: true},
		{UserID: e.Users["cli@exp.com"], SalonID: e.Salon, Role: domain.RoleClient},
	} {
		if err := env.Mems.Save(m); err != nil {
			t.Fatalf("membership %s: %v", m.UserID, err)
		}
	}
	// El salón vive en CDMX (UTC-6 todo el año): «hoy» es el día local.
	if err := env.Settings.SaveSettings(domain.SalonSettings{
		SalonID: e.Salon, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}); err != nil {
		t.Fatalf("settings: %v", err)
	}
	return e
}

func (e *expHTTPEnv) url(salonID, path string) string {
	return "/api/v1/salons/" + salonID + path
}

func (e *expHTTPEnv) do(t *testing.T, method, path, body, email string) *httptest.ResponseRecorder {
	t.Helper()
	return httpkit.Do(e.r, method, path, body, e.tokens[email])
}

func (e *expHTTPEnv) createExpense(t *testing.T, email, body string) string {
	t.Helper()
	return e.createExpenseIn(t, "duena@exp.com", email, body)
}

// createExpenseIn crea el gasto en el salón de QUIEN lo crea (los miembros del
// salón principal usan e.Salon).
func (e *expHTTPEnv) createExpenseIn(t *testing.T, owner, email, body string) string {
	t.Helper()
	salonID := e.Salon
	if owner == "ajeno@exp.com" {
		salonID = e.otros
	}
	w := e.do(t, "POST", e.url(salonID, "/expenses"), body, email)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("la respuesta no trae id: %s", w.Body.String())
	}
	return id
}

const expValidBody = `{"category":"rent","amount_cents":150000,"spent_on":"2026-03-19","description":"Alquiler"}`

// Matriz de permisos de D5 sobre las CINCO rutas: solo owner/admin; sin token 401.
func TestHTTP_expenses_profit_permisos_por_rol(t *testing.T) {
	e := newExpHTTPEnv(t)
	id := e.createExpense(t, "duena@exp.com", expValidBody)
	expenses := e.url(e.Salon, "/expenses?from=2026-03-01&to=2026-03-31")
	one := e.url(e.Salon, "/expenses/"+id)
	profit := e.url(e.Salon, "/profit?kind=month&date=2026-03-10")

	for _, tc := range []struct {
		email string
		want  map[string]int
	}{
		{"duena@exp.com", map[string]int{"list": 200, "update": 200, "delete": 204, "profit": 200}},
		{"admin@exp.com", map[string]int{"list": 200, "update": 200, "delete": 204, "profit": 200}},
		{"recep@exp.com", map[string]int{"list": 403, "update": 403, "delete": 403, "profit": 403}},
		{"ana@exp.com", map[string]int{"list": 403, "update": 403, "delete": 403, "profit": 403}},
		{"cli@exp.com", map[string]int{"list": 403, "update": 403, "delete": 403, "profit": 403}},
		{"ajeno@exp.com", map[string]int{"list": 403, "update": 403, "delete": 403, "profit": 403}},
	} {
		if w := e.do(t, "GET", expenses, "", tc.email); w.Code != tc.want["list"] {
			t.Fatalf("%s listar: %d %s", tc.email, w.Code, w.Body.String())
		}
		if w := e.do(t, "PUT", one, expValidBody, tc.email); w.Code != tc.want["update"] {
			t.Fatalf("%s editar: %d %s", tc.email, w.Code, w.Body.String())
		}
		if w := e.do(t, "GET", profit, "", tc.email); w.Code != tc.want["profit"] {
			t.Fatalf("%s ganancias: %d %s", tc.email, w.Code, w.Body.String())
		}
		// Borrar va AL FINAL de cada rol para no quedarse sin gasto en los
		// siguientes roles: el que puede lo borra y el que no, no lo toca.
		if w := e.do(t, "DELETE", one, "", tc.email); w.Code != tc.want["delete"] {
			t.Fatalf("%s borrar: %d %s", tc.email, w.Code, w.Body.String())
		}
		if tc.want["delete"] == 204 {
			id = e.createExpense(t, "duena@exp.com", expValidBody)
			one = e.url(e.Salon, "/expenses/"+id)
		}
	}

	// Sin token: 401 en las cinco rutas (el middleware corta antes del caso de uso).
	for _, tc := range []struct{ method, path, body string }{
		{"POST", e.url(e.Salon, "/expenses"), expValidBody},
		{"GET", expenses, ""},
		{"PUT", one, expValidBody},
		{"DELETE", one, ""},
		{"GET", profit, ""},
	} {
		if w := httpkit.Do(e.r, tc.method, tc.path, tc.body, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s sin token: %d", tc.method, tc.path, w.Code)
		}
	}
}

// Ciclo completo y forma EXACTA del objeto `expense`.
func TestHTTP_expenses_ciclo_y_forma(t *testing.T) {
	e := newExpHTTPEnv(t)
	w := e.do(t, "POST", e.url(e.Salon, "/expenses"), expValidBody, "duena@exp.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("crear: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &got)
	httpkit.SameKeys(t, "expense", got,
		"id", "category", "amount_cents", "spent_on", "description", "created_at", "created_by")
	if got["category"] != "rent" || got["amount_cents"] != float64(150000) ||
		got["spent_on"] != "2026-03-19" || got["description"] != "Alquiler" ||
		got["created_by"] != e.Users["duena@exp.com"] {
		t.Fatalf("expense = %v", got)
	}
	if _, err := time.Parse(time.RFC3339, got["created_at"].(string)); err != nil {
		t.Fatalf("created_at no es RFC3339: %v (%v)", got["created_at"], err)
	}
	id := got["id"].(string)

	// Listar: forma del listado y orden.
	w = e.do(t, "GET", e.url(e.Salon, "/expenses?from=2026-03-01&to=2026-03-31"), "", "duena@exp.com")
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", w.Code, w.Body.String())
	}
	var list map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	httpkit.SameKeys(t, "listado", list, "expenses", "truncated")
	if list["truncated"] != false {
		t.Fatalf("truncated = %v", list["truncated"])
	}
	if items := list["expenses"].([]any); len(items) != 1 {
		t.Fatalf("gastos = %v", items)
	}

	// Editar (las cuatro claves, descripción vacía permitida).
	w = e.do(t, "PUT", e.url(e.Salon, "/expenses/"+id),
		`{"category":"utilities","amount_cents":1,"spent_on":"2026-03-01","description":""}`, "admin@exp.com")
	if w.Code != http.StatusOK {
		t.Fatalf("editar: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &got)
	if got["category"] != "utilities" || got["amount_cents"] != float64(1) ||
		got["spent_on"] != "2026-03-01" || got["description"] != "" || got["id"] != id {
		t.Fatalf("expense editado = %v", got)
	}

	// Borrar: 204 y después 404 con la frase del contrato.
	if w = e.do(t, "DELETE", e.url(e.Salon, "/expenses/"+id), "", "duena@exp.com"); w.Code != http.StatusNoContent {
		t.Fatalf("borrar: %d %s", w.Code, w.Body.String())
	}
	w = e.do(t, "DELETE", e.url(e.Salon, "/expenses/"+id), "", "duena@exp.com")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Gasto no encontrado") {
		t.Fatalf("borrar dos veces: %d %s", w.Code, w.Body.String())
	}
	w = e.do(t, "PUT", e.url(e.Salon, "/expenses/"+id), expValidBody, "duena@exp.com")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Gasto no encontrado") {
		t.Fatalf("editar inexistente: %d %s", w.Code, w.Body.String())
	}
}

// Las frases EXACTAS de la validación, los bordes del contrato y el 400 que no
// escribe nada.
func TestHTTP_expenses_validacion(t *testing.T) {
	e := newExpHTTPEnv(t)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"monto cero", `{"category":"rent","amount_cents":0,"spent_on":"2026-03-19"}`,
			"El monto debe ser mayor a cero"},
		{"monto negativo", `{"category":"rent","amount_cents":-1,"spent_on":"2026-03-19"}`,
			"El monto debe ser mayor a cero"},
		{"monto sobre el tope", `{"category":"rent","amount_cents":2000000001,"spent_on":"2026-03-19"}`,
			"El monto no puede superar los 2000000000 centavos"},
		{"categoría inválida", `{"category":"salary","amount_cents":100,"spent_on":"2026-03-19"}`,
			"Categoría inválida: usá alquiler, insumos, servicios u otros"},
		{"fecha con barras", `{"category":"rent","amount_cents":100,"spent_on":"19/03/2026"}`,
			"Fecha inválida: usá AAAA-MM-DD"},
		{"fecha futura", `{"category":"rent","amount_cents":100,"spent_on":"2026-03-21"}`,
			"La fecha del gasto no puede ser futura"},
		{"fecha anterior a 2000", `{"category":"rent","amount_cents":100,"spent_on":"1999-12-31"}`,
			"La fecha del gasto es demasiado antigua"},
		{"descripción 201 runes", `{"category":"rent","amount_cents":100,"spent_on":"2026-03-19","description":"` +
			strings.Repeat("ñ", 201) + `"}`, "La descripción no puede superar los 200 caracteres"},
		{"falta la categoría", `{"amount_cents":100,"spent_on":"2026-03-19"}`,
			"Faltan datos del gasto: categoría, monto y fecha"},
		{"falta el monto", `{"category":"rent","spent_on":"2026-03-19"}`,
			"Faltan datos del gasto: categoría, monto y fecha"},
		{"falta la fecha", `{"category":"rent","amount_cents":100}`,
			"Faltan datos del gasto: categoría, monto y fecha"},
	}
	for _, tc := range cases {
		w := e.do(t, "POST", e.url(e.Salon, "/expenses"), tc.body, "duena@exp.com")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: %s, esperaba %q", tc.name, w.Body.String(), tc.want)
		}
	}
	// Un 400 no escribió nada.
	w := e.do(t, "GET", e.url(e.Salon, "/expenses?from=2026-03-01&to=2026-03-31"), "", "duena@exp.com")
	var list map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if items := list["expenses"].([]any); len(items) != 0 {
		t.Fatalf("un 400 escribió: %v", items)
	}

	// Bordes que SÍ entran: monto 1 y el tope, hoy local, 2000-01-01, 200 runes.
	for _, ok := range []string{
		`{"category":"rent","amount_cents":1,"spent_on":"2026-03-19"}`,
		`{"category":"supplies","amount_cents":2000000000,"spent_on":"2026-03-19"}`,
		`{"category":"other","amount_cents":100,"spent_on":"2026-03-20"}`,
		`{"category":"other","amount_cents":100,"spent_on":"2000-01-01"}`,
		`{"category":"utilities","amount_cents":100,"spent_on":"2026-03-19","description":"` +
			strings.Repeat("ñ", 200) + `"}`,
	} {
		if w := e.do(t, "POST", e.url(e.Salon, "/expenses"), ok, "duena@exp.com"); w.Code != http.StatusCreated {
			t.Fatalf("%s debería ser 201: %d %s", ok, w.Code, w.Body.String())
		}
	}

	// Tipos equivocados y JSON roto: 400, nunca 500.
	for _, bad := range []string{
		`{"category":"rent","amount_cents":"100","spent_on":"2026-03-19"}`,
		`{"category":"rent","amount_cents":10.5,"spent_on":"2026-03-19"}`,
		`{"category":7,"amount_cents":100,"spent_on":"2026-03-19"}`,
		`{"category":"rent","amount_cents":100,"spent_on":20260319}`,
		`no-es-json`,
		`[]`,
	} {
		if w := e.do(t, "POST", e.url(e.Salon, "/expenses"), bad, "duena@exp.com"); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body.String())
		}
	}
}

// El PUT exige las CUATRO claves: si falta una es 400 con la frase del contrato y
// no se escribe nada.
func TestHTTP_expenses_put_incompleto_no_escribe(t *testing.T) {
	e := newExpHTTPEnv(t)
	id := e.createExpense(t, "duena@exp.com", expValidBody)
	path := e.url(e.Salon, "/expenses/"+id)
	missing := "Faltan datos del gasto: categoría, monto, fecha y descripción"
	for _, tc := range []struct{ body, want string }{
		{`{"category":"other","amount_cents":5,"spent_on":"2026-03-10"}`, missing},
		{`{"category":"other","amount_cents":5,"description":"x"}`, missing},
		{`{"amount_cents":5,"spent_on":"2026-03-10","description":"x"}`, missing},
		{`{"category":"other","spent_on":"2026-03-10","description":"x"}`, missing},
		{`{"description":null,"category":"other","amount_cents":5,"spent_on":"2026-03-10"}`, missing},
		// Tipo equivocado: 400, nunca 500.
		{`{"category":"other","amount_cents":5,"spent_on":"2026-03-10","description":7}`, "body inválido"},
	} {
		w := e.do(t, "PUT", path, tc.body, "duena@exp.com")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: %s, esperaba %q", tc.body, w.Body.String(), tc.want)
		}
	}
	// El gasto sigue con sus valores originales.
	w := e.do(t, "GET", e.url(e.Salon, "/expenses?from=2026-03-01&to=2026-03-31"), "", "duena@exp.com")
	var list map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	item := list["expenses"].([]any)[0].(map[string]any)
	if item["category"] != "rent" || item["amount_cents"] != float64(150000) || item["description"] != "Alquiler" {
		t.Fatalf("el PUT incompleto escribió: %v", item)
	}
}

// El gasto de OTRO salón es 404 «Gasto no encontrado» (editar y borrar) y el
// listado no lo trae.
func TestHTTP_expenses_aislamiento_de_tenant(t *testing.T) {
	e := newExpHTTPEnv(t)
	ajeno := e.createExpenseIn(t, "ajeno@exp.com", "ajeno@exp.com",
		`{"category":"rent","amount_cents":999,"spent_on":"2026-03-10"}`)

	if w := e.do(t, "PUT", e.url(e.Salon, "/expenses/"+ajeno), expValidBody, "duena@exp.com"); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "Gasto no encontrado") {
		t.Fatalf("editar el de otro salón: %d %s", w.Code, w.Body.String())
	}
	if w := e.do(t, "DELETE", e.url(e.Salon, "/expenses/"+ajeno), "", "duena@exp.com"); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "Gasto no encontrado") {
		t.Fatalf("borrar el de otro salón: %d %s", w.Code, w.Body.String())
	}
	// Y no se borró: su salón lo sigue viendo.
	w := e.do(t, "GET", e.url(e.otros, "/expenses?from=2026-03-01&to=2026-03-31"), "", "ajeno@exp.com")
	if w.Code != http.StatusOK {
		t.Fatalf("listar el otro salón: %d %s", w.Code, w.Body.String())
	}
	var list map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if items := list["expenses"].([]any); len(items) != 1 {
		t.Fatalf("el gasto ajeno desapareció: %v", items)
	}
	// El listado del salón no trae el ajeno.
	w = e.do(t, "GET", e.url(e.Salon, "/expenses?from=2026-03-01&to=2026-03-31"), "", "duena@exp.com")
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if items := list["expenses"].([]any); len(items) != 0 {
		t.Fatalf("el listado trajo otro tenant: %v", items)
	}
}

// El rango del listado: obligatorio, to >= from, hasta 366 días.
func TestHTTP_expenses_rango(t *testing.T) {
	e := newExpHTTPEnv(t)
	for _, q := range []string{
		"", "?from=2026-03-01", "?to=2026-03-31",
		"?from=2026-03-31&to=2026-03-01",
		"?from=2026-01-01&to=2027-01-02",
		"?from=10/03/2026&to=2026-03-31",
		"?from=2026-03-01&to=2026-13-01",
	} {
		w := e.do(t, "GET", e.url(e.Salon, "/expenses"+q), "", "duena@exp.com")
		if w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), "Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días") {
			t.Fatalf("%q: %d %s", q, w.Code, w.Body.String())
		}
	}
	// 366 días inclusive es válido y la lista vacía es [] (no null).
	w := e.do(t, "GET", e.url(e.Salon, "/expenses?from=2026-01-01&to=2027-01-01"), "", "duena@exp.com")
	if w.Code != http.StatusOK {
		t.Fatalf("366 días: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"expenses":[]`) {
		t.Fatalf("la lista vacía tiene que ser []: %s", w.Body.String())
	}
}
