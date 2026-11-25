package httpapi

import (
	"bytes"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// mtf imita pgstore.TwoFactor para los tests HTTP: cada método es UNA sección
// crítica con la misma condición que el SQL real (bloqueo, last_step < paso,
// used_at IS NULL). La concurrencia contra Postgres real se prueba en pgstore.
type mtf struct {
	mu    sync.Mutex
	now   func() time.Time
	rows  map[string]*mtfRow
	codes map[string]map[string]bool
}

type mtfRow struct {
	secretEnc   []byte
	enabled     bool
	lastStep    int64
	failed      int
	lockedUntil time.Time
}

func newMTF(now func() time.Time) *mtf {
	return &mtf{now: now, rows: map[string]*mtfRow{}, codes: map[string]map[string]bool{}}
}

func (f *mtf) Find(userID string) (domain.TwoFactorState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[userID]
	if !ok {
		return domain.TwoFactorState{}, domain.ErrNotFound
	}
	return domain.TwoFactorState{SecretEnc: append([]byte(nil), r.secretEnc...), Enabled: r.enabled, LastStep: r.lastStep}, nil
}

func (f *mtf) SavePending(userID string, enc []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[userID]; ok && r.enabled {
		return domain.ErrAlreadyExists
	}
	f.rows[userID] = &mtfRow{secretEnc: append([]byte(nil), enc...)}
	return nil
}

func (f *mtf) Enable(userID string, enc []byte, step int64, hashes []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[userID]
	if !ok {
		return domain.ErrNotFound
	}
	if r.enabled {
		return domain.ErrAlreadyExists
	}
	if !bytes.Equal(r.secretEnc, enc) {
		return domain.ErrNotFound
	}
	r.enabled, r.lastStep, r.failed, r.lockedUntil = true, step, 0, time.Time{}
	f.codes[userID] = map[string]bool{}
	for _, h := range hashes {
		f.codes[userID][h] = false
	}
	return nil
}

func (f *mtf) BeginAttempt(userID string) (domain.TwoFactorAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[userID]
	if !ok || !r.enabled {
		return domain.TwoFactorAttempt{}, domain.ErrNotFound
	}
	if !r.lockedUntil.IsZero() && r.lockedUntil.After(f.now()) {
		return domain.TwoFactorAttempt{}, domain.ErrTooManyAttempts
	}
	if !r.lockedUntil.IsZero() {
		r.failed, r.lockedUntil = 0, time.Time{}
	}
	r.failed++
	if r.failed >= domain.TwoFactorMaxFailures {
		r.lockedUntil = f.now().Add(domain.TwoFactorLockDuration)
	}
	return domain.TwoFactorAttempt{SecretEnc: append([]byte(nil), r.secretEnc...), LastStep: r.lastStep}, nil
}

func (f *mtf) CompleteStep(userID string, step int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[userID]
	if !ok || r.lastStep >= step {
		return false, nil
	}
	r.lastStep, r.failed, r.lockedUntil = step, 0, time.Time{}
	return true, nil
}

func (f *mtf) UseRecoveryCode(userID, hash string) (bool, error) {
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

func (f *mtf) Disable(userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[userID]; !ok {
		return domain.ErrNotFound
	}
	delete(f.rows, userID)
	delete(f.codes, userID)
	return nil
}

var _ ports.TwoFactorRepository = (*mtf)(nil)

// mtfClock es el reloj de los tests (concurrente-seguro).
type mtfClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *mtfClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *mtfClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
