// Correos de la cobertura de una ausencia (F2d3): el turno se mantiene y quién
// lo atiende ahora, o el turno se cancela y el salón se comunica. Es un archivo
// de copia, como booking_mail.go: quien decide CUÁNDO se manda es
// CoverageService.
//
// Mismas reglas de la copia (T12): español, nombre del salón, nombre de la
// persona, fecha y hora en la ZONA DEL SALÓN (FormatSalonTime), sin ids ni
// tokens, y el HTML escapa lo que escribe otra persona.
package schedules

import (
	"html"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

type coverMailKind int

const (
	// coverMailReassigned: el turno se mantiene, lo atiende otra persona.
	coverMailReassigned coverMailKind = iota
	// coverMailCancelled: el turno se canceló y el salón va a contactarla.
	coverMailCancelled
)

// coverMail arma el mensaje del cambio de turno. professionalName solo se usa al
// reasignar (al cancelar va vacío).
func coverMail(kind coverMailKind, salonName, clientName, professionalName string, startsAt time.Time, settings domain.SalonSettings) ports.Message {
	salon := strings.TrimSpace(salonName)
	name := strings.TrimSpace(clientName)
	who := strings.TrimSpace(professionalName)
	when := kernel.FormatSalonTime(settings, startsAt)

	subject := "Tu turno sigue en pie · " + salon
	text := []string{
		"Hola " + name + ":",
		"",
	}
	body := ""
	switch kind {
	case coverMailCancelled:
		subject = "Turno cancelado · " + salon
		text = append(text, "Tu turno en "+salon+" fue cancelado.")
		body = "Tu turno en <strong>" + html.EscapeString(salon) + "</strong> fue cancelado."
	default:
		line := "Tu turno en " + salon + " se mantiene en el mismo horario."
		if who != "" {
			line = "Tu turno en " + salon + " se mantiene en el mismo horario y te va a atender " + who + "."
		}
		text = append(text, line)
		body = "Tu turno en <strong>" + html.EscapeString(salon) + "</strong> se mantiene en el mismo horario."
		if who != "" {
			body += " Te va a atender <strong>" + html.EscapeString(who) + "</strong>."
		}
	}
	text = append(text, "", "Fecha y hora: "+when, "")
	advice := "Si necesitás cambiarlo o cancelarlo, escribile al salón."
	if kind == coverMailCancelled {
		advice = "El salón se va a comunicar con vos para reprogramarlo."
	}
	text = append(text, advice, "", "¡Te esperamos!", "")

	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola " + html.EscapeString(name) + ":</p>\n" +
		"<p>" + body + "</p>\n" +
		"<p><strong>Fecha y hora:</strong> " + html.EscapeString(when) + "</p>\n" +
		"<p>" + html.EscapeString(advice) + "</p>\n" +
		"<p>¡Te esperamos!</p>\n" +
		"</body></html>\n"

	return ports.Message{Subject: subject, Text: strings.Join(text, "\n"), HTML: htmlBody}
}
