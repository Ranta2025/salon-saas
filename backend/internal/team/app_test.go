package team_test

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/team"
	"salon-saas/internal/testutil"
)

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

func TestStaffService(t *testing.T) {
	staff := &testutil.FakeStaffLister{BySalon: map[string][]domain.StaffMember{
		"s1": {
			{ID: "u-owner", Name: "Ana", Role: domain.RoleOwner, Attends: true},
			{ID: "u-prof", Name: "Bruno", Role: domain.RoleProfessional},
			// F2a: la administración sin la marca no atiende y no se lista.
			{ID: "u-admin", Name: "Carla", Role: domain.RoleAdmin},
			{ID: "u-recep", Name: "Dani", Role: domain.RoleReception, Attends: true},
		},
	}}
	mems := &testutil.FakeMemberships{}
	svc := team.StaffService{Staff: staff, Memberships: mems}
	_ = mems.Save(domain.Membership{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception})

	list, err := svc.List(domain.Membership{UserID: "u-recep", SalonID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("staff: %v", err)
	}
	if len(list) != 2 || list[0].ID != "u-owner" || list[1].ID != "u-prof" {
		t.Fatalf("esperaba dueño (con attends) y profesional, dio %+v", list)
	}
	// No-miembro: 403 (no se filtra el equipo de otro salón).
	if _, err := svc.List(domain.Membership{UserID: "u-ajeno", SalonID: "s2"}, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no-miembro debe dar ErrForbidden, dio %v", err)
	}
}
