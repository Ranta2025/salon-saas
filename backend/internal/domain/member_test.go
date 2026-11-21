package domain

import (
	"errors"
	"testing"
)

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

// E-07: los mensajes que ve una persona no dicen "owner" ni "professional".
// El rol se muestra con su nombre en español.
func TestRole_Label(t *testing.T) {
	want := map[Role]string{
		RoleOwner:        "dueño",
		RoleAdmin:        "administración",
		RoleProfessional: "profesional",
		RoleReception:    "recepción",
		RoleClient:       "cliente",
	}
	for role, label := range want {
		if got := role.Label(); got != label {
			t.Fatalf("Label() de %q = %q, esperaba %q", role, got, label)
		}
	}
	if got := Role("jefe").Label(); got == "" {
		t.Fatal("un rol desconocido igual necesita un texto para mostrar")
	}
}

// E-07: Message lleva el texto que ve la persona pero conserva el error de
// dominio por debajo, así el mapeo a HTTP (errors.Is) no cambia.
func TestMessage_sin_prefijo_interno(t *testing.T) {
	err := NewMessage(ErrValidation, "No se puede quitar al último dueño")
	if err.Error() != "No se puede quitar al último dueño" {
		t.Fatalf("el texto debe ser exactamente el mensaje, dio %q", err.Error())
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatal("el error de dominio tiene que seguir siendo identificable")
	}
	// El prefijo interno no se filtra ni siquiera anidando.
	nested := NewMessage(ErrAlreadyExists, "Ya tiene ese rol")
	if got := nested.Error(); got != "Ya tiene ese rol" {
		t.Fatalf("mensaje: %q", got)
	}
	if !errors.Is(nested, ErrAlreadyExists) {
		t.Fatal("errors.Is debe seguir funcionando")
	}
}
