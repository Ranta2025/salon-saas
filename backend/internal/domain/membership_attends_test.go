package domain

import (
	"errors"
	"strings"
	"testing"
)

// F2a (S-F2a-2): una persona es RESERVABLE si su rol es professional O si
// tiene attends=true. Recepción y cliente nunca atienden, con o sin marca.
// La MISMA regla vale para la lista de la agenda (StaffMember) y para la
// validación del professional_id: si divergen, la agenda ofrece gente que
// después rechaza la reserva.
func TestMembership_Bookable(t *testing.T) {
	cases := []struct {
		role    Role
		attends bool
		want    bool
	}{
		{RoleProfessional, false, true}, // el profesional siempre atiende
		{RoleProfessional, true, true},
		{RoleOwner, true, true},   // el dueño-barbero
		{RoleOwner, false, false}, // el dueño que no atiende (recepción de su salón)
		{RoleAdmin, true, true},
		{RoleAdmin, false, false},
		{RoleReception, true, false}, // recepción nunca atiende
		{RoleReception, false, false},
		{RoleClient, true, false},
		{RoleClient, false, false},
	}
	for _, c := range cases {
		m := Membership{UserID: "u1", SalonID: "s1", Role: c.role, Attends: c.attends}
		if got := m.Bookable(); got != c.want {
			t.Fatalf("Membership %s attends=%v: Bookable()=%v, esperaba %v", c.role, c.attends, got, c.want)
		}
		s := StaffMember{ID: "u1", Name: "Ana", Role: c.role, Attends: c.attends}
		if got := s.Bookable(); got != c.want {
			t.Fatalf("StaffMember %s attends=%v: Bookable()=%v, esperaba %v", c.role, c.attends, got, c.want)
		}
	}
}

// S-F2a-4: el profesional nace atendiendo (es su rol); los demás roles nacen
// sin atender. El dueño de un salón recién creado lo enciende el caso de uso.
func TestNewMembership_attendsDefault(t *testing.T) {
	prof, err := NewMembership("u1", "s1", RoleProfessional)
	if err != nil {
		t.Fatalf("profesional: %v", err)
	}
	if !prof.Attends {
		t.Fatal("un profesional agregado tiene que nacer con attends=true")
	}
	for _, role := range []Role{RoleOwner, RoleAdmin, RoleReception, RoleClient} {
		m, err := NewMembership("u1", "s1", role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if m.Attends {
			t.Fatalf("%s no nace atendiendo: la marca la pone la pantalla de equipo", role)
		}
	}
}

// S-F2a-5: la marca cambia solo cuando el rol lo permite, con el mensaje exacto
// que ve la persona.
func TestMembership_SetAttends(t *testing.T) {
	casos := []struct {
		role    Role
		attends bool
		want    bool
		errMsg  string
	}{
		{RoleOwner, true, true, ""},
		{RoleOwner, false, false, ""},
		{RoleAdmin, true, true, ""},
		{RoleAdmin, false, false, ""},
		{RoleProfessional, true, true, ""},
		{RoleProfessional, false, false, "Las profesionales siempre atienden"},
		{RoleReception, true, false, "Ese rol no atiende clientes"},
		{RoleReception, false, false, "Ese rol no atiende clientes"},
		{RoleClient, true, false, "Ese rol no atiende clientes"},
		{RoleClient, false, false, "Ese rol no atiende clientes"},
	}
	for _, c := range casos {
		base := Membership{UserID: "u1", SalonID: "s1", Role: c.role}
		got, err := base.SetAttends(c.attends)
		if c.errMsg == "" {
			if err != nil {
				t.Fatalf("%s attends=%v: %v", c.role, c.attends, err)
			}
			if got.Attends != c.want {
				t.Fatalf("%s attends=%v: quedó %v", c.role, c.attends, got.Attends)
			}
			continue
		}
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("%s attends=%v: esperaba ErrValidation (400), dio %v", c.role, c.attends, err)
		}
		if err.Error() != c.errMsg {
			t.Fatalf("%s attends=%v: mensaje %q, esperaba %q", c.role, c.attends, err.Error(), c.errMsg)
		}
	}
	// El mensaje no arrastra el prefijo interno del error de dominio.
	if _, err := (Membership{Role: RoleReception}).SetAttends(true); strings.HasPrefix(err.Error(), "validación") {
		t.Fatalf("el mensaje no debe llevar el prefijo interno: %q", err.Error())
	}
}
