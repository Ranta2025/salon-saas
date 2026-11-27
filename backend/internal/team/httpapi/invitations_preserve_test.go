// F2g1: vencimiento del enlace y PRESERVE de los comportamientos existentes
// cuyo código se comparte con las invitaciones (registro, login, alta de equipo
// por email y listado de miembros).
package httpapi_test

import (
	"encoding/json"
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"
)

// S-F2g1-1/4: una invitación vence a los 7 días. Después del vencimiento el
// token responde EXACTAMENTE lo mismo que uno que no existe, deja de listarse y
// no se puede canjear.
func TestHTTP_invitation_vencida(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	created := invite(t, env, ownerTok, salonID, "lucia@mail.com", "professional")
	token := invitationToken(t, created.Link)

	if w := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+token+`"}`, ""); w.Code != http.StatusOK {
		t.Fatalf("antes de vencer: %d %s", w.Code, w.Body.String())
	}
	// Seis días después todavía sirve.
	base := time.Now()
	env.Invitations.Clock = func() time.Time { return base.Add(6 * 24 * time.Hour) }
	if w := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+token+`"}`, ""); w.Code != http.StatusOK {
		t.Fatalf("al sexto día: %d %s", w.Code, w.Body.String())
	}
	// Al octavo, no.
	env.Invitations.Clock = func() time.Time { return base.Add(8 * 24 * time.Hour) }
	expired := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+token+`"}`, "")
	bogus := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"no-existe"}`, "")
	if expired.Code != http.StatusNotFound || expired.Body.String() != bogus.Body.String() {
		t.Fatalf("vencida: %d %s (desconocida: %d %s)", expired.Code, expired.Body.String(), bogus.Code, bogus.Body.String())
	}
	if !strings.Contains(expired.Body.String(), "Invitación inválida o vencida") {
		t.Fatalf("mensaje: %s", expired.Body.String())
	}
	// Tampoco se canjea ni se lista.
	if w := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/accept",
		`{"token":"`+token+`","name":"Lucía","password":"secreta123"}`, ""); w.Code != http.StatusNotFound {
		t.Fatalf("canje de una vencida: %d %s", w.Code, w.Body.String())
	}
	w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/invitations", "", ownerTok)
	if strings.Contains(w.Body.String(), "lucia@mail.com") {
		t.Fatalf("una vencida no se lista como pendiente: %s", w.Body.String())
	}
}

// PRESERVE: el registro sigue igual después de mover la validación del email a
// domain.ValidateEmail (la comparten las cuentas y las invitaciones).
func TestPreserve_registro(t *testing.T) {
	env := httpkit.NewPOSEnv()
	w := httpkit.Do(env.Router, "POST", "/api/v1/auth/register", `{"name":"Ana","email":"Ana@Mail.com","password":"secreta123"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("registro: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" || created.Email != "ana@mail.com" {
		t.Fatalf("respuesta del registro: %s", w.Body.String())
	}
	// Email duplicado (normalizado): 409.
	if w := httpkit.Do(env.Router, "POST", "/api/v1/auth/register", `{"name":"Otra","email":"ANA@mail.com","password":"otraClave1"}`, ""); w.Code != http.StatusConflict {
		t.Fatalf("duplicado: %d %s", w.Code, w.Body.String())
	}
	// Email inválido y clave corta: 400 con el mensaje de siempre.
	w = httpkit.Do(env.Router, "POST", "/api/v1/auth/register", `{"name":"X","email":"sin-arroba","password":"secreta123"}`, "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "email inválido") {
		t.Fatalf("email inválido: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "POST", "/api/v1/auth/register", `{"name":"X","email":"x@mail.com","password":"corta"}`, "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "al menos 8 caracteres") {
		t.Fatalf("clave corta: %d %s", w.Code, w.Body.String())
	}
}

// PRESERVE: el login sigue devolviendo {"token"} y el token sirve en /me; los
// dos fallos (email o clave) responden igual.
func TestPreserve_login(t *testing.T) {
	env, ownerTok, _ := invitationEnv(t)
	w := httpkit.Do(env.Router, "GET", "/api/v1/me", "", ownerTok)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "owner@mail.com") {
		t.Fatalf("/me: %d %s", w.Code, w.Body.String())
	}
	badPass := httpkit.Do(env.Router, "POST", "/api/v1/auth/login", `{"email":"owner@mail.com","password":"otraClave1"}`, "")
	unknown := httpkit.Do(env.Router, "POST", "/api/v1/auth/login", `{"email":"nadie@mail.com","password":"otraClave1"}`, "")
	if badPass.Code != http.StatusUnauthorized || badPass.Body.String() != unknown.Body.String() {
		t.Fatalf("login fallido: %d %s vs %d %s", badPass.Code, badPass.Body.String(), unknown.Code, unknown.Body.String())
	}
}

// PRESERVE: `POST /members` sigue sumando a quien YA tiene cuenta (misma
// autorización, movida a app.manageActor) y rechazando a quien no la tiene.
func TestPreserve_members_add_y_list(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	_, profID := env.Register(t, "profe@mail.com")
	if err := env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleReception}); err != nil {
		t.Fatalf("membresía previa: %v", err)
	}
	// Alguien con cuenta y sin membresía: el alta directa lo suma.
	_, nuevoID := env.Register(t, "nuevo@mail.com")
	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members", `{"email":"nuevo@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", w.Code, w.Body.String())
	}
	var member struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Role    string `json:"role"`
		Attends bool   `json:"attends"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &member); err != nil || member.ID != nuevoID ||
		member.Role != "professional" || !member.Attends {
		t.Fatalf("miembro: %s", w.Body.String())
	}
	// Sin cuenta: 400 "La persona todavía no tiene cuenta" (el alta directa no
	// inventa cuentas; para eso está la invitación).
	w = httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members", `{"email":"nadie@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "todavía no tiene cuenta") {
		t.Fatalf("sin cuenta: %d %s", w.Code, w.Body.String())
	}
	// Listado: array con los miembros y sus campos de siempre.
	w = httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/members", "", ownerTok)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"service_ids":[]`) ||
		!strings.Contains(w.Body.String(), `"attends":`) || !strings.Contains(w.Body.String(), "nuevo@mail.com") {
		t.Fatalf("listado: %d %s", w.Code, w.Body.String())
	}
}
