package auth_test

import (
	"errors"
	"strings"
	"testing"

	"salon-saas/internal/auth"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// B-06: la clave se valida ANTES de hashear (8..72 bytes).
func TestAuth_register_valida_clave(t *testing.T) {
	svc := auth.AuthService{Users: &testutil.FakeUsers{}, Hasher: testutil.FakeHasher{}, Tokens: testutil.FakeTokens{}}
	for _, bad := range []string{"corta", strings.Repeat("x", domain.MaxPasswordBytes+1)} {
		if _, err := svc.Register("Ana", "ana@mail.com", bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("clave de %d bytes debe dar ErrValidation, dio %v", len(bad), err)
		}
	}
	if _, err := svc.Register("Ana", "ana@mail.com", "unaClaveLarga"); err != nil {
		t.Fatalf("clave válida rechazada: %v", err)
	}
}

// B-05: el email se normaliza antes de buscar, así el duplicado con mayúsculas
// se detecta y el login funciona sin importar cómo se tipeó.
func TestAuth_email_normalizado(t *testing.T) {
	users := &testutil.FakeUsers{}
	svc := auth.AuthService{Users: users, Hasher: testutil.FakeHasher{}, Tokens: testutil.FakeTokens{}}
	if _, err := svc.Register("Ana", "  Ana@Mail.COM ", "unaClaveLarga"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := svc.Register("Otra", "ANA@mail.com", "otraClaveLarga"); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicado con mayúsculas debe dar ErrAlreadyExists, dio %v", err)
	}
	if _, err := svc.Login(" Ana@mail.com", "unaClaveLarga"); err != nil {
		t.Fatalf("login con email sin normalizar: %v", err)
	}
}

// B-05: si el usuario no existe igual se gasta el trabajo de bcrypt.
func TestAuth_login_usuario_inexistente_gasta_tiempo(t *testing.T) {
	h := &countingHasher{}
	svc := auth.AuthService{Users: &testutil.FakeUsers{}, Hasher: h, Tokens: testutil.FakeTokens{}}
	if _, err := svc.Login("nadie@mail.com", "unaClaveLarga"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("esperaba ErrUnauthorized, dio %v", err)
	}
	if h.dummyCalls != 1 {
		t.Fatalf("debe comparar contra el hash falso una vez, dio %d", h.dummyCalls)
	}
}

func TestMeService(t *testing.T) {
	users := &testutil.FakeUsers{}
	salons := &testutil.FakeSalons{}
	mems := &testutil.FakeMemberships{}
	svc := auth.MeService{Users: users, Salons: salons, Members: mems}

	u := domain.User{ID: "u1", Name: "Ana", Email: "ana@mail.com"}
	_ = users.Save(u)
	_ = salons.Save(domain.Salon{ID: "s1", Slug: "navajas", Name: "Navajas de Oro"})
	_ = mems.Save(domain.Membership{UserID: "u1", SalonID: "s1", Role: domain.RoleOwner})

	me, err := svc.Me("u1")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if me.User.ID != "u1" || me.User.Email != "ana@mail.com" {
		t.Fatalf("usuario: %+v", me.User)
	}
	if len(me.Salons) != 1 || me.Salons[0].Slug != "navajas" || me.Salons[0].Role != domain.RoleOwner {
		t.Fatalf("salones: %+v", me.Salons)
	}
	// Usuario inexistente (token viejo): 401, no un 500.
	if _, err := svc.Me("u-fantasma"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("usuario inexistente debe dar ErrUnauthorized, dio %v", err)
	}
}

func TestAuth_Register_Login(t *testing.T) {
	svc := auth.AuthService{Users: &testutil.FakeUsers{}, Hasher: testutil.FakeHasher{}, Tokens: testutil.FakeTokens{}}
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
