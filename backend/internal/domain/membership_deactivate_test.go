package domain

import (
	"testing"
	"time"
)

// S-F2g2-2: una membresía dada de baja NO atiende, NO administra y NO tiene
// ausencias. La baja es `EndedAt` no nulo; `Active()` es la única pregunta que
// hacen las reglas.
func TestMembership_endedAt_deja_de_contar(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	prof, err := NewMembership("u-prof", "s1", RoleProfessional)
	if err != nil {
		t.Fatalf("profesional: %v", err)
	}
	if !prof.Active() || !prof.Bookable() {
		t.Fatalf("una membresía nueva está activa y atiende: %+v", prof)
	}
	prof.EndedAt = &now
	if prof.Active() {
		t.Fatalf("con ended_at la membresía no está activa: %+v", prof)
	}
	if prof.Bookable() {
		t.Fatalf("una profesional dada de baja no atiende: %+v", prof)
	}
	if prof.HasTimeOff() {
		t.Fatalf("una profesional dada de baja no tiene ausencias cargables: %+v", prof)
	}

	// Dueño/administración que atendía: al darse de baja deja de atender Y de
	// administrar (no cuenta como dueño en ninguna regla).
	owner, err := NewMembership("u-owner", "s1", RoleOwner)
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	owner.Attends = true
	if !owner.CanManage() || !owner.Bookable() {
		t.Fatalf("owner activo administra y atiende: %+v", owner)
	}
	owner.EndedAt = &now
	if owner.CanManage() {
		t.Fatalf("un owner dado de baja no administra: %+v", owner)
	}
	if owner.Bookable() {
		t.Fatalf("un owner dado de baja no atiende: %+v", owner)
	}
}

// S-F2g2-4: dar de baja NO es la regla del alta mirada al revés. El dueño da de
// baja a CUALQUIER rol, incluido otro dueño (el guard del último owner activo va
// aparte y corre ANTES de esta regla: ver app.MemberService.Remove); la
// administración solo a profesional, recepción o cliente, el mismo criterio de
// su invitación de F2g1 (un admin no reparte privilegios: ni owner ni admin).
// Recepción, profesional y cliente no dan de baja a nadie.
func TestRole_removableBy(t *testing.T) {
	cases := []struct {
		target, actor Role
		want          bool
	}{
		{RoleOwner, RoleOwner, true}, // el dueño baja a su igual: sí
		{RoleAdmin, RoleOwner, true},
		{RoleProfessional, RoleOwner, true},
		{RoleReception, RoleOwner, true},
		{RoleClient, RoleOwner, true},
		{RoleOwner, RoleAdmin, false}, // mayor: no
		{RoleAdmin, RoleAdmin, false}, // igual: no
		{RoleProfessional, RoleAdmin, true},
		{RoleReception, RoleAdmin, true},
		{RoleClient, RoleAdmin, true},
		{RoleProfessional, RoleProfessional, false},
		{RoleReception, RoleReception, false},
		{RoleAdmin, Role("jefe"), false},
		{RoleOwner, Role("jefe"), false},
	}
	for _, c := range cases {
		if got := c.target.RemovableBy(c.actor); got != c.want {
			t.Fatalf("%s dado de baja por %s = %v, esperaba %v", c.target, c.actor, got, c.want)
		}
	}
}
