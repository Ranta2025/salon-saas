package domain

import "testing"

// T6: quién puede sumar qué rol al equipo. El owner agrega admins; un admin
// agrega profesionales y recepción. Nadie crea owners por la API (el owner
// nace con el salón).
func TestRole_AddableBy(t *testing.T) {
	cases := []struct {
		actor   Role
		target  Role
		allowed bool
	}{
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleProfessional, true},
		{RoleOwner, RoleReception, true},
		{RoleOwner, RoleOwner, false},
		{RoleAdmin, RoleAdmin, false},
		{RoleAdmin, RoleProfessional, true},
		{RoleAdmin, RoleReception, true},
		{RoleAdmin, RoleOwner, false},
		{RoleProfessional, RoleProfessional, false},
		{RoleReception, RoleReception, false},
		{RoleClient, RoleClient, false},
	}
	for _, c := range cases {
		if got := c.target.AddableBy(c.actor); got != c.allowed {
			t.Fatalf("%s agrega %s = %v, esperaba %v", c.actor, c.target, got, c.allowed)
		}
	}
}
