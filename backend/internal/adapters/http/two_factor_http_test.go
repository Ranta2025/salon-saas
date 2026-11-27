// Pruebas HTTP del 2FA TOTP (F10, parte B: D4, D7, D8 y D9). Router completo
// con fakes en memoria, JWT y bcrypt REALES, el AuthMiddleware real y el hub de
// tiempo real real: lo que se prueba es el contrato de la API y que el desafío
// del segundo paso NUNCA autentica nada.
package httpapi

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
	auditsvc "salon-saas/internal/audit"
	"salon-saas/internal/domain"
)

type tfHTTP struct {
	r      *gin.Engine
	d      Deps
	env    *posEnv
	repo   *mtf
	clock  *mtfClock
	audit  *maudit
	tokens security.JWTIssuer
	// Cuenta de prueba.
	email, pass, userID, session, salonID string
}

func newTFHTTP(t *testing.T) *tfHTTP {
	t.Helper()
	gin.SetMode(gin.TestMode)
	env := newPOSEnv()
	clock := &mtfClock{t: time.Now().UTC()}
	repo := newMTF(clock.Now)
	audit := &maudit{}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	box, err := domain.NewTOTPSecretBox("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	tfSvc := app.TwoFactorService{
		Repo: repo, Users: env.users, Hasher: security.BcryptHasher{}, Tokens: tokens,
		Challenges: tokens, Box: box, Memberships: env.mems,
		Audit: auditsvc.AuditService{Events: audit, Users: env.users}, Now: clock.Now,
	}
	d := env.d
	d.Auth = AuthHandler{Auth: app.AuthService{Users: env.users, Hasher: security.BcryptHasher{}, Tokens: tokens, TwoFactor: tfSvc}}
	d.TwoFactor = TwoFactorHandler{Service: tfSvc}
	r := gin.New()
	RegisterRoutes(r, d)
	h := &tfHTTP{r: r, d: d, env: env, repo: repo, clock: clock, audit: audit, tokens: tokens,
		email: "ana@mail.com", pass: "secreta123"}

	if w := doReq(r, "POST", "/api/v1/auth/register", `{"name":"Ana","email":"ana@mail.com","password":"secreta123"}`, ""); w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	h.userID = env.users.byEmail[h.email].ID
	h.session = h.login(t)
	w := doReq(r, "POST", "/api/v1/salons", `{"name":"Navajas","slug":"navajas"}`, h.session)
	if w.Code != 201 {
		t.Fatalf("salon: %d %s", w.Code, w.Body.String())
	}
	var s struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &s)
	h.salonID = s.ID
	return h
}

func (h *tfHTTP) login(t *testing.T) string {
	t.Helper()
	w := doReq(h.r, "POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var out struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatalf("login sin token: %s", w.Body.String())
	}
	return out.Token
}

func (h *tfHTTP) do(method, path, body, token string) *httptest.ResponseRecorder {
	return doReq(h.r, method, path, body, token)
}

func body(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("cuerpo no es JSON: %q", w.Body.String())
	}
	return m
}

func wantErr(t *testing.T, w *httptest.ResponseRecorder, code int, text string) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status %d (%s), esperado %d %q", w.Code, strings.TrimSpace(w.Body.String()), code, text)
	}
	if got, _ := body(t, w)["error"].(string); got != text {
		t.Fatalf("error %q, esperado %q", got, text)
	}
}

// setup llama a /me/2fa/setup y devuelve el secreto en claro (la app autenticadora).
func (h *tfHTTP) setup(t *testing.T) []byte {
	t.Helper()
	w := h.do("POST", "/api/v1/me/2fa/setup", "", h.session)
	if w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	b32, _ := body(t, w)["secret"].(string)
	sec, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil || len(sec) != 20 {
		t.Fatalf("secreto base32 inválido %q: %v", b32, err)
	}
	return sec
}

// code es el código TOTP del instante del reloj de prueba + `ahead` pasos.
func (h *tfHTTP) code(secret []byte, ahead int64) string {
	return domain.TOTPCode(secret, domain.TOTPStepAt(h.clock.Now())+ahead, domain.TOTPDigits)
}

// enable activa el 2FA y devuelve secreto y códigos de recuperación. Después
// avanza el reloj 2 pasos para que el código de activación no estorbe.
func (h *tfHTTP) enable(t *testing.T) ([]byte, []string) {
	t.Helper()
	sec := h.setup(t)
	w := h.do("POST", "/api/v1/me/2fa/enable", `{"code":"`+h.code(sec, 0)+`"}`, h.session)
	if w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	h.clock.Add(2 * 30 * time.Second)
	h.audit.mu.Lock()
	h.audit.rows = nil
	h.audit.appendCalls = 0
	h.audit.mu.Unlock()
	return sec, out.RecoveryCodes
}

func (h *tfHTTP) challenge(t *testing.T) string {
	t.Helper()
	w := h.do("POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	m := body(t, w)
	ch, _ := m["challenge"].(string)
	if m["two_factor_required"] != true || ch == "" {
		t.Fatalf("esperaba desafío: %s", w.Body.String())
	}
	return ch
}

func (h *tfHTTP) login2fa(challenge, code string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"challenge": challenge, "code": code})
	return h.do("POST", "/api/v1/auth/login/2fa", string(b), "")
}

var errAuditDownHTTP = errors.New("auditoría caída")

const (
	msgBadCode   = "Codigo incorrecto"
	msgChallenge = "Codigo incorrecto o desafio vencido"
	msgLocked    = "Demasiados intentos. Proba de nuevo en unos minutos"
	msgBadBody   = "body invalido"
)

// --- D8: el login sin 2FA queda EXACTAMENTE igual -------------------------------

func TestTwoFactorHTTP_loginWithoutTwoFactorUnchanged(t *testing.T) {
	h := newTFHTTP(t)
	re := regexp.MustCompile(`^\{"token":"[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+"\}$`)
	w := h.do("POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != 200 || !re.MatchString(w.Body.String()) {
		t.Fatalf("sin fila de 2FA: %d %q", w.Code, w.Body.String())
	}
	// Con un enrolamiento PENDIENTE (enabled=false) también es el camino de siempre.
	h.setup(t)
	w = h.do("POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != 200 || !re.MatchString(w.Body.String()) {
		t.Fatalf("pendiente: %d %q", w.Code, w.Body.String())
	}
	if m := body(t, w); len(m) != 1 || m["token"] == nil {
		t.Fatalf("solo la clave token: %v", m)
	}
	// Mismos errores de siempre.
	wantErr(t, h.do("POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"mala"}`, ""), 401, "no autenticado")
	wantErr(t, h.do("POST", "/api/v1/auth/login", `{"email":"nadie@mail.com","password":"x"}`, ""), 401, "no autenticado")
	wantErr(t, h.do("POST", "/api/v1/auth/login", `{"email":5}`, ""), 400, "body inválido")
	// El token de siempre sirve como sesión.
	if w := h.do("GET", "/api/v1/me", "", h.login(t)); w.Code != 200 {
		t.Fatalf("GET /me con la sesión: %d", w.Code)
	}
}

func TestTwoFactorHTTP_loginWithTwoFactorReturnsChallengeNotToken(t *testing.T) {
	h := newTFHTTP(t)
	h.enable(t)
	w := h.do("POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	m := body(t, w)
	if len(m) != 2 || m["two_factor_required"] != true || m["challenge"] == "" {
		t.Fatalf("forma esperada {two_factor_required,challenge}: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("NO puede haber campo token: %s", w.Body.String())
	}
	// El desafío es un JWT de 5 minutos con typ 2fa.
	tok, _, err := jwt.NewParser().ParseUnverified(m["challenge"].(string), jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	cl := tok.Claims.(jwt.MapClaims)
	exp, _ := cl.GetExpirationTime()
	if cl["typ"] != "2fa" || cl["sub"] != h.userID || time.Until(exp.Time) > 5*time.Minute+5*time.Second || time.Until(exp.Time) < 4*time.Minute {
		t.Errorf("claims del desafío: %v", cl)
	}
	// Una contraseña mala con 2FA da el MISMO 401 de siempre (sin delatar el 2FA).
	wantErr(t, h.do("POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"mala"}`, ""), 401, "no autenticado")
}

// --- D8: el desafío no es una sesión y la sesión no es un desafío -------------------

func TestTwoFactorHTTP_challengeNeverAuthenticatesAndSessionIsNotAChallenge(t *testing.T) {
	h := newTFHTTP(t)
	sec, _ := h.enable(t)
	ch := h.challenge(t)

	// El desafío NO autentica ninguna ruta: /me, 2FA, panel del salón, WS.
	routes := []struct{ method, path, body string }{
		{"GET", "/api/v1/me", ""},
		{"GET", "/api/v1/me/2fa", ""},
		{"POST", "/api/v1/me/2fa/setup", ""},
		{"POST", "/api/v1/me/2fa/enable", `{"code":"123456"}`},
		{"POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"123456"}`},
		{"POST", "/api/v1/salons", `{"name":"X","slug":"x"}`},
		{"GET", "/api/v1/salons/" + h.salonID + "/settings", ""},
		{"GET", "/api/v1/salons/" + h.salonID + "/members", ""},
		{"GET", "/api/v1/salons/" + h.salonID + "/audit-log", ""},
		{"GET", "/api/v1/salons/" + h.salonID + "/bookings", ""},
	}
	for _, rt := range routes {
		wantErr(t, h.do(rt.method, rt.path, rt.body, ch), 401, "token inválido")
	}
	// Websocket: el hub parsea con el mismo Parse; responde 401 ANTES del upgrade.
	wantErr(t, h.do("GET", "/api/v1/ws?token="+ch, "", ""), 401, "token inválido")

	// La sesión normal sí (control), y NO sirve como desafío.
	if w := h.do("GET", "/api/v1/me", "", h.session); w.Code != 200 {
		t.Fatalf("control: la sesión debe seguir sirviendo: %d", w.Code)
	}
	wantErr(t, h.login2fa(h.session, h.code(sec, 0)), 401, msgChallenge)
	// Una sesión con typ:"session" explícito (válida como sesión) tampoco es un desafío.
	explicit, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": h.userID, "typ": "session", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("test-secret"))
	if w := h.do("GET", "/api/v1/me", "", explicit); w.Code != 200 {
		t.Errorf("typ:session debe valer como sesión: %d", w.Code)
	}
	wantErr(t, h.login2fa(explicit, h.code(sec, 0)), 401, msgChallenge)
	// Desafío vencido, de otra firma, basura, vacío: el mismo 401 uniforme.
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": h.userID, "typ": "2fa", "exp": time.Now().Add(-time.Second).Unix(),
	}).SignedString([]byte("test-secret"))
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": h.userID, "typ": "2fa", "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString([]byte("otra-clave"))
	for _, bad := range []string{expired, forged, "basura", ""} {
		wantErr(t, h.login2fa(bad, h.code(sec, 0)), 401, msgChallenge)
	}
}

// --- D8: segundo paso ---------------------------------------------------------------------

func TestTwoFactorHTTP_login2faIssuesSessionToken(t *testing.T) {
	h := newTFHTTP(t)
	sec, _ := h.enable(t)
	ch := h.challenge(t)

	wantErr(t, h.login2fa(ch, "000000"), 401, msgChallenge)
	w := h.login2fa(ch, h.code(sec, 0))
	if w.Code != 200 {
		t.Fatalf("login/2fa: %d %s", w.Code, w.Body.String())
	}
	m := body(t, w)
	tok, _ := m["token"].(string)
	if len(m) != 1 || tok == "" {
		t.Fatalf("forma {token}: %s", w.Body.String())
	}
	// El token funciona como sesión (GET /me y una ruta del panel) y el desafío no.
	if w := h.do("GET", "/api/v1/me", "", tok); w.Code != 200 {
		t.Fatalf("GET /me con el token de login/2fa: %d", w.Code)
	}
	if w := h.do("GET", "/api/v1/salons/"+h.salonID+"/settings", "", tok); w.Code != 200 {
		t.Fatalf("ruta del panel: %d %s", w.Code, w.Body.String())
	}
	wantErr(t, h.do("GET", "/api/v1/me", "", ch), 401, "token inválido")
	// Anti-replay: el mismo código otra vez falla con el mismo 401.
	wantErr(t, h.login2fa(ch, h.code(sec, 0)), 401, msgChallenge)
	// Con espacios en el medio (se pega «123 456»).
	h.clock.Add(31 * time.Second)
	c := h.code(sec, 0)
	if w := h.login2fa(ch, c[:3]+" "+c[3:]); w.Code != 200 {
		t.Fatalf("código con espacio: %d %s", w.Code, w.Body.String())
	}
}

func TestTwoFactorHTTP_login2faRecoveryCodeSingleUse(t *testing.T) {
	h := newTFHTTP(t)
	_, codes := h.enable(t)
	if len(codes) != 8 {
		t.Fatalf("8 códigos, got %d", len(codes))
	}
	ch := h.challenge(t)
	if w := h.login2fa(ch, strings.ToLower(codes[0])); w.Code != 200 {
		t.Fatalf("recuperación: %d %s", w.Code, w.Body.String())
	}
	wantErr(t, h.login2fa(ch, codes[0]), 401, msgChallenge)
	if w := h.login2fa(ch, codes[1]); w.Code != 200 {
		t.Fatalf("otro código: %d", w.Code)
	}
}

func TestTwoFactorHTTP_login2faLockout(t *testing.T) {
	h := newTFHTTP(t)
	sec, _ := h.enable(t)
	ch := h.challenge(t)
	for i := 0; i < 5; i++ {
		wantErr(t, h.login2fa(ch, "000000"), 401, msgChallenge)
	}
	// 429 aun con el código correcto.
	wantErr(t, h.login2fa(ch, h.code(sec, 0)), 429, msgLocked)
	wantErr(t, h.login2fa(ch, h.code(sec, 0)), 429, msgLocked)
	// Y la contraseña buena sigue dando el desafío (el bloqueo es del segundo paso).
	h.challenge(t)
	// A los 15 minutos se libera.
	h.clock.Add(16 * time.Minute)
	if w := h.login2fa(ch, h.code(sec, 0)); w.Code != 200 {
		t.Fatalf("liberado: %d %s", w.Code, w.Body.String())
	}
}

func TestTwoFactorHTTP_login2faBodyValidation(t *testing.T) {
	h := newTFHTTP(t)
	sec, _ := h.enable(t)
	ch := h.challenge(t)
	good := h.code(sec, 0)
	for name, b := range map[string]string{
		"challenge número": `{"challenge":123,"code":"123456"}`,
		"code número":      `{"challenge":"` + ch + `","code":123456}`,
		"code array":       `{"challenge":"` + ch + `","code":["1"]}`,
		"json roto":        `{"challenge":`,
		"vacío":            ``,
		"NUL en code":      `{"challenge":"` + ch + `","code":"` + good + `\u0000"}`,
		"NUL en challenge": `{"challenge":"a\u0000b","code":"123456"}`,
	} {
		w := h.do("POST", "/api/v1/auth/login/2fa", b, "")
		if w.Code != 400 {
			t.Errorf("%s: status %d (%s), esperado 400", name, w.Code, w.Body.String())
			continue
		}
		wantErr(t, w, 400, msgBadBody)
	}
	// Ninguno de esos gastó intentos ni consumió el código: el bueno entra.
	if w := h.login2fa(ch, good); w.Code != 200 {
		t.Fatalf("los 400 no deben contar: %d %s", w.Code, w.Body.String())
	}
	// Campos ausentes = fallo uniforme, no 500.
	wantErr(t, h.do("POST", "/api/v1/auth/login/2fa", `{}`, ""), 401, msgChallenge)
	wantErr(t, h.do("POST", "/api/v1/auth/login/2fa", `{"challenge":"`+ch+`"}`, ""), 401, msgChallenge)
}

// --- D7: rutas de la cuenta --------------------------------------------------------------------

func TestTwoFactorHTTP_requireSession(t *testing.T) {
	h := newTFHTTP(t)
	for _, rt := range []struct{ m, p, b string }{
		{"GET", "/api/v1/me/2fa", ""}, {"POST", "/api/v1/me/2fa/setup", ""},
		{"POST", "/api/v1/me/2fa/enable", `{"code":"1"}`}, {"POST", "/api/v1/me/2fa/disable", `{}`},
	} {
		wantErr(t, h.do(rt.m, rt.p, rt.b, ""), 401, "falta token")
		wantErr(t, h.do(rt.m, rt.p, rt.b, "basura"), 401, "token inválido")
	}
}

func TestTwoFactorHTTP_statusSetupEnableFlow(t *testing.T) {
	h := newTFHTTP(t)
	w := h.do("GET", "/api/v1/me/2fa", "", h.session)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"enabled":false}` {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	// Enable sin setup previo.
	wantErr(t, h.do("POST", "/api/v1/me/2fa/enable", `{"code":"123456"}`, h.session), 409, "Primero genera el codigo con la configuracion")

	w = h.do("POST", "/api/v1/me/2fa/setup", "", h.session)
	if w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	m := body(t, w)
	secret, _ := m["secret"].(string)
	if len(m) != 2 || len(secret) != 32 {
		t.Fatalf("forma {secret,otpauth_url}: %s", w.Body.String())
	}
	if want := "otpauth://totp/Salon:ana@mail.com?secret=" + secret + "&issuer=Salon"; m["otpauth_url"] != want {
		t.Errorf("otpauth_url %v, esperado %s", m["otpauth_url"], want)
	}
	// Pendiente: sigue «false» y un setup nuevo lo REEMPLAZA.
	if w := h.do("GET", "/api/v1/me/2fa", "", h.session); strings.TrimSpace(w.Body.String()) != `{"enabled":false}` {
		t.Errorf("pendiente no es activado: %s", w.Body.String())
	}
	sec2 := h.setup(t)
	// Código malo = 400 y no activa.
	wantErr(t, h.do("POST", "/api/v1/me/2fa/enable", `{"code":"000000"}`, h.session), 400, msgBadCode)
	wantErr(t, h.do("POST", "/api/v1/me/2fa/enable", `{"code":""}`, h.session), 400, msgBadCode)
	wantErr(t, h.do("POST", "/api/v1/me/2fa/enable", `{}`, h.session), 400, msgBadCode)
	if w := h.do("GET", "/api/v1/me/2fa", "", h.session); strings.TrimSpace(w.Body.String()) != `{"enabled":false}` {
		t.Fatalf("un código malo no activa: %s", w.Body.String())
	}
	if len(h.audit.rows) != 0 {
		t.Fatal("sin activar no hay auditoría")
	}
	// Con el código del secreto VIEJO (reemplazado) tampoco.
	old, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if domain.TOTPCode(old, domain.TOTPStepAt(h.clock.Now()), 6) != h.code(sec2, 0) {
		wantErr(t, h.do("POST", "/api/v1/me/2fa/enable", `{"code":"`+h.code(old, 0)+`"}`, h.session), 400, msgBadCode)
	}
	// Bien.
	w = h.do("POST", "/api/v1/me/2fa/enable", `{"code":"`+h.code(sec2, 0)+`"}`, h.session)
	if w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(body(t, w)) != 1 || len(out.RecoveryCodes) != 8 {
		t.Fatalf("forma {recovery_codes:[8]}: %s", w.Body.String())
	}
	re := regexp.MustCompile(`^[A-Z2-7]{5}-[A-Z2-7]{5}$`)
	seen := map[string]bool{}
	for _, c := range out.RecoveryCodes {
		if !re.MatchString(c) || seen[c] {
			t.Errorf("código mal formado o repetido: %q", c)
		}
		seen[c] = true
	}
	if w := h.do("GET", "/api/v1/me/2fa", "", h.session); strings.TrimSpace(w.Body.String()) != `{"enabled":true}` {
		t.Fatalf("activado: %s", w.Body.String())
	}
	// Ya activado: setup y enable dan 409 con el texto exacto, sin pisar nada.
	wantErr(t, h.do("POST", "/api/v1/me/2fa/setup", "", h.session), 409, "La verificacion en dos pasos ya esta activada")
	wantErr(t, h.do("POST", "/api/v1/me/2fa/enable", `{"code":"`+h.code(sec2, 1)+`"}`, h.session), 409, "La verificacion en dos pasos ya esta activada")
	// El secreto NUNCA vuelve a salir: ninguna otra respuesta lo lleva.
	for _, p := range []string{"/api/v1/me/2fa", "/api/v1/me"} {
		if w := h.do("GET", p, "", h.session); strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "otpauth") {
			t.Errorf("%s filtra el secreto", p)
		}
	}
}

func TestTwoFactorHTTP_disable(t *testing.T) {
	h := newTFHTTP(t)
	// Sin 2FA activo.
	wantErr(t, h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"123456"}`, h.session), 409, "La verificacion en dos pasos no esta activada")
	sec, codes := h.enable(t)

	// Contraseña mala = 401 ANTES de mirar el código (aunque el código sea malo).
	wantErr(t, h.do("POST", "/api/v1/me/2fa/disable", `{"password":"mala","code":"000000"}`, h.session), 401, "Credenciales invalidas")
	wantErr(t, h.do("POST", "/api/v1/me/2fa/disable", `{"code":"`+h.code(sec, 0)+`"}`, h.session), 401, "Credenciales invalidas")
	// Código malo = 400.
	for _, c := range []string{"000000", "", "AAAAA-AAAAA", "zz"} {
		wantErr(t, h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"`+c+`"}`, h.session), 400, msgBadCode)
	}
	if w := h.do("GET", "/api/v1/me/2fa", "", h.session); strings.TrimSpace(w.Body.String()) != `{"enabled":true}` {
		t.Fatalf("sigue activado: %s", w.Body.String())
	}
	if len(h.audit.rows) != 0 {
		t.Fatal("sin desactivar no hay auditoría")
	}
	// Bien, con un código de recuperación: 204 sin cuerpo.
	w := h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"`+codes[2]+`"}`, h.session)
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("disable: %d %q", w.Code, w.Body.String())
	}
	if w := h.do("GET", "/api/v1/me/2fa", "", h.session); strings.TrimSpace(w.Body.String()) != `{"enabled":false}` {
		t.Fatalf("desactivado: %s", w.Body.String())
	}
	if _, ok := h.repo.rows[h.userID]; ok || len(h.repo.codes[h.userID]) != 0 {
		t.Error("se borran el secreto y los códigos")
	}
	// El login vuelve a ser el de siempre.
	if w := h.do("POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, ""); !strings.HasPrefix(w.Body.String(), `{"token":"`) {
		t.Fatalf("login tras desactivar: %s", w.Body.String())
	}
	// Se puede volver a activar y desactivar con un código TOTP.
	sec2, _ := h.enable(t)
	if w := h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"`+h.code(sec2, 0)+`"}`, h.session); w.Code != 204 {
		t.Fatalf("disable con TOTP: %d %s", w.Code, w.Body.String())
	}
}

func TestTwoFactorHTTP_disableLockout(t *testing.T) {
	h := newTFHTTP(t)
	sec, _ := h.enable(t)
	for i := 0; i < 5; i++ {
		wantErr(t, h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"000000"}`, h.session), 400, msgBadCode)
	}
	wantErr(t, h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"`+h.code(sec, 0)+`"}`, h.session), 429, msgLocked)
	if w := h.do("GET", "/api/v1/me/2fa", "", h.session); strings.TrimSpace(w.Body.String()) != `{"enabled":true}` {
		t.Fatalf("bloqueado no desactiva: %s", w.Body.String())
	}
}

func TestTwoFactorHTTP_bodyValidationOnAccountRoutes(t *testing.T) {
	h := newTFHTTP(t)
	sec := h.setup(t)
	good := h.code(sec, 0)
	cases := []struct{ name, path, body string }{
		{"enable code número", "/api/v1/me/2fa/enable", `{"code":123456}`},
		{"enable code objeto", "/api/v1/me/2fa/enable", `{"code":{"a":1}}`},
		{"enable NUL", "/api/v1/me/2fa/enable", `{"code":"` + good + `\u0000"}`},
		{"enable roto", "/api/v1/me/2fa/enable", `{"code":`},
		{"enable vacío", "/api/v1/me/2fa/enable", ``},
		{"disable password número", "/api/v1/me/2fa/disable", `{"password":1,"code":"123456"}`},
		{"disable code bool", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":true}`},
		{"disable NUL en password", "/api/v1/me/2fa/disable", `{"password":"a\u0000b","code":"123456"}`},
		{"disable NUL en code", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"1\u00002"}`},
		{"disable roto", "/api/v1/me/2fa/disable", `[`},
	}
	for _, c := range cases {
		w := h.do("POST", c.path, c.body, h.session)
		if w.Code != 400 || body(t, w)["error"] != msgBadBody {
			t.Errorf("%s: %d %s (esperado 400 %q)", c.name, w.Code, strings.TrimSpace(w.Body.String()), msgBadBody)
		}
	}
	// Ninguno activó el 2FA ni gastó el código: el bueno sigue sirviendo.
	if w := h.do("POST", "/api/v1/me/2fa/enable", `{"code":"`+good+`"}`, h.session); w.Code != 200 {
		t.Fatalf("enable tras los 400: %d %s", w.Code, w.Body.String())
	}
}

// --- D4: auditoría ------------------------------------------------------------------------

func TestTwoFactorHTTP_auditEnabledAndDisabled(t *testing.T) {
	h := newTFHTTP(t)
	sec := h.setup(t)
	if w := h.do("POST", "/api/v1/me/2fa/enable", `{"code":"`+h.code(sec, 0)+`"}`, h.session); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(h.audit.rows) != 1 {
		t.Fatalf("un evento por salón activo: %+v", h.audit.rows)
	}
	ev := h.audit.rows[0]
	if ev.Action != "auth.2fa_enabled" || ev.SalonID != h.salonID || ev.ActorUserID != h.userID || ev.EntityID != "" {
		t.Errorf("evento: %+v", ev)
	}
	h.clock.Add(time.Minute)
	if w := h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"`+h.code(sec, 1)+`"}`, h.session); w.Code != 204 {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if len(h.audit.rows) != 2 || h.audit.rows[1].Action != "auth.2fa_disabled" || h.audit.rows[1].SalonID != h.salonID {
		t.Errorf("eventos: %+v", h.audit.rows)
	}
}

func TestTwoFactorHTTP_auditFailureDoesNotBreakRequest(t *testing.T) {
	h := newTFHTTP(t)
	h.audit.appendErr = errAuditDownHTTP
	sec := h.setup(t)
	if w := h.do("POST", "/api/v1/me/2fa/enable", `{"code":"`+h.code(sec, 0)+`"}`, h.session); w.Code != 200 {
		t.Fatalf("enable con la auditoría caída: %d %s", w.Code, w.Body.String())
	}
	h.clock.Add(time.Minute)
	if w := h.do("POST", "/api/v1/me/2fa/disable", `{"password":"secreta123","code":"`+h.code(sec, 1)+`"}`, h.session); w.Code != 204 {
		t.Fatalf("disable con la auditoría caída: %d %s", w.Code, w.Body.String())
	}
	if h.audit.appendCalls == 0 {
		t.Error("tenía que intentar escribir")
	}
}

// --- Límite por IP ----------------------------------------------------------------------------

// La ruta nueva cuelga de /api/v1/auth/, así que el limitador por IP del
// composition root (RateLimit(..., "/api/v1/auth/")) la cubre sin cambios.
func TestTwoFactorHTTP_ipRateLimitCoversLogin2fa(t *testing.T) {
	h := newTFHTTP(t)
	r := gin.New()
	r.Use(RateLimit(3, time.Minute, "/api/v1/auth/"))
	RegisterRoutes(r, h.d)
	last := 0
	for i := 0; i < 5; i++ {
		w := doReq(r, "POST", "/api/v1/auth/login/2fa", `{"challenge":"x","code":"1"}`, "")
		if i < 3 && w.Code == 429 {
			t.Fatalf("el pedido %d no debía limitarse", i+1)
		}
		last = w.Code
	}
	if last != 429 {
		t.Fatalf("pasado el cupo debe dar 429, dio %d", last)
	}
}
