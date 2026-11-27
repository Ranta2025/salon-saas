package httpkit

import (
	"bytes"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sync"
	"time"
)

// TwoFactor imita pgstore.TwoFactor para los tests HTTP: cada método es UNA sección
// crítica con la misma condición que el SQL real (bloqueo, last_step < paso,
// used_at IS NULL). La concurrencia contra Postgres real se prueba en pgstore.
type TwoFactor struct {
	Mu    sync.Mutex
	now   func() time.Time
	Rows  map[string]*TwoFactorRow
	Codes map[string]map[string]bool
}

type TwoFactorRow struct {
	SecretEnc   []byte
	Enabled     bool
	LastStep    int64
	Failed      int
	LockedUntil time.Time
}

func NewTwoFactor(now func() time.Time) *TwoFactor {
	return &TwoFactor{now: now, Rows: map[string]*TwoFactorRow{}, Codes: map[string]map[string]bool{}}
}

func (f *TwoFactor) Find(userID string) (domain.TwoFactorState, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	r, ok := f.Rows[userID]
	if !ok {
		return domain.TwoFactorState{}, domain.ErrNotFound
	}
	return domain.TwoFactorState{SecretEnc: append([]byte(nil), r.SecretEnc...), Enabled: r.Enabled, LastStep: r.LastStep}, nil
}

func (f *TwoFactor) SavePending(userID string, enc []byte) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if r, ok := f.Rows[userID]; ok && r.Enabled {
		return domain.ErrAlreadyExists
	}
	f.Rows[userID] = &TwoFactorRow{SecretEnc: append([]byte(nil), enc...)}
	return nil
}

func (f *TwoFactor) Enable(userID string, enc []byte, step int64, hashes []string) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	r, ok := f.Rows[userID]
	if !ok {
		return domain.ErrNotFound
	}
	if r.Enabled {
		return domain.ErrAlreadyExists
	}
	if !bytes.Equal(r.SecretEnc, enc) {
		return domain.ErrNotFound
	}
	r.Enabled, r.LastStep, r.Failed, r.LockedUntil = true, step, 0, time.Time{}
	f.Codes[userID] = map[string]bool{}
	for _, h := range hashes {
		f.Codes[userID][h] = false
	}
	return nil
}

func (f *TwoFactor) BeginAttempt(userID string) (domain.TwoFactorAttempt, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	r, ok := f.Rows[userID]
	if !ok || !r.Enabled {
		return domain.TwoFactorAttempt{}, domain.ErrNotFound
	}
	if !r.LockedUntil.IsZero() && r.LockedUntil.After(f.now()) {
		return domain.TwoFactorAttempt{}, domain.ErrTooManyAttempts
	}
	if !r.LockedUntil.IsZero() {
		r.Failed, r.LockedUntil = 0, time.Time{}
	}
	r.Failed++
	if r.Failed >= domain.TwoFactorMaxFailures {
		r.LockedUntil = f.now().Add(domain.TwoFactorLockDuration)
	}
	return domain.TwoFactorAttempt{SecretEnc: append([]byte(nil), r.SecretEnc...), LastStep: r.LastStep}, nil
}

func (f *TwoFactor) CompleteStep(userID string, step int64) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	r, ok := f.Rows[userID]
	if !ok || r.LastStep >= step {
		return false, nil
	}
	r.LastStep, r.Failed, r.LockedUntil = step, 0, time.Time{}
	return true, nil
}

func (f *TwoFactor) UseRecoveryCode(userID, hash string) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	used, ok := f.Codes[userID][hash]
	if !ok || used {
		return false, nil
	}
	f.Codes[userID][hash] = true
	if r, ok := f.Rows[userID]; ok {
		r.Failed, r.LockedUntil = 0, time.Time{}
	}
	return true, nil
}

func (f *TwoFactor) Disable(userID string) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if _, ok := f.Rows[userID]; !ok {
		return domain.ErrNotFound
	}
	delete(f.Rows, userID)
	delete(f.Codes, userID)
	return nil
}

var _ ports.TwoFactorRepository = (*TwoFactor)(nil)

// TwoFactorClock es el reloj de los tests (concurrente-seguro).
type TwoFactorClock struct {
	Mu sync.Mutex
	T  time.Time
}

func (c *TwoFactorClock) Now() time.Time { c.Mu.Lock(); defer c.Mu.Unlock(); return c.T }
func (c *TwoFactorClock) Add(d time.Duration) {
	c.Mu.Lock()
	c.T = c.T.Add(d)
	c.Mu.Unlock()
}
