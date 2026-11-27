package app

import (
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
)

// El correo lo lee una persona: nombre del salón, nombre del cliente, fecha y
// hora en la zona del salón (con el nombre de la zona) y una línea para
// cambiar/cancelar. Nada de ids, tokens ni imágenes remotas.
func TestBookingMail_confirmacion(t *testing.T) {
	settings := domain.SalonSettings{SalonID: "s1", Timezone: "America/Mexico_City"}
	starts := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)

	msg := kernel.BookingMail(kernel.BookingMailConfirmation, "Navajas de Oro", "Lía", starts, settings)
	if msg.Subject != "Turno confirmado · Navajas de Oro" {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	for _, want := range []string{
		"Lía", "Navajas de Oro", "01/12 09:00 (America/Mexico_City)",
		"escribile al salón", "Te esperamos",
	} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("falta %q en el texto:\n%s", want, msg.Text)
		}
	}
	for _, forbidden := range []string{"http://", "https://", "s1"} {
		if strings.Contains(msg.Text, forbidden) || strings.Contains(msg.HTML, forbidden) {
			t.Fatalf("el correo no puede llevar %q:\n%s", forbidden, msg.Text)
		}
	}
	if !strings.Contains(msg.HTML, "<strong>Navajas de Oro</strong>") {
		t.Fatalf("el HTML no lleva el salón:\n%s", msg.HTML)
	}
}

func TestBookingMail_recordatorio(t *testing.T) {
	settings := domain.SalonSettings{SalonID: "s1", Timezone: "America/Mexico_City"}
	starts := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	msg := kernel.BookingMail(kernel.BookingMailReminder, "Navajas de Oro", "Lía", starts, settings)
	if msg.Subject != "Recordatorio de tu turno · Navajas de Oro" {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	if !strings.Contains(msg.Text, "Te recordamos tu turno") || !strings.Contains(msg.Text, "01/12 09:00 (America/Mexico_City)") {
		t.Fatalf("texto del recordatorio:\n%s", msg.Text)
	}
}

// El nombre del cliente y del salón los escribe otra persona: el HTML los
// escapa (un nombre con "<script>" no puede inyectar nada).
func TestBookingMail_html_escapado(t *testing.T) {
	settings := domain.SalonSettings{SalonID: "s1", Timezone: "America/Mexico_City"}
	starts := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	msg := kernel.BookingMail(kernel.BookingMailConfirmation, `Salón <b>"Oro"</b>`, `Lía <script>alert(1)</script>`, starts, settings)
	if strings.Contains(msg.HTML, "<script>") || strings.Contains(msg.HTML, "</b>") {
		t.Fatalf("HTML sin escapar:\n%s", msg.HTML)
	}
	if !strings.Contains(msg.HTML, "&lt;script&gt;") || !strings.Contains(msg.HTML, "&lt;b&gt;") {
		t.Fatalf("esperaba entidades escapadas:\n%s", msg.HTML)
	}
	// El texto plano no escapa (no es HTML): el nombre se lee tal cual.
	if !strings.Contains(msg.Text, "Lía <script>alert(1)</script>") {
		t.Fatalf("texto plano:\n%s", msg.Text)
	}
}
