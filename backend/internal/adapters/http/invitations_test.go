// F2g1 por HTTP: invitaciones de equipo (S-F2g1-3/4).
//
// Lo que estos tests NO pueden dejar pasar: el token aparece una sola vez (en la
// respuesta del alta), nunca en el listado ni en el lookup ni en el log; y los
// cuatro tokens que no sirven responden exactamente lo mismo.
package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// invitationEnv deja un salón con su dueño y devuelve el token del dueño y el id.
func invitationEnv(t *testing.T) (*httpkit.Env, string, string) {
	t.Helper()
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "navajas")
	return env, ownerTok, salonID
}

type createdInvitation struct {
	Invitation struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expires_at"`
	} `json:"invitation"`
	Link      string `json:"link"`
	EmailSent bool   `json:"email_sent"`
}

func invite(t *testing.T, e *httpkit.Env, token, salonID, email, role string) createdInvitation {
	t.Helper()
	w := httpkit.Do(e.Router, "POST", "/api/v1/salons/"+salonID+"/invitations",
		`{"email":"`+email+`","role":"`+role+`"}`, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("invitar %s: %d %s", email, w.Code, w.Body.String())
	}
	var out createdInvitation
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("respuesta del alta: %s", w.Body.String())
	}
	if out.Link == "" || !strings.Contains(out.Link, "#") {
		t.Fatalf("el enlace tiene que llevar el token en el fragmento: %q", out.Link)
	}
	return out
}

// tokenOf saca el token del fragmento del enlace (el servidor nunca lo devuelve
// por otro lado: se muestra una sola vez).
func invitationToken(t *testing.T, link string) string {
	t.Helper()
	i := strings.Index(link, "#")
	if i < 0 || i == len(link)-1 {
		t.Fatalf("enlace sin token en el fragmento: %q", link)
	}
	return link[i+1:]
}

// El flujo completo: invitar -> mail -> lookup -> aceptar -> sesión -> miembro.
func TestHTTP_invitation_flujo_completo(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	created := invite(t, env, ownerTok, salonID, "  Lucia@Mail.com ", "professional")
	if created.Invitation.Email != "lucia@mail.com" || created.Invitation.Role != "professional" {
		t.Fatalf("invitación: %+v", created.Invitation)
	}
	if created.Invitation.ExpiresAt == "" {
		t.Fatal("la respuesta del alta lleva el vencimiento")
	}
	if !created.EmailSent {
		t.Fatal("con el mailer del entorno el correo sale")
	}
	token := invitationToken(t, created.Link)
	if !strings.HasPrefix(created.Link, "http://localhost:8081/invitacion#") {
		t.Fatalf("APP_URL base del enlace: %q", created.Link)
	}

	// El listado muestra la pendiente y NUNCA el token ni su hash.
	w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/invitations", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, token) || strings.Contains(body, domain.HashInvitationToken(token)) {
		t.Fatalf("el listado no puede llevar el token ni el hash: %s", body)
	}
	if !strings.Contains(body, "lucia@mail.com") {
		t.Fatalf("el listado lleva el email: %s", body)
	}

	// Lookup público, sin sesión.
	w = httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+token+`"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	var lookup struct {
		SalonName  string `json:"salon_name"`
		Role       string `json:"role"`
		RoleLabel  string `json:"role_label"`
		EmailHint  string `json:"email_hint"`
		ExpiresAt  string `json:"expires_at"`
		HasAccount bool   `json:"has_account"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lookup); err != nil {
		t.Fatalf("lookup: %s", w.Body.String())
	}
	if lookup.SalonName != "Salón navajas" || lookup.Role != "professional" || lookup.RoleLabel != "profesional" {
		t.Fatalf("lookup: %+v", lookup)
	}
	if lookup.EmailHint != "l***@mail.com" {
		t.Fatalf("email enmascarado: %q", lookup.EmailHint)
	}
	if lookup.HasAccount {
		t.Fatal("todavía no tiene cuenta")
	}

	// Aceptar como persona nueva: la respuesta tiene la forma del login.
	w = httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/accept",
		`{"token":"`+token+`","name":"Lucía","password":"secreta123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("aceptar: %d %s", w.Code, w.Body.String())
	}
	var session map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatalf("sesión: %s", w.Body.String())
	}
	jwt, _ := session["token"].(string)
	if jwt == "" {
		t.Fatalf("la sesión es la del login (token): %s", w.Body.String())
	}
	if len(session) != 1 {
		t.Fatalf("la sesión tiene la MISMA forma que el login: %s", w.Body.String())
	}

	// La sesión sirve en /me y el salón aparece.
	w = httpkit.Do(env.Router, "GET", "/api/v1/me", "", jwt)
	if w.Code != http.StatusOK {
		t.Fatalf("/me con la sesión nueva: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Salón navajas") || !strings.Contains(w.Body.String(), "professional") {
		t.Fatalf("/me después de aceptar: %s", w.Body.String())
	}

	// Y el miembro aparece en el equipo con su rol.
	w = httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/members", "", ownerTok)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "lucia@mail.com") ||
		!strings.Contains(w.Body.String(), `"role":"professional"`) {
		t.Fatalf("miembros: %d %s", w.Code, w.Body.String())
	}

	// El mismo token otra vez: el 404 genérico, idéntico al de un token que no
	// existe (nada distingue "ya se usó" de "no existe").
	second := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/accept",
		`{"token":"`+token+`","name":"Otra","password":"secreta123"}`, "")
	bogus := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/accept",
		`{"token":"token-que-no-existe","name":"Otra","password":"secreta123"}`, "")
	if second.Code != http.StatusNotFound || second.Body.String() != bogus.Body.String() {
		t.Fatalf("segundo uso: %d %s vs %d %s", second.Code, second.Body.String(), bogus.Code, bogus.Body.String())
	}
	if !strings.Contains(second.Body.String(), "Invitación inválida o vencida") {
		t.Fatalf("mensaje genérico: %s", second.Body.String())
	}
}

// S-F2g1-4: el lookup público no revela si un email tiene cuenta salvo por
// `has_account` y solo con un token válido.
func TestHTTP_invitation_lookup_sin_oraculo(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	// Una cuenta que existe y no está en este salón.
	if _, _ = env.Register(t, "con-cuenta@mail.com"); false {
		t.Fatal("no debería ejecutarse")
	}
	withAccount := invite(t, env, ownerTok, salonID, "con-cuenta@mail.com", "professional")
	withoutAccount := invite(t, env, ownerTok, salonID, "sin-cuenta@mail.com", "professional")

	// Con token válido: has_account dice la verdad (y solo eso).
	var lookup struct {
		HasAccount bool `json:"has_account"`
	}
	w := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+invitationToken(t, withAccount.Link)+`"}`, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &lookup) != nil || !lookup.HasAccount {
		t.Fatalf("con cuenta: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+invitationToken(t, withoutAccount.Link)+`"}`, "")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &lookup) != nil || lookup.HasAccount {
		t.Fatalf("sin cuenta: %d %s", w.Code, w.Body.String())
	}

	// Revocada vs desconocida vs usada: misma respuesta, byte por byte.
	revoked := invite(t, env, ownerTok, salonID, "revocada@mail.com", "professional")
	if w := httpkit.Do(env.Router, "DELETE", "/api/v1/salons/"+salonID+"/invitations/"+revoked.Invitation.ID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("revocar: %d %s", w.Code, w.Body.String())
	}
	used := invite(t, env, ownerTok, salonID, "usada@mail.com", "professional")
	if w := httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/accept",
		`{"token":"`+invitationToken(t, used.Link)+`","name":"Usada","password":"secreta123"}`, ""); w.Code != http.StatusOK {
		t.Fatalf("aceptar: %d %s", w.Code, w.Body.String())
	}
	responses := []*httptest.ResponseRecorder{
		httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+invitationToken(t, revoked.Link)+`"}`, ""),
		httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"`+invitationToken(t, used.Link)+`"}`, ""),
		httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":"nada"}`, ""),
		httpkit.Do(env.Router, "POST", "/api/v1/public/invitations/lookup", `{"token":""}`, ""),
	}
	for i, w := range responses {
		if w.Code != http.StatusNotFound || w.Body.String() != responses[0].Body.String() {
			t.Fatalf("respuesta %d: %d %s (la primera: %s)", i, w.Code, w.Body.String(), responses[0].Body.String())
		}
	}
}

// S-F2g1-4: una cuenta que ya existe se une por la ruta autenticada, y solo si
// su email es el de la invitación.
func TestHTTP_invitation_accept_cuenta_existente(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	invited, _ := env.Register(t, "invitada@mail.com")
	inv, _ := env.Register(t, "otra@mail.com")
	created := invite(t, env, ownerTok, salonID, "invitada@mail.com", "reception")
	token := invitationToken(t, created.Link)

	// Otra cuenta con el token: 403 con el texto del contrato.
	w := httpkit.Do(env.Router, "POST", "/api/v1/invitations/accept", `{"token":"`+token+"\"}", inv)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Esta invitación es para otra cuenta") {
		t.Fatalf("otra cuenta: %d %s", w.Code, w.Body.String())
	}
	// Sin sesión: 401 (la ruta es autenticada).
	if w := httpkit.Do(env.Router, "POST", "/api/v1/invitations/accept", `{"token":"`+token+"\"}", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin sesión: %d %s", w.Code, w.Body.String())
	}
	// La persona invitada acepta y queda en el equipo con su rol.
	w = httpkit.Do(env.Router, "POST", "/api/v1/invitations/accept", `{"token":"`+token+"\"}", invited)
	if w.Code != http.StatusOK {
		t.Fatalf("aceptar existente: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Salón navajas") || !strings.Contains(w.Body.String(), "reception") {
		t.Fatalf("respuesta del canje autenticado: %s", w.Body.String())
	}
	w = httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/members", "", ownerTok)
	if !strings.Contains(w.Body.String(), "invitada@mail.com") || !strings.Contains(w.Body.String(), `"role":"reception"`) {
		t.Fatalf("miembros: %s", w.Body.String())
	}
	// Y un token inválido por la ruta autenticada: el mismo 404 genérico.
	if w := httpkit.Do(env.Router, "POST", "/api/v1/invitations/accept", `{"token":"nada"}`, invited); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "Invitación inválida o vencida") {
		t.Fatalf("token inválido autenticado: %d %s", w.Code, w.Body.String())
	}
}

// S-F2g1-3: errores exactos del alta y autorización.
func TestHTTP_invitation_errores(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	// Un admin (no owner) y un profesional.
	adminTok, adminID := env.Register(t, "admin@mail.com")
	if err := env.Mems.Save(domain.Membership{UserID: adminID, SalonID: salonID, Role: domain.RoleAdmin}); err != nil {
		t.Fatalf("admin: %v", err)
	}
	profTok, profID := env.Register(t, "profe@mail.com")
	if err := env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional, Attends: true}); err != nil {
		t.Fatalf("profesional: %v", err)
	}

	cases := []struct {
		name   string
		token  string
		salon  string
		body   string
		status int
		text   string
	}{
		{"admin invita admin", adminTok, salonID, `{"email":"nuevo@mail.com","role":"admin"}`, http.StatusBadRequest, "No podés invitar con ese rol"},
		{"nadie invita clientes", ownerTok, salonID, `{"email":"cliente@mail.com","role":"client"}`, http.StatusBadRequest, "No podés invitar con ese rol"},
		{"nadie invita dueños", ownerTok, salonID, `{"email":"dueno@mail.com","role":"owner"}`, http.StatusBadRequest, "No podés invitar con ese rol"},
		{"rol inexistente", ownerTok, salonID, `{"email":"x@mail.com","role":"jefe"}`, http.StatusBadRequest, "No podés invitar con ese rol"},
		{"email inválido", ownerTok, salonID, `{"email":"sin-arroba","role":"professional"}`, http.StatusBadRequest, "Email inválido"},
		{"profesional no invita", profTok, salonID, `{"email":"x@mail.com","role":"professional"}`, http.StatusForbidden, "sin permiso"},
		{"otro salón", ownerTok, "s-ajeno", `{"email":"x@mail.com","role":"professional"}`, http.StatusForbidden, "sin acceso al salón"},
	}
	for _, c := range cases {
		w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+c.salon+"/invitations", c.body, c.token)
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.text) {
			t.Fatalf("%s: %d %s (esperaba %d con %q)", c.name, w.Code, w.Body.String(), c.status, c.text)
		}
	}

	// Ya es parte del equipo: 409 con el texto del contrato.
	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/invitations",
		`{"email":"admin@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Esa persona ya es parte del equipo") {
		t.Fatalf("ya es miembro: %d %s", w.Code, w.Body.String())
	}
	// Sin sesión: 401 en todas las rutas autenticadas (lista, alta, revocación y
	// canje de una cuenta existente).
	sinSesion := []struct{ method, path string }{
		{"GET", "/api/v1/salons/" + salonID + "/invitations"},
		{"POST", "/api/v1/salons/" + salonID + "/invitations"},
		{"DELETE", "/api/v1/salons/" + salonID + "/invitations/i-1"},
		{"POST", "/api/v1/invitations/accept"},
	}
	for _, c := range sinSesion {
		if w := httpkit.Do(env.Router, c.method, c.path, `{}`, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("sin sesión %s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// S-F2g1-4: las rutas públicas están bajo el rate limit por IP, como /auth.
// Se prueba con una ráfaga: la tercera llamada de la misma IP da 429 y otra IP
// sigue pasando.
func TestHTTP_invitation_publicas_rate_limit(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	created := invite(t, env, ownerTok, salonID, "lucia@mail.com", "professional")
	token := invitationToken(t, created.Link)

	d := env.Deps
	d.PublicInvitationLimit = httpapi.RateLimitFor(2, time.Minute, "POST", "/api/v1/public/invitations/")
	r := gin.New()
	httpapi.RegisterRoutes(r, d)

	post := func(path, ip, body string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	lookup := `{"token":"` + token + `"}`
	accept := `{"token":"nada","name":"X","password":"secreta123"}`

	if c := post("/api/v1/public/invitations/lookup", "203.0.113.5", lookup); c != http.StatusOK {
		t.Fatalf("primer lookup: %d", c)
	}
	if c := post("/api/v1/public/invitations/lookup", "203.0.113.5", lookup); c != http.StatusOK {
		t.Fatalf("segundo lookup: %d", c)
	}
	if c := post("/api/v1/public/invitations/lookup", "203.0.113.5", lookup); c != http.StatusTooManyRequests {
		t.Fatalf("la ráfaga tiene que dar 429, dio %d", c)
	}
	// Otra IP no está limitada, y el canje público comparte el cubo por IP.
	if c := post("/api/v1/public/invitations/lookup", "198.51.100.9", lookup); c != http.StatusOK {
		t.Fatalf("otra IP: %d", c)
	}
	if c := post("/api/v1/public/invitations/accept", "198.51.100.9", accept); c == http.StatusTooManyRequests {
		t.Fatalf("el segundo intento de otra IP no debe limitarse: %d", c)
	}
	if c := post("/api/v1/public/invitations/accept", "198.51.100.9", accept); c != http.StatusTooManyRequests {
		t.Fatalf("el canje también está limitado: %d", c)
	}
}
