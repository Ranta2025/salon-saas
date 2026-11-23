// Plantillas de los correos a clientes (confirmación, recordatorio y aviso de
// cancelación). Es un archivo de copia, no un caso de uso: quien decide CUÁNDO
// se manda es PublicBookingService (confirmación), ReminderService (recordatorio)
// y BookingLinkService (cancelación y reprogramación).
//
// Reglas de la copia (T12 + F1/D8): español, nombre del salón, nombre de la
// persona, fecha y hora en la ZONA DEL SALÓN con el nombre de la zona
// (FormatSalonTime), el ENLACE de un solo uso para cambiar o cancelar (va en el
// fragmento de la URL: `/turno#<token>`, así no queda en ningún log) más una
// línea de contacto para hablar con el salón, y NADA de ids ni teléfonos. El
// HTML escapa lo que escribe otra persona (nombres de salón y cliente).
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
	// bookingMailCancelled avisa que el turno quedó cancelado (por el enlace o
	// por el salón). No lleva enlace: ya no hay nada que cambiar.
	bookingMailCancelled
)

// bookingMail arma el mensaje SIN enlace. Es el respaldo de un turno que no tiene
// token: un turno sin email nunca llega acá, pero un envío cuyo token no se pudo
// emitir (crypto o DB caídos) sale igual, con la línea de contacto en lugar del
// link, porque un aviso sin enlace es mejor que ningún aviso.
func bookingMail(kind bookingMailKind, salonName, clientName string, startsAt time.Time, settings domain.SalonSettings) ports.Message {
	return bookingMailWithLink(kind, salonName, clientName, startsAt, settings, "")
}

// bookingMailWithLink arma el mensaje completo con el enlace de un solo uso. El
// destinatario lo pone quien llama (ports.Message.To), porque la plantilla no
// conoce direcciones.
func bookingMailWithLink(kind bookingMailKind, salonName, clientName string, startsAt time.Time, settings domain.SalonSettings, link string) ports.Message {
	salon := strings.TrimSpace(salonName)
	name := strings.TrimSpace(clientName)
	when := FormatSalonTime(settings, startsAt)
	link = strings.TrimSpace(link)

	subject := "Turno confirmado · " + salon
	intro := "Tu turno en " + salon + " quedó confirmado."
	switch kind {
	case bookingMailReminder:
		subject = "Recordatorio de tu turno · " + salon
		intro = "Te recordamos tu turno en " + salon + "."
	case bookingMailCancelled:
		subject = "Turno cancelado · " + salon
		intro = "Tu turno en " + salon + " quedó cancelado."
	}

	// La línea de contacto se conserva SIEMPRE (D8): si no hay enlace, es la
	// única salida para la persona.
	contact := "Si necesitás cambiarlo o cancelarlo, escribile al salón."
	if kind == bookingMailCancelled {
		contact = "Si querés agendar otro turno, escribile al salón."
	}
	lines := []string{"Hola " + name + ":", "", intro, "", "Fecha y hora: " + when, ""}
	if link != "" && kind != bookingMailCancelled {
		lines = append(lines,
			"Para cambiarlo o cancelarlo, abrí este enlace:",
			link,
			"",
			"Si preferís hablarlo, escribile al salón.",
		)
	} else {
		lines = append(lines, contact)
	}
	closing := "¡Te esperamos!"
	if kind == bookingMailCancelled {
		closing = "¡Gracias!"
	}
	lines = append(lines, "", closing, "")
	text := strings.Join(lines, "\n")

	salonHTML := html.EscapeString(salon)
	nameHTML := html.EscapeString(name)
	whenHTML := html.EscapeString(when)
	body := "Tu turno en <strong>" + salonHTML + "</strong> quedó confirmado."
	switch kind {
	case bookingMailReminder:
		body = "Te recordamos tu turno en <strong>" + salonHTML + "</strong>."
	case bookingMailCancelled:
		body = "Tu turno en <strong>" + salonHTML + "</strong> quedó cancelado."
	}
	// HTML simple y autocontenido: sin imágenes remotas ni recursos externos.
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola " + nameHTML + ":</p>\n" +
		"<p>" + body + "</p>\n" +
		"<p><strong>Fecha y hora:</strong> " + whenHTML + "</p>\n"
	if link != "" && kind != bookingMailCancelled {
		htmlBody += `<p><a href="` + html.EscapeString(link) + `">Cambiar o cancelar el turno</a></p>` + "\n" +
			"<p>Si preferís hablarlo, escribile al salón.</p>\n"
	} else {
		htmlBody += "<p>" + html.EscapeString(contact) + "</p>\n"
	}
	htmlBody += "<p>" + closing + "</p>\n" +
		"</body></html>\n"

	return ports.Message{Subject: subject, Text: text, HTML: htmlBody}
}

// bookingLinkURL arma el enlace que viaja en el email. El token va en el
// FRAGMENTO: lo que sigue al "#" no se manda al servidor, así no queda en ningún
// log de la API ni de un proxy. Sin AppURL configurada se usa el respaldo del
// proyecto (el mismo de las invitaciones).
func bookingLinkURL(appURL, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	base := strings.TrimSpace(appURL)
	if base == "" {
		base = DefaultAppURL
	}
	return strings.TrimRight(base, "/") + "/turno#" + token
}
