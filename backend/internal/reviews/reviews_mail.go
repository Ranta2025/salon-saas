// Plantilla del correo que pide la reseña (F13, D3). Es un archivo de copia, no
// un caso de uso: quien decide CUÁNDO se manda es Service.RunOnce.
//
// Reglas de la copia (las mismas de los correos de F15 y de la reserva): español,
// el enlace con el token en el FRAGMENTO (`/resena#<token>`: lo que va después de
// "#" no se manda al servidor, así no queda en ningún log), el HTML escapa todo
// lo variable y NADA de ids, correos ajenos ni datos del turno más allá del salón,
// el servicio y la fecha.
package reviews

import (
	"html"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// reviewRequestMail arma el correo «¿cómo te fue?». El destinatario lo pone el
// caso de uso (ports.Message.To lo completa el adaptador de correo: acá va el
// email del turno).
func reviewRequestMail(d ports.DueReview, link string) ports.Message {
	salon := strings.TrimSpace(d.SalonName)
	if salon == "" {
		salon = "el salón"
	}
	// La fecha del turno va en la ZONA DEL SALÓN, como los correos de la reserva.
	settings := domain.SalonSettings{SalonID: d.SalonID, Timezone: d.Timezone}
	if _, err := settings.Location(); err != nil {
		// Zona ilegible: se dice UTC explícitamente en vez de mostrar una hora sin
		// referencia (mismo criterio de kernel.FormatSalonTime).
		settings = domain.SalonSettings{SalonID: d.SalonID, Timezone: "UTC"}
	}
	when := kernel.FormatSalonTime(settings, d.StartsAt)
	greeting := "Hola:"
	if name := firstWord(d.ClientName); name != "" {
		greeting = "Hola " + name + ":"
	}

	lines := []string{
		greeting,
		"",
		"Gracias por tu visita a " + salon + ".",
		"Nos gustaría saber cómo te fue: podés dejarnos una reseña con una calificación de 1 a 5 estrellas y un comentario.",
		"",
		"Tu visita: " + when,
	}
	if svc := ServiceName(d.ServiceName); svc != "" {
		lines = append(lines, "Servicio: "+svc)
	}
	lines = append(lines,
		"",
		"Para dejar tu reseña, abrí este enlace:",
		link,
		"",
		"El enlace vence en 30 días y sirve una sola vez.",
		"Tu reseña se publica con tu nombre y la inicial de tu apellido: nunca con tu correo.",
		"",
		"¡Gracias!",
		"",
	)
	text := strings.Join(lines, "\n")

	salonHTML := html.EscapeString(salon)
	whenHTML := html.EscapeString(when)
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>" + html.EscapeString(greeting) + "</p>\n" +
		"<p>Gracias por tu visita a <strong>" + salonHTML + "</strong>.</p>\n" +
		"<p>Nos gustaría saber cómo te fue: podés dejarnos una reseña con una calificación de 1 a 5 estrellas y un comentario.</p>\n" +
		"<p><strong>Tu visita:</strong> " + whenHTML + "</p>\n"
	if svc := ServiceName(d.ServiceName); svc != "" {
		htmlBody += "<p><strong>Servicio:</strong> " + html.EscapeString(svc) + "</p>\n"
	}
	htmlBody += `<p><a href="` + html.EscapeString(link) + `">Dejar mi reseña</a></p>` + "\n" +
		"<p>El enlace vence en 30 días y sirve una sola vez.</p>\n" +
		"<p>Tu reseña se publica con tu nombre y la inicial de tu apellido: nunca con tu correo.</p>\n" +
		"<p>¡Gracias!</p>\n" +
		"</body></html>\n"

	return ports.Message{
		To:      strings.TrimSpace(d.Email),
		Subject: "¿Cómo te fue en " + salon + "?",
		Text:    text,
		HTML:    htmlBody,
	}
}

// firstWord es el nombre de pila del saludo: el correo no lleva el apellido.
func firstWord(name string) string {
	fields := strings.Fields(cleanText(name, MaxAuthorNameLen))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
