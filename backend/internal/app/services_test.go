package app

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
)

func TestServiceService_create_ok_y_permisos(t *testing.T) {
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := ServiceService{Services: services, Memberships: mems}

	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	admin := miembro(mems, "u-admin", "s1", domain.RoleAdmin)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)

	// owner y admin configuran el catálogo.
	for _, actor := range []domain.Membership{owner, admin} {
		if _, err := svc.Create(actor, "s1", "Corte "+actor.UserID, 30, 100000, 20); err != nil {
			t.Fatalf("%s debe poder crear: %v", actor.Role, err)
		}
	}
	// Profesional, recepción y miembro de otro salón: 403.
	for _, actor := range []domain.Membership{profe, recep, ajeno} {
		if _, err := svc.Create(actor, "s1", "Corte "+actor.UserID, 30, 100000, 20); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no debe poder crear, dio %v", actor.Role, err)
		}
	}
	// Ni siquiera owner de otro salón puede escribir acá.
	if _, err := svc.Create(ajeno, "s1", "X", 30, 1, 0); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
	// Actor que no es miembro (aunque diga tener rol): 403.
	fantasma := domain.Membership{UserID: "nadie", SalonID: "s1", Role: domain.RoleOwner}
	if _, err := svc.Create(fantasma, "s1", "X", 30, 1, 0); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no-miembro debe dar Forbidden, dio %v", err)
	}
	// Validación de dominio: 400.
	if _, err := svc.Create(owner, "s1", "  ", 30, 1, 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nombre vacío debe dar Validation, dio %v", err)
	}
}

func TestServiceService_duplicado_activo(t *testing.T) {
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := ServiceService{Services: services, Memberships: mems}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)

	if _, err := svc.Create(owner, "s1", "Corte", 30, 100000, 20); err != nil {
		t.Fatalf("primero: %v", err)
	}
	// Duplicado exacto y con otras mayúsculas/espacios: 409 (el índice de la DB
	// es sobre lower(name), el caso de uso lo refleja igual).
	for _, dup := range []string{"Corte", "corte", "  CORTE  "} {
		if _, err := svc.Create(owner, "s1", dup, 45, 120000, 25); !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("duplicado %q debe dar AlreadyExists, dio %v", dup, err)
		}
	}
	// Otro salón puede usar el mismo nombre.
	otro := miembro(mems, "u-otro", "s2", domain.RoleOwner)
	if _, err := svc.Create(otro, "s2", "Corte", 30, 90000, 20); err != nil {
		t.Fatalf("mismo nombre en otro salón debe entrar: %v", err)
	}
	// Si el original se desactiva, el nombre queda libre.
	list, _ := svc.List(owner, "s1", true)
	up, err := svc.Update(owner, "s1", list[0].ID, list[0].Name, list[0].DurationMin, list[0].PriceCents, list[0].CommissionPct, false)
	if err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	if up.Active {
		t.Fatal("quedó activo")
	}
	if _, err := svc.Create(owner, "s1", "Corte", 30, 100000, 20); err != nil {
		t.Fatalf("nombre libre tras desactivar: %v", err)
	}
}

func TestServiceService_update(t *testing.T) {
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := ServiceService{Services: services, Memberships: mems}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)

	created, err := svc.Create(owner, "s1", "Corte", 30, 100000, 20)
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	up, err := svc.Update(owner, "s1", created.ID, "Corte y brushing", 60, 180000, 35, true)
	if err != nil {
		t.Fatalf("actualizar: %v", err)
	}
	if up.Name != "Corte y brushing" || up.DurationMin != 60 || up.PriceCents != 180000 || up.CommissionPct != 35 {
		t.Fatalf("no aplicó cambios: %+v", up)
	}
	// Actualizarse a sí mismo con el mismo nombre no es duplicado.
	if _, err := svc.Update(owner, "s1", up.ID, "corte y BRUSHING", 60, 180000, 35, true); err != nil {
		t.Fatalf("renombrar a sí mismo cambia mayúsculas: %v", err)
	}
	// No existe -> 404.
	if _, err := svc.Update(owner, "s1", "nope", "X", 30, 1, 0, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("inexistente debe dar NotFound, dio %v", err)
	}
	// Servicio de otro salón -> 404 (no filtramos existencia cross-tenant).
	otroSvc, err := svc.Create(ajeno, "s2", "Manicura", 45, 90000, 10)
	if err != nil {
		t.Fatalf("crear en s2: %v", err)
	}
	if _, err := svc.Update(owner, "s1", otroSvc.ID, "Hack", 30, 1, 0, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("servicio de otro salón debe dar NotFound, dio %v", err)
	}
	// Permisos y validación.
	if _, err := svc.Update(profe, "s1", up.ID, "X", 30, 1, 0, true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional debe dar Forbidden, dio %v", err)
	}
	if _, err := svc.Update(owner, "s1", up.ID, "X", 0, 1, 0, true); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("duración 0 debe dar Validation, dio %v", err)
	}
}

func TestServiceService_list(t *testing.T) {
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := ServiceService{Services: services, Memberships: mems}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)

	a, _ := svc.Create(owner, "s1", "Corte", 30, 100000, 20)
	if _, err := svc.Create(owner, "s1", "Color", 90, 250000, 25); err != nil {
		t.Fatalf("segundo: %v", err)
	}
	if _, err := svc.Update(owner, "s1", a.ID, a.Name, a.DurationMin, a.PriceCents, a.CommissionPct, false); err != nil {
		t.Fatalf("desactivar: %v", err)
	}

	// Cualquier miembro del salón puede leer (la web pública es otra historia).
	got, err := svc.List(profe, "s1", false)
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Color" {
		t.Fatalf("sin inactivos esperaba solo Color, dio %+v", got)
	}
	all, err := svc.List(owner, "s1", true)
	if err != nil || len(all) != 2 {
		t.Fatalf("con inactivos esperaba 2, dio %d (%v)", len(all), err)
	}
	// No-miembro y otro salón: 403 / lista vacía scopeada.
	if _, err := svc.List(ajeno, "s1", true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
	if own, _ := svc.List(ajeno, "s2", true); len(own) != 0 {
		t.Fatalf("s2 está vacío, dio %d", len(own))
	}
}
