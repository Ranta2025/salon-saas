package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil"
)

type recFix struct {
	st     *f15Store
	mailer *testutil.FakeMailer
	run    *deferredRunner
	svc    AccountRecoveryService
}

func newRecFix() *recFix {
	st := newF15Store()
	m := &testutil.FakeMailer{}
	r := &deferredRunner{}
	return &recFix{st: st, mailer: m, run: r, svc: AccountRecoveryService{
		Users: st, Resets: st, Hasher: testutil.FakeHasher{}, Mailer: m, AppURL: "https://app.test",
		Run: r.Run, Now: st.clock.Now,
	}}
}

// issue pide el restablecer de ana y devuelve el token del correo.
func (f *recFix) issue(t *testing.T) string {
	t.Helper()
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatalf("Forgot: %v", err)
	}
	f.run.Flush()
	path, tok := lastLinkToken(t, f.mailer)
	if path != "restablecer" {
		t.Fatalf("ruta del enlace: %q", path)
	}
	return tok
}

func f15WantMsg(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("error = %v, esperaba (%v) %q", err, kind, text)
	}
}

// D2 (regla 1 anti-enumeración): Forgot responde ANTES de hacer el trabajo. El
// runner registra y difiere: al volver Forgot no hubo ni búsqueda de usuario, ni
// token, ni correo; todo ocurre recién al correr el trabajo.
func TestForgot_respondsBeforeAnyWorkRuns(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatalf("Forgot: %v", err)
	}
	if f.run.Pending() != 1 {
		t.Fatalf("debió encolar UN trabajo, hay %d", f.run.Pending())
	}
	if f.st.findByEmail != 0 || len(f.st.resets) != 0 || f.mailer.Count() != 0 {
		t.Fatalf("el trabajo corrió antes de responder: lookups=%d resets=%d mails=%d",
			f.st.findByEmail, len(f.st.resets), f.mailer.Count())
	}
	f.run.Flush()
	if f.st.findByEmail != 1 || len(f.st.resets) != 1 || f.mailer.Count() != 1 {
		t.Fatalf("tras correr el trabajo: lookups=%d resets=%d mails=%d",
			f.st.findByEmail, len(f.st.resets), f.mailer.Count())
	}
}

// D2: para un email desconocido el resultado visible es el mismo (nil, un
// trabajo encolado, nada hecho antes) y el trabajo no crea nada ni envía nada.
func TestForgot_unknownEmailLooksIdentical(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	for _, email := range []string{"ana@mail.com", "nadie@mail.com"} {
		if err := f.svc.Forgot(email); err != nil {
			t.Fatalf("%s: %v", email, err)
		}
	}
	if f.run.Pending() != 2 || f.st.findByEmail != 0 {
		t.Fatalf("ambos deben encolar y no tocar nada antes: pending=%d lookups=%d", f.run.Pending(), f.st.findByEmail)
	}
	f.run.Flush()
	if len(f.st.resets) != 1 || f.mailer.Count() != 1 || f.mailer.Sent[0].To != "ana@mail.com" {
		t.Fatalf("solo la conocida emite y envía: resets=%d mails=%d", len(f.st.resets), f.mailer.Count())
	}
}

// El email se normaliza (espacios y mayúsculas) y se valida SIN tocar nada.
func TestForgot_normalizesAndValidatesEmail(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	if err := f.svc.Forgot("  ANA@Mail.com "); err != nil {
		t.Fatal(err)
	}
	f.run.Flush()
	if f.mailer.Count() != 1 {
		t.Fatalf("no normalizó el email: %d correos", f.mailer.Count())
	}
	for _, bad := range []string{"", "sin-arroba", "a b@x.com", "x@y.com,evil@z.com", strings.Repeat("a", 300) + "@x.com"} {
		f2 := newRecFix()
		err := f2.svc.Forgot(bad)
		if !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%q: esperaba ErrValidation, dio %v", bad, err)
		}
		if f2.run.Pending() != 0 {
			t.Errorf("%q: un email inválido no debe encolar trabajo", bad)
		}
	}
}

// D2 (regla 2): el token se guarda SOLO como SHA-256, el enlace va en el
// FRAGMENTO, el asunto es el del contrato y el HTML escapa el enlace.
func TestForgot_storesOnlyHashAndMailsFragmentLink(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	tok := f.issue(t)
	if len(f.st.resets) != 1 {
		t.Fatalf("resets: %d", len(f.st.resets))
	}
	r := f.st.resets[0]
	if r.TokenHash != domain.HashRecoveryToken(tok) || r.TokenHash == tok {
		t.Fatalf("hash guardado %q para el token %q", r.TokenHash, tok)
	}
	if containsAny(fmt.Sprintf("%+v", *r), tok) {
		t.Fatal("el token en claro está en lo guardado")
	}
	if !r.ExpiresAt.Equal(f.st.now().Add(60 * time.Minute)) {
		t.Errorf("vence a los 60 minutos: %v", r.ExpiresAt)
	}
	msg := f.mailer.Sent[0]
	if msg.Subject != "Restablece tu contrasena" || msg.To != "ana@mail.com" {
		t.Errorf("asunto/destino: %q %q", msg.Subject, msg.To)
	}
	link := "https://app.test/restablecer#" + tok
	if !strings.Contains(msg.Text, link) || !strings.Contains(msg.HTML, `href="`+link+`"`) {
		t.Errorf("falta el enlace con el token en el fragmento:\n%s\n%s", msg.Text, msg.HTML)
	}
	if strings.Contains(msg.Text, "?") && strings.Contains(strings.Split(msg.Text, "#"+tok)[0], "restablecer?") {
		t.Error("el token no puede ir en el query")
	}
	if !strings.Contains(msg.Text, "60 minutos") {
		t.Errorf("el correo dice cuánto dura el enlace:\n%s", msg.Text)
	}
}

// El HTML se escapa: un APP_URL con caracteres especiales no inyecta marcado.
func TestForgot_htmlEscapesTheLink(t *testing.T) {
	f := newRecFix()
	f.svc.AppURL = `https://app.test"><script>x</script>`
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatal(err)
	}
	f.run.Flush()
	if f.mailer.Count() == 0 {
		t.Fatal("no se envió el correo")
	}
	if strings.Contains(f.mailer.Sent[0].HTML, "<script>") {
		t.Errorf("el HTML no escapa el enlace:\n%s", f.mailer.Sent[0].HTML)
	}
}

// D2: un vigente por usuario. Al emitir otro, el anterior queda invalidado.
func TestForgot_newOneInvalidatesThePreviousOne(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	first := f.issue(t)
	f.st.clock.Add(61 * time.Second)
	second := f.issue(t)
	if first == second {
		t.Fatal("tokens repetidos")
	}
	if err := f.svc.Reset(first, "nuevaClave1"); !errors.Is(err, domain.ErrValidation) || err.Error() != domain.MsgLinkInvalid {
		t.Fatalf("el primero debió quedar invalidado: %v", err)
	}
	if err := f.svc.Reset(second, "nuevaClave1"); err != nil {
		t.Fatalf("el segundo sirve: %v", err)
	}
}

// D2 throttle por usuario: dentro de los 60 s no se emite ni se envía (silencioso
// y la respuesta es la misma); a los 60 s sí; 5 en la hora y no hay sexto.
func TestForgot_throttlePerUser(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	f.st.addUser("u-bea", "Bea", "bea@mail.com", true)
	f.issue(t)
	f.st.clock.Add(59 * time.Second)
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatalf("el throttle es silencioso: %v", err)
	}
	f.run.Flush()
	if f.mailer.Count() != 1 || len(f.st.resets) != 1 {
		t.Fatalf("a los 59 s no debe emitir: mails=%d resets=%d", f.mailer.Count(), len(f.st.resets))
	}
	// Otro usuario no se ve afectado.
	if err := f.svc.Forgot("bea@mail.com"); err != nil {
		t.Fatal(err)
	}
	f.run.Flush()
	if f.mailer.Count() != 2 {
		t.Fatalf("el throttle es por usuario: %d correos", f.mailer.Count())
	}
	// A los 60 s exactos del primero ya se puede (2.º de ana).
	f.st.clock.Add(1 * time.Second)
	f.issue(t)
	// 3.º, 4.º y 5.º, cada uno a 61 s del anterior: 5 en la hora.
	for i := 0; i < 3; i++ {
		f.st.clock.Add(61 * time.Second)
		f.issue(t)
	}
	before := f.mailer.Count()
	f.st.clock.Add(61 * time.Second) // 6.º dentro de la misma hora
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatal(err)
	}
	f.run.Flush()
	if f.mailer.Count() != before {
		t.Fatalf("el 6.º en la hora no debe enviarse (%d -> %d)", before, f.mailer.Count())
	}
	// Pasada la hora desde los primeros, vuelve a andar.
	f.st.clock.Add(time.Hour)
	f.issue(t)
}

// D2: con SMTP deshabilitado no se envía, no se emite token y no se loguea nada
// de la dirección; el resultado sigue siendo el mismo (nil).
func TestForgot_smtpDisabledDoesNothing(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	f := newRecFix()
	f.svc.Mailer = nil
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatal(err)
	}
	f.run.Flush()
	if len(f.st.resets) != 0 {
		t.Errorf("sin SMTP no se emite token: %d", len(f.st.resets))
	}
	if strings.Contains(buf.String(), "ana@mail.com") {
		t.Errorf("se logueó la dirección: %q", buf.String())
	}
}

// Regla 2: ni el token ni la dirección aparecen en el log, ni cuando el envío
// falla con un error que trae la dirección.
func TestForgot_neverLogsAddressOrToken(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	f := newRecFix()
	secret := strings.Repeat("S", 43)
	f.svc.GenToken = func() (string, error) { return secret, nil }
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	f.mailer.Err = errors.New("550 <ana@mail.com> rechazado, enlace #" + secret)
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatal(err)
	}
	f.run.Flush()
	f.st.failFindEmail = errors.New("base caída para ana@mail.com")
	_ = f.svc.Forgot("ana@mail.com")
	f.run.Flush()
	if out := buf.String(); out == "" {
		t.Error("el fallo de envío debería dejar una línea de log (sin datos sensibles)")
	} else if containsAny(out, "ana@mail.com", secret, domain.HashRecoveryToken(secret)) {
		t.Errorf("el log filtra la dirección o el token: %q", out)
	}
}

// Un fallo de la base dentro del trabajo no tumba nada ni cambia el resultado.
func TestForgot_jobErrorsAreSwallowed(t *testing.T) {
	f := newRecFix()
	f.st.failFindEmail = errors.New("base caída")
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatal(err)
	}
	f.run.Flush() // no entra en pánico ni devuelve nada
}

// El runner por defecto es una goroutine con timeout y recover.
func TestGoAsync_runsAfterReturnRecoversAndTimesOut(t *testing.T) {
	run := kernel.GoAsync(50 * time.Millisecond)
	done := make(chan struct{})
	release := make(chan struct{})
	run(func(ctx context.Context) {
		<-release
		close(done)
	})
	select {
	case <-done:
		t.Fatal("el trabajo corrió antes de volver (no es asíncrono)")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("el trabajo no corrió")
	}
	// Un pánico no tumba el proceso y el runner sigue andando.
	run(func(context.Context) { panic("boom") })
	ok := make(chan struct{})
	run(func(context.Context) { close(ok) })
	select {
	case <-ok:
	case <-time.After(2 * time.Second):
		t.Fatal("el runner murió tras un pánico")
	}
	// El contexto trae el timeout.
	expired := make(chan error, 1)
	run(func(ctx context.Context) {
		select {
		case <-ctx.Done():
			expired <- ctx.Err()
		case <-time.After(2 * time.Second):
			expired <- nil
		}
	})
	if err := <-expired; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("el contexto debía vencer: %v", err)
	}
}

// Con Run nil el servicio usa el runner por defecto (goroutine): no se cuelga.
func TestForgot_nilRunnerUsesGoroutine(t *testing.T) {
	f := newRecFix()
	f.svc.Run = nil
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	if err := f.svc.Forgot("ana@mail.com"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.mailer.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.mailer.Count() != 1 {
		t.Fatal("el runner por defecto no envió el correo")
	}
}

// D3: todos los errores del canje.
func TestReset_errors(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	tok := f.issue(t)
	good := "nuevaClave1"

	// Formato inválido o desconocido -> el mismo texto uniforme.
	for name, bad := range map[string]string{
		"vacío": "", "corto": "abc", "con NUL": strings.Repeat("A", 42) + "\x00",
		"bien formado pero desconocido": strings.Repeat("A", 43),
	} {
		f15WantMsg(t, f.svc.Reset(bad, good), domain.ErrValidation, "Enlace invalido o vencido")
		_ = name
	}
	// Contraseña inválida: su mensaje y el token NO se consume.
	pwErr := f.svc.Reset(tok, "corta")
	if !errors.Is(pwErr, domain.ErrValidation) || pwErr.Error() != domain.ValidatePassword("corta").Error() {
		t.Fatalf("contraseña corta: %v", pwErr)
	}
	if err := f.svc.Reset(tok, strings.Repeat("x", 73)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("contraseña larga: %v", err)
	}
	if f.st.resets[0].UsedAt != nil || f.st.users["u-ana"].PasswordHash != "h:vieja-clave" {
		t.Fatal("una contraseña inválida consumió el token o cambió la clave")
	}
	// Con token desconocido Y contraseña inválida manda el enlace (primero el token).
	f15WantMsg(t, f.svc.Reset(strings.Repeat("A", 43), "corta"), domain.ErrValidation, "Enlace invalido o vencido")
	// Ahora sí.
	if err := f.svc.Reset(tok, good); err != nil {
		t.Fatalf("canje válido: %v", err)
	}
	// Usado: el mismo texto.
	f15WantMsg(t, f.svc.Reset(tok, good), domain.ErrValidation, "Enlace invalido o vencido")
}

func TestReset_expiredIsTheSameError(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	tok := f.issue(t)
	f.st.clock.Add(60*time.Minute - time.Second)
	// Un segundo antes de vencer todavía sirve para la lectura previa...
	if _, err := f.st.FindReset(domain.HashRecoveryToken(tok)); err != nil {
		t.Fatal(err)
	}
	f.st.clock.Add(time.Second)
	f15WantMsg(t, f.svc.Reset(tok, "nuevaClave1"), domain.ErrValidation, "Enlace invalido o vencido")
	if f.st.users["u-ana"].PasswordHash != "h:vieja-clave" {
		t.Fatal("un token vencido cambió la clave")
	}
}

// D3: éxito. Cambia la clave, marca password_changed_at, verifica el email (quien
// lee el correo demostró que es dueño) e invalida los demás pedidos. NO emite
// sesión (el método devuelve solo error).
func TestReset_success(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", false) // sin verificar
	tok := f.issue(t)
	f.st.clock.Add(5 * time.Minute)
	if err := f.svc.Reset(tok, "nuevaClave1"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	u := f.st.users["u-ana"]
	if u.PasswordHash != "h:nuevaClave1" {
		t.Errorf("clave: %q", u.PasswordHash)
	}
	if f.st.changedAt["u-ana"] == nil || !f.st.changedAt["u-ana"].Equal(f.st.now()) {
		t.Errorf("password_changed_at: %v", f.st.changedAt["u-ana"])
	}
	if u.EmailVerifiedAt == nil {
		t.Error("el reinicio debe verificar el email")
	}
}

// Un fallo del repositorio al canjear (no ErrNotFound) no se disfraza de enlace
// inválido: es un error de infraestructura.
type failingConsume struct{ *f15Store }

func (failingConsume) ConsumeReset(string, string) error { return errors.New("db down") }

func TestReset_infraErrorIsNotMaskedAsInvalidLink(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	tok := f.issue(t)
	f.svc.Resets = failingConsume{f.st}
	err := f.svc.Reset(tok, "nuevaClave1")
	if err == nil || errors.Is(err, domain.ErrValidation) {
		t.Fatalf("esperaba el error de infraestructura, dio %v", err)
	}
}

// Concurrencia a nivel de servicio (con el fake que respeta el canje condicional):
// 30 canjes del mismo token, exactamente uno gana.
func TestReset_thirtyParallelRedeemsOneWins(t *testing.T) {
	f := newRecFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", true)
	tok := f.issue(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := f.svc.Reset(tok, fmt.Sprintf("nuevaClave%02d", i)); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("ganadores: %d", wins)
	}
}

// --- AuthenticateSession (D4) ---

type sessParser struct {
	claims domain.SessionClaims
	err    error
}

func (sessParser) Issue(string) (string, error)   { return "", nil }
func (p sessParser) Parse(string) (string, error) { return p.claims.UserID, p.err }
func (p sessParser) ParseSession(string) (domain.SessionClaims, error) {
	return p.claims, p.err
}

type plainParser struct{}

func (plainParser) Issue(string) (string, error) { return "", nil }
func (plainParser) Parse(t string) (string, error) {
	if t == "" {
		return "", domain.ErrUnauthorized
	}
	return "u1", nil
}

type gateFake struct {
	at  *time.Time
	err error
}

func (g gateFake) PasswordChangedAt(string) (*time.Time, error) { return g.at, g.err }

var (
	_ ports.SessionTokenParser = sessParser{}
	_ ports.SessionGate        = gateFake{}
)

func TestAuthenticateSession(t *testing.T) {
	changed := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ms := changed.UnixMilli()
	tokens := func(ia int64, has bool) ports.TokenIssuer {
		return sessParser{claims: domain.SessionClaims{UserID: "u1", IssuedAtMs: ia, HasIA: has}}
	}
	unauth := func(err error) bool { return errors.Is(err, domain.ErrUnauthorized) }

	// Sin compuerta = comportamiento de siempre (solo Parse).
	if uid, err := AuthenticateSession(plainParser{}, nil, "x"); err != nil || uid != "u1" {
		t.Errorf("sin gate: %q %v", uid, err)
	}
	if _, err := AuthenticateSession(plainParser{}, nil, ""); !unauth(err) {
		t.Errorf("parse falla: %v", err)
	}
	// Contraseña nunca cambiada: vale con o sin ia, también con un TokenIssuer
	// que no implementa ParseSession.
	if uid, err := AuthenticateSession(plainParser{}, gateFake{}, "x"); err != nil || uid != "u1" {
		t.Errorf("sin ParseSession y sin cambio: %q %v", uid, err)
	}
	if uid, err := AuthenticateSession(tokens(0, false), gateFake{}, "x"); err != nil || uid != "u1" {
		t.Errorf("token viejo sin ia, sin cambio: %q %v", uid, err)
	}
	// Con cambio: sin ParseSession = sin ia = inválido.
	if _, err := AuthenticateSession(plainParser{}, gateFake{at: &changed}, "x"); !unauth(err) {
		t.Errorf("sin ia con cambio: %v", err)
	}
	if _, err := AuthenticateSession(tokens(0, false), gateFake{at: &changed}, "x"); !unauth(err) {
		t.Errorf("token viejo con cambio: %v", err)
	}
	if _, err := AuthenticateSession(tokens(ms-1, true), gateFake{at: &changed}, "x"); !unauth(err) {
		t.Errorf("anterior al cambio: %v", err)
	}
	if _, err := AuthenticateSession(tokens(ms, true), gateFake{at: &changed}, "x"); !unauth(err) {
		t.Errorf("mismo ms: %v", err)
	}
	if uid, err := AuthenticateSession(tokens(ms+1, true), gateFake{at: &changed}, "x"); err != nil || uid != "u1" {
		t.Errorf("posterior: %q %v", uid, err)
	}
	// Falla CERRADO: la consulta falla (cualquier error, incluso usuario
	// inexistente) = 401.
	if _, err := AuthenticateSession(tokens(ms+1, true), gateFake{err: errors.New("db down")}, "x"); !unauth(err) {
		t.Errorf("falla de la consulta debe ser 401: %v", err)
	}
	if _, err := AuthenticateSession(tokens(ms+1, true), gateFake{err: domain.ErrNotFound}, "x"); !unauth(err) {
		t.Errorf("usuario inexistente debe ser 401: %v", err)
	}
	// Token que no parsea: 401 sin consultar la compuerta.
	if _, err := AuthenticateSession(sessParser{err: domain.ErrUnauthorized}, gateFake{}, "x"); !unauth(err) {
		t.Errorf("token inválido: %v", err)
	}
}
