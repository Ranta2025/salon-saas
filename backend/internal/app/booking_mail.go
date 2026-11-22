// Plantillas de los correos a clientes (confirmación y recordatorio). Es un
// archivo de copia, no un caso de uso: quien decide CUÁNDO se manda es
// PublicBookingService (confirmación) y ReminderService (recordatorio).
//
// Reglas de la copia (T12): español, nombre del salón, nombre de la persona,
// fecha y hora en la ZONA DEL SALÓN con el nombre de la zona (FormatSalonTime),
// una línea para cambiar o cancelar (el salón no tiene campos de contacto: no
// se inventan) y NADA de ids ni tokens. El HTML escapa lo que escribe otra
// persona (nombres de salón y cliente).
package app

import (
	"html"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type bookingMailKind int

const (
	bookingMailConfirmation bookingMailKind = iota
	bookingMailReminder
)

// bookingMail arma el mensaje completo. El destinatario lo pone quien llama
// (ports.Message.To), porque la plantilla no conoce direcciones.
func bookingMail(kind bookingMailKind, salonName, clientName string, startsAt time.Time, settings domain.SalonSettings) ports.Message {
	salon := strings.TrimSpace(salonName)
	name := strings.TrimSpace(clientName)
	when := FormatSalonTime(settings, startsAt)

	subject := "Turno confirmado · " + salon
	intro := "Tu turno en " + salon + " quedó confirmado."
	if kind == bookingMailReminder {
		subject = "Recordatorio de tu turno · " + salon
		intro = "Te recordamos tu turno en " + salon + "."
	}

	text := strings.Join([]string{
		"Hola " + name + ":",
		"",
		intro,
		"",
		"Fecha y hora: " + when,
		"",
		"Si necesitás cambiarlo o cancelarlo, escribile al salón.",
		"",
		"¡Te esperamos!",
		"",
	}, "\n")

	salonHTML := html.EscapeString(salon)
	nameHTML := html.EscapeString(name)
	whenHTML := html.EscapeString(when)
	body := "Tu turno en <strong>" + salonHTML + "</strong> quedó confirmado."
	if kind == bookingMailReminder {
		body = "Te recordamos tu turno en <strong>" + salonHTML + "</strong>."
	}
	// HTML simple y autocontenido: sin imágenes remotas ni recursos externos.
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola " + nameHTML + ":</p>\n" +
		"<p>" + body + "</p>\n" +
		"<p><strong>Fecha y hora:</strong> " + whenHTML + "</p>\n" +
		"<p>Si necesitás cambiarlo o cancelarlo, escribile al salón.</p>\n" +
		"<p>¡Te esperamos!</p>\n" +
		"</body></html>\n"

	return ports.Message{Subject: subject, Text: text, HTML: htmlBody}
}
