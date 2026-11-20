package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewNotification_ok(t *testing.T) {
	n, err := NewNotification("s1", "", "  Cierre de caja  ", "  hubo un faltante de $200  ", KindCash)
	if err != nil {
		t.Fatalf("notificación válida: %v", err)
	}
	if n.ID == "" {
		t.Fatal("la notificación debe nacer con id")
	}
	if n.SalonID != "s1" {
		t.Fatalf("salon_id: %q", n.SalonID)
	}
	if n.UserID != "" {
		t.Fatalf("user_id vacío = aviso para todo el salón, dio %q", n.UserID)
	}
	if n.Title != "Cierre de caja" || n.Body != "hubo un faltante de $200" {
		t.Fatalf("texto sin recortar: %+v", n)
	}
	if n.Kind != KindCash {
		t.Fatalf("kind: %q", n.Kind)
	}
	if n.Read() || n.ReadAt != nil {
		t.Fatal("una notificación nueva nace no leída")
	}
	if n.CreatedAt.IsZero() {
		t.Fatal("created_at debe venir seteado")
	}
}

func TestNewNotification_aviso_a_un_usuario(t *testing.T) {
	n, err := NewNotification("s1", "u-prof", "Nuevo turno", "", KindBooking)
	if err != nil {
		t.Fatalf("aviso a un usuario: %v", err)
	}
	if n.UserID != "u-prof" {
		t.Fatalf("user_id: %q", n.UserID)
	}
}

func TestNewNotification_rechaza_invalidos(t *testing.T) {
	cases := []struct {
		name    string
		salonID string
		title   string
		kind    NotificationKind
	}{
		{"sin salón", "", "Aviso", KindInfo},
		{"título vacío", "s1", "   ", KindInfo},
		{"kind vacío", "s1", "Aviso", NotificationKind("")},
		{"kind desconocido", "s1", "Aviso", NotificationKind("push")},
	}
	for _, c := range cases {
		if _, err := NewNotification(c.salonID, "", c.title, "", c.kind); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: esperaba ErrValidation, dio %v", c.name, err)
		}
	}
	// El catálogo de kinds es cerrado y todos los válidos se aceptan.
	for _, k := range []NotificationKind{KindInfo, KindBooking, KindSale, KindCash} {
		if !k.Valid() {
			t.Fatalf("%q debería ser un kind válido", k)
		}
		if _, err := NewNotification("s1", "", "Aviso", "", k); err != nil {
			t.Fatalf("kind %q: %v", k, err)
		}
	}
}

func TestNotification_MarkRead(t *testing.T) {
	n, err := NewNotification("s1", "", "Aviso", "", KindInfo)
	if err != nil {
		t.Fatalf("nueva: %v", err)
	}
	at := time.Now().UTC()
	n.MarkRead(at)
	if !n.Read() || n.ReadAt == nil || !n.ReadAt.Equal(at) {
		t.Fatalf("MarkRead no dejó la marca: %+v", n)
	}
	// Marcar dos veces conserva la primera marca (idempotente).
	n.MarkRead(at.Add(time.Hour))
	if !n.ReadAt.Equal(at) {
		t.Fatalf("Marcar de nuevo no debe mover la fecha: %v", n.ReadAt)
	}
}
