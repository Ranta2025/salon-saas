package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"sort"
	"strings"
	"testing"
	"time"
)

// pkgWorld es un salón con todos los roles, dos clientas y un paquete de 3
// sesiones listo para vender.
type pkgWorld struct {
	Env     *httpkit.Env
	catalog *httpkit.PkgCatalog
	owned   *httpkit.PkgOwned
	Salon   string
	// otherSalon es OTRO tenant con su propio dueño (w.other).
	otherSalon string
	// Tokens por rol.
	Owner, Admin, Recep, Prof, Client, Removed, other string
	OwnerID, ProfID                                   string
	svcID, ClientID, clientTwoID, pkgID               string
}

func newPkgWorld(t *testing.T) *pkgWorld {
	t.Helper()
	env, catalog, owned := httpkit.NewPackagesEnv()
	w := &pkgWorld{Env: env, catalog: catalog, owned: owned}
	w.Owner, w.OwnerID = env.Register(t, "owner-pkg@mail.com")
	w.Salon = env.Salon(t, w.Owner, "paquetes")
	var id string
	w.Admin, id = env.Register(t, "admin-pkg@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleAdmin)
	w.Recep, id = env.Register(t, "recep-pkg@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleReception)
	w.other, _ = env.Register(t, "other-pkg@mail.com")
	w.otherSalon = env.Salon(t, w.other, "otro-paquetes")

	w.svcID = env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	w.ClientID = w.newClient(t, w.Salon, w.Owner, "Lía Pérez")
	w.clientTwoID = w.newClient(t, w.Salon, w.Owner, "Mora Díaz")
	w.pkgID = w.createPkg(t, `{"name":"Pack 3 cortes","service_id":"`+w.svcID+`","sessions":3,"price_cents":40000,"validity_days":30}`)
	return w
}

// withAllRoles suma los roles que solo prueba la matriz de permisos (cada
// registro paga un bcrypt: los tests que no los necesitan no los crean).
func (w *pkgWorld) withAllRoles(t *testing.T) {
	t.Helper()
	var id string
	w.Prof, w.ProfID = w.Env.Register(t, "prof-pkg@mail.com")
	httpkit.SeedRole(t, w.Env, w.Salon, w.ProfID, domain.RoleProfessional)
	w.Client, id = w.Env.Register(t, "client-pkg@mail.com")
	httpkit.SeedRole(t, w.Env, w.Salon, id, domain.RoleClient)
	w.Removed, id = w.Env.Register(t, "removed-pkg@mail.com")
	httpkit.SeedRole(t, w.Env, w.Salon, id, domain.RoleAdmin)
	at := time.Now().Add(-time.Hour)
	m := w.Env.Mems.ByKey[id+"|"+w.Salon]
	m.EndedAt = &at
	w.Env.Mems.ByKey[id+"|"+w.Salon] = m
}

func (w *pkgWorld) newClient(t *testing.T, salon, token, name string) string {
	t.Helper()
	r := httpkit.Do(w.Env.Router, "POST", "/api/v1/salons/"+salon+"/clients", `{"name":"`+name+`"}`, token)
	if r.Code != http.StatusCreated {
		t.Fatalf("crear clienta: %d %s", r.Code, r.Body.String())
	}
	var c struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &c)
	return c.ID
}

func (w *pkgWorld) createPkg(t *testing.T, body string) string {
	t.Helper()
	r := httpkit.Do(w.Env.Router, "POST", w.catalogPath(), body, w.Owner)
	if r.Code != http.StatusCreated {
		t.Fatalf("crear paquete: %d %s", r.Code, r.Body.String())
	}
	var p struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &p)
	return p.ID
}

func (w *pkgWorld) catalogPath() string { return "/api/v1/salons/" + w.Salon + "/packages" }
func (w *pkgWorld) clientPath(clientID string) string {
	return "/api/v1/salons/" + w.Salon + "/clients/" + clientID + "/packages"
}

func (w *pkgWorld) buyBody(op string) string {
	b := `{"package_id":"` + w.pkgID + `","professional_id":"` + w.OwnerID + `","method":"card"`
	if op != "" {
		b += `,"client_op_id":"` + op + `"`
	}
	return b + `}`
}

// buy compra el paquete para la primera clienta y devuelve el id del paquete comprado.
func (w *pkgWorld) buy(t *testing.T, op string) (cpID, saleID string) {
	t.Helper()
	r := httpkit.Do(w.Env.Router, "POST", w.clientPath(w.ClientID), w.buyBody(op), w.Recep)
	if r.Code != http.StatusCreated {
		t.Fatalf("comprar: %d %s", r.Code, r.Body.String())
	}
	var out struct {
		SaleID        string         `json:"sale_id"`
		ClientPackage map[string]any `json:"client_package"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	return out.ClientPackage["id"].(string), out.SaleID
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func errText(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &e)
	return e.Error
}

func wantHTTP(t *testing.T, w *httptest.ResponseRecorder, code int, text string) {
	t.Helper()
	if w.Code != code || (text != "" && errText(t, w) != text) {
		t.Fatalf("esperaba %d %q, dio %d %s", code, text, w.Code, w.Body.String())
	}
}

// --- Formas y códigos EXACTOS del contrato ----------------------------------------

func TestHTTP_packages_shapes(t *testing.T) {
	w := newPkgWorld(t)

	// Catálogo: forma del paquete.
	r := httpkit.Do(w.Env.Router, "GET", w.catalogPath(), "", w.Recep)
	if r.Code != http.StatusOK {
		t.Fatalf("catálogo: %d %s", r.Code, r.Body.String())
	}
	var cat struct {
		Packages []map[string]any `json:"packages"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &cat)
	if len(cat.Packages) != 1 {
		t.Fatalf("catálogo: %s", r.Body.String())
	}
	p := cat.Packages[0]
	if got := strings.Join(keysOf(p), ","); got != "active,created_at,id,name,price_cents,service_id,service_name,sessions,validity_days" {
		t.Fatalf("claves del paquete: %s", got)
	}
	if p["name"] != "Pack 3 cortes" || p["service_name"] != "Corte" || p["sessions"] != float64(3) || p["price_cents"] != float64(40000) ||
		p["validity_days"] != float64(30) || p["active"] != true || p["service_id"] != w.svcID {
		t.Fatalf("paquete: %+v", p)
	}
	if _, err := time.Parse(time.RFC3339, p["created_at"].(string)); err != nil {
		t.Fatalf("created_at: %v", err)
	}
	// Sin vigencia: validity_days es null (no se omite).
	noVal := w.createPkg(t, `{"name":"Sin vencer","service_id":"`+w.svcID+`","sessions":1,"price_cents":0}`)
	r = httpkit.Do(w.Env.Router, "GET", w.catalogPath(), "", w.Owner)
	httpkit.MustJSON(t, r.Body.Bytes(), &cat)
	for _, q := range cat.Packages {
		if q["id"] == noVal {
			if v, present := q["validity_days"]; !present || v != nil {
				t.Fatalf("validity_days debe ser null: %+v", q)
			}
		}
	}

	// Comprar: 201 {sale_id, client_package}.
	r = httpkit.Do(w.Env.Router, "POST", w.clientPath(w.ClientID), w.buyBody(""), w.Recep)
	if r.Code != http.StatusCreated {
		t.Fatalf("comprar: %d %s", r.Code, r.Body.String())
	}
	var bought map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &bought)
	if got := strings.Join(keysOf(bought), ","); got != "client_package,sale_id" {
		t.Fatalf("claves de la compra: %s", got)
	}
	cp := bought["client_package"].(map[string]any)
	if got := strings.Join(keysOf(cp), ","); got != "expired,expires_at,id,name,price_cents,purchased_at,service_id,service_name,sessions_left,sessions_total" {
		t.Fatalf("claves del paquete de la clienta (forma D8): %s", got)
	}
	if bought["sale_id"] == "" || cp["name"] != "Pack 3 cortes" || cp["sessions_total"] != float64(3) || cp["sessions_left"] != float64(3) ||
		cp["price_cents"] != float64(40000) || cp["service_name"] != "Corte" || cp["expired"] != false || cp["service_id"] != w.svcID {
		t.Fatalf("paquete comprado: %+v", cp)
	}
	purchased, err := time.Parse(time.RFC3339, cp["purchased_at"].(string))
	if err != nil {
		t.Fatalf("purchased_at: %v", err)
	}
	expires, err := time.Parse(time.RFC3339, cp["expires_at"].(string))
	if err != nil || !expires.After(purchased.AddDate(0, 0, 29)) {
		t.Fatalf("expires_at: %v %v", expires, err)
	}
	cpID := cp["id"].(string)

	// Descontar: 200 {client_package} (cuerpo {} y también SIN cuerpo).
	r = httpkit.Do(w.Env.Router, "POST", w.clientPath(w.ClientID)+"/"+cpID+"/redeem",
		`{"professional_id":"`+w.OwnerID+`","note":"primer corte"}`, w.Recep)
	if r.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", r.Code, r.Body.String())
	}
	var red map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &red)
	if got := strings.Join(keysOf(red), ","); got != "client_package" {
		t.Fatalf("claves del redeem: %s", got)
	}
	if red["client_package"].(map[string]any)["sessions_left"] != float64(2) {
		t.Fatalf("redeem: %s", r.Body.String())
	}
	if r = httpkit.Do(w.Env.Router, "POST", w.clientPath(w.ClientID)+"/"+cpID+"/redeem", "", w.Owner); r.Code != http.StatusOK {
		t.Fatalf("redeem sin cuerpo debe valer como {}: %d %s", r.Code, r.Body.String())
	}
	// Ajuste: 200 {client_package}.
	r = httpkit.Do(w.Env.Router, "POST", w.clientPath(w.ClientID)+"/"+cpID+"/adjust", `{"delta":1,"note":"se descontó de más"}`, w.Admin)
	if r.Code != http.StatusOK {
		t.Fatalf("adjust: %d %s", r.Code, r.Body.String())
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &red)
	if red["client_package"].(map[string]any)["sessions_left"] != float64(2) {
		t.Fatalf("adjust: %s", r.Body.String())
	}

	// Lectura: {"packages":[...]} con la forma D8.
	r = httpkit.Do(w.Env.Router, "GET", w.clientPath(w.ClientID), "", w.Recep)
	var list struct {
		Packages []map[string]any `json:"packages"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &list)
	if r.Code != http.StatusOK || len(list.Packages) != 1 || list.Packages[0]["sessions_left"] != float64(2) {
		t.Fatalf("lista de la clienta: %d %s", r.Code, r.Body.String())
	}
	// Otra clienta sin paquetes: lista vacía (no null).
	r = httpkit.Do(w.Env.Router, "GET", w.clientPath(w.clientTwoID), "", w.Recep)
	if r.Code != http.StatusOK || strings.TrimSpace(r.Body.String()) != `{"packages":[]}` {
		t.Fatalf("lista vacía: %d %s", r.Code, r.Body.String())
	}
	// Movimientos: más nuevo primero, note y professional_id null cuando no hay.
	r = httpkit.Do(w.Env.Router, "GET", w.clientPath(w.ClientID)+"/"+cpID+"/movements", "", w.Owner)
	var mv struct {
		Movements []map[string]any `json:"movements"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &mv)
	if r.Code != http.StatusOK || len(mv.Movements) != 4 {
		t.Fatalf("movimientos: %d %s", r.Code, r.Body.String())
	}
	for _, m := range mv.Movements {
		if got := strings.Join(keysOf(m), ","); got != "created_at,delta,id,note,professional_id,reason" {
			t.Fatalf("claves del movimiento: %s", got)
		}
	}
	reasons := []string{}
	for _, m := range mv.Movements {
		reasons = append(reasons, m["reason"].(string))
	}
	if strings.Join(reasons, ",") != "adjust,redeem,redeem,purchase" {
		t.Fatalf("orden de los movimientos: %v", reasons)
	}
	// El primer consumo lleva nota y profesional; el ajuste lleva nota y NO profesional (null).
	adjust := mv.Movements[0]
	if adjust["note"] != "se descontó de más" || adjust["professional_id"] != nil || adjust["delta"] != float64(1) {
		t.Fatalf("movimiento de ajuste: %+v", adjust)
	}
	first := mv.Movements[2]
	if first["note"] != "primer corte" || first["professional_id"] != w.OwnerID || first["delta"] != float64(-1) {
		t.Fatalf("movimiento de consumo: %+v", first)
	}
	if purchase := mv.Movements[3]; purchase["note"] != nil || purchase["delta"] != float64(3) {
		t.Fatalf("movimiento de compra: %+v", purchase)
	}
}

// D5: comprar crea la venta (total = precio, sin líneas), la clienta y el método.
func TestHTTP_packages_purchase_writes_the_sale(t *testing.T) {
	w := newPkgWorld(t)
	_, saleID := w.buy(t, "")
	if len(w.Env.Sales.All) != 1 {
		t.Fatalf("ventas = %d", len(w.Env.Sales.All))
	}
	s := w.Env.Sales.All[0]
	if s.ID != saleID || s.TotalCents != 40000 || len(s.Items) != 0 || s.ClientName != "Lía Pérez" || s.Method != domain.MethodCard || s.ProfessionalID != w.OwnerID {
		t.Fatalf("venta: %+v", s)
	}
	// Consumir no es una venta.
	cpID := w.owned.Packages[0].ID
	httpkit.Do(w.Env.Router, "POST", w.clientPath(w.ClientID)+"/"+cpID+"/redeem", `{}`, w.Recep)
	if len(w.Env.Sales.All) != 1 {
		t.Fatalf("consumir creó una venta")
	}
}

func TestHTTP_packages_purchase_replay(t *testing.T) {
	w := newPkgWorld(t)
	cp1, sale1 := w.buy(t, "op-http-1")
	cp2, sale2 := w.buy(t, "op-http-1")
	if cp1 != cp2 || sale1 != sale2 {
		t.Fatalf("el repetido no devolvió la original")
	}
	if len(w.owned.Packages) != 1 || len(w.Env.Sales.All) != 1 {
		t.Fatalf("el repetido creó otra compra")
	}
	// Otra clienta con el mismo op id: 409.
	r := httpkit.Do(w.Env.Router, "POST", w.clientPath(w.clientTwoID), w.buyBody("op-http-1"), w.Recep)
	wantHTTP(t, r, http.StatusConflict, "Esa operación ya se usó en otro pedido")
}

func TestHTTP_packages_redeem_replay_does_not_decrement(t *testing.T) {
	w := newPkgWorld(t)
	cpID, _ := w.buy(t, "")
	path := w.clientPath(w.ClientID) + "/" + cpID + "/redeem"
	for i := 0; i < 3; i++ {
		r := httpkit.Do(w.Env.Router, "POST", path, `{"client_op_id":"op-red-1"}`, w.Recep)
		if r.Code != http.StatusOK {
			t.Fatalf("redeem %d: %d %s", i, r.Code, r.Body.String())
		}
	}
	if w.owned.Packages[0].SessionsLeft != 2 {
		t.Fatalf("saldo = %d, esperaba 2 (un solo descuento)", w.owned.Packages[0].SessionsLeft)
	}
}

// --- Permisos rol por rol ---------------------------------------------------------------

func TestHTTP_packages_roles(t *testing.T) {
	w := newPkgWorld(t)
	w.withAllRoles(t)
	cpID, _ := w.buy(t, "")
	cl := w.clientPath(w.ClientID)
	type req struct{ method, path, body string }
	// Cada ruta con su cuerpo válido y quién puede.
	routes := []struct {
		name    string
		req     req
		allowed []string // tokens que pasan
	}{
		{"GET catálogo", req{"GET", w.catalogPath(), ""}, []string{w.Owner, w.Admin, w.Recep}},
		{"POST catálogo", req{"POST", w.catalogPath(), `{"name":"Nuevo %s","service_id":"` + w.svcID + `","sessions":2,"price_cents":100}`}, []string{w.Owner, w.Admin}},
		{"PUT catálogo", req{"PUT", w.catalogPath() + "/" + w.pkgID, `{"name":"Pack 3 cortes","price_cents":40000,"validity_days":30,"active":true}`}, []string{w.Owner, w.Admin}},
		{"GET paquetes de la clienta", req{"GET", cl, ""}, []string{w.Owner, w.Admin, w.Recep}},
		{"POST comprar", req{"POST", cl, w.buyBody("")}, []string{w.Owner, w.Admin, w.Recep}},
		{"GET movimientos", req{"GET", cl + "/" + cpID + "/movements", ""}, []string{w.Owner, w.Admin, w.Recep}},
		{"POST consumir", req{"POST", cl + "/" + cpID + "/redeem", `{}`}, []string{w.Owner, w.Admin, w.Recep}},
		{"POST ajustar", req{"POST", cl + "/" + cpID + "/adjust", `{"delta":1,"note":"x"}`}, []string{w.Owner, w.Admin}},
	}
	all := map[string]string{
		w.Owner: "owner", w.Admin: "admin", w.Recep: "reception", w.Prof: "professional",
		w.Client: "client", w.Removed: "baja", w.other: "otro salón",
	}
	for _, rt := range routes {
		for tok, role := range all {
			allowed := false
			for _, a := range rt.allowed {
				allowed = allowed || a == tok
			}
			body := strings.ReplaceAll(rt.req.body, "%s", role)
			r := httpkit.Do(w.Env.Router, rt.req.method, rt.req.path, body, tok)
			if allowed && (r.Code == http.StatusForbidden || r.Code == http.StatusUnauthorized) {
				t.Errorf("%s: %s debería poder y dio %d %s", rt.name, role, r.Code, r.Body.String())
			}
			if !allowed && r.Code != http.StatusForbidden {
				t.Errorf("%s: %s debe dar 403, dio %d %s", rt.name, role, r.Code, r.Body.String())
			}
			if r.Code >= 500 {
				t.Errorf("%s: %s dio 5xx: %d", rt.name, role, r.Code)
			}
		}
		// Sin token: 401.
		if r := httpkit.Do(w.Env.Router, rt.req.method, rt.req.path, strings.ReplaceAll(rt.req.body, "%s", "x"), ""); r.Code != http.StatusUnauthorized {
			t.Errorf("%s sin token debe dar 401, dio %d", rt.name, r.Code)
		}
	}
	// Un 403 NO escribe: el saldo y el número de movimientos solo reflejan lo que
	// hicieron los roles permitidos (3 consumos {} y los ajustes +1 que se rechazan
	// por exceder el total).
	if got := w.owned.Packages[0]; got.SessionsLeft < 0 || got.SessionsLeft > got.SessionsTotal || w.owned.SumDelta(got.ID) != got.SessionsLeft {
		t.Fatalf("invariante roto: %+v Σ=%d", got, w.owned.SumDelta(got.ID))
	}
}

// --- Validaciones, textos exactos y que un 400 no escribe ------------------------------

func TestHTTP_packages_catalog_validations(t *testing.T) {
	w := newPkgWorld(t)
	base := w.catalogPath()
	svc := w.svcID
	n := len(w.catalog.All)

	// POST sin nombre, servicio, sesiones o precio: el texto exacto y NO escribe.
	for _, body := range []string{
		`{}`,
		`{"service_id":"` + svc + `","sessions":2,"price_cents":100}`,
		`{"name":"X","sessions":2,"price_cents":100}`,
		`{"name":"X","service_id":"` + svc + `","price_cents":100}`,
		`{"name":"X","service_id":"` + svc + `","sessions":2}`,
		`{"name":null,"service_id":"` + svc + `","sessions":2,"price_cents":100}`,
		`{"name":"X","service_id":"` + svc + `","sessions":null,"price_cents":100}`,
		`{"name":"  ","service_id":"` + svc + `","sessions":2,"price_cents":100}`,
	} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, body, w.Owner), 400, "Faltan datos del paquete: nombre, servicio, sesiones y precio")
	}
	// Servicio inexistente: 400 «Paquete inválido».
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"X","service_id":"nope","sessions":2,"price_cents":100}`, w.Owner), 400, "Paquete inválido")
	// Rangos.
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"X","service_id":"`+svc+`","sessions":0,"price_cents":100}`, w.Owner), 400, domain.MsgPackageSessions)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"X","service_id":"`+svc+`","sessions":101,"price_cents":100}`, w.Owner), 400, domain.MsgPackageSessions)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"X","service_id":"`+svc+`","sessions":2,"price_cents":-1}`, w.Owner), 400, domain.MsgPackagePriceRange)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"X","service_id":"`+svc+`","sessions":2,"price_cents":10000001}`, w.Owner), 400, domain.MsgPackagePriceRange)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"X","service_id":"`+svc+`","sessions":2,"price_cents":1,"validity_days":0}`, w.Owner), 400, domain.MsgPackageValidity)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"X","service_id":"`+svc+`","sessions":2,"price_cents":1,"validity_days":3651}`, w.Owner), 400, domain.MsgPackageValidity)
	// Tipos equivocados: 400 «body inválido», nunca 500; los números con decimales, tampoco.
	for _, body := range []string{
		`{"name":123,"service_id":"` + svc + `","sessions":2,"price_cents":1}`,
		`{"name":"X","service_id":"` + svc + `","sessions":"2","price_cents":1}`,
		`{"name":"X","service_id":"` + svc + `","sessions":1.5,"price_cents":1}`,
		`{"name":"X","service_id":"` + svc + `","sessions":2,"price_cents":10.5}`,
		`{"name":"X","service_id":"` + svc + `","sessions":2,"price_cents":1,"validity_days":"x"}`,
		`{"name":"X","service_id":"` + svc + `","sessions":99999999999999999999,"price_cents":1}`,
		`no es json`,
	} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, body, w.Owner), 400, "body inválido")
	}
	// NUL y salto de línea en el nombre: 400 del dominio, nunca 500.
	for _, name := range []string{`a\u0000b`, `a\nb`, `a\rb`, `a\u2028b`} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"`+name+`","service_id":"`+svc+`","sessions":2,"price_cents":1}`, w.Owner), 400, domain.MsgPackageInvalidChars)
	}
	if len(w.catalog.All) != n {
		t.Fatalf("un 400 escribió: %d -> %d", n, len(w.catalog.All))
	}
	// Nombre activo repetido: 409 con el texto exacto (y sin importar mayúsculas).
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base, `{"name":"PACK 3 CORTES","service_id":"`+svc+`","sessions":2,"price_cents":1}`, w.Owner), 409, "Ya hay un paquete activo con ese nombre")
	// Un 0 explícito es un precio válido.
	if r := httpkit.Do(w.Env.Router, "POST", base, `{"name":"Regalo","service_id":"`+svc+`","sessions":1,"price_cents":0}`, w.Admin); r.Code != http.StatusCreated {
		t.Fatalf("precio 0: %d %s", r.Code, r.Body.String())
	}
}

// D10: el PUT REEMPLAZA y exige las CUATRO claves; validity_days: null explícito =
// sin vencimiento; la clave ausente NO se rellena en silencio.
func TestHTTP_packages_put_requires_all_four_keys(t *testing.T) {
	w := newPkgWorld(t)
	path := w.catalogPath() + "/" + w.pkgID
	const missing = "Faltan datos del paquete: nombre, precio, vigencia y estado"
	before := w.catalog.All[0]

	for _, body := range []string{
		`{}`,
		`{"price_cents":1,"validity_days":null,"active":true}`,
		`{"name":"X","validity_days":null,"active":true}`,
		`{"name":"X","price_cents":1,"active":true}`, // validity_days AUSENTE
		`{"name":"X","price_cents":1,"validity_days":null}`,
		`{"name":null,"price_cents":1,"validity_days":null,"active":true}`,
		`{"name":"X","price_cents":null,"validity_days":null,"active":true}`,
		`{"name":"X","price_cents":1,"validity_days":null,"active":null}`,
	} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", path, body, w.Owner), 400, missing)
	}
	// Tipos equivocados: 400 body inválido.
	for _, body := range []string{
		`{"name":"X","price_cents":"1","validity_days":null,"active":true}`,
		`{"name":"X","price_cents":1,"validity_days":"30","active":true}`,
		`{"name":"X","price_cents":1,"validity_days":1.5,"active":true}`,
		`{"name":"X","price_cents":1,"validity_days":null,"active":"si"}`,
	} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", path, body, w.Owner), 400, "body inválido")
	}
	// Validaciones del dominio.
	wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", path, `{"name":"X","price_cents":1,"validity_days":0,"active":true}`, w.Owner), 400, domain.MsgPackageValidity)
	wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", path, `{"name":"a\u0000b","price_cents":1,"validity_days":null,"active":true}`, w.Owner), 400, domain.MsgPackageInvalidChars)
	wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", path, `{"name":"a\nb","price_cents":1,"validity_days":null,"active":true}`, w.Owner), 400, domain.MsgPackageInvalidChars)
	wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", path, `{"name":"","price_cents":1,"validity_days":null,"active":true}`, w.Owner), 400, domain.MsgPackageNameRequired)
	// Nada de lo anterior escribió.
	if got := w.catalog.All[0]; got.Name != before.Name || got.PriceCents != before.PriceCents || got.ValidityDays == nil || *got.ValidityDays != 30 || !got.Active {
		t.Fatalf("un 400 modificó el paquete: %+v", got)
	}

	// Un PUT completo con validity_days: null LIMPIA la vigencia (la respuesta lo dice).
	r := httpkit.Do(w.Env.Router, "PUT", path, `{"name":"Pack renombrado","price_cents":55000,"validity_days":null,"active":true}`, w.Owner)
	if r.Code != http.StatusOK {
		t.Fatalf("PUT completo: %d %s", r.Code, r.Body.String())
	}
	var got map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &got)
	if got["name"] != "Pack renombrado" || got["price_cents"] != float64(55000) || got["validity_days"] != nil || got["active"] != true ||
		got["sessions"] != float64(3) || got["service_id"] != w.svcID || got["service_name"] != "Corte" {
		t.Fatalf("respuesta del PUT: %s", r.Body.String())
	}
	if w.catalog.All[0].ValidityDays != nil {
		t.Fatalf("la vigencia nula no se guardó")
	}
	// Con un número, lo guarda; service_id y sessions del body se ignoran.
	r = httpkit.Do(w.Env.Router, "PUT", path, `{"name":"Pack renombrado","price_cents":55000,"validity_days":90,"active":false,"sessions":50,"service_id":"otro"}`, w.Admin)
	if r.Code != http.StatusOK || w.catalog.All[0].ValidityDays == nil || *w.catalog.All[0].ValidityDays != 90 || w.catalog.All[0].Active ||
		w.catalog.All[0].Sessions != 3 || w.catalog.All[0].ServiceID != w.svcID {
		t.Fatalf("PUT con número: %d %s %+v", r.Code, r.Body.String(), w.catalog.All[0])
	}
	// 404 inexistente / de otro salón y 409 de nombre.
	wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", w.catalogPath()+"/no-existe", `{"name":"X","price_cents":1,"validity_days":null,"active":true}`, w.Owner), 404, "Paquete no encontrado")
	wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", "/api/v1/salons/"+w.otherSalon+"/packages/"+w.pkgID, `{"name":"X","price_cents":1,"validity_days":null,"active":true}`, w.other), 404, "Paquete no encontrado")
	if r := httpkit.Do(w.Env.Router, "PUT", path, `{"name":"Pack renombrado","price_cents":55000,"validity_days":null,"active":true}`, w.Owner); r.Code != http.StatusOK {
		t.Fatalf("reactivar: %d %s", r.Code, r.Body.String())
	}
	second := w.createPkg(t, `{"name":"Segundo","service_id":"`+w.svcID+`","sessions":1,"price_cents":1}`)
	wantHTTP(t, httpkit.Do(w.Env.Router, "PUT", w.catalogPath()+"/"+second, `{"name":"pack RENOMBRADO","price_cents":1,"validity_days":null,"active":true}`, w.Owner), 409, "Ya hay un paquete activo con ese nombre")
	if w.catalog.All[1].Name != "Segundo" {
		t.Fatalf("un 409 modificó el paquete")
	}
}

// D5/D7/D9: validaciones de los pedidos por clienta con sus textos exactos; cada
// 400/404/409 no escribe nada.
func TestHTTP_packages_client_routes_errors_write_nothing(t *testing.T) {
	w := newPkgWorld(t)
	cpID, _ := w.buy(t, "")
	cl := w.clientPath(w.ClientID)
	snap := func() [4]int {
		return [4]int{len(w.Env.Sales.All), len(w.owned.Packages), len(w.owned.Movements), w.owned.Packages[0].SessionsLeft}
	}
	before := snap()

	// Comprar: paquete inválido (inexistente / vacío / null / NUL), 400 exactos.
	for _, pid := range []string{`"no-existe"`, `""`, `null`, `"a\u0000b"`} {
		body := `{"package_id":` + pid + `,"professional_id":"` + w.OwnerID + `","method":"cash"}`
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", cl, body, w.Recep), 400, "Paquete inválido")
	}
	// Paquete inactivo / servicio inactivo.
	off := w.createPkg(t, `{"name":"Apagado","service_id":"`+w.svcID+`","sessions":2,"price_cents":100}`)
	httpkit.Do(w.Env.Router, "PUT", w.catalogPath()+"/"+off, `{"name":"Apagado","price_cents":100,"validity_days":null,"active":false}`, w.Owner)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", cl, `{"package_id":"`+off+`","professional_id":"`+w.OwnerID+`","method":"cash"}`, w.Recep), 400, "Paquete inválido")
	// Método inválido, profesional inválido o ausente, op id con NUL: 400 y nada.
	if r := httpkit.Do(w.Env.Router, "POST", cl, `{"package_id":"`+w.pkgID+`","professional_id":"`+w.OwnerID+`","method":"bitcoin"}`, w.Recep); r.Code != http.StatusBadRequest {
		t.Fatalf("método: %d %s", r.Code, r.Body.String())
	}
	for _, prof := range []string{`"ghost"`, `""`, `null`, `"a\u0000b"`} {
		if r := httpkit.Do(w.Env.Router, "POST", cl, `{"package_id":"`+w.pkgID+`","professional_id":`+prof+`,"method":"cash"}`, w.Recep); r.Code != http.StatusBadRequest {
			t.Fatalf("profesional %s: %d %s", prof, r.Code, r.Body.String())
		}
	}
	if r := httpkit.Do(w.Env.Router, "POST", cl, `{"package_id":"`+w.pkgID+`","professional_id":"`+w.OwnerID+`","method":"cash","client_op_id":"a\u0000b"}`, w.Recep); r.Code != http.StatusBadRequest {
		t.Fatalf("op id con NUL: %d %s", r.Code, r.Body.String())
	}
	// Tipos equivocados en el body: 400, nunca 500.
	for _, body := range []string{`{"package_id":5}`, `{"method":true}`, `[]`, `nope`} {
		if r := httpkit.Do(w.Env.Router, "POST", cl, body, w.Recep); r.Code != http.StatusBadRequest {
			t.Fatalf("body %s: %d", body, r.Code)
		}
	}

	// Clienta de otro salón / inexistente / con NUL: 404 con el texto de las rutas de clientes.
	foreignClient := w.newClient(t, w.otherSalon, w.other, "Clienta Ajena")
	for _, cid := range []string{foreignClient, "no-existe", "%00"} {
		r := httpkit.Do(w.Env.Router, "POST", w.clientPath(cid), w.buyBody(""), w.Recep)
		wantHTTP(t, r, 404, "Ficha no encontrada")
		wantHTTP(t, httpkit.Do(w.Env.Router, "GET", w.clientPath(cid), "", w.Recep), 404, "Ficha no encontrada")
	}
	// Consumir/ajustar/ver movimientos: id de paquete inexistente, de OTRA clienta
	// del mismo salón, de OTRO salón (y NUL): 404 «Paquete no encontrado».
	for _, c := range []struct{ client, pkg string }{
		{w.ClientID, "no-existe"}, {w.clientTwoID, cpID}, {foreignClient, cpID}, {w.ClientID, "%00"}, {"%00", cpID},
	} {
		base := w.clientPath(c.client) + "/" + c.pkg
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base+"/redeem", `{}`, w.Recep), 404, "Paquete no encontrado")
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", base+"/adjust", `{"delta":1,"note":"x"}`, w.Owner), 404, "Paquete no encontrado")
		wantHTTP(t, httpkit.Do(w.Env.Router, "GET", base+"/movements", "", w.Owner), 404, "Paquete no encontrado")
	}
	// Una persona de OTRO salón no toca los paquetes de este: 403 en la ruta de
	// este salón, y en la suya el paquete no existe (404).
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", cl+"/"+cpID+"/redeem", `{}`, w.other), 403, "")
	otherBase := "/api/v1/salons/" + w.otherSalon + "/clients/" + w.ClientID + "/packages/" + cpID
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", otherBase+"/redeem", `{}`, w.other), 404, "Paquete no encontrado")

	// Descontar: nota con NUL / salto / demasiado larga, profesional inválido, op con NUL.
	for _, body := range []string{
		`{"note":"a\u0000b"}`, `{"note":"a\nb"}`, `{"note":"` + strings.Repeat("x", 201) + `"}`,
		`{"professional_id":"ghost"}`, `{"professional_id":"a\u0000b"}`, `{"client_op_id":"a\u0000b"}`,
	} {
		if r := httpkit.Do(w.Env.Router, "POST", cl+"/"+cpID+"/redeem", body, w.Recep); r.Code != http.StatusBadRequest {
			t.Fatalf("redeem %s: %d %s", body, r.Code, r.Body.String())
		}
	}
	for _, body := range []string{`{"note":5}`, `{"professional_id":5}`, `nope`} {
		if r := httpkit.Do(w.Env.Router, "POST", cl+"/"+cpID+"/redeem", body, w.Recep); r.Code != http.StatusBadRequest {
			t.Fatalf("redeem tipo %s: %d", body, r.Code)
		}
	}
	// Ajustar: textos exactos.
	adj := cl + "/" + cpID + "/adjust"
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":1,"note":""}`, w.Owner), 400, "Escribí el motivo del ajuste")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":1}`, w.Owner), 400, "Escribí el motivo del ajuste")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":0,"note":"x"}`, w.Owner), 400, "El ajuste no puede ser cero")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"note":"x"}`, w.Owner), 400, "El ajuste no puede ser cero")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":1,"note":"x"}`, w.Owner), 409, "El ajuste deja el saldo fuera de rango")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":-4,"note":"x"}`, w.Owner), 409, "El ajuste deja el saldo fuera de rango")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":9999999999,"note":"x"}`, w.Owner), 409, "El ajuste deja el saldo fuera de rango")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":1,"note":"a\u0000b"}`, w.Owner), 400, domain.MsgPackageInvalidChars)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, `{"delta":1,"note":"a\nb"}`, w.Owner), 400, domain.MsgPackageInvalidChars)
	for _, body := range []string{`{"delta":1.5,"note":"x"}`, `{"delta":"1","note":"x"}`, `{"delta":1,"note":3}`, `{"delta":99999999999999999999,"note":"x"}`} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", adj, body, w.Owner), 400, "body inválido")
	}
	// Límites del movimientos? (solo lectura). Nada de lo anterior escribió.
	// (La compra de «Apagado» se creó y desactivó, pero no vendió nada.)
	if after := snap(); after != before {
		t.Fatalf("un rechazo escribió: %v -> %v", before, after)
	}
}

// D7: sin saldo, vencido y agotado, con los textos exactos y SIN escribir.
func TestHTTP_packages_redeem_409s(t *testing.T) {
	w := newPkgWorld(t)
	cpID, _ := w.buy(t, "")
	path := w.clientPath(w.ClientID) + "/" + cpID + "/redeem"
	for i := 0; i < 3; i++ {
		if r := httpkit.Do(w.Env.Router, "POST", path, `{}`, w.Recep); r.Code != http.StatusOK {
			t.Fatalf("redeem %d: %d %s", i, r.Code, r.Body.String())
		}
	}
	n := len(w.owned.Movements)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", path, `{}`, w.Recep), 409, "No quedan sesiones en este paquete")
	if len(w.owned.Movements) != n || w.owned.Packages[0].SessionsLeft != 0 {
		t.Fatalf("un 409 escribió")
	}
	// Vencido: se fuerza el vencimiento al pasado.
	cp2, _ := w.buy(t, "")
	for i := range w.owned.Packages {
		if w.owned.Packages[i].ID == cp2 {
			past := time.Now().Add(-time.Hour)
			w.owned.Packages[i].ExpiresAt = &past
		}
	}
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.clientPath(w.ClientID)+"/"+cp2+"/redeem", `{}`, w.Recep), 409, "El paquete venció")
	// La lectura lo marca vencido y los vigentes con saldo primero.
	r := httpkit.Do(w.Env.Router, "GET", w.clientPath(w.ClientID), "", w.Recep)
	var list struct {
		Packages []map[string]any `json:"packages"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &list)
	if len(list.Packages) != 2 {
		t.Fatalf("lista: %s", r.Body.String())
	}
	for _, p := range list.Packages {
		if p["id"] == cp2 && p["expired"] != true {
			t.Fatalf("expired: %+v", p)
		}
		if p["id"] == cpID && p["expired"] != false {
			t.Fatalf("expired del agotado: %+v", p)
		}
	}
}

// Las rutas y respuestas EXISTENTES no cambian: el POS sigue vendiendo con su
// misma forma y `GET /sales` ve la compra del paquete como una venta más.
func TestHTTP_packages_preserve_existing_endpoints(t *testing.T) {
	w := newPkgWorld(t)
	r := httpkit.Do(w.Env.Router, "POST", "/api/v1/salons/"+w.Salon+"/sales",
		`{"professional_id":"`+w.OwnerID+`","client_name":"Mostrador","method":"cash","items":[{"service_id":"`+w.svcID+`","qty":2}]}`, w.Recep)
	if r.Code != http.StatusCreated {
		t.Fatalf("POST /sales: %d %s", r.Code, r.Body.String())
	}
	var sale map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &sale)
	if sale["total"] != float64(30000) || sale["sale_id"] == "" {
		t.Fatalf("forma de POST /sales: %s", r.Body.String())
	}
	if _, has := sale["packages_cents"]; has {
		t.Fatalf("POST /sales no debe exponer packages_cents: %s", r.Body.String())
	}
	w.buy(t, "")
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	r = httpkit.Do(w.Env.Router, "GET", "/api/v1/salons/"+w.Salon+"/sales?from="+from+"&to="+to, "", w.Owner)
	if r.Code != http.StatusOK {
		t.Fatalf("GET /sales: %d %s", r.Code, r.Body.String())
	}
	var out struct {
		Sales []map[string]any `json:"sales"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	total := 0.0
	for _, s := range out.Sales {
		total += s["total_cents"].(float64)
	}
	if len(out.Sales) != 2 || total != 70000 {
		t.Fatalf("GET /sales debe ver la venta del POS y la del paquete: %s", r.Body.String())
	}
	// Las rutas de clientes siguen igual.
	if r := httpkit.Do(w.Env.Router, "GET", "/api/v1/salons/"+w.Salon+"/clients/"+w.ClientID, "", w.Recep); r.Code != http.StatusOK {
		t.Fatalf("GET cliente: %d", r.Code)
	}
}

var _ = json.Marshal
