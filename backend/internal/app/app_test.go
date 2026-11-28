package app

import (
	"errors"
	"testing"

	"salon-saas/internal/auth"
	"salon-saas/internal/domain"
	"salon-saas/internal/team"
	"salon-saas/internal/testutil"
)

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

func TestTenants_Create_Add(t *testing.T) {
	salons, mems := &testutil.FakeSalons{}, &testutil.FakeMemberships{}
	svc := team.TenantService{Salons: salons, Memberships: mems}
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
