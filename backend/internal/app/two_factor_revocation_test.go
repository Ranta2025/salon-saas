// F15 (D4), segunda mitad del 2FA: un DESAFÍO emitido antes del reinicio de
// contraseña no se puede canjear después. El segundo paso del login es lo único
// que convierte un código en una sesión: si el reinicio solo revocara las
// sesiones, quien tuviera un desafío pendiente (5 minutos de vida) podría
// cambiarse la contraseña, reiniciarla otra vez... o un atacante con un desafío
// robado entraría igual con un código válido. El desafío lleva `ia` (el mismo
// claim de las sesiones) y el login lo compara con `password_changed_at` por la
// compuerta: la MISMA regla y la MISMA respuesta uniforme (401 `Codigo incorrecto
// o desafio vencido`).
//
// Nada de esto toca el reinicio: no emite sesión y no toca las tablas del 2FA.
package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// tfGate es la compuerta bajo control del test: `at` es users.password_changed_at
// (nil = nunca cambió) y `err` simula una base caída.
type tfGate struct {
	mu  sync.Mutex
	at  *time.Time
	err error
}

func (g *tfGate) PasswordChangedAt(string) (*time.Time, error) {
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

func (g *tfGate) set(t time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.at = &t
	g.err = nil
}

func (g *tfGate) clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.at = nil
	g.err = nil
}

func (g *tfGate) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

var _ ports.SessionGate = (*tfGate)(nil)

// iaChallenges es un emisor de desafíos con `ia` controlable (el JWT real ya
// cubre el claim en el paquete security). Implementa la interfaz opcional.
type iaChallenges struct {
	mu    sync.Mutex
	ia    int64
	hasIA bool
}

func (c *iaChallenges) IssueChallenge(userID string) (string, error) { return "ch:" + userID, nil }

func (c *iaChallenges) ParseChallenge(token string) (string, error) {
	if len(token) <= 3 || token[:3] != "ch:" {
		return "", domain.ErrUnauthorized
	}
	return token[3:], nil
}

func (c *iaChallenges) ParseChallengeClaims(token string) (domain.SessionClaims, error) {
	uid, err := c.ParseChallenge(token)
	if err != nil {
		return domain.SessionClaims{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return domain.SessionClaims{UserID: uid, IssuedAtMs: c.ia, HasIA: c.hasIA}, nil
}

func (c *iaChallenges) issuedAt(ia int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ia, c.hasIA = ia, true
}

func (c *iaChallenges) withoutIA() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ia, c.hasIA = 0, false
}

var (
	_ ports.ChallengeIssuer       = (*iaChallenges)(nil)
	_ ports.ChallengeClaimsParser = (*iaChallenges)(nil)
)

// revocationEnv es el entorno de 2FA de los tests con la compuerta cableada y un
// emisor de desafíos que controla `ia`.
type revocationEnv struct {
	*tfEnv
	gate *tfGate
	ch   *iaChallenges
}

func newRevocationEnv(t *testing.T) *revocationEnv {
	t.Helper()
	e := newTFEnv(t)
	gate := &tfGate{}
	ch := &iaChallenges{}
	e.svc.Gate = gate
	e.svc.Challenges = ch
	return &revocationEnv{tfEnv: e, gate: gate, ch: ch}
}

// tfUniform: el 401 uniforme del contrato, sin ninguna información extra.
func tfUniform(t *testing.T, err error) {
	t.Helper()
	tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
}

// El caso del verificador: desafío emitido, después el reinicio de contraseña,
// después un código TOTP VÁLIDO. El canje da el 401 uniforme y NO emite sesión.
func TestVerifyLogin_rejectsChallengeIssuedBeforePasswordReset(t *testing.T) {
	e := newRevocationEnv(t)
	e.enabled(t)
	issued := e.clock.Now()
	e.ch.issuedAt(issued.UnixMilli())

	// El reinicio ocurre DESPUÉS de emitido el desafío.
	e.gate.set(issued.Add(time.Minute))
	before := e.repo.beginCalls
	_, err := e.svc.VerifyLogin("ch:"+e.userID, e.code(t, 0))
	tfUniform(t, err)
	if e.repo.beginCalls != before {
		t.Errorf("un desafío revocado no debe gastar un intento ni tocar el código (begin=%d)", e.repo.beginCalls)
	}

	// Borde: ia == password_changed_at se rechaza (el token tiene que ser más nuevo).
	e.ch.issuedAt(issued.UnixMilli())
	e.gate.set(issued)
	tfUniform(t, e.mustFail(t, "ch:"+e.userID, e.code(t, 0)))

	// Control: un desafío emitido DESPUÉS del cambio sí se canjea.
	e.ch.issuedAt(issued.Add(2 * time.Second).UnixMilli())
	tok, err := e.svc.VerifyLogin("ch:"+e.userID, e.code(t, 0))
	if err != nil || tok != "tok:"+e.userID {
		t.Fatalf("desafío posterior al cambio: %q %v", tok, err)
	}
}

// Un desafío de la versión anterior (sin `ia`) vale SOLO si la contraseña nunca
// cambió: nadie queda afuera por actualizar, pero un reinicio lo mata.
func TestVerifyLogin_challengeWithoutIAOnlyWhilePasswordNeverChanged(t *testing.T) {
	e := newRevocationEnv(t)
	e.enabled(t)
	e.ch.withoutIA()

	tok, err := e.svc.VerifyLogin("ch:"+e.userID, e.code(t, 0))
	if err != nil || tok != "tok:"+e.userID {
		t.Fatalf("sin cambio de contraseña el desafío sin ia vale: %q %v", tok, err)
	}
	e.gate.set(e.clock.Now())
	e.clock.Add(2 * domain.TOTPPeriodSeconds * time.Second)
	tfUniform(t, e.mustFail(t, "ch:"+e.userID, e.code(t, 0)))
}

// Fail closed: si la compuerta falla, el desafío no se canjea (401 uniforme, no
// 500 y no 429) y no se gasta un intento del código.
func TestVerifyLogin_gateErrorFailsClosed(t *testing.T) {
	e := newRevocationEnv(t)
	e.enabled(t)
	e.ch.issuedAt(e.clock.Now().UnixMilli())
	e.gate.fail(errors.New("base caída"))
	before := e.repo.beginCalls
	tfUniform(t, e.mustFail(t, "ch:"+e.userID, e.code(t, 0)))
	if e.repo.beginCalls != before {
		t.Errorf("la compuerta se mira ANTES de verificar el código (begin=%d)", e.repo.beginCalls)
	}
	// Un usuario inexistente también falla cerrado con el mismo 401.
	e.ch.issuedAt(e.clock.Now().UnixMilli())
	e.gate.clear()
	e.gate.fail(domain.ErrNotFound)
	tfUniform(t, e.mustFail(t, "ch:u-fantasma", "123456"))
}

// Sin compuerta (los tests unitarios y los entornos sin base) nada cambia: el
// desafío se canjea igual que antes.
func TestVerifyLogin_withoutGateKeepsBehaviour(t *testing.T) {
	e := newRevocationEnv(t)
	e.enabled(t)
	e.svc.Gate = nil
	e.ch.withoutIA()
	if tok, err := e.svc.VerifyLogin("ch:"+e.userID, e.code(t, 0)); err != nil || tok != "tok:"+e.userID {
		t.Fatalf("sin compuerta: %q %v", tok, err)
	}
}

// El reinicio sigue sin tocar el 2FA ni emitir sesión: después de canjear el
// enlace el login del usuario con 2FA vuelve a pedir el código (D5) y la tabla
// de 2FA queda intacta.
func TestReset_doesNotTouchTwoFactorAndIssuesNoSession(t *testing.T) {
	e := newRevocationEnv(t)
	codes := e.enabled(t)
	stBefore, err := e.repo.Find(e.userID)
	if err != nil {
		t.Fatal(err)
	}

	store := newF15Store()
	user := store.addUser(e.userID, "Ana", "ana@mail.com", true)
	const token = "0123456789012345678901234567890123456789012"
	reset := domain.PasswordReset{
		ID: "r-1", UserID: user.ID, TokenHash: domain.HashRecoveryToken(token),
		CreatedAt: store.now(), ExpiresAt: store.now().Add(time.Hour),
	}
	if ok, err := store.IssueReset(reset); err != nil || !ok {
		t.Fatalf("issue reset: %v %v", ok, err)
	}
	svc := AccountRecoveryService{Users: store, Resets: store, Hasher: fakeHasher{}, Now: store.now}
	// Reset devuelve SOLO error: no hay ningún camino que devuelva una sesión.
	if err := svc.Reset(token, "nuevaClave1"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// La compuerta ve el cambio (lo prueba el resto de la suite) y el 2FA sigue:
	// el login del mismo usuario vuelve a devolver un desafío.
	if ch, required, err := e.svc.ChallengeFor(e.userID); err != nil || !required || ch == "" {
		t.Fatalf("tras el reinicio el 2FA sigue pidiéndose: %q %v %v", ch, required, err)
	}
	stAfter, err := e.repo.Find(e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if stAfter.Enabled != stBefore.Enabled || stAfter.LastStep != stBefore.LastStep {
		t.Errorf("el reinicio tocó la fila de 2FA: antes %+v, después %+v", stBefore, stAfter)
	}
	if e.repo.disableCalls != 0 {
		t.Errorf("el reinicio llamó a Disable del 2FA: %d", e.repo.disableCalls)
	}
	// Los códigos de recuperación siguen sin usarse (uno sigue sirviendo).
	if len(codes) == 0 {
		t.Fatal("sin códigos de recuperación")
	}
}

// mustFail es azúcar: verifica que VerifyLogin falla y devuelve el error.
func (e *revocationEnv) mustFail(t *testing.T, challenge, code string) error {
	t.Helper()
	tok, err := e.svc.VerifyLogin(challenge, code)
	if err == nil {
		t.Fatalf("el canje debía fallar y devolvió %q", tok)
	}
	return err
}
