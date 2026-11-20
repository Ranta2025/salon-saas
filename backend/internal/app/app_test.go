package app

import (
	"errors"
	"strings"
	"testing"

	"salon-saas/internal/domain"
)

// --- Fakes: implementan ports sin Postgres ni bcrypt. Para unit test alcanzan. ---

type fakeUsers struct{ byEmail map[string]domain.User }

func (f *fakeUsers) Save(u domain.User) error {
	if f.byEmail == nil {
		f.byEmail = map[string]domain.User{}
	}
	f.byEmail[strings.ToLower(u.Email)] = u
	return nil
}
func (f *fakeUsers) FindByEmail(email string) (domain.User, error) {
	u, ok := f.byEmail[strings.ToLower(email)]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

// FindByID: lo usa GET /me (el token trae el userID, no el email).
func (f *fakeUsers) FindByID(id string) (domain.User, error) {
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

type fakeSalons struct{ bySlug map[string]domain.Salon }

func (f *fakeSalons) Save(s domain.Salon) error {
	if f.bySlug == nil {
		f.bySlug = map[string]domain.Salon{}
	}
	f.bySlug[s.Slug] = s
	return nil
}
func (f *fakeSalons) FindBySlug(slug string) (domain.Salon, error) {
	s, ok := f.bySlug[slug]
	if !ok {
		return domain.Salon{}, domain.ErrNotFound
	}
	return s, nil
}

// FindByID: lo usa GET /me para el nombre y el slug de cada salón del usuario.
func (f *fakeSalons) FindByID(id string) (domain.Salon, error) {
	for _, s := range f.bySlug {
		if s.ID == id {
			return s, nil
		}
	}
	return domain.Salon{}, domain.ErrNotFound
}

type fakeMemberships struct{ byKey map[string]domain.Membership }

func (f *fakeMemberships) Save(m domain.Membership) error {
	if f.byKey == nil {
		f.byKey = map[string]domain.Membership{}
	}
	f.byKey[m.UserID+"|"+m.SalonID] = m
	return nil
}
func (f *fakeMemberships) Find(userID, salonID string) (domain.Membership, error) {
	m, ok := f.byKey[userID+"|"+salonID]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// ListByUser: puerto MembershipLister (rooms del websocket y marcar leídas).
func (f *fakeMemberships) ListByUser(userID string) ([]domain.Membership, error) {
	var out []domain.Membership
	for _, m := range f.byKey {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

type fakeHasher struct{}

func (fakeHasher) Hash(plain string) (string, error) { return "h:" + plain, nil }
func (fakeHasher) Compare(hash, plain string) bool   { return hash == "h:"+plain }
func (fakeHasher) DummyCompare(string) bool          { return false }

type fakeTokens struct{}

func (fakeTokens) Issue(userID string) (string, error) { return "tok:" + userID, nil }
func (fakeTokens) Parse(t string) (string, error) {
	if !strings.HasPrefix(t, "tok:") {
		return "", domain.ErrUnauthorized
	}
	return strings.TrimPrefix(t, "tok:"), nil
}

func TestAuth_Register_Login(t *testing.T) {
	svc := AuthService{Users: &fakeUsers{}, Hasher: fakeHasher{}, Tokens: fakeTokens{}}
	// B-06: la clave mínima son 8 bytes.
	u, err := svc.Register("Ana", "ana@mail.com", "secreta123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := svc.Register("Otra", "ana@mail.com", "otraClave1"); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("esperaba ErrAlreadyExists, dio %v", err)
	}
	tok, err := svc.Login("ana@mail.com", "secreta123")
	if err != nil || tok == "" {
		t.Fatalf("login: %v %q", err, tok)
	}
	if _, err := svc.Login("ana@mail.com", "otraClave2"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("clave mala debe dar Unauthorized, dio %v", err)
	}
	if _, err := svc.Login("nadie@mail.com", "otraClave2"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("usuario inexistente debe dar Unauthorized, dio %v", err)
	}
	_ = u
}

func TestTenants_Create_Add(t *testing.T) {
	salons, mems := &fakeSalons{}, &fakeMemberships{}
	svc := TenantService{Salons: salons, Memberships: mems}
	s, err := svc.CreateSalon("u-owner", "Navajas de Oro", "navajas-oro")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.CreateSalon("u-otro", "Copia", "navajas-oro"); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("slug duplicado debe dar AlreadyExists, dio %v", err)
	}
	if _, err := svc.CreateSalon("", "X", "x"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("sin owner debe dar Unauthorized, dio %v", err)
	}
	owner, _ := mems.Find("u-owner", s.ID)
	if err := svc.AddMember(owner, "u-prof", s.ID, domain.RoleProfessional); err != nil {
		t.Fatalf("owner agrega profesional: %v", err)
	}
	barb, _ := mems.Find("u-prof", s.ID)
	if err := svc.AddMember(barb, "u-x", s.ID, domain.RoleClient); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional no debe agregar, dio %v", err)
	}
}
