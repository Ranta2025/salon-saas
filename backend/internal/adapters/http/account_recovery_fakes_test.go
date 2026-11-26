// Fakes de F15 para las pruebas HTTP: UNA tienda en memoria que es el
// repositorio de usuarios, el de pedidos de restablecer, el de pedidos de
// verificar y la compuerta de sesiones, con la MISMA semántica que el adaptador
// Postgres (canje condicional, uno vigente por usuario, throttle bajo el lock,
// idempotencia del token usado y ya verificado).
//
// El router es el REAL (RegisterRoutes + AuthMiddleware + el hub de tiempo real):
// lo que se prueba es el contrato de la API, no el cableado.
package httpapi

import (
	"context"
	"regexp"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// mrecovery es la tienda en memoria de F15.
type mrecovery struct {
	mu      sync.Mutex
	users   map[string]*domain.User // por id
	resets  []*domain.PasswordReset
	verifs  []*domain.EmailVerification
	changed map[string]*time.Time
}

func newMRecovery() *mrecovery {
	return &mrecovery{
		users:   map[string]*domain.User{},
		changed: map[string]*time.Time{},
	}
}

// seedUser deja una cuenta lista (verified = con email_verified_at).
func (m *mrecovery) seedUser(id, name, email, passHash string, verified bool) domain.User {
	u := domain.User{ID: id, Name: name, Email: email, PasswordHash: passHash, CreatedAt: time.Now().UTC()}
	if verified {
		at := u.CreatedAt
		u.EmailVerifiedAt = &at
	}
	m.mu.Lock()
	m.users[id] = &u
	m.mu.Unlock()
	return u
}

// UserRepository
func (m *mrecovery) Save(u domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := u
	m.users[u.ID] = &cp
	return nil
}

func (m *mrecovery) FindByID(id string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		return *u, nil
	}
	return domain.User{}, domain.ErrNotFound
}

func (m *mrecovery) FindByEmail(email string) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if domain.NormalizeEmail(u.Email) == domain.NormalizeEmail(email) {
			return *u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

// PasswordResetRepository
func (m *mrecovery) IssueReset(r domain.PasswordReset) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *time.Time
	inHour := 0
	for _, x := range m.resets {
		if x.UserID != r.UserID {
			continue
		}
		if last == nil || x.CreatedAt.After(*last) {
			at := x.CreatedAt
			last = &at
		}
		if x.CreatedAt.After(r.CreatedAt.Add(-time.Hour)) {
			inHour++
		}
	}
	if domain.ResetThrottled(last, inHour, r.CreatedAt) {
		return false, nil
	}
	for _, x := range m.resets {
		if x.UserID == r.UserID && x.UsedAt == nil {
			at := r.CreatedAt
			x.UsedAt = &at
		}
	}
	cp := r
	m.resets = append(m.resets, &cp)
	return true, nil
}

func (m *mrecovery) FindReset(hash string) (domain.PasswordReset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.resets {
		if x.TokenHash == hash {
			return *x, nil
		}
	}
	return domain.PasswordReset{}, domain.ErrNotFound
}

func (m *mrecovery) ConsumeReset(hash, passHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, x := range m.resets {
		if x.TokenHash != hash || !x.Usable(now) {
			continue
		}
		at := now
		x.UsedAt = &at
		u := m.users[x.UserID]
		if u == nil {
			return domain.ErrNotFound
		}
		u.PasswordHash = passHash
		m.changed[u.ID] = &at
		if u.EmailVerifiedAt == nil {
			v := now
			u.EmailVerifiedAt = &v
		}
		for _, o := range m.resets {
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
func (m *mrecovery) IssueVerification(v domain.EmailVerification, minInterval time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if minInterval > 0 {
		for _, x := range m.verifs {
			if x.UserID == v.UserID && v.CreatedAt.Sub(x.CreatedAt) < minInterval {
				return false, nil
			}
		}
	}
	cp := v
	m.verifs = append(m.verifs, &cp)
	return true, nil
}

func (m *mrecovery) ConsumeVerification(hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, x := range m.verifs {
		if x.TokenHash != hash {
			continue
		}
		u := m.users[x.UserID]
		if u == nil {
			return domain.ErrNotFound
		}
		// El email del pedido tiene que seguir siendo el de la cuenta.
		if domain.NormalizeEmail(x.Email) != domain.NormalizeEmail(u.Email) {
			return domain.ErrNotFound
		}
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

// SessionGate (D4)
func (m *mrecovery) PasswordChangedAt(userID string) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return nil, domain.ErrNotFound
	}
	return m.changed[userID], nil
}

// resetCount y mailCount: introspección de las pruebas.
func (m *mrecovery) resetCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.resets)
}

// inbox es el MailSender de las pruebas: guarda lo enviado y nunca falla.
type inbox struct {
	mu   sync.Mutex
	sent []ports.Message
}

func (b *inbox) Send(_ context.Context, msg ports.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, msg)
	return nil
}

func (b *inbox) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sent)
}

func (b *inbox) last() ports.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sent) == 0 {
		return ports.Message{}
	}
	return b.sent[len(b.sent)-1]
}

// tokenFromLink saca el token del último correo (solo la prueba lo ve).
func tokenFromLink(t interface {
	Helper()
	Fatalf(string, ...any)
}, link string) string {
	t.Helper()
	m := regexp.MustCompile(`#(\S+)`).FindStringSubmatch(link)
	if m == nil {
		t.Fatalf("el correo no trae un enlace con fragmento:\n%s", link)
	}
	return m[1]
}

var (
	_ ports.UserRepository              = (*mrecovery)(nil)
	_ ports.PasswordResetRepository     = (*mrecovery)(nil)
	_ ports.EmailVerificationRepository = (*mrecovery)(nil)
	_ ports.SessionGate                 = (*mrecovery)(nil)
	_ ports.MailSender                  = (*inbox)(nil)
)
