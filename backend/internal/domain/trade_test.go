package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// S-F2c-2: el oficio es un texto visible (barbero, estilista...) que solo
// tienen quienes atienden. Se guarda sin espacios de sobra, con tope de 40
// caracteres y sin saltos de línea (no es un currículum).
func TestMembership_SetTrade(t *testing.T) {
	pro, err := NewMembership("u-pro", "s1", RoleProfessional)
	if err != nil {
		t.Fatalf("membresía: %v", err)
	}
	if pro.Trade != "" {
		t.Fatalf("el oficio nace vacío: %q", pro.Trade)
	}

	// Se guarda recortado: lo que ve la clienta no lleva espacios de sobra.
	withTrade, err := pro.SetTrade("  Barbero  ")
	if err != nil {
		t.Fatalf("oficio de un profesional: %v", err)
	}
	if withTrade.Trade != "Barbero" || withTrade.UserID != "u-pro" || withTrade.Role != RoleProfessional {
		t.Fatalf("membresía con oficio: %+v", withTrade)
	}

	// Vacío es válido: borrar el oficio no rompe nada.
	if cleared, err := withTrade.SetTrade("   "); err != nil || cleared.Trade != "" {
		t.Fatalf("borrar el oficio: %+v %v", cleared, err)
	}

	// Exactamente 40 caracteres entra; 41 no.
	if _, err := pro.SetTrade(strings.Repeat("á", MaxTextTrade)); err != nil {
		t.Fatalf("40 caracteres tienen que entrar: %v", err)
	}
	_, err = pro.SetTrade(strings.Repeat("á", MaxTextTrade+1))
	if !errors.Is(err, ErrValidation) || err.Error() != "El oficio no puede superar los 40 caracteres" {
		t.Fatalf("41 caracteres: %v", err)
	}

	// Un salto de línea rompe la etiqueta en la pantalla de equipo.
	for _, bad := range []string{"Barbero\nEstilista", "Barbero\rEstilista"} {
		if _, err := pro.SetTrade(bad); !errors.Is(err, ErrValidation) ||
			err.Error() != "El oficio no puede tener saltos de línea" {
			t.Fatalf("oficio con salto de línea %q: %v", bad, err)
		}
	}

	// Solo quien atiende tiene oficio: recepción y cliente nunca, y el dueño o
	// la administración únicamente con la marca encendida.
	for _, role := range []Role{RoleReception, RoleClient} {
		m, _ := NewMembership("u-"+string(role), "s1", role)
		if _, err := m.SetTrade("Barbero"); !errors.Is(err, ErrValidation) || err.Error() != "Ese rol no tiene oficio" {
			t.Fatalf("oficio en %s: %v", role, err)
		}
	}
	owner, _ := NewMembership("u-own", "s1", RoleOwner)
	if _, err := owner.SetTrade("Barbero"); !errors.Is(err, ErrValidation) || err.Error() != "Ese rol no tiene oficio" {
		t.Fatalf("oficio en un dueño que no atiende: %v", err)
	}
	barbero, _ := owner.SetAttends(true)
	if got, err := barbero.SetTrade("Barbero"); err != nil || got.Trade != "Barbero" {
		t.Fatalf("oficio en el dueño-barbero: %+v %v", got, err)
	}
}

// S-F2c-2: el turno puede llevar el servicio reservado. Es opcional: la reserva
// de staff (calendario) no exige servicio y sigue funcionando como hoy.
func TestBooking_ServiceID_opcional(t *testing.T) {
	day := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	b, err := NewBooking("s1", "prof1", "Lía", "555", day, day.Add(time.Hour))
	if err != nil {
		t.Fatalf("reserva: %v", err)
	}
	if b.ServiceID != "" {
		t.Fatalf("sin servicio el turno queda sin servicio, no con basura: %q", b.ServiceID)
	}
	b.ServiceID = "svc-1"
	if b.ServiceID != "svc-1" {
		t.Fatal("el turno tiene que poder llevar el servicio reservado")
	}
}
