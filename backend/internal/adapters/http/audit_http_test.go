// Pruebas HTTP del registro de auditoría (F10, D2 y D3): el middleware
// `Audit(action)` (registra solo en 2xx, no cambia la respuesta, un fallo del
// escritor se traga) y `GET /salons/:id/audit-log` (permiso primero, límite,
// cursor, filtro, aislamiento).
package httpapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	httpapi "salon-saas/internal/adapters/http"
	auditsvc "salon-saas/internal/audit"
	"salon-saas/internal/domain"
	"salon-saas/internal/finance"
	"salon-saas/internal/loyalty"
	"salon-saas/internal/notifications"
	"salon-saas/internal/products"
	httpkit "salon-saas/internal/testutil/httpkit"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// --- Middleware solo (router mínimo) -----------------------------------------------

type mwEnv struct {
	r    *gin.Engine
	repo *httpkit.AuditEvents
	logs []string
	// order registra lo que pasó: «handler» cuando corre la ruta y «append:<acción>»
	// cuando se escribe, para probar que el registro va DESPUÉS de la ruta.
	order []string
}

// newMWEnv monta tres rutas con la acción real del contrato (la tabla de
// entidades es por acción): una con id propio (member.update, :uid), una sin id
// (settings.update) y una con DOS ids (payroll_run.adjust, :rid y :uid). El
// handler responde el status de ?s= y un cuerpo fijo.
func newMWEnv(t *testing.T, users *httpkit.Users) *mwEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := &mwEnv{repo: &httpkit.AuditEvents{}}
	e.repo.Log = &e.order
	h := httpapi.AuditHandler{
		Service: auditsvc.AuditService{Events: e.repo, Users: users},
		Logf:    func(f string, a ...any) { e.logs = append(e.logs, fmt.Sprintf(f, a...)) },
	}
	e.r = gin.New()
	handler := func(c *gin.Context) {
		e.order = append(e.order, "handler")
		var st int
		fmt.Sscanf(c.Query("s"), "%d", &st)
		c.Header("X-Extra", "1")
		c.String(st, `{"echo":"%s"}`, c.Param("uid"))
	}
	auth := func(c *gin.Context) {
		if u := c.GetHeader("X-User"); u != "" {
			c.Set("userID", u)
		}
		c.Next()
	}
	g := e.r.Group("/api/v1", auth)
	g.PATCH("/salons/:id/members/:uid", h.Audit("member.update"), handler)
	g.PUT("/salons/:id/settings", h.Audit("settings.update"), handler)
	g.PATCH("/salons/:id/payroll/runs/:rid/lines/:uid", h.Audit("payroll_run.adjust"), handler)
	g.PATCH("/salons/:id/plain/:uid", handler) // sin Audit: control
	return e
}

func (e *mwEnv) call(method, path, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if user != "" {
		req.Header.Set("X-User", user)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func mwUsers() *httpkit.Users {
	u := &httpkit.Users{}
	_ = u.Save(domain.User{ID: "u-ana", Name: "Ana Dueña", Email: "ana@x.com"})
	return u
}

func TestAuditMW_recordsOnlyOn2xx(t *testing.T) {
	for _, st := range []int{200, 201, 202, 204} {
		e := newMWEnv(t, mwUsers())
		w := e.call("PATCH", fmt.Sprintf("/api/v1/salons/s1/members/m-7?s=%d", st), "u-ana")
		if w.Code != st {
			t.Fatalf("status %d alterado: %d", st, w.Code)
		}
		if got := e.repo.Actions(); len(got) != 1 || got[0] != "member.update|m-7" {
			t.Errorf("status %d: esperaba 1 evento member.update|m-7, hay %v", st, got)
		}
	}
	for _, st := range []int{400, 401, 403, 404, 409, 422, 429, 500, 503} {
		e := newMWEnv(t, mwUsers())
		w := e.call("PATCH", fmt.Sprintf("/api/v1/salons/s1/members/m-7?s=%d", st), "u-ana")
		if w.Code != st {
			t.Fatalf("status %d alterado: %d", st, w.Code)
		}
		if e.repo.AppendCalls != 0 {
			t.Errorf("status %d no debe registrar (appendCalls=%d)", st, e.repo.AppendCalls)
		}
	}
	// Un 3xx tampoco.
	e := newMWEnv(t, mwUsers())
	e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=302", "u-ana")
	if e.repo.AppendCalls != 0 {
		t.Error("un 302 no registra")
	}
}

func TestAuditMW_entityIsTheRoutesOwnParam(t *testing.T) {
	e := newMWEnv(t, mwUsers())
	e.call("PATCH", "/api/v1/salons/s1/members/uid-42?s=200", "u-ana")
	e.call("PUT", "/api/v1/salons/s1/settings?s=200", "u-ana")
	// payroll_run.adjust: la entidad es la corrida (:rid), no la línea (:uid).
	e.call("PATCH", "/api/v1/salons/s1/payroll/runs/run-9/lines/uid-3?s=200", "u-ana")
	want := []string{"member.update|uid-42", "settings.update|", "payroll_run.adjust|run-9"}
	got := e.repo.Actions()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("esperaba %v, hay %v", want, got)
	}
	ev := e.repo.Rows[0]
	if ev.SalonID != "s1" || ev.ActorUserID != "u-ana" {
		t.Errorf("salón y actor: %+v", ev)
	}
}

func TestAuditMW_responseIsUntouched(t *testing.T) {
	// Misma ruta con y sin el middleware, y con el escritor sano, caído y en
	// pánico: status, cuerpo y cabeceras idénticos.
	control := newMWEnv(t, mwUsers()).call("PATCH", "/api/v1/salons/s1/plain/m-7?s=201", "u-ana")
	for name, mutate := range map[string]func(*httpkit.AuditEvents){
		"sano":   func(*httpkit.AuditEvents) {},
		"caído":  func(r *httpkit.AuditEvents) { r.AppendErr = httpkit.ErrAuditDown },
		"pánico": func(r *httpkit.AuditEvents) { r.PanicOn = true },
	} {
		e := newMWEnv(t, mwUsers())
		mutate(e.repo)
		w := e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=201", "u-ana")
		if w.Code != control.Code || w.Body.String() != strings.ReplaceAll(control.Body.String(), "m-7", "m-7") ||
			w.Header().Get("X-Extra") != control.Header().Get("X-Extra") ||
			w.Header().Get("Content-Type") != control.Header().Get("Content-Type") {
			t.Errorf("%s: respuesta distinta: %d %q %v vs %d %q %v", name,
				w.Code, w.Body.String(), w.Header(), control.Code, control.Body.String(), control.Header())
		}
		if len(w.Header()) != len(control.Header()) {
			t.Errorf("%s: cabeceras de más o de menos: %v vs %v", name, w.Header(), control.Header())
		}
	}
}

func TestAuditMW_writerFailureIsLoggedAndSwallowed(t *testing.T) {
	e := newMWEnv(t, mwUsers())
	e.repo.AppendErr = httpkit.ErrAuditDown
	w := e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=200", "u-ana")
	if w.Code != 200 || w.Body.String() != `{"echo":"m-7"}` {
		t.Fatalf("la respuesta original debe quedar: %d %s", w.Code, w.Body.String())
	}
	if len(e.logs) != 1 || !strings.Contains(e.logs[0], "member.update") || !strings.Contains(e.logs[0], "audit down") {
		t.Fatalf("el fallo se loguea con la acción y el error: %v", e.logs)
	}
	// Un pánico del escritor también se traga y se loguea.
	e = newMWEnv(t, mwUsers())
	e.repo.PanicOn = true
	w = e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=200", "u-ana")
	if w.Code != 200 || w.Body.String() != `{"echo":"m-7"}` {
		t.Fatalf("pánico: la respuesta original debe quedar: %d %s", w.Code, w.Body.String())
	}
	if len(e.logs) != 1 || !strings.Contains(e.logs[0], "audit writer panic") {
		t.Fatalf("el pánico se loguea: %v", e.logs)
	}
}

func TestAuditMW_recordsAfterTheRoute(t *testing.T) {
	e := newMWEnv(t, mwUsers())
	e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=200", "u-ana")
	if fmt.Sprint(e.order) != "[handler append:member.update]" {
		t.Fatalf("el registro va DESPUÉS de la ruta: %v", e.order)
	}
	// Con un 400 la ruta corre y no se escribe nada.
	e = newMWEnv(t, mwUsers())
	e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=400", "u-ana")
	if fmt.Sprint(e.order) != "[handler]" {
		t.Fatalf("un 400 no registra: %v", e.order)
	}
}

func TestAuditMW_actorNameSnapshot(t *testing.T) {
	users := mwUsers()
	e := newMWEnv(t, users)
	e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=200", "u-ana")
	// Un actor que el repositorio de usuarios no conoce: nombre vacío, sin fallar.
	w := e.call("PATCH", "/api/v1/salons/s1/members/m-8?s=200", "u-fantasma")
	if w.Code != 200 {
		t.Fatalf("un actor sin resolver no rompe nada: %d", w.Code)
	}
	if len(e.repo.Rows) != 2 || e.repo.Rows[0].ActorName != "Ana Dueña" || e.repo.Rows[1].ActorName != "" {
		t.Fatalf("snapshot del nombre: %+v", e.repo.Rows)
	}
	// Renombrar después no cambia lo ya escrito.
	_ = users.Save(domain.User{ID: "u-ana", Name: "Ana Nueva", Email: "ana@x.com"})
	if e.repo.Rows[0].ActorName != "Ana Dueña" {
		t.Fatal("el nombre es una foto")
	}
}

func TestAuditMW_noActorOrDisabledIsPassThrough(t *testing.T) {
	// Sin userID en el contexto no hay a quién atribuir: no escribe y no rompe.
	e := newMWEnv(t, mwUsers())
	if w := e.call("PATCH", "/api/v1/salons/s1/members/m-7?s=200", ""); w.Code != 200 {
		t.Fatalf("sin actor: %d", w.Code)
	}
	if e.repo.AppendCalls != 0 {
		t.Fatal("sin actor no se escribe")
	}
	// Servicio sin repositorio (el httpapi.Deps de los tests unitarios): pass-through.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := httpapi.AuditHandler{}
	r.PUT("/x/:id", func(c *gin.Context) { c.Set("userID", "u"); c.Next() }, h.Audit("settings.update"),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/x/s1", nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("pass-through: %d %s", w.Code, w.Body.String())
	}
}

// --- Rutas reales ---------------------------------------------------------------------

type audWorld struct {
	Env  *httpkit.Env
	repo *httpkit.AuditEvents
	logs []string

	Salon, otherSalon                                    string
	Owner, Admin, Recep, Prof, Client, Removed, Outsider string
	OwnerID, adminID, ProfID                             string
}

var audClock = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func newAudWorld(t *testing.T) *audWorld {
	t.Helper()
	env := httpkit.NewPOSEnv()
	w := &audWorld{Env: env, repo: &httpkit.AuditEvents{}}
	// Fakes de gastos, fidelidad y productos montados sobre el mismo httpkit.Env.
	notifSvc := notifications.NotificationService{Notifications: env.Notifications, Salons: env.Mems, Events: env.Events}
	inv := &httpkit.Products{}
	env.Deps.Products = httpapi.ProductHandler{
		Products:    products.ProductService{Products: inv, Memberships: env.Mems, Events: env.Events, Notifier: notifSvc},
		Memberships: env.Mems,
	}
	env.Deps.Expenses = httpapi.ExpenseHandler{
		Expenses: finance.ExpenseService{
			Expenses: httpkit.NewExpenses(), Memberships: env.Mems, Settings: env.Settings,
			Now: func() time.Time { return audClock },
		},
		Memberships: env.Mems,
	}
	programs := &httpkit.LoyaltyPrograms{Services: env.Services}
	rewards := &httpkit.LoyaltyRewards{Programs: programs, VisitCounts: map[string]int{}}
	env.Deps.Loyalty = httpapi.LoyaltyHandler{
		Loyalty: loyalty.LoyaltyService{
			Programs: programs, Rewards: rewards, Services: env.Services, Clients: env.Clients,
			Memberships: env.Mems, Settings: env.Settings,
		},
		Memberships: env.Mems,
	}
	env.Deps.Audit = httpapi.AuditHandler{
		Service: auditsvc.AuditService{
			Events: w.repo, Users: env.Users, Memberships: env.Mems,
			Now: func() time.Time { return audClock },
		},
		Memberships: env.Mems,
		Logf:        func(f string, a ...any) { w.logs = append(w.logs, fmt.Sprintf(f, a...)) },
	}
	env.Router = gin.New()
	httpapi.RegisterRoutes(env.Router, env.Deps)

	w.Owner, w.OwnerID = env.Register(t, "owner-aud@mail.com")
	w.Salon = env.Salon(t, w.Owner, "auditoria-uno")
	var id string
	w.Admin, w.adminID = env.Register(t, "admin-aud@mail.com")
	httpkit.SeedRole(t, env, w.Salon, w.adminID, domain.RoleAdmin)
	w.Recep, id = env.Register(t, "recep-aud@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleReception)
	w.Prof, w.ProfID = env.Register(t, "prof-aud@mail.com")
	httpkit.SeedRole(t, env, w.Salon, w.ProfID, domain.RoleProfessional)
	w.Client, id = env.Register(t, "client-aud@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleClient)
	w.Removed, id = env.Register(t, "removed-aud@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleAdmin)
	at := time.Now().Add(-time.Hour)
	m := env.Mems.ByKey[id+"|"+w.Salon]
	m.EndedAt = &at
	env.Mems.ByKey[id+"|"+w.Salon] = m
	w.Outsider, _ = env.Register(t, "outsider-aud@mail.com")
	w.otherSalon = env.Salon(t, w.Outsider, "auditoria-dos")
	return w
}

func (w *audWorld) path(suffix string) string { return "/api/v1/salons/" + w.Salon + suffix }

func (w *audWorld) do(method, path, body, token string) *httptest.ResponseRecorder {
	return httpkit.Do(w.Env.Router, method, path, body, token)
}

// seed escribe n eventos directamente en el repositorio, uno por minuto hacia
// atrás desde el reloj.
func (w *audWorld) seed(t *testing.T, salon string, n int, action string) {
	t.Helper()
	for i := 0; i < n; i++ {
		ev, err := domain.NewAuditEvent(salon, w.OwnerID, "Dueña", action, fmt.Sprintf("e%03d", i), audClock.Add(-time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		_ = w.repo.Append(ev)
	}
}

type auditPageJSON struct {
	Events []struct {
		ID          string  `json:"id"`
		Action      string  `json:"action"`
		EntityID    *string `json:"entity_id"`
		ActorUserID string  `json:"actor_user_id"`
		ActorName   string  `json:"actor_name"`
		CreatedAt   string  `json:"created_at"`
	} `json:"events"`
	NextCursor *string `json:"next_cursor"`
}

func (w *audWorld) page(t *testing.T, token, query string) (auditPageJSON, *httptest.ResponseRecorder) {
	t.Helper()
	r := w.do("GET", w.path("/audit-log"+query), "", token)
	var p auditPageJSON
	if r.Code == http.StatusOK {
		httpkit.MustJSON(t, r.Body.Bytes(), &p)
	}
	return p, r
}

func wantErrBody(t *testing.T, r *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	if r.Code != code || strings.TrimSpace(r.Body.String()) != `{"error":"`+msg+`"}` {
		t.Fatalf("esperaba %d {\"error\":%q}, obtuve %d %s", code, msg, r.Code, r.Body.String())
	}
}

// D3: owner/admin; 403 «sin permiso» ANTES de tocar la base; baja y otro salón
// 403 «sin acceso al salón»; sin token 401.
func TestAuditLog_rolesAndPermissionFirst(t *testing.T) {
	w := newAudWorld(t)
	w.seed(t, w.Salon, 3, "member.add")
	for name, tok := range map[string]string{"dueña": w.Owner, "admin": w.Admin} {
		p, r := w.page(t, tok, "")
		if r.Code != 200 || len(p.Events) != 3 {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	// Con parámetros inválidos a propósito: gana el 403, y la base no se toca.
	bad := "?limit=999&before=basura&action=%00"
	for name, tok := range map[string]string{"recepción": w.Recep, "profesional": w.Prof, "clienta": w.Client} {
		r := w.do("GET", w.path("/audit-log"+bad), "", tok)
		wantErrBody(t, r, 403, "sin permiso")
		if r2 := w.do("GET", w.path("/audit-log"), "", tok); r2.Code != 403 {
			t.Errorf("%s: %d", name, r2.Code)
		}
	}
	for name, tok := range map[string]string{"dada de baja": w.Removed, "otro salón": w.Outsider} {
		wantErrBody(t, w.do("GET", w.path("/audit-log"+bad), "", tok), 403, "sin acceso al salón")
		_ = name
	}
	if r := w.do("GET", w.path("/audit-log"), "", ""); r.Code != 401 {
		t.Errorf("sin token: %d", r.Code)
	}
	if w.repo.ListCalls != 2 {
		t.Fatalf("solo las dos lecturas permitidas tocan la base: %d", w.repo.ListCalls)
	}
}

func TestAuditLog_shapeAndEmpty(t *testing.T) {
	w := newAudWorld(t)
	r := w.do("GET", w.path("/audit-log"), "", w.Owner)
	if r.Code != 200 || strings.TrimSpace(r.Body.String()) != `{"events":[],"next_cursor":null}` {
		t.Fatalf("vacío: %d %s", r.Code, r.Body.String())
	}
	ev, _ := domain.NewAuditEvent(w.Salon, "u1", "Ana", "member.update", "m-1", audClock)
	_ = w.repo.Append(ev)
	noEnt, _ := domain.NewAuditEvent(w.Salon, "u1", "", "settings.update", "", audClock.Add(-time.Minute))
	_ = w.repo.Append(noEnt)
	r = w.do("GET", w.path("/audit-log"), "", w.Owner)
	var raw struct {
		Events     []map[string]any `json:"events"`
		NextCursor any              `json:"next_cursor"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &raw)
	if len(raw.Events) != 2 || raw.NextCursor != nil {
		t.Fatalf("forma: %s", r.Body.String())
	}
	first := raw.Events[0]
	if len(first) != 6 || first["id"] != ev.ID || first["action"] != "member.update" || first["entity_id"] != "m-1" ||
		first["actor_user_id"] != "u1" || first["actor_name"] != "Ana" || first["created_at"] != "2026-06-01T12:00:00Z" {
		t.Fatalf("evento: %v", first)
	}
	// Sin entidad: entity_id null; sin nombre: cadena vacía.
	second := raw.Events[1]
	if v, ok := second["entity_id"]; !ok || v != nil || second["actor_name"] != "" {
		t.Fatalf("evento sin entidad: %v", second)
	}
}

func TestAuditLog_limitDefaultAndBounds(t *testing.T) {
	w := newAudWorld(t)
	w.seed(t, w.Salon, 130, "sale.x")
	count := func(q string) int {
		p, r := w.page(t, w.Owner, q)
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", q, r.Code, r.Body.String())
		}
		return len(p.Events)
	}
	if n := count(""); n != 50 {
		t.Errorf("por defecto 50: %d", n)
	}
	if n := count("?limit=100"); n != 100 {
		t.Errorf("límite 100: %d", n)
	}
	if n := count("?limit=1"); n != 1 {
		t.Errorf("límite 1: %d", n)
	}
	calls := w.repo.ListCalls
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=-1", "?limit=abc", "?limit=1.5", "?limit=%20"} {
		wantErrBody(t, w.do("GET", w.path("/audit-log"+q), "", w.Owner), 400, "El limite debe ser entre 1 y 100")
	}
	if w.repo.ListCalls != calls {
		t.Error("un límite inválido no toca la base")
	}
}

func TestAuditLog_threePagesAndCursor(t *testing.T) {
	w := newAudWorld(t)
	w.seed(t, w.Salon, 25, "sale.x")
	var seen []string
	q := "?limit=10"
	var sizes []int
	var cursors []string
	for i := 0; i < 5; i++ {
		p, r := w.page(t, w.Owner, q)
		if r.Code != 200 {
			t.Fatalf("página %d: %d %s", i, r.Code, r.Body.String())
		}
		sizes = append(sizes, len(p.Events))
		for _, e := range p.Events {
			seen = append(seen, *e.EntityID)
		}
		if p.NextCursor == nil {
			break
		}
		cursors = append(cursors, *p.NextCursor)
		q = "?limit=10&before=" + *p.NextCursor
	}
	if fmt.Sprint(sizes) != "[10 10 5]" || len(cursors) != 2 {
		t.Fatalf("páginas %v con %d cursores, quería [10 10 5] y 2", sizes, len(cursors))
	}
	for i, id := range seen {
		if id != fmt.Sprintf("e%03d", i) {
			t.Fatalf("posición %d: %s (más nuevo primero, sin saltos ni repetidos)", i, id)
		}
	}
	// La última página lleva next_cursor null explícito en el JSON.
	r := w.do("GET", w.path("/audit-log?limit=10&before="+cursors[1]), "", w.Owner)
	if !strings.Contains(r.Body.String(), `"next_cursor":null`) {
		t.Fatalf("última página: %s", r.Body.String())
	}
	// El cursor es opaco (base64url): no deja ver el id ni la fecha en claro.
	if strings.Contains(cursors[0], "e009") || strings.ContainsAny(cursors[0], "+/=|:") {
		t.Errorf("el cursor debe ser opaco base64url: %q", cursors[0])
	}
}

func TestAuditLog_invalidCursor(t *testing.T) {
	w := newAudWorld(t)
	w.seed(t, w.Salon, 3, "sale.x")
	calls := w.repo.ListCalls
	for _, c := range []string{"basura", "AAAA", "!!!", strings.Repeat("A", 500), "MjAyNi0wNi0wMVQxMjowMDowMFo"} {
		wantErrBody(t, w.do("GET", w.path("/audit-log?before="+c), "", w.Owner), 400, "Cursor invalido")
	}
	if w.repo.ListCalls != calls {
		t.Error("un cursor inválido no toca la base")
	}
}

func TestAuditLog_actionFilter(t *testing.T) {
	w := newAudWorld(t)
	w.seed(t, w.Salon, 2, "member.add")
	w.seed(t, w.Salon, 2, "member.update")
	w.seed(t, w.Salon, 2, "memberx.add")
	w.seed(t, w.Salon, 2, "payroll_run.pay")
	count := func(q string) int {
		p, r := w.page(t, w.Owner, q)
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", q, r.Code, r.Body.String())
		}
		return len(p.Events)
	}
	for q, want := range map[string]int{
		"": 8, "?action=member": 4, "?action=member.add": 2, "?action=mem": 0, "?action=payroll_run": 2,
		"?action=member.": 0, "?action=": 8,
	} {
		if n := count(q); n != want {
			t.Errorf("%q: %d eventos, quería %d", q, n, want)
		}
	}
	for _, bad := range []string{"%00", "a%00b", "a%0Ab", "a%0Db", "%0A"} {
		wantErrBody(t, w.do("GET", w.path("/audit-log?action="+bad), "", w.Owner), 400, "Accion invalida")
	}
	// Filtro + paginación juntos.
	p, _ := w.page(t, w.Owner, "?action=member&limit=3")
	if len(p.Events) != 3 || p.NextCursor == nil {
		t.Fatalf("filtro con página: %d %v", len(p.Events), p.NextCursor)
	}
	p2, _ := w.page(t, w.Owner, "?action=member&limit=3&before="+*p.NextCursor)
	if len(p2.Events) != 1 || p2.NextCursor != nil {
		t.Fatalf("segunda página filtrada: %d %v", len(p2.Events), p2.NextCursor)
	}
}

func TestAuditLog_salonIsolation(t *testing.T) {
	w := newAudWorld(t)
	w.seed(t, w.Salon, 3, "member.add")
	w.seed(t, w.otherSalon, 5, "secret.action")
	p, _ := w.page(t, w.Owner, "")
	if len(p.Events) != 3 {
		t.Fatalf("el salón uno ve solo lo suyo: %d", len(p.Events))
	}
	for _, e := range p.Events {
		if e.Action != "member.add" {
			t.Fatalf("fuga entre salones: %+v", e)
		}
	}
	// La dueña del otro salón lee el suyo y NO el ajeno.
	r := w.do("GET", "/api/v1/salons/"+w.otherSalon+"/audit-log", "", w.Outsider)
	var po auditPageJSON
	httpkit.MustJSON(t, r.Body.Bytes(), &po)
	if r.Code != 200 || len(po.Events) != 5 {
		t.Fatalf("el otro salón: %d %s", r.Code, r.Body.String())
	}
	wantErrBody(t, w.do("GET", w.path("/audit-log"), "", w.Outsider), 403, "sin acceso al salón")
	// Un cursor de OTRO salón usado acá no filtra eventos ajenos.
	po2, _ := func() (auditPageJSON, int) {
		var x auditPageJSON
		rr := w.do("GET", "/api/v1/salons/"+w.otherSalon+"/audit-log?limit=2", "", w.Outsider)
		httpkit.MustJSON(t, rr.Body.Bytes(), &x)
		return x, rr.Code
	}()
	p3, r3 := w.page(t, w.Owner, "?before="+*po2.NextCursor)
	if r3.Code != 200 {
		t.Fatalf("cursor de otro salón: %d", r3.Code)
	}
	for _, e := range p3.Events {
		if e.Action == "secret.action" {
			t.Fatalf("fuga por cursor: %+v", e)
		}
	}
}

func TestAuditLog_repoFailureIs500Generic(t *testing.T) {
	w := newAudWorld(t)
	w.repo.ListErr = httpkit.ErrAuditDown
	wantErrBody(t, w.do("GET", w.path("/audit-log"), "", w.Owner), 500, "error interno")
}

// Leer el registro no se audita, y los eventos de un salón no aparecen por
// escribir en otro.
func TestAuditLog_readingIsNotAudited(t *testing.T) {
	w := newAudWorld(t)
	w.do("GET", w.path("/audit-log"), "", w.Owner)
	w.do("GET", w.path("/services"), "", w.Owner)
	if w.repo.AppendCalls != 0 {
		t.Fatalf("leer no registra: %d", w.repo.AppendCalls)
	}
}

// --- D2 sobre las rutas reales -------------------------------------------------------

func (w *audWorld) lastActions() []string { return w.repo.Actions() }

func TestAuditRoutes_membersInvitationsSettingsCashClose(t *testing.T) {
	w := newAudWorld(t)
	env := w.Env
	newTok, newID := env.Register(t, "nuevo-aud@mail.com")
	_ = newTok
	steps := []struct {
		method, path, body, token string
		status                    int
		want                      string // «acción|entidad» o "" si NO debe registrar
	}{
		{"PUT", w.path("/settings"), `{"timezone":"America/Bogota","open_time":"08:00","close_time":"17:30","slot_step_min":20}`, w.Owner, 200, "settings.update|"},
		{"PUT", w.path("/settings"), `{"timezone":"Marte/Olympus","open_time":"08:00","close_time":"17:00","slot_step_min":30}`, w.Owner, 400, ""},
		{"PUT", w.path("/settings"), `{"timezone":"America/Bogota","open_time":"08:00","close_time":"17:30","slot_step_min":20}`, w.Recep, 403, ""},
		{"POST", w.path("/members"), `{"email":"nuevo-aud@mail.com","role":"professional"}`, w.Owner, 201, "member.add|"},
		{"POST", w.path("/members"), `{"email":"nuevo-aud@mail.com","role":"professional"}`, w.Recep, 403, ""},
		{"PATCH", w.path("/members/" + newID), `{"attends":true}`, w.Owner, 200, "member.update|" + newID},
		{"PUT", w.path("/members/" + newID + "/services"), `{"service_ids":[]}`, w.Owner, 200, "member.services|" + newID},
		{"DELETE", w.path("/members/" + newID), "", w.Owner, 204, "member.remove|" + newID},
		{"DELETE", w.path("/members/" + newID), "", w.Owner, 404, ""},
		{"POST", w.path("/members/" + newID + "/reactivate"), "", w.Owner, 200, "member.reactivate|" + newID},
		{"POST", w.path("/members/" + newID + "/reactivate"), "", w.Owner, 409, ""},
		{"POST", w.path("/invitations"), `{"email":"invitada-aud@mail.com","role":"reception"}`, w.Owner, 201, "invitation.create|"},
		{"POST", w.path("/invitations"), `{"email":"mal"}`, w.Owner, 400, ""},
		{"POST", w.path("/cash-closes"), `{"counted_cents":0,"note":"x"}`, w.Recep, 403, ""},
		{"POST", w.path("/cash-closes"), `{"counted_cents":0,"note":"x"}`, w.Owner, 201, "cash_close.create|"},
	}
	for i, s := range steps {
		before := len(w.repo.Rows)
		r := w.do(s.method, s.path, s.body, s.token)
		if r.Code != s.status {
			t.Fatalf("paso %d %s %s: %d %s (quería %d)", i, s.method, s.path, r.Code, r.Body.String(), s.status)
		}
		got := w.repo.Actions()
		if s.want == "" {
			if len(got) != before {
				t.Errorf("paso %d (%d): no debe registrar, hay %v", i, s.status, got[before:])
			}
			continue
		}
		if len(got) != before+1 || got[before] != s.want {
			t.Errorf("paso %d: esperaba %q, hay %v", i, s.want, got[before:])
			continue
		}
		ev := w.repo.Rows[len(w.repo.Rows)-1]
		if ev.SalonID != w.Salon || ev.ActorUserID != w.OwnerID || ev.ActorName != "owner-aud@mail.com" {
			t.Errorf("paso %d: actor/salón: %+v", i, ev)
		}
	}
	// La revocación de la invitación: id propio de la ruta (:iid).
	var invs struct {
		Invitations []struct {
			ID string `json:"id"`
		} `json:"invitations"`
	}
	httpkit.MustJSON(t, w.do("GET", w.path("/invitations"), "", w.Owner).Body.Bytes(), &invs)
	if len(invs.Invitations) != 1 {
		t.Fatalf("esperaba 1 invitación pendiente: %+v", invs)
	}
	iid := invs.Invitations[0].ID
	if r := w.do("DELETE", w.path("/invitations/"+iid), "", w.Recep); r.Code != 403 {
		t.Fatalf("recepción revocando: %d", r.Code)
	}
	if r := w.do("DELETE", w.path("/invitations/"+iid), "", w.Owner); r.Code != 204 {
		t.Fatalf("revocar: %d", r.Code)
	}
	got := w.repo.Actions()
	if len(got) == 0 || got[len(got)-1] != "invitation.revoke|"+iid {
		t.Fatalf("revoke: %v", got)
	}
	// Un salón ajeno no escribe en el nuestro: ese 403 no registra.
	n := len(got)
	w.do("PUT", w.path("/settings"), `{"timezone":"America/Bogota","open_time":"08:00","close_time":"17:30","slot_step_min":20}`, w.Outsider)
	if len(w.repo.Rows) != n {
		t.Fatal("un 403 de otro salón no registra")
	}
	if len(w.logs) != 0 {
		t.Fatalf("no debe haber logs de error: %v", w.logs)
	}
}

func TestAuditRoutes_expensesLoyaltyProducts(t *testing.T) {
	w := newAudWorld(t)
	svcID := w.Env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	exp := `{"category":"rent","amount_cents":150000,"spent_on":"2026-03-19","description":"Alquiler"}`
	r := w.do("POST", w.path("/expenses"), exp, w.Owner)
	if r.Code != 201 {
		t.Fatalf("gasto: %d %s", r.Code, r.Body.String())
	}
	var ex struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &ex)
	if r := w.do("POST", w.path("/expenses"), `{}`, w.Owner); r.Code != 400 {
		t.Fatalf("gasto inválido: %d", r.Code)
	}
	if r := w.do("PUT", w.path("/expenses/"+ex.ID), exp, w.Owner); r.Code != 200 {
		t.Fatalf("editar gasto: %d %s", r.Code, r.Body.String())
	}
	if r := w.do("DELETE", w.path("/expenses/"+ex.ID), "", w.Recep); r.Code != 403 {
		t.Fatalf("recepción borrando: %d", r.Code)
	}
	if r := w.do("DELETE", w.path("/expenses/"+ex.ID), "", w.Owner); r.Code != 204 {
		t.Fatalf("borrar gasto: %d", r.Code)
	}
	if r := w.do("PUT", w.path("/loyalty-program"), `{"enabled":true,"visits_required":5,"reward_service_id":"`+svcID+`","reward_validity_days":null}`, w.Owner); r.Code != 200 {
		t.Fatalf("programa: %d %s", r.Code, r.Body.String())
	}
	if r := w.do("PUT", w.path("/loyalty-program"), `{"enabled":true}`, w.Owner); r.Code != 400 {
		t.Fatalf("programa parcial: %d", r.Code)
	}
	r = w.do("POST", w.path("/products"), `{"name":"Shampoo","price_cents":50000,"stock":5,"low_stock_threshold":2}`, w.Owner)
	var pr struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &pr)
	if r := w.do("POST", w.path("/products/"+pr.ID+"/adjust"), `{"delta":1,"reason":"restock"}`, w.Owner); r.Code != 200 {
		t.Fatalf("ajuste: %d %s", r.Code, r.Body.String())
	}
	if r := w.do("POST", w.path("/products/"+pr.ID+"/adjust"), `{}`, w.Owner); r.Code != 400 {
		t.Fatalf("ajuste inválido: %d", r.Code)
	}
	// Crear/editar productos NO está en la lista de D2: no se audita.
	// expense.create no tiene id propio en la ruta (:eid solo existe al editar y
	// borrar): su entidad queda vacía.
	want := []string{
		"expense.create|", "expense.update|" + ex.ID, "expense.delete|" + ex.ID,
		"loyalty.program|", "product.adjust|" + pr.ID,
	}
	if got := w.repo.Actions(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("esperaba %v, hay %v", want, got)
	}
}

// Las rutas que D2 NO audita siguen sin escribir nada (ventas del POS, altas de
// servicios y productos, turnos...).
func TestAuditRoutes_unwrappedRoutesDoNotWrite(t *testing.T) {
	w := newAudWorld(t)
	svcID := w.Env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	httpkit.SeedProfessional(w.Env, w.Salon, "prof-x")
	for _, s := range []struct{ method, path, body string }{
		{"POST", w.path("/services"), `{"name":"Tinte","duration_min":30,"price_cents":9000}`},
		{"POST", w.path("/sales"), `{"professional_id":"prof-x","client_name":"Lía","method":"cash","items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":15000}]}`},
		{"POST", w.path("/clients"), `{"name":"Mora"}`},
		{"POST", w.path("/products"), `{"name":"Gel","price_cents":100,"stock":1,"low_stock_threshold":0}`},
		{"GET", w.path("/services"), ""},
	} {
		r := w.do(s.method, s.path, s.body, w.Owner)
		if r.Code < 200 || r.Code > 299 {
			t.Fatalf("%s %s: %d %s", s.method, s.path, r.Code, r.Body.String())
		}
	}
	if w.repo.AppendCalls != 0 {
		t.Fatalf("rutas fuera de D2 no auditan: %v", w.repo.Actions())
	}
}

// Un fallo del escritor no cambia la respuesta de una ruta real.
func TestAuditRoutes_writerDownKeepsResponse(t *testing.T) {
	w := newAudWorld(t)
	body := `{"timezone":"America/Bogota","open_time":"08:00","close_time":"17:30","slot_step_min":20}`
	ok := w.do("PUT", w.path("/settings"), body, w.Owner)
	w.repo.AppendErr = httpkit.ErrAuditDown
	down := w.do("PUT", w.path("/settings"), body, w.Owner)
	if down.Code != ok.Code || down.Body.String() != ok.Body.String() {
		t.Fatalf("la respuesta cambió con el escritor caído: %d %s vs %d %s", down.Code, down.Body.String(), ok.Code, ok.Body.String())
	}
	if len(w.logs) != 1 {
		t.Fatalf("el fallo se loguea una vez: %v", w.logs)
	}
}

// --- Cableado: la lista cerrada de D2 ------------------------------------------------

// La forma de la tabla de rutas no se puede mirar en gin (solo el último
// handler), así que se lee routes.go: EXACTAMENTE estas 22 rutas llevan
// audit("<acción>") y ninguna otra. (D2 también nombra `export.Expenses`, pero
// no existe una exportación CSV de gastos: no hay ruta que envolver.) Es la guarda de que ninguna ruta de D2 se
// quede sin envolver ni una ruta fuera de D2 quede envuelta.
func TestAuditRoutes_exactlyTheD2ListIsWrapped(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`authed\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)", audit\("([^"]+)"\), d\.`)
	got := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		got[m[3]] = m[1] + " " + m[2]
	}
	want := map[string]string{
		"member.add":         "POST /salons/:id/members",
		"member.update":      "PATCH /salons/:id/members/:uid",
		"member.remove":      "DELETE /salons/:id/members/:uid",
		"member.reactivate":  "POST /salons/:id/members/:uid/reactivate",
		"member.services":    "PUT /salons/:id/members/:uid/services",
		"member.pay_model":   "PUT /salons/:id/members/:uid/pay-model",
		"invitation.create":  "POST /salons/:id/invitations",
		"invitation.revoke":  "DELETE /salons/:id/invitations/:iid",
		"payroll_run.create": "POST /salons/:id/payroll/runs",
		"payroll_run.adjust": "PATCH /salons/:id/payroll/runs/:rid/lines/:uid",
		"payroll_run.pay":    "POST /salons/:id/payroll/runs/:rid/pay",
		"payroll_run.delete": "DELETE /salons/:id/payroll/runs/:rid",
		"cash_close.create":  "POST /salons/:id/cash-closes",
		"expense.create":     "POST /salons/:id/expenses",
		"expense.update":     "PUT /salons/:id/expenses/:eid",
		"expense.delete":     "DELETE /salons/:id/expenses/:eid",
		"export.sales":       "GET /salons/:id/export/sales.csv",
		"export.clients":     "GET /salons/:id/export/clients.csv",
		"settings.update":    "PUT /salons/:id/settings",
		"profile.update":     "PUT /salons/:id/profile",
		"loyalty.program":    "PUT /salons/:id/loyalty-program",
		"deposit.policy":     "PUT /salons/:id/deposit-policy",
		"product.adjust":     "POST /salons/:id/products/:pid/adjust",
	}
	var keys []string
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got[k] != want[k] {
			t.Errorf("%s: esperaba %q, está cableada en %q", k, want[k], got[k])
		}
	}
	if len(got) != len(want) {
		t.Errorf("hay %d rutas envueltas, la lista cerrada tiene %d: %v", len(got), len(want), got)
	}
	if n := strings.Count(string(src), `, audit("`); n != len(want) {
		t.Errorf("routes.go usa audit(\"...\") %d veces (¿una ruta con otra forma?), esperaba %d", n, len(want))
	}
	// Cada acción envuelta tiene su entrada en la tabla de entidades iff la ruta
	// tiene un id propio.
	for action, route := range want {
		hasOwn := httpapi.AuditEntityParams[action] != ""
		wantParam := ""
		switch action {
		case "payroll_run.adjust", "payroll_run.pay", "payroll_run.delete":
			wantParam = "rid"
		default:
			for _, p := range []string{"uid", "iid", "eid", "pid"} {
				if strings.HasSuffix(strings.Split(route, " ")[1], ":"+p) || strings.Contains(strings.Split(route, " ")[1], ":"+p+"/") {
					wantParam = p
				}
			}
		}
		if hasOwn != (wantParam != "") || httpapi.AuditEntityParams[action] != wantParam {
			t.Errorf("%s: parámetro de entidad %q, esperaba %q", action, httpapi.AuditEntityParams[action], wantParam)
		}
	}
}
