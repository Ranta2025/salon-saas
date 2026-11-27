// Plantilla del aviso de la lista de espera (F7/D7). Es copia, no un caso de
// uso: quien decide CUÁNDO se manda es WaitlistService.RunOnce.
//
// Reglas de la copia: español, nombre del salón, nombre de la persona, servicio,
// el DÍA LOCAL del salón, el enlace a la página pública del salón para reservar,
// el enlace de baja (que va en el FRAGMENTO de la URL, así no queda en ningún
// log) y el aviso de que se escribe solo por esta lista. Nada de ids, ni de
// teléfonos, ni de direcciones de otras personas.
package waitlist

import (
	"html"
	"strings"
	"time"

	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// waitlistMailData es todo lo que la plantilla necesita (nada de la DB).
type waitlistMailData struct {
	SalonName   string
	ServiceName string
	// Day es el día pedido, "YYYY-MM-DD" del calendario LOCAL del salón.
	Day string
	// Location es la zona del salón (para mostrar el día con su nombre).
	Location *time.Location
	// BookURL es la página pública del salón; LeaveURL, la de baja.
	BookURL  string
	LeaveURL string
}

// waitlistLeaveNotice es la frase que el contrato exige en el cuerpo (D7):
// quien recibe el aviso tiene que saber que no va a entrar en ninguna lista de
// correo.
const waitlistLeaveNotice = "te escribimos solo por esta lista; ya no recibirás más avisos de esta entrada"

// waitlistMail arma el mensaje completo. El destinatario lo pone quien llama
// (ports.Message.To), porque la plantilla no conoce direcciones.
func waitlistMail(d waitlistMailData, name string) ports.Message {
	salon := strings.TrimSpace(d.SalonName)
	person := strings.TrimSpace(name)
	service := strings.TrimSpace(d.ServiceName)
	when := waitlistDayLabel(d.Day, d.Location)

	subject := "Se liberó un turno en " + salon

	lines := []string{
		"Hola " + person + ":",
		"",
		"Se liberó un turno en " + salon + " para " + service + " el " + when + ".",
		"",
		"Si todavía te sirve, reservalo acá:",
		d.BookURL,
		"",
		"Es un aviso: el turno no queda reservado a tu nombre hasta que lo reserves.",
		"",
		"Si no querés recibir más avisos de esta entrada, date de baja acá:",
		d.LeaveURL,
		"",
		"Una aclaración: " + waitlistLeaveNotice + ".",
		"",
		"¡Gracias!",
		"",
	}
	text := strings.Join(lines, "\n")

	// HTML simple y autocontenido (sin imágenes remotas) y escapado: el nombre
	// del salón, el de la persona y el del servicio los escribe otra persona.
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola " + html.EscapeString(person) + ":</p>\n" +
		"<p>Se liberó un turno en <strong>" + html.EscapeString(salon) + "</strong> para " +
		html.EscapeString(service) + " el " + html.EscapeString(when) + ".</p>\n" +
		`<p><a href="` + html.EscapeString(d.BookURL) + `">Reservar el turno</a></p>` + "\n" +
		"<p>Es un aviso: el turno no queda reservado a tu nombre hasta que lo reserves.</p>\n" +
		`<p><a href="` + html.EscapeString(d.LeaveURL) + `">Dejar de recibir avisos de esta entrada</a></p>` + "\n" +
		"<p>Una aclaración: " + html.EscapeString(waitlistLeaveNotice) + ".</p>\n" +
		"<p>¡Gracias!</p>\n" +
		"</body></html>\n"

	return ports.Message{Subject: subject, Text: text, HTML: htmlBody}
}

// waitlistDayLabel muestra el día local como "02/01/2027 (America/Mexico_City)".
// La zona va explícita: el día es del calendario del salón, no del de quien lee
// el correo en otro país.
func waitlistDayLabel(day string, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	parsed, err := time.ParseInLocation(kernel.DateLayout, strings.TrimSpace(day), loc)
	if err != nil {
		// Un día ilegible es un bug de datos: se muestra tal cual antes que
		// inventar una fecha.
		return strings.TrimSpace(day)
	}
	return parsed.Format("02/01/2006") + " (" + loc.String() + ")"
}

// waitlistLeaveURL arma el enlace de baja del aviso. El token va en el
// FRAGMENTO (`/lista-de-espera#<token>`): lo que sigue al "#" no se manda al
// servidor, así que no queda en ningún log de la API ni de un proxy.
func waitlistLeaveURL(appURL, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	return publicBaseURL(appURL) + "/lista-de-espera#" + token
}

// publicSalonURL es la página pública del salón, donde se reserva.
func publicSalonURL(appURL, slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return publicBaseURL(appURL)
	}
	return publicBaseURL(appURL) + "/salon/" + slug
}

// publicBaseURL es APP_URL sin la barra final, con el respaldo del proyecto
// cuando no está configurada (el mismo de las invitaciones y los enlaces de
// turno).
func publicBaseURL(appURL string) string {
	base := strings.TrimSpace(appURL)
	if base == "" {
		base = kernel.DefaultAppURL
	}
	return strings.TrimRight(base, "/")
}
