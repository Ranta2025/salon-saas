package httpapi_test

import (
	"encoding/json"
	"net/http"
	httpkit "salon-saas/internal/testutil/httpkit"
	"testing"
)

// Flujo: register -> login -> crear salón ->
// slug duplicado 409 -> sin token 401.
func TestHTTP_flujo_auth_salon(t *testing.T) {
	r := httpkit.NewPOSEnv().Router

	w := httpkit.Do(r, "POST", "/api/v1/auth/register", `{"name":"Ana","email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(r, "POST", "/api/v1/auth/register", `{"name":"Otra","email":"ana@mail.com","password":"otraClave1"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicado debe dar 409, dio %d", w.Code)
	}
	w = httpkit.Do(r, "POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("login sin token: %s", w.Body.String())
	}
	w = httpkit.Do(r, "POST", "/api/v1/salons", `{"name":"Navajas de Oro","slug":"navajas-oro"}`, lr.Token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create salon: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(r, "POST", "/api/v1/salons", `{"name":"Copia","slug":"navajas-oro"}`, lr.Token)
	if w.Code != http.StatusConflict {
		t.Fatalf("slug duplicado debe dar 409, dio %d", w.Code)
	}
	w = httpkit.Do(r, "POST", "/api/v1/salons", `{"name":"X","slug":"x"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

// Flujo: login como receptora -> crear reserva 201 -> solape 409 ->
// adyacente 201 -> listado del día trae 2 -> sin token 401.
func TestHTTP_bookings(t *testing.T) {
	env := httpkit.NewPOSEnv()
	r := env.Router
	httpkit.Do(r, "POST", "/api/v1/auth/register", `{"name":"Recep","email":"recep@mail.com","password":"secreta123"}`, "")
	lr := httpkit.Do(r, "POST", "/api/v1/auth/login", `{"email":"recep@mail.com","password":"secreta123"}`, "")
	var lt struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &lt); err != nil || lt.Token == "" {
		t.Fatalf("login: %s", lr.Body.String())
	}
	ws := httpkit.Do(r, "POST", "/api/v1/salons", `{"name":"Salón","slug":"salon-http"}`, lt.Token)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ws.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("salón: %s", ws.Body.String())
	}
	// B-12: el profesional del turno tiene que atender en el salón.
	httpkit.SeedProfessional(env, created.ID, "prof1")
	p := "/api/v1/salons/" + created.ID + "/bookings"
	b10 := `{"professional_id":"prof1","client_name":"Lía","starts_at":"2026-12-03T10:00:00Z","ends_at":"2026-12-03T11:00:00Z"}`
	if w := httpkit.Do(r, "POST", p, b10, lt.Token); w.Code != http.StatusCreated {
		t.Fatalf("crear: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(r, "POST", p, b10, lt.Token); w.Code != http.StatusConflict {
		t.Fatalf("solape debe dar 409, dio %d %s", w.Code, w.Body.String())
	}
	b11 := `{"professional_id":"prof1","client_name":"Ana","starts_at":"2026-12-03T11:00:00Z","ends_at":"2026-12-03T12:00:00Z"}`
	if w := httpkit.Do(r, "POST", p, b11, lt.Token); w.Code != http.StatusCreated {
		t.Fatalf("adyacente: %d %s", w.Code, w.Body.String())
	}
	w := httpkit.Do(r, "GET", p+"?from=2026-12-03T00:00:00Z&to=2026-12-04T00:00:00Z", "", lt.Token)
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Bookings []map[string]interface{} `json:"bookings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Bookings) != 2 {
		t.Fatalf("esperaba 2 reservas, dio %s", w.Body.String())
	}
	if w := httpkit.Do(r, "GET", p, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}
