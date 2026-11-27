package httpapi_test

import (
	"net/http"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"sort"
	"strings"
	"testing"
	"time"
)

// loyWorld es un salón con todos los roles, dos clientas y un servicio de regalo.
type loyWorld struct {
	Env      *httpkit.Env
	programs *httpkit.LoyaltyPrograms
	Rewards  *httpkit.LoyaltyRewards
	Salon    string
	Slug     string
	// otherSalon es OTRO tenant con su propio dueño (w.Outsider).
	otherSalon string
	// Tokens por rol.
	Owner, Admin, Recep, Prof, Client, Removed, Outsider string
	OwnerID, ProfID                                      string
	svcID, svcTwoID, liaID, moraID, otherClientID        string
}

func (w *loyWorld) fastUser(t *testing.T, email string) (string, string) {
	t.Helper()
	u := domain.User{ID: domain.NewID(), Name: email, Email: email, PasswordHash: "x"}
	if err := w.Env.Users.Save(u); err != nil {
		t.Fatal(err)
	}
	tok, err := security.JWTIssuer{Secret: "test-secret"}.Issue(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tok, u.ID
}

func newLoyWorld(t *testing.T) *loyWorld {
	t.Helper()
	env, programs, rewards := httpkit.NewLoyaltyEnv()
	w := &loyWorld{Env: env, programs: programs, Rewards: rewards, Slug: "fidelidad-uno"}
	var id string
	w.Owner, w.OwnerID = w.fastUser(t, "owner-loy@mail.com")
	w.Salon = env.Salon(t, w.Owner, w.Slug)
	w.Admin, id = w.fastUser(t, "admin-loy@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleAdmin)
	w.Recep, id = w.fastUser(t, "recep-loy@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleReception)
	w.Prof, w.ProfID = w.fastUser(t, "prof-loy@mail.com")
	httpkit.SeedRole(t, env, w.Salon, w.ProfID, domain.RoleProfessional)
	w.Client, id = w.fastUser(t, "client-loy@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleClient)
	w.Removed, id = w.fastUser(t, "removed-loy@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleAdmin)
	at := time.Now().Add(-time.Hour)
	m := env.Mems.ByKey[id+"|"+w.Salon]
	m.EndedAt = &at
	env.Mems.ByKey[id+"|"+w.Salon] = m
	w.Outsider, _ = w.fastUser(t, "other-loy@mail.com")
	w.otherSalon = env.Salon(t, w.Outsider, "fidelidad-dos")

	w.svcID = env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	w.svcTwoID = env.SeedService(t, w.Owner, w.Salon, "Tinte", 9000)
	w.liaID = w.newClient(t, w.Salon, w.Owner, "Lía Pérez")
	w.moraID = w.newClient(t, w.Salon, w.Owner, "Mora Díaz")
	w.otherClientID = w.newClient(t, w.otherSalon, w.Outsider, "Clienta Ajena")
	return w
}

func (w *loyWorld) newClient(t *testing.T, salon, token, name string) string {
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

func (w *loyWorld) programPath() string { return "/api/v1/salons/" + w.Salon + "/loyalty-program" }
func (w *loyWorld) clientPath(cid string) string {
	return "/api/v1/salons/" + w.Salon + "/clients/" + cid + "/loyalty"
}
func (w *loyWorld) redeemPath(cid, rid string) string {
	return w.clientPath(cid) + "/rewards/" + rid + "/redeem"
}

func (w *loyWorld) programBody(enabled bool, visits int) string {
	b := `{"enabled":` + map[bool]string{true: "true", false: "false"}[enabled] + `,"visits_required":` + httpkit.Itoa(visits) +
		`,"reward_service_id":"` + w.svcID + `","reward_validity_days":null}`
	return b
}

// saveProgram guarda el programa con el dueño y falla si no es 200.
func (w *loyWorld) saveProgram(t *testing.T, enabled bool, visits int) {
	t.Helper()
	if r := httpkit.Do(w.Env.Router, "PUT", w.programPath(), w.programBody(enabled, visits), w.Owner); r.Code != http.StatusOK {
		t.Fatalf("guardar programa: %d %s", r.Code, r.Body.String())
	}
}

func (w *loyWorld) setVisits(clientID string, n int) {
	w.Rewards.VisitCounts[httpkit.LoyaltyKey(w.Salon, clientID)] = n
}

// grant entrega un vale a Lía con recepción y devuelve su id.
func (w *loyWorld) grant(t *testing.T, op string) string {
	t.Helper()
	body := ""
	if op != "" {
		body = `{"client_op_id":"` + op + `"}`
	}
	r := httpkit.Do(w.Env.Router, "POST", w.clientPath(w.liaID)+"/grant", body, w.Recep)
	if r.Code != http.StatusCreated {
		t.Fatalf("entregar: %d %s", r.Code, r.Body.String())
	}
	var out struct {
		Reward map[string]any `json:"reward"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	return out.Reward["id"].(string)
}

// --- D4: programa -----------------------------------------------------------------

// GET lo ve cualquier miembro activo con la forma exacta; sin guardar es
// enabled:false y el resto null.
func TestLoyaltyHTTP_getProgram_shapeAndRoles(t *testing.T) {
	w := newLoyWorld(t)
	for name, tok := range map[string]string{"owner": w.Owner, "admin": w.Admin, "recep": w.Recep, "prof": w.Prof, "client": w.Client} {
		r := httpkit.Do(w.Env.Router, "GET", w.programPath(), "", tok)
		if r.Code != http.StatusOK {
			t.Fatalf("%s lee el programa: %d %s", name, r.Code, r.Body.String())
		}
		var got map[string]any
		httpkit.MustJSON(t, r.Body.Bytes(), &got)
		if strings.Join(keysOf(got), ",") != "counting_from,enabled,reward_service_id,reward_service_name,reward_validity_days,visits_required" {
			t.Fatalf("claves: %v", keysOf(got))
		}
		if got["enabled"] != false || got["visits_required"] != nil || got["reward_service_id"] != nil ||
			got["reward_service_name"] != nil || got["reward_validity_days"] != nil || got["counting_from"] != nil {
			t.Fatalf("sin guardar: %v", got)
		}
	}
	for name, tok := range map[string]string{"baja": w.Removed, "otro salón": w.Outsider} {
		if r := httpkit.Do(w.Env.Router, "GET", w.programPath(), "", tok); r.Code != http.StatusForbidden {
			t.Fatalf("%s lee el programa: %d, quería 403", name, r.Code)
		}
	}
	if r := httpkit.Do(w.Env.Router, "GET", w.programPath(), "", ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d, quería 401", r.Code)
	}
}

func TestLoyaltyHTTP_putProgram_savesAndKeepsCountingFrom(t *testing.T) {
	w := newLoyWorld(t)
	r := httpkit.Do(w.Env.Router, "PUT", w.programPath(),
		`{"enabled":true,"visits_required":5,"reward_service_id":"`+w.svcID+`","reward_validity_days":30}`, w.Admin)
	if r.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", r.Code, r.Body.String())
	}
	var first map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &first)
	if first["enabled"] != true || first["visits_required"] != float64(5) || first["reward_service_id"] != w.svcID ||
		first["reward_service_name"] != "Corte" || first["reward_validity_days"] != float64(30) {
		t.Fatalf("respuesta: %v", first)
	}
	cf, _ := first["counting_from"].(string)
	if _, err := time.Parse(time.RFC3339, cf); err != nil {
		t.Fatalf("counting_from %q no es RFC3339", cf)
	}
	// Reguardar (otros datos, vigencia null explícita): counting_from NO cambia.
	time.Sleep(1100 * time.Millisecond)
	r = httpkit.Do(w.Env.Router, "PUT", w.programPath(),
		`{"enabled":false,"visits_required":8,"reward_service_id":"`+w.svcTwoID+`","reward_validity_days":null}`, w.Owner)
	if r.Code != http.StatusOK {
		t.Fatalf("segundo PUT: %d %s", r.Code, r.Body.String())
	}
	var second map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &second)
	if second["counting_from"] != cf || second["enabled"] != false || second["visits_required"] != float64(8) ||
		second["reward_service_name"] != "Tinte" || second["reward_validity_days"] != nil {
		t.Fatalf("segundo guardado: %v (counting_from original %s)", second, cf)
	}
	// GET devuelve lo mismo.
	g := httpkit.Do(w.Env.Router, "GET", w.programPath(), "", w.Recep)
	var got map[string]any
	httpkit.MustJSON(t, g.Body.Bytes(), &got)
	if got["counting_from"] != cf || got["visits_required"] != float64(8) {
		t.Fatalf("GET tras PUT: %v", got)
	}
}

// D4: el permiso va ANTES del cuerpo (un cuerpo roto o vacío da 403 a quien no
// puede) y nada se escribe.
func TestLoyaltyHTTP_putProgram_permissionBeforeBody(t *testing.T) {
	w := newLoyWorld(t)
	for _, body := range []string{`{}`, `{"enabled":`, `[]`, w.programBody(true, 5)} {
		for name, tok := range map[string]string{"recep": w.Recep, "prof": w.Prof, "client": w.Client, "baja": w.Removed, "otro salón": w.Outsider} {
			if r := httpkit.Do(w.Env.Router, "PUT", w.programPath(), body, tok); r.Code != http.StatusForbidden {
				t.Fatalf("%s con %q: %d, quería 403", name, body, r.Code)
			}
		}
	}
	if w.programs.Saves != 0 {
		t.Fatalf("un 403 escribió el programa")
	}
	// Quien sí puede sigue recibiendo la validación del cuerpo.
	if r := httpkit.Do(w.Env.Router, "PUT", w.programPath(), `{}`, w.Owner); r.Code != http.StatusBadRequest {
		t.Fatalf("dueño con {}: %d", r.Code)
	}
	if r := httpkit.Do(w.Env.Router, "PUT", w.programPath(), `{"enabled":`, w.Admin); r.Code != http.StatusBadRequest {
		t.Fatalf("admin con cuerpo roto: %d", r.Code)
	}
}

// D4: textos EXACTOS y nada escrito ante un cuerpo inválido; las CUATRO claves
// son obligatorias (null explícito solo para la vigencia).
func TestLoyaltyHTTP_putProgram_validation(t *testing.T) {
	w := newLoyWorld(t)
	missing := "Faltan datos del programa: activado, visitas, servicio y vigencia"
	sv := `"` + w.svcID + `"`
	cases := []struct {
		name, body string
		code       int
		text       string
	}{
		{"sin enabled", `{"visits_required":5,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, missing},
		{"sin visitas", `{"enabled":true,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, missing},
		{"sin servicio", `{"enabled":true,"visits_required":5,"reward_validity_days":null}`, 400, missing},
		{"sin vigencia", `{"enabled":true,"visits_required":5,"reward_service_id":` + sv + `}`, 400, missing},
		{"enabled null", `{"enabled":null,"visits_required":5,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, missing},
		{"visitas null", `{"enabled":true,"visits_required":null,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, missing},
		{"servicio null", `{"enabled":true,"visits_required":5,"reward_service_id":null,"reward_validity_days":null}`, 400, missing},
		{"vacío", `{}`, 400, missing},
		{"visitas 1", `{"enabled":true,"visits_required":1,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, "Las visitas necesarias deben ser entre 2 y 50"},
		{"visitas 51", `{"enabled":true,"visits_required":51,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, "Las visitas necesarias deben ser entre 2 y 50"},
		{"servicio inexistente", `{"enabled":true,"visits_required":5,"reward_service_id":"nada","reward_validity_days":null}`, 400, "Servicio inválido"},
		{"servicio vacío", `{"enabled":true,"visits_required":5,"reward_service_id":"","reward_validity_days":null}`, 400, "Servicio inválido"},
		{"servicio con NUL", `{"enabled":true,"visits_required":5,"reward_service_id":"a\u0000b","reward_validity_days":null}`, 400, "Servicio inválido"},
		{"vigencia 0", `{"enabled":true,"visits_required":5,"reward_service_id":` + sv + `,"reward_validity_days":0}`, 400, "La vigencia debe ser entre 1 y 3650 días"},
		{"vigencia 3651", `{"enabled":true,"visits_required":5,"reward_service_id":` + sv + `,"reward_validity_days":3651}`, 400, "La vigencia debe ser entre 1 y 3650 días"},
		{"tipo malo", `{"enabled":"si","visits_required":5,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, "body inválido"},
		{"visitas decimales", `{"enabled":true,"visits_required":2.5,"reward_service_id":` + sv + `,"reward_validity_days":null}`, 400, "body inválido"},
		{"JSON roto", `{"enabled":`, 400, "body inválido"},
	}
	for _, c := range cases {
		r := httpkit.Do(w.Env.Router, "PUT", w.programPath(), c.body, w.Owner)
		if r.Code != c.code || errText(t, r) != c.text {
			t.Fatalf("%s: %d %q, quería %d %q", c.name, r.Code, r.Body.String(), c.code, c.text)
		}
	}
	// Servicio de OTRO salón e inactivo: «Servicio inválido».
	foreign := w.Env.SeedService(t, w.Outsider, w.otherSalon, "Ajeno", 100)
	r := httpkit.Do(w.Env.Router, "PUT", w.programPath(), `{"enabled":true,"visits_required":5,"reward_service_id":"`+foreign+`","reward_validity_days":null}`, w.Owner)
	wantHTTP(t, r, 400, "Servicio inválido")
	inactive := w.Env.SeedService(t, w.Owner, w.Salon, "Inactivo", 100)
	if r := httpkit.Do(w.Env.Router, "PUT", "/api/v1/salons/"+w.Salon+"/services/"+inactive, `{"name":"Inactivo","duration_min":45,"price_cents":100,"commission_pct":20,"active":false}`, w.Owner); r.Code != 200 {
		t.Fatalf("desactivar servicio: %d %s", r.Code, r.Body.String())
	}
	r = httpkit.Do(w.Env.Router, "PUT", w.programPath(), `{"enabled":true,"visits_required":5,"reward_service_id":"`+inactive+`","reward_validity_days":null}`, w.Owner)
	wantHTTP(t, r, 400, "Servicio inválido")
	// Ningún rechazo escribió nada.
	if w.programs.Saves != 0 {
		t.Fatalf("un 400 escribió el programa (%d guardados)", w.programs.Saves)
	}
	g := httpkit.Do(w.Env.Router, "GET", w.programPath(), "", w.Owner)
	var got map[string]any
	httpkit.MustJSON(t, g.Body.Bytes(), &got)
	if got["enabled"] != false || got["counting_from"] != nil {
		t.Fatalf("el programa quedó guardado: %v", got)
	}
	// Los límites exactos pasan.
	for _, body := range []string{
		`{"enabled":true,"visits_required":2,"reward_service_id":` + sv + `,"reward_validity_days":1}`,
		`{"enabled":false,"visits_required":50,"reward_service_id":` + sv + `,"reward_validity_days":3650}`,
	} {
		if r := httpkit.Do(w.Env.Router, "PUT", w.programPath(), body, w.Owner); r.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, r.Code, r.Body.String())
		}
	}
}

// --- D5: ver a una clienta -----------------------------------------------------------

func TestLoyaltyHTTP_getClient_shapeNumbersAndRoles(t *testing.T) {
	w := newLoyWorld(t)
	// Programa nunca guardado: enabled false, ceros y null.
	r := httpkit.Do(w.Env.Router, "GET", w.clientPath(w.liaID), "", w.Recep)
	if r.Code != http.StatusOK {
		t.Fatalf("sin programa: %d %s", r.Code, r.Body.String())
	}
	var empty map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &empty)
	if strings.Join(keysOf(empty), ",") != "enabled,next_in,rewards,rewards_available,rewards_earned,rewards_granted,visits,visits_required" {
		t.Fatalf("claves: %v", keysOf(empty))
	}
	if empty["enabled"] != false || empty["visits"] != float64(0) || empty["visits_required"] != nil || empty["next_in"] != nil ||
		empty["rewards_available"] != float64(0) || len(empty["rewards"].([]any)) != 0 {
		t.Fatalf("sin programa: %v", empty)
	}

	w.saveProgram(t, true, 5)
	w.setVisits(w.liaID, 12)
	vale := w.grant(t, "") // el vale 1
	r = httpkit.Do(w.Env.Router, "GET", w.clientPath(w.liaID), "", w.Admin)
	var got map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &got)
	if got["enabled"] != true || got["visits"] != float64(12) || got["visits_required"] != float64(5) ||
		got["rewards_earned"] != float64(2) || got["rewards_granted"] != float64(1) ||
		got["rewards_available"] != float64(1) || got["next_in"] != float64(3) {
		t.Fatalf("cuentas: %v", got)
	}
	rewards := got["rewards"].([]any)
	if len(rewards) != 1 {
		t.Fatalf("vales: %v", rewards)
	}
	rw := rewards[0].(map[string]any)
	if strings.Join(keysOf(rw), ",") != "expires_at,granted_at,id,redeemed_at,reward_number,service_id,service_name,state" {
		t.Fatalf("claves del vale: %v", keysOf(rw))
	}
	if rw["id"] != vale || rw["reward_number"] != float64(1) || rw["service_id"] != w.svcID || rw["service_name"] != "Corte" ||
		rw["state"] != "available" || rw["expires_at"] != nil || rw["redeemed_at"] != nil {
		t.Fatalf("vale: %v", rw)
	}
	// Permisos: owner/admin/recepción sí; profesional, clienta, baja y otro salón no.
	for name, tok := range map[string]string{"owner": w.Owner, "admin": w.Admin, "recep": w.Recep} {
		if r := httpkit.Do(w.Env.Router, "GET", w.clientPath(w.liaID), "", tok); r.Code != http.StatusOK {
			t.Fatalf("%s: %d", name, r.Code)
		}
	}
	for name, tok := range map[string]string{"prof": w.Prof, "client": w.Client, "baja": w.Removed, "otro salón": w.Outsider} {
		if r := httpkit.Do(w.Env.Router, "GET", w.clientPath(w.liaID), "", tok); r.Code != http.StatusForbidden {
			t.Fatalf("%s: %d, quería 403", name, r.Code)
		}
	}
	if r := httpkit.Do(w.Env.Router, "GET", w.clientPath(w.liaID), "", ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", r.Code)
	}
}

// Una clienta de otro salón, inexistente o con id sucio: 404 con el texto de las
// rutas de clientas, en las TRES rutas por clienta y sin 500.
func TestLoyaltyHTTP_unknownClient_is404(t *testing.T) {
	w := newLoyWorld(t)
	w.saveProgram(t, true, 2)
	for _, cid := range []string{w.otherClientID, "no-existe", "a%00b", "a%ffb", "%00"} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "GET", w.clientPath(cid), "", w.Recep), 404, "Ficha no encontrada")
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.clientPath(cid)+"/grant", `{}`, w.Recep), 404, "Ficha no encontrada")
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(cid, "x"), `{}`, w.Recep), 404, "Ficha no encontrada")
	}
}

// --- D6: entregar ----------------------------------------------------------------------

func TestLoyaltyHTTP_grant(t *testing.T) {
	w := newLoyWorld(t)
	grantPath := w.clientPath(w.liaID) + "/grant"
	// Nunca guardado / apagado: 409.
	w.setVisits(w.liaID, 50)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", grantPath, `{}`, w.Recep), 409, "La fidelidad no está activada")
	w.saveProgram(t, false, 5)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", grantPath, "", w.Recep), 409, "La fidelidad no está activada")
	// Sin visitas suficientes.
	w.saveProgram(t, true, 5)
	w.setVisits(w.liaID, 4)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", grantPath, `{}`, w.Recep), 409, "No hay premios disponibles")
	if len(w.Rewards.Rewards) != 0 {
		t.Fatal("un 409 entregó un vale")
	}
	// Con una visita más: 201 con la forma del contrato (cuerpo vacío vale como {}).
	w.setVisits(w.liaID, 5)
	r := httpkit.Do(w.Env.Router, "POST", grantPath, "", w.Admin)
	if r.Code != http.StatusCreated {
		t.Fatalf("entregar: %d %s", r.Code, r.Body.String())
	}
	var out map[string]map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	rw := out["reward"]
	if len(out) != 1 || rw["reward_number"] != float64(1) || rw["service_name"] != "Corte" || rw["state"] != "available" || rw["redeemed_at"] != nil {
		t.Fatalf("respuesta: %v", out)
	}
	// El segundo pedido sin visitas nuevas: 409 y un solo vale.
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", grantPath, `{}`, w.Owner), 409, "No hay premios disponibles")
	if len(w.Rewards.Rewards) != 1 {
		t.Fatalf("hay %d vales", len(w.Rewards.Rewards))
	}
}

func TestLoyaltyHTTP_grant_idempotentByOp(t *testing.T) {
	w := newLoyWorld(t)
	w.saveProgram(t, true, 2)
	w.setVisits(w.liaID, 40)
	first := w.grant(t, "op-1")
	again := w.grant(t, "op-1") // también 201, con el MISMO vale
	if again != first || len(w.Rewards.Rewards) != 1 {
		t.Fatalf("el repetido entregó otro: %s vs %s (%d vales)", again, first, len(w.Rewards.Rewards))
	}
	w.setVisits(w.moraID, 40)
	r := httpkit.Do(w.Env.Router, "POST", w.clientPath(w.moraID)+"/grant", `{"client_op_id":"op-1"}`, w.Recep)
	wantHTTP(t, r, 409, "Esa operación ya se usó en otro pedido")
	r = httpkit.Do(w.Env.Router, "POST", w.clientPath(w.liaID)+"/grant", `{"client_op_id":"a\u0000b"}`, w.Recep)
	wantHTTP(t, r, 400, "El identificador de la operación no es válido")
	if len(w.Rewards.Rewards) != 1 {
		t.Fatalf("hay %d vales", len(w.Rewards.Rewards))
	}
}

// --- D7: usar ----------------------------------------------------------------------------

func TestLoyaltyHTTP_redeem(t *testing.T) {
	w := newLoyWorld(t)
	w.saveProgram(t, true, 2)
	w.setVisits(w.liaID, 4)
	a, b := w.grant(t, ""), w.grant(t, "")
	_ = b
	// 404: inexistente, ajeno y con id sucio.
	for _, rid := range []string{"no-existe", "a%00b", "a%ffb", "%00"} {
		wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, rid), `{}`, w.Recep), 404, "Premio no encontrado")
	}
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.moraID, a), `{}`, w.Recep), 404, "Premio no encontrado")
	// 200 con la forma del contrato, profesional y nota.
	r := httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, a), `{"professional_id":"`+w.ProfID+`","note":"  Cumple  "}`, w.Recep)
	if r.Code != http.StatusOK {
		t.Fatalf("usar: %d %s", r.Code, r.Body.String())
	}
	var out map[string]map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	rw := out["reward"]
	if len(out) != 1 || rw["id"] != a || rw["state"] != "redeemed" || rw["redeemed_at"] == nil {
		t.Fatalf("respuesta: %v", out)
	}
	if w.Rewards.Rewards[0].RedeemNote != "Cumple" || w.Rewards.Rewards[0].RedeemedProfessionalID != w.ProfID {
		t.Fatalf("no se guardó la nota o la profesional: %+v", w.Rewards.Rewards[0])
	}
	// Ya usado: 409.
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, a), "", w.Owner), 409, "Ese premio ya se usó")
	// Vencido: 409 (se fuerza el vencimiento en el fake).
	past := time.Now().Add(-time.Hour)
	w.Rewards.Rewards[1].ExpiresAt = &past
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, w.Rewards.Rewards[1].ID), `{}`, w.Admin), 409, "El premio venció")
	// Profesional de otro salón / nota con salto de línea / op id con NUL: 400, sin usar.
	w.Rewards.Rewards[1].ExpiresAt = nil
	for _, body := range []string{
		`{"professional_id":"nadie"}`, `{"note":"a\nb"}`, `{"client_op_id":"a\u0000b"}`,
	} {
		r := httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, w.Rewards.Rewards[1].ID), body, w.Recep)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, r.Code, r.Body.String())
		}
	}
	if w.Rewards.Rewards[1].RedeemedAt != nil {
		t.Fatal("un 400 usó el vale")
	}
	// Programa apagado: 409 y el vale se conserva.
	w.saveProgram(t, false, 2)
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, w.Rewards.Rewards[1].ID), `{}`, w.Recep), 409, "La fidelidad no está activada")
	if w.Rewards.Rewards[1].RedeemedAt != nil || len(w.Rewards.Rewards) != 2 {
		t.Fatal("apagar el programa tocó los vales")
	}
}

func TestLoyaltyHTTP_redeem_idempotentByOp(t *testing.T) {
	w := newLoyWorld(t)
	w.saveProgram(t, true, 2)
	w.setVisits(w.liaID, 4)
	a, b := w.grant(t, ""), w.grant(t, "")
	r1 := httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, a), `{"client_op_id":"op-r"}`, w.Recep)
	r2 := httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, a), `{"client_op_id":"op-r"}`, w.Recep)
	if r1.Code != 200 || r2.Code != 200 || r1.Body.String() != r2.Body.String() {
		t.Fatalf("el repetido: %d %s / %d %s", r1.Code, r1.Body.String(), r2.Code, r2.Body.String())
	}
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, b), `{"client_op_id":"op-r"}`, w.Recep), 409, "Esa operación ya se usó en otro pedido")
}

// El permiso va ANTES del cuerpo en entregar y usar, rol por rol; quien sí puede
// recibe la validación del cuerpo.
func TestLoyaltyHTTP_grantRedeem_permissionsBeforeBody(t *testing.T) {
	w := newLoyWorld(t)
	w.saveProgram(t, true, 2)
	w.setVisits(w.liaID, 10)
	rid := w.grant(t, "")
	for _, body := range []string{`{}`, ``, `{"client_op_id":`, `[]`, `"x"`} {
		for name, tok := range map[string]string{"prof": w.Prof, "client": w.Client, "baja": w.Removed, "otro salón": w.Outsider} {
			if r := httpkit.Do(w.Env.Router, "POST", w.clientPath(w.liaID)+"/grant", body, tok); r.Code != http.StatusForbidden {
				t.Fatalf("grant %s con %q: %d, quería 403", name, body, r.Code)
			}
			if r := httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, rid), body, tok); r.Code != http.StatusForbidden {
				t.Fatalf("redeem %s con %q: %d, quería 403", name, body, r.Code)
			}
		}
	}
	if len(w.Rewards.Rewards) != 1 || w.Rewards.Rewards[0].RedeemedAt != nil {
		t.Fatal("un 403 escribió")
	}
	for _, tok := range []string{w.Owner, w.Admin, w.Recep} {
		if r := httpkit.Do(w.Env.Router, "POST", w.clientPath(w.liaID)+"/grant", `[]`, tok); r.Code != http.StatusBadRequest {
			t.Fatalf("grant con cuerpo malo: %d", r.Code)
		}
		if r := httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, rid), `{"client_op_id":`, tok); r.Code != http.StatusBadRequest {
			t.Fatalf("redeem con cuerpo roto: %d", r.Code)
		}
	}
	// Las tres sí pueden entregar y usar.
	for i, tok := range []string{w.Owner, w.Admin, w.Recep} {
		w.setVisits(w.liaID, 20)
		if r := httpkit.Do(w.Env.Router, "POST", w.clientPath(w.liaID)+"/grant", `{}`, tok); r.Code != http.StatusCreated {
			t.Fatalf("grant %d: %d %s", i, r.Code, r.Body.String())
		}
	}
	for i, tok := range []string{w.Owner, w.Admin, w.Recep} {
		if r := httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, w.Rewards.Rewards[i].ID), `{}`, tok); r.Code != http.StatusOK {
			t.Fatalf("redeem %d: %d %s", i, r.Code, r.Body.String())
		}
	}
}

// Aislamiento: el vale de un salón no se usa desde otro ni con la clienta de otro.
func TestLoyaltyHTTP_tenantIsolation(t *testing.T) {
	w := newLoyWorld(t)
	w.saveProgram(t, true, 2)
	w.setVisits(w.liaID, 2)
	rid := w.grant(t, "")
	// El dueño del OTRO salón no entra a este (403) ni con su propio salón en la ruta.
	other := "/api/v1/salons/" + w.otherSalon
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", other+"/clients/"+w.liaID+"/loyalty/rewards/"+rid+"/redeem", `{}`, w.Outsider), 404, "Ficha no encontrada")
	wantHTTP(t, httpkit.Do(w.Env.Router, "GET", other+"/clients/"+w.liaID+"/loyalty", "", w.Outsider), 404, "Ficha no encontrada")
	wantHTTP(t, httpkit.Do(w.Env.Router, "POST", w.redeemPath(w.liaID, rid), `{}`, w.Outsider), 403, "sin acceso al salón")
	// El programa del otro salón es suyo, no de este.
	g := httpkit.Do(w.Env.Router, "GET", other+"/loyalty-program", "", w.Outsider)
	var got map[string]any
	httpkit.MustJSON(t, g.Body.Bytes(), &got)
	if g.Code != 200 || got["enabled"] != false || got["visits_required"] != nil {
		t.Fatalf("programa del otro salón: %d %v", g.Code, got)
	}
	if w.Rewards.Rewards[0].RedeemedAt != nil {
		t.Fatal("el otro salón usó el vale")
	}
}

// --- D8: público ---------------------------------------------------------------------------

func (w *loyWorld) publicPayload(t *testing.T) map[string]any {
	t.Helper()
	r := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/"+w.Slug, "", "")
	if r.Code != http.StatusOK {
		t.Fatalf("público: %d %s", r.Code, r.Body.String())
	}
	var got map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &got)
	return got
}

// D8: `loyalty` es null salvo programa ACTIVADO con servicio ACTIVO; el resto del
// payload no cambia (aditivo).
func TestLoyaltyHTTP_publicPayload(t *testing.T) {
	w := newLoyWorld(t)
	keys := func(p map[string]any) string {
		ks := keysOf(p)
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	// Las claves de siempre más `loyalty`, nada quitado ni renombrado.
	p := w.publicPayload(t)
	if keys(p) != "gallery,hours,loyalty,packages,salon,services,team" {
		t.Fatalf("claves: %s", keys(p))
	}
	if v, ok := p["loyalty"]; !ok || v != nil {
		t.Fatalf("sin programa loyalty debe ser null: %v", p["loyalty"])
	}
	if len(p["services"].([]any)) != 2 || p["salon"].(map[string]any)["slug"] != w.Slug {
		t.Fatalf("el resto del payload cambió: %v", p)
	}
	// Apagado: null.
	w.saveProgram(t, false, 5)
	if v := w.publicPayload(t)["loyalty"]; v != nil {
		t.Fatalf("apagado: %v", v)
	}
	// Encendido: forma exacta, solo visits_required y reward_service_name.
	w.saveProgram(t, true, 5)
	l, _ := w.publicPayload(t)["loyalty"].(map[string]any)
	if l == nil || keys(l) != "reward_service_name,visits_required" || l["visits_required"] != float64(5) || l["reward_service_name"] != "Corte" {
		t.Fatalf("encendido: %v", l)
	}
	// Servicio de regalo desactivado: null.
	r := httpkit.Do(w.Env.Router, "PUT", "/api/v1/salons/"+w.Salon+"/services/"+w.svcID, `{"name":"Corte","duration_min":45,"price_cents":15000,"commission_pct":20,"active":false}`, w.Owner)
	if r.Code != 200 {
		t.Fatalf("desactivar: %d %s", r.Code, r.Body.String())
	}
	if v := w.publicPayload(t)["loyalty"]; v != nil {
		t.Fatalf("servicio inactivo: %v", v)
	}
	// El otro salón no muestra el programa de este.
	r = httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/fidelidad-dos", "", "")
	var other map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &other)
	if other["loyalty"] != nil {
		t.Fatalf("el otro salón: %v", other["loyalty"])
	}
}
