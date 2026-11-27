// Fakes de F15 para las pruebas HTTP: UNA tienda en memoria que es el
// repositorio de usuarios, el de pedidos de restablecer, el de pedidos de
// verificar y la compuerta de sesiones, con la MISMA semántica que el adaptador
// Postgres (canje condicional, uno vigente por usuario, throttle bajo el lock,
// idempotencia del token usado y ya verificado).
//
// El router es el REAL (httpapi.RegisterRoutes + httpapi.AuthMiddleware + el hub de tiempo real):
// lo que se prueba es el contrato de la API, no el cableado.
package httpkit

import (
	"context"
	"regexp"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sync"
	"time"
)

// Recovery es la tienda en memoria de F15.
type Recovery struct {
	Mu      sync.Mutex
	Users   map[string]*domain.User // por id
	Resets  []*domain.PasswordReset
	Verifs  []*domain.EmailVerification
	Changed map[string]*time.Time
}

func NewRecovery() *Recovery {
	return &Recovery{
		Users:   map[string]*domain.User{},
		Changed: map[string]*time.Time{},
	}
}

// seedUser deja una cuenta lista (verified = con email_verified_at).
func (m *Recovery) SeedUser(id, name, email, passHash string, verified bool) domain.User {
	u := domain.User{ID: id, Name: name, Email: email, PasswordHash: passHash, CreatedAt: time.Now().UTC()}
	if verified {
		at := u.CreatedAt
		u.EmailVerifiedAt = &at
	}
	m.Mu.Lock()
	m.Users[id] = &u
	m.Mu.Unlock()
	return u
}

// UserRepository
func (m *Recovery) Save(u domain.User) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	cp := u
	m.Users[u.ID] = &cp
	return nil
}

func (m *Recovery) FindByID(id string) (domain.User, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if u, ok := m.Users[id]; ok {
		return *u, nil
	}
	return domain.User{}, domain.ErrNotFound
}

func (m *Recovery) FindByEmail(email string) (domain.User, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	for _, u := range m.Users {
		if domain.NormalizeEmail(u.Email) == domain.NormalizeEmail(email) {
			return *u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

// PasswordResetRepository
func (m *Recovery) IssueReset(r domain.PasswordReset) (bool, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	var last *time.Time
	inHour := 0
	for _, x := range m.Resets {
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
	for _, x := range m.Resets {
		if x.UserID == r.UserID && x.UsedAt == nil {
			at := r.CreatedAt
			x.UsedAt = &at
		}
	}
	cp := r
	m.Resets = append(m.Resets, &cp)
	return true, nil
}

func (m *Recovery) FindReset(hash string) (domain.PasswordReset, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	for _, x := range m.Resets {
		if x.TokenHash == hash {
			return *x, nil
		}
	}
	return domain.PasswordReset{}, domain.ErrNotFound
}

func (m *Recovery) ConsumeReset(hash, passHash string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	now := time.Now().UTC()
	for _, x := range m.Resets {
		if x.TokenHash != hash || !x.Usable(now) {
			continue
		}
		at := now
		x.UsedAt = &at
		u := m.Users[x.UserID]
		if u == nil {
			return domain.ErrNotFound
		}
		u.PasswordHash = passHash
		m.Changed[u.ID] = &at
		if u.EmailVerifiedAt == nil {
			v := now
			u.EmailVerifiedAt = &v
		}
		for _, o := range m.Resets {
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
func (m *Recovery) IssueVerification(v domain.EmailVerification, minInterval time.Duration) (bool, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if minInterval > 0 {
		for _, x := range m.Verifs {
			if x.UserID == v.UserID && v.CreatedAt.Sub(x.CreatedAt) < minInterval {
				return false, nil
			}
		}
	}
	cp := v
	m.Verifs = append(m.Verifs, &cp)
	return true, nil
}

func (m *Recovery) ConsumeVerification(hash string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	now := time.Now().UTC()
	for _, x := range m.Verifs {
		if x.TokenHash != hash {
			continue
		}
		u := m.Users[x.UserID]
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
func (m *Recovery) PasswordChangedAt(userID string) (*time.Time, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if _, ok := m.Users[userID]; !ok {
		return nil, domain.ErrNotFound
	}
	return m.Changed[userID], nil
}

// resetCount y mailCount: introspección de las pruebas.
func (m *Recovery) ResetCount() int {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	return len(m.Resets)
}

// Inbox es el MailSender de las pruebas: guarda lo enviado y nunca falla.
type Inbox struct {
	Mu   sync.Mutex
	Sent []ports.Message
}

func (b *Inbox) Send(_ context.Context, msg ports.Message) error {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.Sent = append(b.Sent, msg)
	return nil
}

func (b *Inbox) Count() int {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return len(b.Sent)
}

func (b *Inbox) Last() ports.Message {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if len(b.Sent) == 0 {
		return ports.Message{}
	}
	return b.Sent[len(b.Sent)-1]
}

// TokenFromLink saca el token del último correo (solo la prueba lo ve).
func TokenFromLink(t interface {
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
	_ ports.UserRepository              = (*Recovery)(nil)
	_ ports.PasswordResetRepository     = (*Recovery)(nil)
	_ ports.EmailVerificationRepository = (*Recovery)(nil)
	_ ports.SessionGate                 = (*Recovery)(nil)
	_ ports.MailSender                  = (*Inbox)(nil)
)
