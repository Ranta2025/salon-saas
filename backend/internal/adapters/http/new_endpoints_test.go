package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"salon-saas/internal/domain"
)

// errFakeInfra simula un fallo de infraestructura con detalle de Postgres: no
// puede llegar al cliente (B-14).
var errFakeInfra = errors.New(`pq: password authentication failed for user "salon"`)

// B-01/B-07/B-08 contratos de /me, /salons/:id/staff y /public/salons/:slug.
// El frontend codifica contra estas formas exactas.

func TestHTTP_me(t *testing.T) {
	env := newPOSEnv()
	tok, uid := env.register(t, "ana@mail.com")
	salonID := env.salon(t, tok, "navajas-oro")

	w := doReq(env.r, "GET", "/api/v1/me", "", tok)
	if w.Code != http.StatusOK {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		User struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"user"`
		Salons []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"salons"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.User.ID != uid || out.User.Email != "ana@mail.com" || out.User.Name == "" {
		t.Fatalf("usuario: %s", w.Body.String())
	}
	if len(out.Salons) != 1 || out.Salons[0].ID != salonID || out.Salons[0].Slug != "navajas-oro" ||
		out.Salons[0].Name != "Salón navajas-oro" || out.Salons[0].Role != string(domain.RoleOwner) {
		t.Fatalf("salones: %s", w.Body.String())
	}
	// Sin token: 401.
	if w := doReq(env.r, "GET", "/api/v1/me", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
	// Usuario sin salones: array vacío, nunca null.
	otroTok, _ := env.register(t, "solo@mail.com")
	w = doReq(env.r, "GET", "/api/v1/me", "", otroTok)
	if w.Code != http.StatusOK {
		t.Fatalf("me sin salones: %d %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, `"salons":[]`) {
		t.Fatalf("salones debe ser [] y no null: %s", got)
	}
}

func TestHTTP_staff(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo")
	profTok, profID := env.register(t, "prof@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	// Cualquier miembro lee el equipo (el select de profesional del frontend).
	w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/staff", "", profTok)
	if w.Code != http.StatusOK {
		t.Fatalf("staff: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Staff []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"staff"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if len(out.Staff) != 2 {
		t.Fatalf("esperaba owner + profesional, dio %s", w.Body.String())
	}
	roles := map[string]string{}
	for _, s := range out.Staff {
		if s.ID == "" || s.Name == "" {
			t.Fatalf("miembro incompleto: %s", w.Body.String())
		}
		roles[s.ID] = s.Role
	}
	if roles[profID] != string(domain.RoleProfessional) {
		t.Fatalf("rol del profesional: %s", w.Body.String())
	}
	// No-miembro: 403. Sin token: 401.
	strangerTok, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/staff", "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/staff", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

func TestHTTP_public_salon(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "vidriera")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	// Un servicio inactivo no sale en la vidriera.
	_ = doReq(env.r, "PUT", "/api/v1/salons/"+salonID+"/services/"+svcID, `{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20,"active":false}`, ownerTok)

	// SIN auth: es la landing pública.
	w := doReq(env.r, "GET", "/api/v1/public/salons/vidriera", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("public: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Salon struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"salon"`
		Services []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DurationMin int    `json:"duration_min"`
			PriceCents  int    `json:"price_cents"`
		} `json:"services"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Salon.ID != salonID || out.Salon.Slug != "vidriera" || out.Salon.Name != "Salón vidriera" {
		t.Fatalf("salón: %s", w.Body.String())
	}
	if len(out.Services) != 0 {
		t.Fatalf("solo servicios activos, dio %s", w.Body.String())
	}
	// Con un servicio activo, sale con la forma del contrato.
	_ = doReq(env.r, "PUT", "/api/v1/salons/"+salonID+"/services/"+svcID, `{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20,"active":true}`, ownerTok)
	w = doReq(env.r, "GET", "/api/v1/public/salons/vidriera", "", "")
	mustJSON(t, w.Body.Bytes(), &out)
	if len(out.Services) != 1 || out.Services[0].Name != "Corte" || out.Services[0].DurationMin != 45 || out.Services[0].PriceCents != 150000 {
		t.Fatalf("servicios: %s", w.Body.String())
	}
	// Slug inexistente: 404.
	if w := doReq(env.r, "GET", "/api/v1/public/salons/no-existe", "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("slug inexistente debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
}
