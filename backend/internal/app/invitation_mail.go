// Plantilla del correo de invitación (S-F2g1-5). Es un archivo de copia, no un
// caso de uso: quien decide CUÁNDO se manda es InvitationService.
//
// Reglas: español, nombre del salón, el rol en español ("profesional",
// "administración", "recepción"), la fecha de vencimiento en la zona del salón,
// el enlace con el token en el fragmento y NADA más (ni ids ni datos internos).
// El HTML escapa el nombre del salón, que lo escribe una persona.
package app

import (
	"html"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// invitationMail arma el mensaje completo. El destinatario lo pone quien llama
// (ports.Message.To), porque la plantilla no conoce direcciones. `expires` es la
// fecha ya formateada en el día LOCAL del salón (ver expiryLabel): el día que
// importa es el del salón, no el de UTC.
func invitationMail(salonName string, inv domain.Invitation, link, expires string) ports.Message {
	salon := strings.TrimSpace(salonName)
	role := inv.Role.Label()
	subject := salon + " te invitó a su equipo"

	text := strings.Join([]string{
		"Hola:",
		"",
		salon + " te invitó a sumarte a su equipo como " + role + ".",
		"",
		"Para crear tu cuenta y entrar, abrí este enlace:",
		link,
		"",
		"El enlace vence el " + expires + " y sirve una sola vez.",
		"Si no esperabas esta invitación, podés ignorar este correo.",
		"",
	}, "\n")

	salonHTML := html.EscapeString(salon)
	roleHTML := html.EscapeString(role)
	linkHTML := html.EscapeString(link)
	// HTML simple y autocontenido: sin imágenes remotas ni recursos externos.
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola:</p>\n" +
		"<p><strong>" + salonHTML + "</strong> te invitó a sumarte a su equipo como " + roleHTML + ".</p>\n" +
		`<p><a href="` + linkHTML + `">Creá tu cuenta y entrá</a></p>\n` +
		"<p>El enlace vence el " + html.EscapeString(expires) + " y sirve una sola vez.</p>\n" +
		"<p>Si no esperabas esta invitación, podés ignorar este correo.</p>\n" +
		"</body></html>\n"

	return ports.Message{Subject: subject, Text: text, HTML: htmlBody, To: inv.Email}
}

// expiryLabel formatea la fecha de vencimiento en la zona del salón. Una zona
// desconocida cae a UTC: el correo sale igual, con una fecha ordenada.
func expiryLabel(settings domain.SalonSettings, t time.Time) string {
	loc := time.UTC
	if settings.Timezone != "" {
		if l, err := time.LoadLocation(settings.Timezone); err == nil {
			loc = l
		}
	}
	return t.In(loc).Format("02/01/2006")
}
