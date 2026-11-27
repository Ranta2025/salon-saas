// Pruebas HTTP de F15: recuperar la contraseña (D2/D3), verificar el email (D7),
// la compuerta de revocación de sesiones en `/me` y en `/ws` (D4), y el contrato
// anti-enumeración (202 idéntico, trabajo después de responder).
//
// Router REAL (`RegisterRoutes`), `AuthMiddleware` real, hub de tiempo real real y
// JWT reales: lo que se prueba es la API, no el cableado.
package httpapi_test

import (
	"context"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/auth"
	authhttpapi "salon-saas/internal/auth/httpapi"
	"salon-saas/internal/auth/security"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// queuedRunner difiere los trabajos: prueba que TODO el trabajo del pedido corre
// después de responder (regla anti-enumeración de D2).
type queuedRunner struct {
	mu   sync.Mutex
	jobs []func(ctx context.Context)
}

func (q *queuedRunner) Run(job func(ctx context.Context)) {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	q.mu.Unlock()
}

func (q *queuedRunner) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

func (q *queuedRunner) Flush() {
	q.mu.Lock()
	jobs := q.jobs
	q.jobs = nil
	q.mu.Unlock()
	for _, j := range jobs {
		j(context.Background())
	}
}

type recHTTP struct {
	r      *gin.Engine
	store  *httpkit.Recovery
	box    *httpkit.Inbox
	tokens security.JWTIssuer
	run    *queuedRunner
}

// newRecHTTP arma el router completo (el de httpkit.NewPOSEnv) con los casos de uso de
// F15 sobre los fakes, la compuerta cableada en el middleware y en el hub.
func newRecHTTP(t *testing.T) *recHTTP {
	t.Helper()
	gin.SetMode(gin.TestMode)
	env := httpkit.NewPOSEnv()
	store := httpkit.NewRecovery()
	box := &httpkit.Inbox{}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	hasher := security.BcryptHasher{Cost: 4}
	run := &queuedRunner{}
	recoverySvc := auth.AccountRecoveryService{
		Users: store, Resets: store, Hasher: hasher, Mailer: box,
		AppURL: "https://app.test", Run: run.Run,
	}
	verifSvc := auth.EmailVerificationService{
		Users: store, Verifs: store, Mailer: box, AppURL: "https://app.test", Run: run.Run,
	}
	d := env.Deps
	d.Auth = authhttpapi.AuthHandler{
		Auth:         auth.AuthService{Users: store, Hasher: hasher, Tokens: tokens},
		Verification: verifSvc,
	}
	d.Recovery = authhttpapi.AccountRecoveryHandler{Recovery: recoverySvc, Verif: verifSvc}
	d.Me = authhttpapi.MeHandler{Me: auth.MeService{Users: store, Salons: env.Salons, Members: env.Mems}}
	d.WS = realtime.NewHub(tokens, env.Mems, realtime.HubSessionGate(store)).ServeWS
	d.TokenIssuer = tokens
	d.SessionGate = store
	r := gin.New()
	httpapi.RegisterRoutes(r, d)
	return &recHTTP{r: r, store: store, box: box, tokens: tokens, run: run}
}

// register crea una cuenta por la ruta real y devuelve su id.
func (h *recHTTP) register(t *testing.T, email string) string {
	t.Helper()
	w := httpkit.Do(h.r, "POST", "/api/v1/auth/register",
		`{"name":"Ana","email":"`+email+`","password":"secreta123"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", email, w.Code, w.Body.String())
	}
	var out struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("register sin id: %s", w.Body.String())
	}
	return out.ID
}

// resetLink devuelve el token del último correo de restablecer.
func (h *recHTTP) resetLink(t *testing.T) string {
	t.Helper()
	text := h.box.Last().Text
	idx := strings.Index(text, "https://app.test/restablecer#")
	if idx < 0 {
		t.Fatalf("el correo no trae el enlace de restablecer:\n%s", text)
	}
	return httpkit.TokenFromLink(t, text[idx:])
}

// verifyLink devuelve el token del último correo de verificación.
func (h *recHTTP) verifyLink(t *testing.T) string {
	t.Helper()
	text := h.box.Last().Text
	idx := strings.Index(text, "https://app.test/verificar-email#")
	if idx < 0 {
		t.Fatalf("el correo no trae el enlace de verificación:\n%s", text)
	}
	return httpkit.TokenFromLink(t, text[idx:])
}

// D2: el mismo 202 con el mismo cuerpo para un email conocido y uno desconocido,
// y TODO el trabajo corre después de responder.
func TestF15HTTP_Forgot_sameResponseAndWorkAfterResponding(t *testing.T) {
	h := newRecHTTP(t)
	h.store.SeedUser("u-ana", "Ana", "ana@mail.com", "h", true)

	known := httpkit.Do(h.r, "POST", "/api/v1/auth/forgot", `{"email":"ana@mail.com"}`, "")
	unknown := httpkit.Do(h.r, "POST", "/api/v1/auth/forgot", `{"email":"nadie@mail.com"}`, "")
	for name, w := range map[string]int{"conocido": known.Code, "desconocido": unknown.Code} {
		if w != http.StatusAccepted {
			t.Fatalf("%s: status %d", name, w)
		}
	}
	if known.Body.String() != `{"ok":true}` || unknown.Body.String() != `{"ok":true}` {
		t.Fatalf("cuerpos: %q / %q", known.Body.String(), unknown.Body.String())
	}
	if known.Body.String() != unknown.Body.String() {
		t.Fatal("el cuerpo tiene que ser idéntico para que no se pueda enumerar")
	}
	if known.Header().Get("Content-Type") != unknown.Header().Get("Content-Type") {
		t.Fatal("el Content-Type tiene que ser idéntico")
	}
	// Nada se hizo todavía: el trabajo está encolado.
	if h.store.ResetCount() != 0 || h.box.Count() != 0 || h.run.Pending() != 2 {
		t.Fatalf("antes de correr el trabajo: resets=%d mails=%d pending=%d",
			h.store.ResetCount(), h.box.Count(), h.run.Pending())
	}
	h.run.Flush()
	// Solo la cuenta que existe emite el token y manda el correo.
	if h.store.ResetCount() != 1 || h.box.Count() != 1 {
		t.Fatalf("después: resets=%d mails=%d", h.store.ResetCount(), h.box.Count())
	}
	if h.box.Last().To != "ana@mail.com" || h.box.Last().Subject != "Restablece tu contrasena" {
		t.Fatalf("correo: %+v", h.box.Last())
	}
	if tok := h.resetLink(t); len(tok) != 43 {
		t.Fatalf("el enlace lleva un token de 32 bytes en base64url: %q", tok)
	}
}

// D2: la forma del email sí es un 400 (y no encola nada); los tipos equivocados y
// el NUL también son 400 sin 500.
func TestF15HTTP_Forgot_badInput(t *testing.T) {
	h := newRecHTTP(t)
	for _, body := range []string{
		`{"email":""}`, `{"email":"sin-arroba"}`, `{"email":"a b@x.com"}`,
	} {
		w := httpkit.Do(h.r, "POST", "/api/v1/auth/forgot", body, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"email":123}`, `{"email":null}`, `{`, `[]`, `{"email":"ana\u0000@mail.com"}`} {
		w := httpkit.Do(h.r, "POST", "/api/v1/auth/forgot", body, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if h.run.Pending() != 0 || h.store.ResetCount() != 0 {
		t.Fatal("un email inválido no puede encolar trabajo ni emitir token")
	}
}

// D3: el canje completo. Contraseña inválida NO consume el token; el token usado,
// vencido o desconocido da el mismo 400; el éxito es 204 sin sesión.
func TestF15HTTP_Reset(t *testing.T) {
	h := newRecHTTP(t)
	userID := h.store.SeedUser("u-ana", "Ana", "ana@mail.com", "h", true).ID
	h.store.SeedUser("u-bea", "Bea", "bea@mail.com", "h", true)
	if w := httpkit.Do(h.r, "POST", "/api/v1/auth/forgot", `{"email":"ana@mail.com"}`, ""); w.Code != 202 {
		t.Fatalf("forgot: %d", w.Code)
	}
	h.run.Flush()
	tok := h.resetLink(t)

	// Errores del canje: texto uniforme del contrato.
	for _, body := range []string{
		`{"token":"` + tok + `","password":"corta"}`,
		`{"token":"` + tok + `","password":"` + strings.Repeat("x", 73) + `"}`,
	} {
		w := httpkit.Do(h.r, "POST", "/api/v1/auth/reset", body, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("contraseña inválida: %d %s", w.Code, w.Body.String())
		}
	}
	// El token NO se gastó con esas dos: sigue sirviendo.
	for _, body := range []string{
		`{"token":"","password":"nuevaClave1"}`,
		`{"token":"corto","password":"nuevaClave1"}`,
		`{"token":"` + strings.Repeat("A", 43) + `","password":"nuevaClave1"}`,
	} {
		w := httpkit.Do(h.r, "POST", "/api/v1/auth/reset", body, "")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Enlace invalido o vencido") {
			t.Fatalf("token inválido: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"token":123,"password":"nuevaClave1"}`, `{"token":"` + tok + `","password":123}`, `{"token":"` + tok + `","password":"nueva\u0000Clave1"}`} {
		w := httpkit.Do(h.r, "POST", "/api/v1/auth/reset", body, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("cuerpo inválido: %d %s", w.Code, w.Body.String())
		}
	}
	// Éxito: 204 y NINGÚN cuerpo (no se emite sesión).
	w := httpkit.Do(h.r, "POST", "/api/v1/auth/reset", `{"token":"`+tok+`","password":"nuevaClave1"}`, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("el 204 no lleva cuerpo (no hay token de sesión): %q", w.Body.String())
	}
	if _, err := h.store.FindByID(userID); err != nil {
		t.Fatal(err)
	}
	// El token ya se usó: 400 con el texto uniforme y la clave no cambia.
	if w := httpkit.Do(h.r, "POST", "/api/v1/auth/reset", `{"token":"`+tok+`","password":"otraClave1"}`, ""); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "Enlace invalido o vencido") {
		t.Fatalf("reuso: %d %s", w.Code, w.Body.String())
	}
}

// D4/D7: la sesión emitida ANTES del reinicio da 401 en `/me` y en `/ws`; una
// emitida después entra. Un token viejo (sin `ia`) vale mientras la contraseña
// nunca cambió. Sin contraseña cambiada el token sigue valiendo.
func TestF15HTTP_SessionGateOnMeAndWebsocket(t *testing.T) {
	h := newRecHTTP(t)
	h.store.SeedUser("u-ana", "Ana", "ana@mail.com", "h", true)
	oldToken, err := h.tokens.Issue("u-ana")
	if err != nil {
		t.Fatal(err)
	}
	// Sin cambio de contraseña: la sesión vale en los dos lados.
	if w := httpkit.Do(h.r, "GET", "/api/v1/me", "", oldToken); w.Code != http.StatusOK {
		t.Fatalf("/me antes del cambio: %d %s", w.Code, w.Body.String())
	}
	// El WS sin membresías da 403 (el token pasó la compuerta y llegó al hub).
	if w := httpkit.Do(h.r, "GET", "/api/v1/ws?token="+oldToken, "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("/ws sin salones: %d %s", w.Code, w.Body.String())
	}

	// Un reinicio real por la API: el token anterior muere.
	if w := httpkit.Do(h.r, "POST", "/api/v1/auth/forgot", `{"email":"ana@mail.com"}`, ""); w.Code != 202 {
		t.Fatalf("forgot: %d", w.Code)
	}
	h.run.Flush()
	tok := h.resetLink(t)
	if w := httpkit.Do(h.r, "POST", "/api/v1/auth/reset", `{"token":"`+tok+`","password":"nuevaClave1"}`, ""); w.Code != 204 {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/me", "/api/v1/ws?token=" + oldToken} {
		w := httpkit.Do(h.r, "GET", path, "", oldToken)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "token") {
			t.Fatalf("%s con el token anterior: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Una sesión nueva (emitida después del cambio) entra.
	time.Sleep(2 * time.Millisecond)
	fresh, err := h.tokens.Issue("u-ana")
	if err != nil {
		t.Fatal(err)
	}
	w := httpkit.Do(h.r, "GET", "/api/v1/me", "", fresh)
	if w.Code != http.StatusOK {
		t.Fatalf("/me con la sesión nueva: %d %s", w.Code, w.Body.String())
	}
	// El reinicio verifica el email (quien lee el correo demostró que es dueño) y
	// GET /me lo informa.
	var me struct {
		User struct {
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || me.User.Email != "ana@mail.com" || !me.User.EmailVerified {
		t.Fatalf("/me: %s", w.Body.String())
	}
}

// D4: si la compuerta falla, el pedido autenticado FALLA CERRADO (401).
func TestF15HTTP_gateFailureFailsClosed(t *testing.T) {
	h := newRecHTTP(t)
	tok, err := h.tokens.Issue("u-fantasma") // usuario que no existe: la consulta da error
	if err != nil {
		t.Fatal(err)
	}
	if w := httpkit.Do(h.r, "GET", "/api/v1/me", "", tok); w.Code != http.StatusUnauthorized {
		t.Fatalf("usuario inexistente: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(h.r, "GET", "/api/v1/ws?token="+tok, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("/ws usuario inexistente: %d %s", w.Code, w.Body.String())
	}
}

// D7: el registro no cambia de respuesta, queda SIN verificar, manda el correo de
// verificación (async) y GET /me lo informa. Verificar es 204 e idempotente.
func TestF15HTTP_VerifyEmail(t *testing.T) {
	h := newRecHTTP(t)
	userID := h.register(t, "ana@mail.com")
	if h.run.Pending() != 1 || h.box.Count() != 0 {
		t.Fatalf("el correo de verificación corre DESPUÉS: pending=%d mails=%d", h.run.Pending(), h.box.Count())
	}
	h.run.Flush()
	if h.box.Count() != 1 || h.box.Last().Subject != "Verifica tu email" || h.box.Last().To != "ana@mail.com" {
		t.Fatalf("correo de verificación: %+v", h.box.Last())
	}
	tok := h.verifyLink(t)
	// La sesión de la cuenta recién creada (sin verificar) funciona igual: D9.
	session, _ := h.tokens.Issue(userID)
	w := httpkit.Do(h.r, "GET", "/api/v1/me", "", session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"email_verified":false`) {
		t.Fatalf("/me sin verificar: %d %s", w.Code, w.Body.String())
	}
	// Canje: 204.
	if w := httpkit.Do(h.r, "POST", "/api/v1/auth/verify-email", `{"token":"`+tok+`"}`, ""); w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(h.r, "GET", "/api/v1/me", "", session); !strings.Contains(w.Body.String(), `"email_verified":true`) {
		t.Fatalf("/me verificado: %s", w.Body.String())
	}
	// Reuso: idempotente (la cuenta ya está verificada).
	if w := httpkit.Do(h.r, "POST", "/api/v1/auth/verify-email", `{"token":"`+tok+`"}`, ""); w.Code != http.StatusNoContent {
		t.Fatalf("reuso idempotente: %d %s", w.Code, w.Body.String())
	}
	// Desconocido / con forma inválida: el texto uniforme.
	for _, body := range []string{`{"token":""}`, `{"token":"corto"}`, `{"token":"` + strings.Repeat("A", 43) + `"}`} {
		w := httpkit.Do(h.r, "POST", "/api/v1/auth/verify-email", body, "")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Enlace invalido o vencido") {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := httpkit.Do(h.r, "POST", "/api/v1/auth/verify-email", `{"token":1}`, ""); w.Code != 400 {
		t.Fatalf("tipo equivocado: %d", w.Code)
	}
}

// D7: el reenvío. 202 con un pedido nuevo; 429 dentro del minuto; 409 si la
// cuenta ya está verificada. Requiere sesión.
func TestF15HTTP_ResendVerification(t *testing.T) {
	h := newRecHTTP(t)
	userID := h.register(t, "ana@mail.com")
	h.run.Flush()
	session, _ := h.tokens.Issue(userID)
	if w := httpkit.Do(h.r, "POST", "/api/v1/me/verify-email/resend", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin sesión: %d", w.Code)
	}
	// El primer reenvío cae dentro del minuto del correo del registro: 429.
	w := httpkit.Do(h.r, "POST", "/api/v1/me/verify-email/resend", "", session)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "Espera un minuto antes de pedir otro correo") {
		t.Fatalf("429 esperado: %d %s", w.Code, w.Body.String())
	}
	// Una cuenta ya verificada: 409 con el texto del contrato.
	verified := h.store.SeedUser("u-bea", "Bea", "bea@mail.com", "h", true).ID
	beaSession, _ := h.tokens.Issue(verified)
	w = httpkit.Do(h.r, "POST", "/api/v1/me/verify-email/resend", "", beaSession)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "El email ya esta verificado") {
		t.Fatalf("409 esperado: %d %s", w.Code, w.Body.String())
	}
}

// D7: el reenvío con un pedido que no choca con el throttle es 202 y manda el
// correo por el runner (después de responder).
func TestF15HTTP_ResendIs202AndMailsAsync(t *testing.T) {
	h := newRecHTTP(t)
	userID := h.register(t, "ana@mail.com")
	h.run.Flush()
	session, _ := h.tokens.Issue(userID)
	// Fuera de la ventana del minuto: se envejece el pedido anterior.
	h.store.Mu.Lock()
	for _, v := range h.store.Verifs {
		v.CreatedAt = time.Now().UTC().Add(-2 * time.Minute)
	}
	h.store.Mu.Unlock()
	before := h.box.Count()
	w := httpkit.Do(h.r, "POST", "/api/v1/me/verify-email/resend", "", session)
	if w.Code != http.StatusAccepted || w.Body.String() != `{"ok":true}` {
		t.Fatalf("resend: %d %s", w.Code, w.Body.String())
	}
	if h.box.Count() != before || h.run.Pending() != 1 {
		t.Fatalf("el correo sale por el runner: mails=%d pending=%d", h.box.Count(), h.run.Pending())
	}
	h.run.Flush()
	if h.box.Count() != before+1 {
		t.Fatalf("correos: %d", h.box.Count())
	}
}

// El desafío de 2FA nunca es una sesión: no autentica /me ni /ws (D4, punta a
// punta con la compuerta cableada).
func TestF15HTTP_twoFactorChallengeIsNotASession(t *testing.T) {
	h := newRecHTTP(t)
	h.store.SeedUser("u-ana", "Ana", "ana@mail.com", "h", true)
	// Un JWT con typ:"2fa" es lo que emite el desafío de F10.
	challenge := issueTyped(t, "test-secret", "u-ana", "2fa")
	for _, path := range []string{"/api/v1/me", "/api/v1/ws?token=" + challenge} {
		w := httpkit.Do(h.r, "GET", path, "", challenge)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s con el desafío: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// issueTyped firma un JWT con un `typ` explícito (el desafío de 2FA es typ:"2fa").
func issueTyped(t *testing.T, secret, sub, typ string) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub, "typ": typ, "exp": time.Now().Add(time.Hour).Unix(), "ia": time.Now().UnixMilli(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
