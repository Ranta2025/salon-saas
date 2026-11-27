package team

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// T6: gestión de miembros del salón (GET/POST/DELETE /salons/:id/members).
// Solo owner/admin; el owner suma admins, el admin suma profesionales/recepción;
// nunca se puede quedar el salón sin owner.

func memberFixture(t *testing.T) (*MemberService, *testutil.FakeMemberships, *fakeMembers) {
	t.Helper()
	users := &testutil.FakeUsers{}
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
	mems := &testutil.FakeMemberships{}
	admin := &fakeMembers{mems: mems, users: users}
	svc := &MemberService{Members: admin, Memberships: mems, Users: users, Tx: admin, Now: func() time.Time { return f2g2Now }}
	testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)
	testutil.Miembro(mems, "u-admin", "s1", domain.RoleAdmin)
	testutil.Miembro(mems, "u-owner", "s2", domain.RoleOwner)
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
	prof := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
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
	m, err := svc.Add(owner, "s1", "CARO@mail.com", domain.RoleAdmin, nil)
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
	if _, err := svc.Add(admin, "s1", "dani@mail.com", domain.RoleReception, nil); err != nil {
		t.Fatalf("admin agrega recepción: %v", err)
	}
	// Admin no puede crear otro admin (escalada de privilegios): 403.
	if _, err := svc.Add(admin, "s1", "eva@mail.com", domain.RoleAdmin, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin agregando admin: %v", err)
	}
	// Nadie reparte el rol owner por la API: 403.
	if _, err := svc.Add(owner, "s1", "eva@mail.com", domain.RoleOwner, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner agregando owner: %v", err)
	}
	// Profesional / recepción no administran: 403.
	prof := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if _, err := svc.Add(prof, "s1", "eva@mail.com", domain.RoleProfessional, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional agregando: %v", err)
	}
	// Cuenta inexistente: 400 con mensaje claro (no se crean cuentas desde acá).
	if _, err := svc.Add(owner, "s1", "nadie@mail.com", domain.RoleProfessional, nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("email sin cuenta: %v", err)
	}
	if _, err := svc.Add(owner, "s1", "   ", domain.RoleProfessional, nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("email vacío: %v", err)
	}
	// Ya es miembro: 409.
	if _, err := svc.Add(owner, "s1", "beto@mail.com", domain.RoleProfessional, nil); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("ya miembro: %v", err)
	}
	// Rol desconocido: 400.
	if _, err := svc.Add(owner, "s1", "eva@mail.com", domain.Role("jefe"), nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("rol desconocido: %v", err)
	}
	// Ajeno al salón: 403.
	ajeno, _ := mems.Find("u-owner", "s2")
	if _, err := svc.Add(ajeno, "s1", "eva@mail.com", domain.RoleProfessional, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ajeno agregando: %v", err)
	}
}

func TestMemberService_remove(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin, _ := mems.Find("u-admin", "s1")

	// Dar de baja al último owner: 400, el salón nunca queda sin dueño (igual
	// que antes de F2g2).
	if err := svc.Remove(owner, "s1", "u-owner"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("último owner: %v", err)
	}
	if err := svc.Remove(admin, "s1", "u-owner"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("admin quitando al último owner: %v", err)
	}
	if _, err := mems.Find("u-owner", "s1"); err != nil {
		t.Fatalf("el owner debe seguir: %v", err)
	}
	// Dar de baja a un miembro común: deja de tener acceso (Find no lo ve) pero
	// la fila queda con ended_at (F2g2).
	if err := svc.Remove(owner, "s1", "u-admin"); err != nil {
		t.Fatalf("quitar admin: %v", err)
	}
	if _, err := mems.Find("u-admin", "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el admin debe estar fuera: %v", err)
	}
	if raw := mems.ByKey["u-admin|s1"]; raw.Active() {
		t.Fatalf("la fila se conserva dada de baja: %+v", raw)
	}
	// Quitar a alguien que no está: 404.
	if err := svc.Remove(owner, "s1", "u-admin"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("quitar inexistente: %v", err)
	}
	// Quitar a un miembro de otro salón por esta ruta: 404 (scope por salón).
	if err := svc.Remove(owner, "s1", "u-owner2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro de otro salón: %v", err)
	}
	// Con otro owner en el salón, el primero sí se puede ir: el dueño da de baja
	// a cualquier rol, incluido otro dueño (la regla es el ÚLTIMO owner activo).
	_ = mems.Save(domain.Membership{UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner})
	if err := svc.Remove(owner, "s1", "u-owner"); err != nil {
		t.Fatalf("con otro owner: %v", err)
	}
	// La baja no borra la fila: pierde el acceso y queda con ended_at (F2g2).
	if _, err := mems.Find("u-owner", "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el owner dado de baja ya no tiene acceso: %v", err)
	}
	if raw := mems.ByKey["u-owner|s1"]; raw.Active() || raw.EndedAt == nil {
		t.Fatalf("la fila del owner se conserva dada de baja: %+v", raw)
	}
	// Profesional no administra: 403.
	prof := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if err := svc.Remove(prof, "s1", "u-owner2"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional quitando: %v", err)
	}
}

// E-07: los mensajes de la pantalla de equipo no llevan el prefijo interno del
// error ("validación:", "ya existe:") ni nombres de rol en inglés ("owner",
// "professional"). El status HTTP no cambia: se sigue identificando el mismo
// error de dominio por debajo.
func TestMemberService_mensajes_para_la_persona(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin, _ := mems.Find("u-admin", "s1")

	sinPrefijo := func(err error, esperado string) {
		t.Helper()
		if err == nil {
			t.Fatalf("esperaba el error %q", esperado)
		}
		if got := err.Error(); got != esperado {
			t.Fatalf("mensaje = %q, esperaba %q", got, esperado)
		}
		for _, feo := range []string{"validación:", "ya existe:", "no encontrado:", "owner", "professional", "reception"} {
			if strings.Contains(err.Error(), feo) {
				t.Fatalf("el mensaje %q no puede contener %q", err.Error(), feo)
			}
		}
	}

	// Ya es parte del equipo con ese rol.
	_, err := svc.Add(owner, "s1", "beto@mail.com", domain.RoleAdmin, nil)
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("ya miembro debe seguir siendo AlreadyExists (409), dio %v", err)
	}
	sinPrefijo(err, "Ya tiene ese rol")

	// Ya es parte del equipo con OTRO rol: se dice cuál, en español.
	_, err = svc.Add(admin, "s1", "beto@mail.com", domain.RoleProfessional, nil)
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("ya miembro debe seguir siendo AlreadyExists (409), dio %v", err)
	}
	sinPrefijo(err, "Ya es parte del equipo como administración")

	// La persona todavía no tiene cuenta.
	_, err = svc.Add(owner, "s1", "nadie@mail.com", domain.RoleProfessional, nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("email sin cuenta debe seguir siendo Validation (400), dio %v", err)
	}
	sinPrefijo(err, "La persona todavía no tiene cuenta")

	// Rol desconocido: se listan los roles válidos en español.
	_, err = svc.Add(owner, "s1", "eva@mail.com", domain.Role("jefe"), nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("rol desconocido debe seguir siendo Validation (400), dio %v", err)
	}
	sinPrefijo(err, "Rol desconocido: usá dueño, administración, profesional, recepción o cliente")

	// Dar de baja al último owner: sigue siendo Validation (400).
	err = svc.Remove(owner, "s1", "u-owner")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("último owner debe seguir siendo Validation (400), dio %v", err)
	}
	sinPrefijo(err, "No se puede quitar al último dueño")

	// El email vacío tampoco arrastra el prefijo.
	_, err = svc.Add(owner, "s1", "   ", domain.RoleProfessional, nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("email vacío: %v", err)
	}
	sinPrefijo(err, "El email es obligatorio")
}
