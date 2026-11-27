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
package kernel

import (
	"html"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type BookingMailKind int

const (
	BookingMailConfirmation BookingMailKind = iota
	BookingMailReminder
	// BookingMailCancelled avisa que el turno quedó cancelado (por el enlace o
	// por el salón). No lleva enlace: ya no hay nada que cambiar.
	BookingMailCancelled
)

// BookingMail arma el mensaje SIN enlace. Es el respaldo de un turno que no tiene
// token: un turno sin email nunca llega acá, pero un envío cuyo token no se pudo
// emitir (crypto o DB caídos) sale igual, con la línea de contacto en lugar del
// link, porque un aviso sin enlace es mejor que ningún aviso.
func BookingMail(kind BookingMailKind, salonName, clientName string, startsAt time.Time, settings domain.SalonSettings) ports.Message {
	return BookingMailWithLink(kind, salonName, clientName, startsAt, settings, "")
}

// BookingMailWithLink arma el mensaje completo con el enlace de un solo uso. El
// destinatario lo pone quien llama (ports.Message.To), porque la plantilla no
// conoce direcciones.
func BookingMailWithLink(kind BookingMailKind, salonName, clientName string, startsAt time.Time, settings domain.SalonSettings, link string) ports.Message {
	salon := strings.TrimSpace(salonName)
	name := strings.TrimSpace(clientName)
	when := FormatSalonTime(settings, startsAt)
	link = strings.TrimSpace(link)

	subject := "Turno confirmado · " + salon
	intro := "Tu turno en " + salon + " quedó confirmado."
	switch kind {
	case BookingMailReminder:
		subject = "Recordatorio de tu turno · " + salon
		intro = "Te recordamos tu turno en " + salon + "."
	case BookingMailCancelled:
		subject = "Turno cancelado · " + salon
		intro = "Tu turno en " + salon + " quedó cancelado."
	}

	// La línea de contacto se conserva SIEMPRE (D8): si no hay enlace, es la
	// única salida para la persona.
	contact := "Si necesitás cambiarlo o cancelarlo, escribile al salón."
	if kind == BookingMailCancelled {
		contact = "Si querés agendar otro turno, escribile al salón."
	}
	lines := []string{"Hola " + name + ":", "", intro, "", "Fecha y hora: " + when, ""}
	if link != "" && kind != BookingMailCancelled {
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
	if kind == BookingMailCancelled {
		closing = "¡Gracias!"
	}
	lines = append(lines, "", closing, "")
	text := strings.Join(lines, "\n")

	salonHTML := html.EscapeString(salon)
	nameHTML := html.EscapeString(name)
	whenHTML := html.EscapeString(when)
	body := "Tu turno en <strong>" + salonHTML + "</strong> quedó confirmado."
	switch kind {
	case BookingMailReminder:
		body = "Te recordamos tu turno en <strong>" + salonHTML + "</strong>."
	case BookingMailCancelled:
		body = "Tu turno en <strong>" + salonHTML + "</strong> quedó cancelado."
	}
	// HTML simple y autocontenido: sin imágenes remotas ni recursos externos.
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola " + nameHTML + ":</p>\n" +
		"<p>" + body + "</p>\n" +
		"<p><strong>Fecha y hora:</strong> " + whenHTML + "</p>\n"
	if link != "" && kind != BookingMailCancelled {
		htmlBody += `<p><a href="` + html.EscapeString(link) + `">Cambiar o cancelar el turno</a></p>` + "\n" +
			"<p>Si preferís hablarlo, escribile al salón.</p>\n"
	} else {
		htmlBody += "<p>" + html.EscapeString(contact) + "</p>\n"
	}
	htmlBody += "<p>" + closing + "</p>\n" +
		"</body></html>\n"

	return ports.Message{Subject: subject, Text: text, HTML: htmlBody}
}

// BookingLinkURL arma el enlace que viaja en el email. El token va en el
// FRAGMENTO: lo que sigue al "#" no se manda al servidor, así no queda en ningún
// log de la API ni de un proxy. Sin AppURL configurada se usa el respaldo del
// proyecto (el mismo de las invitaciones).
func BookingLinkURL(appURL, token string) string {
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

// MintBookingLink emite un token NUEVO de un turno y guarda SOLO su SHA-256.
// Devuelve el enlace y el token en claro (que solo vive en la llamada y en el
// correo).
//
// Vive en el núcleo porque lo piden DOS funcionalidades: la reserva (confirmación
// y recordatorio) y la seña (confirmación de la reserva ya pagada). Ninguna de
// las dos importa a la otra por esto.
func MintBookingLink(repo ports.BookingLinkRepository, salonID, bookingID string, now time.Time) (domain.BookingLink, string, error) {
	token, err := domain.NewBookingLinkToken()
	if err != nil {
		return domain.BookingLink{}, "", err
	}
	link, err := domain.NewBookingLink(salonID, bookingID, domain.HashBookingLinkToken(token), now)
	if err != nil {
		return domain.BookingLink{}, "", err
	}
	if err := repo.Save(link); err != nil {
		return domain.BookingLink{}, "", err
	}
	return link, token, nil
}
