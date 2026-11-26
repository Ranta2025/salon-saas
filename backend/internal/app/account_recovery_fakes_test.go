package app

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de F15: UNA tienda en memoria que hace de repositorio de usuarios, de
// pedidos de restablecer y de pedidos de verificación, con la MISMA semántica
// que el adaptador Postgres (canje condicional, un vigente por usuario,
// throttle bajo el lock del usuario).

var f15T0 = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

type f15Store struct {
	mu            sync.Mutex
	clock         *fakeClock
	users         map[string]*domain.User // por id
	changedAt     map[string]*time.Time   // password_changed_at por id
	resets        []*domain.PasswordReset
	verifs        []*domain.EmailVerification
	findByEmail   int
	failFindEmail error
}

func newF15Store() *f15Store {
	return &f15Store{clock: &fakeClock{t: f15T0}, users: map[string]*domain.User{}, changedAt: map[string]*time.Time{}}
}

func (s *f15Store) addUser(id, name, email string, verified bool) domain.User {
	u := domain.User{ID: id, Name: name, Email: email, PasswordHash: "h:vieja-clave", CreatedAt: f15T0}
	if verified {
		v := f15T0
		u.EmailVerifiedAt = &v
	}
	s.mu.Lock()
	s.users[id] = &u
	s.mu.Unlock()
	return u
}

func (s *f15Store) now() time.Time { return s.clock.Now() }

// UserRepository
func (s *f15Store) Save(u domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.ID] = &u
	return nil
}

func (s *f15Store) FindByEmail(email string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.findByEmail++
	if s.failFindEmail != nil {
		return domain.User{}, s.failFindEmail
	}
	for _, u := range s.users {
		if u.Email == email {
			return *u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

func (s *f15Store) FindByID(id string) (domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		return *u, nil
	}
	return domain.User{}, domain.ErrNotFound
}

// PasswordResetRepository
func (s *f15Store) IssueReset(r domain.PasswordReset) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last *time.Time
	inHour := 0
	for _, x := range s.resets {
		if x.UserID != r.UserID {
			continue
		}
		if last == nil || x.CreatedAt.After(*last) {
			c := x.CreatedAt
			last = &c
		}
		if x.CreatedAt.After(r.CreatedAt.Add(-time.Hour)) {
			inHour++
		}
	}
	if domain.ResetThrottled(last, inHour, r.CreatedAt) {
		return false, nil
	}
	for _, x := range s.resets {
		if x.UserID == r.UserID && x.UsedAt == nil {
			at := r.CreatedAt
			x.UsedAt = &at
		}
	}
	cp := r
	s.resets = append(s.resets, &cp)
	return true, nil
}

func (s *f15Store) FindReset(hash string) (domain.PasswordReset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.resets {
		if x.TokenHash == hash {
			return *x, nil
		}
	}
	return domain.PasswordReset{}, domain.ErrNotFound
}

func (s *f15Store) ConsumeReset(hash, passHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, x := range s.resets {
		if x.TokenHash != hash || !x.Usable(now) {
			continue
		}
		at := now
		x.UsedAt = &at
		u := s.users[x.UserID]
		u.PasswordHash = passHash
		c := now
		s.changedAt[u.ID] = &c
		if u.EmailVerifiedAt == nil {
			v := now
			u.EmailVerifiedAt = &v
		}
		for _, o := range s.resets {
			if o.UserID == x.UserID && o.UsedAt == nil {
				oa := now
				o.UsedAt = &oa
			}
		}
		return nil
	}
	return domain.ErrNotFound
}

// EmailVerificationRepository
func (s *f15Store) IssueVerification(v domain.EmailVerification, minInterval time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if minInterval > 0 {
		for _, x := range s.verifs {
			if x.UserID == v.UserID && v.CreatedAt.Sub(x.CreatedAt) < minInterval {
				return false, nil
			}
		}
	}
	cp := v
	s.verifs = append(s.verifs, &cp)
	return true, nil
}

func (s *f15Store) ConsumeVerification(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, x := range s.verifs {
		if x.TokenHash != hash {
			continue
		}
		u := s.users[x.UserID]
		if x.Usable(now) {
			at := now
			x.UsedAt = &at
			if u.EmailVerifiedAt == nil {
				v := now
				u.EmailVerifiedAt = &v
			}
			return nil
		}
		if x.UsedAt != nil && u.EmailVerifiedAt != nil {
			return nil // idempotente
		}
	}
	return domain.ErrNotFound
}

var (
	_ ports.UserRepository              = (*f15Store)(nil)
	_ ports.PasswordResetRepository     = (*f15Store)(nil)
	_ ports.EmailVerificationRepository = (*f15Store)(nil)
)

// deferredRunner registra los trabajos y NO los corre hasta Flush: prueba que
// la respuesta sale antes que el trabajo.
type deferredRunner struct {
	mu   sync.Mutex
	jobs []func(ctx context.Context)
}

func (d *deferredRunner) Run(job func(ctx context.Context)) {
	d.mu.Lock()
	d.jobs = append(d.jobs, job)
	d.mu.Unlock()
}

func (d *deferredRunner) Pending() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.jobs)
}

func (d *deferredRunner) Flush() {
	d.mu.Lock()
	jobs := d.jobs
	d.jobs = nil
	d.mu.Unlock()
	for _, j := range jobs {
		j(context.Background())
	}
}

func inlineRunner(job func(ctx context.Context)) { job(context.Background()) }

var linkRe = regexp.MustCompile(`https://app\.test/(restablecer|verificar-email)#(\S+)`)

// lastLinkToken saca el token del último correo enviado (solo el test lo ve).
func lastLinkToken(t *testing.T, m *fakeMailer) (path, token string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("no se envió ningún correo")
	}
	match := linkRe.FindStringSubmatch(m.sent[len(m.sent)-1].Text)
	if match == nil {
		t.Fatalf("el correo no trae el enlace esperado:\n%s", m.sent[len(m.sent)-1].Text)
	}
	return match[1], match[2]
}

func sentCount(m *fakeMailer) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(s, n) {
			return true
		}
	}
	return false
}
