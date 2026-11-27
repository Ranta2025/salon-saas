// F15 (D4) punta a punta: un DESAFÍO de 2FA emitido antes del reinicio de
// contraseña ya no se canjea por una sesión, y un websocket abierto antes del
// reinicio se cierra solo. Router REAL (RegisterRoutes + AuthMiddleware + hub de
// tiempo real), JWT y TOTP reales: lo que se prueba es el contrato de la API.
//
// La compuerta es un fake con la misma semántica que pgstore.SessionGate (nil =
// la contraseña nunca cambió) para poder mover `password_changed_at` sin Postgres.
package httpapi

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
	auditsvc "salon-saas/internal/audit"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// httpGate es users.password_changed_at bajo control del test.
type httpGate struct {
	mu  sync.Mutex
	at  *time.Time
	err error
}

func (g *httpGate) PasswordChangedAt(string) (*time.Time, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return nil, g.err
	}
	if g.at == nil {
		return nil, nil
	}
	at := *g.at
	return &at, nil
}

func (g *httpGate) set(at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.at = &at
	g.err = nil
}

func (g *httpGate) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

var _ ports.SessionGate = (*httpGate)(nil)

const wsChecksEvery = 5 * time.Millisecond

// field extrae una clave de cadena del cuerpo JSON (los helpers body/wantErr son
// los del resto de las pruebas HTTP).
func field(t *testing.T, w *httptest.ResponseRecorder, key string) string {
	t.Helper()
	v, _ := body(t, w)[key].(string)
	if v == "" {
		t.Fatalf("el cuerpo no trae %q: %s", key, w.Body.String())
	}
	return v
}

// decodeBase32Secret pasa el secreto base32 sin relleno a bytes (el test hace de
// app autenticadora y calcula el TOTP).
func decodeBase32Secret(t *testing.T, b32 string) []byte {
	t.Helper()
	sec, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil || len(sec) != 20 {
		t.Fatalf("secreto base32 inválido %q: %v", b32, err)
	}
	return sec
}

// tfRevHTTP es el router completo con el 2FA, la compuerta y el hub cableados.
type tfRevHTTP struct {
	r       *gin.Engine
	srv     *httptest.Server
	env     *posEnv
	repo    *mtf
	clock   *mtfClock
	gate    *httpGate
	tokens  security.JWTIssuer
	hub     *realtime.Hub
	userID  string
	salonID string
	session string
}

func newTFRevHTTP(t *testing.T) *tfRevHTTP {
	t.Helper()
	gin.SetMode(gin.TestMode)
	env := newPOSEnv()
	clock := &mtfClock{t: time.Now().UTC()}
	repo := newMTF(clock.Now)
	tokens := security.JWTIssuer{Secret: "test-secret"}
	gate := &httpGate{}
	box, err := domain.NewTOTPSecretBox("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	tfSvc := app.TwoFactorService{
		Repo: repo, Users: env.users, Hasher: security.BcryptHasher{}, Tokens: tokens,
		Challenges: tokens, Box: box, Memberships: env.mems,
		Audit: auditsvc.AuditService{Events: &maudit{}, Users: env.users}, Now: clock.Now,
		Gate: gate,
	}
	d := env.d
	d.Auth = AuthHandler{Auth: app.AuthService{
		Users: env.users, Hasher: security.BcryptHasher{}, Tokens: tokens, TwoFactor: tfSvc,
	}}
	d.TwoFactor = TwoFactorHandler{Service: tfSvc}
	d.TokenIssuer = tokens
	d.SessionGate = gate
	hub := realtime.NewHub(tokens, env.mems,
		realtime.HubOrigins("http://localhost:5173"),
		realtime.HubSessionGate(gate),
		realtime.HubSessionCheckInterval(wsChecksEvery),
	)
	d.WS = hub.ServeWS
	r := gin.New()
	RegisterRoutes(r, d)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	h := &tfRevHTTP{r: r, srv: srv, env: env, repo: repo, clock: clock, gate: gate, tokens: tokens, hub: hub}
	if w := doReq(r, "POST", "/api/v1/auth/register", `{"name":"Ana","email":"ana@mail.com","password":"secreta123"}`, ""); w.Code != 201 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	h.userID = env.users.byEmail["ana@mail.com"].ID
	h.session = h.login(t)
	w := doReq(r, "POST", "/api/v1/salons", `{"name":"Navajas","slug":"navajas"}`, h.session)
	if w.Code != 201 {
		t.Fatalf("salon: %d %s", w.Code, w.Body.String())
	}
	var s struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil || s.ID == "" {
		t.Fatalf("salón sin id: %s", w.Body.String())
	}
	h.salonID = s.ID
	return h
}

func (h *tfRevHTTP) login(t *testing.T) string {
	t.Helper()
	w := doReq(h.r, "POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	return field(t, w, "token")
}

// enable activa el 2FA y devuelve el secreto en claro (la app autenticadora).
func (h *tfRevHTTP) enable(t *testing.T) []byte {
	t.Helper()
	w := doReq(h.r, "POST", "/api/v1/me/2fa/setup", "", h.session)
	if w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	sec := decodeBase32Secret(t, field(t, w, "secret"))
	w = doReq(h.r, "POST", "/api/v1/me/2fa/enable", `{"code":"`+h.code(sec, 0)+`"}`, h.session)
	if w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	h.clock.Add(2 * 30 * time.Second) // el paso de activación no estorba
	return sec
}

func (h *tfRevHTTP) code(secret []byte, ahead int64) string {
	return domain.TOTPCode(secret, domain.TOTPStepAt(h.clock.Now())+ahead, domain.TOTPDigits)
}

// challenge pide el desafío por la ruta real del login.
func (h *tfRevHTTP) challenge(t *testing.T) string {
	t.Helper()
	w := doReq(h.r, "POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != 200 {
		t.Fatalf("login con 2FA: %d %s", w.Code, w.Body.String())
	}
	m := body(t, w)
	if m["two_factor_required"] != true {
		t.Fatalf("esperaba un desafío: %s", w.Body.String())
	}
	ch, _ := m["challenge"].(string)
	if ch == "" || m["token"] != nil {
		t.Fatalf("desafío sin token: %s", w.Body.String())
	}
	return ch
}

func (h *tfRevHTTP) login2fa(challenge, code string) *httptest.ResponseRecorder {
	return doReq(h.r, "POST", "/api/v1/auth/login/2fa",
		`{"challenge":"`+challenge+`","code":"`+code+`"}`, "")
}

// El caso del verificador: desafío obtenido, después el reinicio (la compuerta
// reporta un `password_changed_at` posterior al `ia`), después un código VÁLIDO.
// El canje da el 401 uniforme del contrato, sin token y sin ninguna pista extra.
func TestF15Fix_challengeIssuedBeforeResetCannotBeExchanged(t *testing.T) {
	h := newTFRevHTTP(t)
	sec := h.enable(t)

	// Control: sin reinicio, el desafío se canjea (la respuesta de siempre).
	ch := h.challenge(t)
	if w := h.login2fa(ch, h.code(sec, 0)); w.Code != http.StatusOK {
		t.Fatalf("control sin reinicio: %d %s", w.Code, w.Body.String())
	}
	h.clock.Add(60 * time.Second)

	// El reinicio ocurre DESPUÉS del desafío.
	ch = h.challenge(t)
	h.gate.set(time.Now().Add(time.Minute))

	revoked := h.login2fa(ch, h.code(sec, 0))
	if revoked.Code != http.StatusUnauthorized || revoked.Body.String() != `{"error":"Codigo incorrecto o desafio vencido"}` {
		t.Fatalf("desafío revocado: %d %s", revoked.Code, revoked.Body.String())
	}
	// La MISMA respuesta que un código malo: no se puede distinguir una causa de otra.
	h.clock.Add(60 * time.Second)
	badCode := h.login2fa(ch, "000000")
	if badCode.Code != revoked.Code || badCode.Body.String() != revoked.Body.String() {
		t.Errorf("el 401 no es uniforme: revocado=%d %s, código malo=%d %s",
			revoked.Code, revoked.Body.String(), badCode.Code, badCode.Body.String())
	}

	// Un desafío obtenido DESPUÉS del reinicio sí se canjea.
	h.gate.set(time.Now().Add(-time.Minute))
	h.clock.Add(60 * time.Second)
	fresh := h.challenge(t)
	w := h.login2fa(fresh, h.code(sec, 0))
	if w.Code != http.StatusOK {
		t.Fatalf("desafío posterior al reinicio: %d %s", w.Code, w.Body.String())
	}
	tok := field(t, w, "token")
	if w2 := doReq(h.r, "GET", "/api/v1/me", "", tok); w2.Code != http.StatusOK {
		t.Fatalf("la sesión del canje nuevo no sirve: %d %s", w2.Code, w2.Body.String())
	}
}

// Fail closed y uniforme: si la compuerta falla, el desafío no se canjea y la
// respuesta es el MISMO 401 (nunca 500, nunca un mensaje distinto).
func TestF15Fix_challengeWithGateErrorGetsUniform401(t *testing.T) {
	h := newTFRevHTTP(t)
	sec := h.enable(t)
	ch := h.challenge(t)
	h.gate.fail(errors.New("base caída"))
	w := h.login2fa(ch, h.code(sec, 0))
	if w.Code != http.StatusUnauthorized || w.Body.String() != `{"error":"Codigo incorrecto o desafio vencido"}` {
		t.Fatalf("compuerta caída: %d %s", w.Code, w.Body.String())
	}
}

// El websocket: abierto con una sesión válida, el reinicio lo cierra (en unos
// pocos intervalos de re-verificación) y la sesión vieja tampoco puede reconectar.
func TestF15Fix_websocketClosesAfterPasswordReset(t *testing.T) {
	h := newTFRevHTTP(t)
	u := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(h.session)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("no se pudo conectar: %v", err)
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for h.hub.Rooms()[h.salonID] != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := h.hub.Rooms()[h.salonID]; n != 1 {
		t.Fatalf("el socket no entró a la room: %d", n)
	}

	h.gate.set(time.Now()) // el reinicio de contraseña
	readDeadline := time.Now().Add(2 * time.Second)
	for {
		if err := conn.SetReadDeadline(readDeadline); err != nil {
			t.Fatalf("deadline: %v", err)
		}
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("el socket siguió abierto después del reinicio")
		}
		break
	}
	// La sesión anterior al cambio ya no abre un socket (handshake 401).
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		t.Fatal("la sesión revocada reabrió el socket")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, got %+v (%v)", resp, err)
	}
}
