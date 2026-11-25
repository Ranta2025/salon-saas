// Concurrencia con Postgres real del 2FA (F10, D5 y D9). Se saltea sin
// SALON_IT=1. Son las pruebas que un mutante tiene que romper: sin la condición
// de bloqueo del UPDATE de BeginAttempt, sin el `last_step < paso` o sin el
// `used_at IS NULL`, pedidos simultáneos pasarían de 5 verificaciones o se
// llevarían el mismo código dos veces.
package pgstore

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// tfBigPool: 40 conexiones para que los 30 pedidos lleguen DE VERDAD a la vez.
func tfBigPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SALON_IT") == "" {
		t.Skip("SALON_IT no definido: test de integración salteado")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns, cfg.MinConns = 40, 40
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// countingRepo cuenta las verificaciones de código que el repositorio dejó
// hacer: un BeginAttempt que NO devolvió error es una verificación posible.
type countingRepo struct {
	ports.TwoFactorRepository
	granted atomic.Int64
	locked  atomic.Int64
}

func (c *countingRepo) BeginAttempt(userID string) (domain.TwoFactorAttempt, error) {
	att, err := c.TwoFactorRepository.BeginAttempt(userID)
	switch {
	case err == nil:
		c.granted.Add(1)
	case errors.Is(err, domain.ErrTooManyAttempts):
		c.locked.Add(1)
	}
	return att, err
}

type tfStack struct {
	*tfFix
	svc     app.TwoFactorService
	counter *countingRepo
	box     domain.TOTPSecretBox
	pass    string
}

// tfStackFor arma el servicio REAL (repo Postgres + JWT + bcrypt) con 2FA activo.
func tfStackFor(t *testing.T) *tfStack {
	t.Helper()
	f := tfFixtures(t)
	pool := tfBigPool(t)
	f.pool = pool
	f.repo = TwoFactor{Pool: pool}
	hash, err := bcrypt.GenerateFromPassword([]byte("clave-secreta-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(f.ctx, `UPDATE users SET pass_hash=$1 WHERE id=$2`, string(hash), tfUser); err != nil {
		t.Fatal(err)
	}
	box, err := domain.NewTOTPSecretBox("jwt-secret-de-prueba-it")
	if err != nil {
		t.Fatal(err)
	}
	cnt := &countingRepo{TwoFactorRepository: f.repo}
	jwtIssuer := security.JWTIssuer{Secret: "jwt-secret-de-prueba-it"}
	svc := app.TwoFactorService{
		Repo: cnt, Users: Users{Pool: pool}, Hasher: security.BcryptHasher{Cost: bcrypt.MinCost},
		Tokens: jwtIssuer, Challenges: jwtIssuer, Box: box, Memberships: Memberships{Pool: pool},
		Audit: app.AuditService{Events: Audit{Pool: pool}, Users: Users{Pool: pool}},
	}
	return &tfStack{tfFix: f, svc: svc, counter: cnt, box: box, pass: "clave-secreta-1"}
}

// enroll hace setup + enable reales y devuelve el secreto y los códigos de
// recuperación. El código de activación usa el paso ACTUAL; los logins del test
// usan el paso SIGUIENTE (dentro de la ventana ±1) para no chocar con last_step.
func (s *tfStack) enroll(t *testing.T) (secret []byte, recovery []string) {
	t.Helper()
	if _, err := s.svc.Setup(tfUser); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, err := s.repo.Find(tfUser)
	if err != nil {
		t.Fatal(err)
	}
	secret, err = s.box.Open(st.SecretEnc, tfUser)
	if err != nil {
		t.Fatal(err)
	}
	cur := domain.TOTPStepAt(time.Now())
	recovery, err = s.svc.Enable(tfUser, domain.TOTPCode(secret, cur-1, domain.TOTPDigits))
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	// Enable dejó last_step = cur-1: el paso `cur` y `cur+1` sirven para entrar.
	return secret, recovery
}

func (s *tfStack) challenge(t *testing.T) string {
	t.Helper()
	ch, required, err := s.svc.ChallengeFor(tfUser)
	if err != nil || !required {
		t.Fatalf("desafío: %q %v %v", ch, required, err)
	}
	return ch
}

// D9: 30 logins paralelos con un código malo => nunca más de 5 verificaciones,
// failed_count consistente (5), bloqueo puesto, y los otros 25 reciben 429.
func TestTwoFactorConcurrency_wrongCodesNeverMoreThanFiveVerifications(t *testing.T) {
	s := tfStackFor(t)
	s.enroll(t)
	ch := s.challenge(t)
	var unauth, locked, other atomic.Int64
	race(30, func(int) {
		_, err := s.svc.VerifyLogin(ch, "000000")
		switch {
		case errors.Is(err, domain.ErrTooManyAttempts):
			locked.Add(1)
		case errors.Is(err, domain.ErrUnauthorized):
			unauth.Add(1)
		default:
			other.Add(1)
		}
	})
	if got := s.counter.granted.Load(); got > domain.TwoFactorMaxFailures {
		t.Fatalf("se verificaron %d códigos (máximo %d)", got, domain.TwoFactorMaxFailures)
	}
	if unauth.Load() != 5 || locked.Load() != 25 || other.Load() != 0 {
		t.Errorf("401=%d 429=%d otros=%d (esperado 5/25/0)", unauth.Load(), locked.Load(), other.Load())
	}
	if _, _, failed, isLocked := s.row(t, tfUser); failed != 5 || !isLocked {
		t.Errorf("estado en la base: failed_count=%d locked=%v (esperado 5/true)", failed, isLocked)
	}
	// Bloqueado: AUN con el código correcto (paso siguiente) responde 429.
	secret, _ := s.openSecret(t)
	good := domain.TOTPCode(secret, domain.TOTPStepAt(time.Now())+1, domain.TOTPDigits)
	if _, err := s.svc.VerifyLogin(ch, good); !errors.Is(err, domain.ErrTooManyAttempts) {
		t.Fatalf("bloqueado con el código correcto: %v", err)
	}
}

func (s *tfStack) openSecret(t *testing.T) ([]byte, int64) {
	t.Helper()
	st, err := s.repo.Find(tfUser)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := s.box.Open(st.SecretEnc, tfUser)
	if err != nil {
		t.Fatal(err)
	}
	return sec, st.LastStep
}

// D5/D9: el MISMO código TOTP usado a la vez en 30 logins => exactamente UN
// acierto (el `last_step < paso` del UPDATE), y el replay cuenta como fallo.
func TestTwoFactorConcurrency_sameTOTPCodeExactlyOneSuccess(t *testing.T) {
	s := tfStackFor(t)
	secret, _ := s.enroll(t)
	ch := s.challenge(t)
	code := domain.TOTPCode(secret, domain.TOTPStepAt(time.Now())+1, domain.TOTPDigits)
	var ok atomic.Int64
	race(30, func(int) {
		if tok, err := s.svc.VerifyLogin(ch, code); err == nil && tok != "" {
			ok.Add(1)
		}
	})
	if ok.Load() != 1 {
		t.Fatalf("%d aciertos con el mismo código TOTP (esperado exactamente 1)", ok.Load())
	}
	if _, step, _, _ := s.row(t, tfUser); step != domain.TOTPStepAt(time.Now())+1 {
		t.Errorf("last_step=%d", step)
	}
}

// Nivel repositorio, sin el tope de 5: 30 CompleteStep del mismo paso => 1 gana.
func TestTwoFactorConcurrency_completeStepOneWinner(t *testing.T) {
	s := tfStackFor(t)
	s.enroll(t)
	var won atomic.Int64
	race(30, func(int) {
		if ok, err := s.repo.CompleteStep(tfUser, 4_000_000_000); err == nil && ok {
			won.Add(1)
		}
	})
	if won.Load() != 1 {
		t.Fatalf("%d ganadores del mismo paso (esperado 1)", won.Load())
	}
}

// D6/D9: 30 usos simultáneos de UN código de recuperación => exactamente UN
// acierto, en el repositorio (`used_at IS NULL`) y a través del login.
func TestTwoFactorConcurrency_recoveryCodeExactlyOneSuccess(t *testing.T) {
	s := tfStackFor(t)
	_, recovery := s.enroll(t)
	norm, _ := domain.NormalizeRecoveryCode(recovery[0])
	hash := domain.HashRecoveryCode(norm)

	var won atomic.Int64
	race(30, func(int) {
		if ok, err := s.repo.UseRecoveryCode(tfUser, hash); err == nil && ok {
			won.Add(1)
		}
	})
	if won.Load() != 1 {
		t.Fatalf("repositorio: %d usos del mismo código (esperado 1)", won.Load())
	}
	if n := s.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND used_at IS NOT NULL`, tfUser); n != 1 {
		t.Errorf("filas usadas: %d", n)
	}

	// Por el login (con el tope de intentos de por medio).
	ch := s.challenge(t)
	var logged atomic.Int64
	race(30, func(int) {
		if tok, err := s.svc.VerifyLogin(ch, recovery[1]); err == nil && tok != "" {
			logged.Add(1)
		}
	})
	if logged.Load() != 1 {
		t.Fatalf("login: %d usos del mismo código de recuperación (esperado 1)", logged.Load())
	}
}

// Un acierto reinicia el contador aunque haya fallos previos (4 fallos + acierto
// + 4 fallos no bloquea), con la base real.
func TestTwoFactorConcurrency_successResetsCounter(t *testing.T) {
	s := tfStackFor(t)
	secret, _ := s.enroll(t)
	ch := s.challenge(t)
	cur := domain.TOTPStepAt(time.Now())
	for round, step := range []int64{cur, cur + 1} {
		for i := 0; i < 4; i++ {
			if _, err := s.svc.VerifyLogin(ch, "000000"); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("ronda %d fallo %d: %v", round, i, err)
			}
		}
		if _, err := s.svc.VerifyLogin(ch, domain.TOTPCode(secret, step, domain.TOTPDigits)); err != nil {
			t.Fatalf("ronda %d: 4 fallos + acierto no debe bloquear: %v", round, err)
		}
		if _, _, failed, locked := s.row(t, tfUser); failed != 0 || locked {
			t.Errorf("ronda %d: contador sin reiniciar: %d %v", round, failed, locked)
		}
	}
}

// Disable real extremo a extremo: contraseña (bcrypt), código TOTP, borrado y
// auditoría opcional sin salones (no escribe evento).
func TestTwoFactorConcurrency_disableEndToEnd(t *testing.T) {
	s := tfStackFor(t)
	secret, _ := s.enroll(t)
	if err := s.svc.Disable(tfUser, "mala", "000000"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("contraseña mala: %v", err)
	}
	if _, _, failed, _ := s.row(t, tfUser); failed != 0 {
		t.Errorf("la contraseña mala no cuenta intentos: %d", failed)
	}
	if err := s.svc.Disable(tfUser, s.pass, "000000"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("código malo: %v", err)
	}
	code := domain.TOTPCode(secret, domain.TOTPStepAt(time.Now())+1, domain.TOTPDigits)
	if err := s.svc.Disable(tfUser, s.pass, code); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if n := s.count(t, `SELECT count(*) FROM user_totp WHERE user_id=$1`, tfUser) +
		s.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser); n != 0 {
		t.Errorf("quedó 2FA: %d filas", n)
	}
}
