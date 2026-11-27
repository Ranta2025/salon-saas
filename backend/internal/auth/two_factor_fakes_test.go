package auth

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeTwoFactorRepo imita pgstore.TwoFactor: cada método es UNA sección crítica
// (la «sentencia atómica» de la base) con la misma condición que el SQL real:
// bloqueo y contador en BeginAttempt, `last_step < step`, `used_at IS NULL`.
type fakeTwoFactorRepo struct {
	mu    sync.Mutex
	now   func() time.Time
	rows  map[string]*fakeTotpRow
	codes map[string]map[string]bool // user -> hash -> usado

	beginCalls   int // BeginAttempt llamados
	beginGranted int // intentos reservados (= verificaciones de código posibles)
	findErr      error
	disableCalls int
}

type fakeTotpRow struct {
	secretEnc   []byte
	enabled     bool
	lastStep    int64
	failed      int
	lockedUntil time.Time
}

func newFakeTwoFactorRepo(now func() time.Time) *fakeTwoFactorRepo {
	return &fakeTwoFactorRepo{now: now, rows: map[string]*fakeTotpRow{}, codes: map[string]map[string]bool{}}
}

func (f *fakeTwoFactorRepo) Find(userID string) (domain.TwoFactorState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return domain.TwoFactorState{}, f.findErr
	}
	r, ok := f.rows[userID]
	if !ok {
		return domain.TwoFactorState{}, domain.ErrNotFound
	}
	return domain.TwoFactorState{SecretEnc: append([]byte(nil), r.secretEnc...), Enabled: r.enabled, LastStep: r.lastStep}, nil
}

func (f *fakeTwoFactorRepo) SavePending(userID string, secretEnc []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[userID]; ok && r.enabled {
		return domain.ErrAlreadyExists
	}
	f.rows[userID] = &fakeTotpRow{secretEnc: append([]byte(nil), secretEnc...)}
	return nil
}

func (f *fakeTwoFactorRepo) Enable(userID string, secretEnc []byte, step int64, hashes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[userID]
	if !ok {
		return domain.ErrNotFound
	}
	if r.enabled {
		return domain.ErrAlreadyExists
	}
	if !bytes.Equal(r.secretEnc, secretEnc) {
		return domain.ErrNotFound
	}
	r.enabled, r.lastStep, r.failed, r.lockedUntil = true, step, 0, time.Time{}
	f.codes[userID] = map[string]bool{}
	for _, h := range hashes {
		f.codes[userID][h] = false
	}
	return nil
}

func (f *fakeTwoFactorRepo) locked(r *fakeTotpRow) bool {
	return !r.lockedUntil.IsZero() && r.lockedUntil.After(f.now())
}

func (f *fakeTwoFactorRepo) BeginAttempt(userID string) (domain.TwoFactorAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beginCalls++
	r, ok := f.rows[userID]
	if !ok || !r.enabled {
		return domain.TwoFactorAttempt{}, domain.ErrNotFound
	}
	if f.locked(r) {
		return domain.TwoFactorAttempt{}, domain.ErrTooManyAttempts
	}
	if !r.lockedUntil.IsZero() { // bloqueo vencido: cuenta desde cero
		r.failed, r.lockedUntil = 0, time.Time{}
	}
	r.failed++
	if r.failed >= domain.TwoFactorMaxFailures {
		r.lockedUntil = f.now().Add(domain.TwoFactorLockDuration)
	}
	f.beginGranted++
	return domain.TwoFactorAttempt{SecretEnc: append([]byte(nil), r.secretEnc...), LastStep: r.lastStep}, nil
}

func (f *fakeTwoFactorRepo) CompleteStep(userID string, step int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[userID]
	if !ok || r.lastStep >= step {
		return false, nil
	}
	r.lastStep, r.failed, r.lockedUntil = step, 0, time.Time{}
	return true, nil
}

func (f *fakeTwoFactorRepo) UseRecoveryCode(userID, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used, ok := f.codes[userID][hash]
	if !ok || used {
		return false, nil
	}
	f.codes[userID][hash] = true
	if r, ok := f.rows[userID]; ok {
		r.failed, r.lockedUntil = 0, time.Time{}
	}
	return true, nil
}

func (f *fakeTwoFactorRepo) Disable(userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disableCalls++
	if _, ok := f.rows[userID]; !ok {
		return domain.ErrNotFound
	}
	delete(f.rows, userID)
	delete(f.codes, userID)
	return nil
}

var _ ports.TwoFactorRepository = (*fakeTwoFactorRepo)(nil)

// fakeChallenges: «ch:<uid>» es un desafío; «expired:<uid>» uno vencido; un
// token de sesión («tok:») NO es un desafío (como el parser real).
type fakeChallenges struct{}

func (fakeChallenges) IssueChallenge(userID string) (string, error) { return "ch:" + userID, nil }
func (fakeChallenges) ParseChallenge(t string) (string, error) {
	if !strings.HasPrefix(t, "ch:") || len(t) == 3 {
		return "", domain.ErrUnauthorized
	}
	return strings.TrimPrefix(t, "ch:"), nil
}

var _ ports.ChallengeIssuer = fakeChallenges{}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var errTwoFactorFindDown = errors.New("base caída")
