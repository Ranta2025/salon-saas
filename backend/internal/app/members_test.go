package app

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
)

// T6: gestión de miembros del salón (GET/POST/DELETE /salons/:id/members).
// Solo owner/admin; el owner suma admins, el admin suma profesionales/recepción;
// nunca se puede quedar el salón sin owner.

func memberFixture(t *testing.T) (*MemberService, *fakeMemberships, *fakeMembers) {
	t.Helper()
	users := &fakeUsers{}
	for _, u := range []domain.User{
		{ID: "u-owner", Name: "Ana", Email: "ana@mail.com"},
		{ID: "u-admin", Name: "Beto", Email: "beto@mail.com"},
		{ID: "u-prof", Name: "Caro", Email: "caro@mail.com"},
		{ID: "u-recep", Name: "Dani", Email: "dani@mail.com"},
		{ID: "u-owner2", Name: "Eva", Email: "eva@mail.com"},
	} {
		if err := users.Save(u); err != nil {
			t.Fatalf("usuario: %v", err)
		}
	}
	mems := &fakeMemberships{}
	admin := &fakeMembers{mems: mems, users: users}
	svc := &MemberService{Members: admin, Memberships: mems, Users: users}
	miembro(mems, "u-owner", "s1", domain.RoleOwner)
	miembro(mems, "u-admin", "s1", domain.RoleAdmin)
	miembro(mems, "u-owner", "s2", domain.RoleOwner)
	return svc, mems, admin
}

func TestMemberService_list(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")

	list, err := svc.List(owner, "s1")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("esperaba owner + admin, dio %+v", list)
	}
	for _, m := range list {
		if m.ID == "" || m.Name == "" || m.Email == "" || m.Role == "" {
			t.Fatalf("miembro incompleto: %+v", m)
		}
	}
	// Admin también lista. Profesional y ajeno: 403.
	admin, _ := mems.Find("u-admin", "s1")
	if _, err := svc.List(admin, "s1"); err != nil {
		t.Fatalf("admin lista: %v", err)
	}
	prof := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if _, err := svc.List(prof, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional listando: %v", err)
	}
	ajeno, _ := mems.Find("u-owner", "s2")
	if _, err := svc.List(ajeno, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ajeno listando: %v", err)
	}
	// Lista vacía: [] y no nil.
	if _, err := svc.List(owner, "s1"); err != nil {
		t.Fatalf("lista: %v", err)
	}
}

func TestMemberService_add(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin, _ := mems.Find("u-admin", "s1")

	// Owner suma un admin.
	m, err := svc.Add(owner, "s1", "CARO@mail.com", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("owner agrega admin: %v", err)
	}
	if m.ID != "u-prof" || m.Email != "caro@mail.com" || m.Role != domain.RoleAdmin || m.Name != "Caro" {
		t.Fatalf("miembro agregado: %+v", m)
	}
	if got, _ := mems.Find("u-prof", "s1"); got.Role != domain.RoleAdmin {
		t.Fatalf("membership persistida: %+v", got)
	}
	// Admin suma profesional y recepción.
	if _, err := svc.Add(admin, "s1", "dani@mail.com", domain.RoleReception); err != nil {
		t.Fatalf("admin agrega recepción: %v", err)
	}
	// Admin no puede crear otro admin (escalada de privilegios): 403.
	if _, err := svc.Add(admin, "s1", "eva@mail.com", domain.RoleAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin agregando admin: %v", err)
	}
	// Nadie reparte el rol owner por la API: 403.
	if _, err := svc.Add(owner, "s1", "eva@mail.com", domain.RoleOwner); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner agregando owner: %v", err)
	}
	// Profesional / recepción no administran: 403.
	prof := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if _, err := svc.Add(prof, "s1", "eva@mail.com", domain.RoleProfessional); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional agregando: %v", err)
	}
	// Cuenta inexistente: 400 con mensaje claro (no se crean cuentas desde acá).
	if _, err := svc.Add(owner, "s1", "nadie@mail.com", domain.RoleProfessional); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("email sin cuenta: %v", err)
	}
	if _, err := svc.Add(owner, "s1", "   ", domain.RoleProfessional); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("email vacío: %v", err)
	}
	// Ya es miembro: 409.
	if _, err := svc.Add(owner, "s1", "beto@mail.com", domain.RoleProfessional); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("ya miembro: %v", err)
	}
	// Rol desconocido: 400.
	if _, err := svc.Add(owner, "s1", "eva@mail.com", domain.Role("jefe")); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("rol desconocido: %v", err)
	}
	// Ajeno al salón: 403.
	ajeno, _ := mems.Find("u-owner", "s2")
	if _, err := svc.Add(ajeno, "s1", "eva@mail.com", domain.RoleProfessional); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ajeno agregando: %v", err)
	}
}

func TestMemberService_remove(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin, _ := mems.Find("u-admin", "s1")

	// Quitar al último owner: 400, el salón nunca queda sin dueño.
	if err := svc.Remove(owner, "s1", "u-owner"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("último owner: %v", err)
	}
	if err := svc.Remove(admin, "s1", "u-owner"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("admin quitando al último owner: %v", err)
	}
	if _, err := mems.Find("u-owner", "s1"); err != nil {
		t.Fatalf("el owner debe seguir: %v", err)
	}
	// Quitar a un miembro común: ok y desaparece.
	if err := svc.Remove(owner, "s1", "u-admin"); err != nil {
		t.Fatalf("quitar admin: %v", err)
	}
	if _, err := mems.Find("u-admin", "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el admin debe estar fuera: %v", err)
	}
	// Quitar a alguien que no está: 404.
	if err := svc.Remove(owner, "s1", "u-admin"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("quitar inexistente: %v", err)
	}
	// Quitar a un miembro de otro salón por esta ruta: 404 (scope por salón).
	if err := svc.Remove(owner, "s1", "u-owner2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro de otro salón: %v", err)
	}
	// Con otro owner en el salón, el primero sí se puede ir.
	_ = mems.Save(domain.Membership{UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner})
	if err := svc.Remove(owner, "s1", "u-owner"); err != nil {
		t.Fatalf("con otro owner: %v", err)
	}
	// Profesional no administra: 403.
	prof := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if err := svc.Remove(prof, "s1", "u-owner2"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional quitando: %v", err)
	}
}
