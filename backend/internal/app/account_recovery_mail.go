// Plantillas de los correos de F15 (recuperar contraseña y verificar email).
// Son archivos de copia, no casos de uso: quien decide CUÁNDO se manda es
// AccountRecoveryService / EmailVerificationService.
//
// Reglas: español, el enlace con el token en el FRAGMENTO (lo que va después de
// "#" no se manda al servidor, así no queda en ningún log), el HTML escapa todo lo
// variable y nada más (ni ids, ni nombres, ni datos internos).
package app

import (
	"html"
	"strings"

	"salon-saas/internal/ports"
)

// recoveryLink arma `<APP_URL><path>#<token>`. El token va SIEMPRE en el
// fragmento, nunca en el query.
func recoveryLink(appURL, path, token string) string {
	base := strings.TrimSpace(appURL)
	if base == "" {
		base = DefaultAppURL
	}
	return strings.TrimRight(base, "/") + path + "#" + token
}

// resetMail es el correo de restablecer la contraseña (asunto del contrato D2).
func resetMail(to, link string) ports.Message {
	text := strings.Join([]string{
		"Hola:",
		"",
		"Pediste restablecer tu contraseña. Para elegir una nueva, abrí este enlace:",
		link,
		"",
		"El enlace vence en 60 minutos y sirve una sola vez.",
		"Si no lo pediste, podés ignorar este correo: tu contraseña no cambia.",
		"",
	}, "\n")
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola:</p>\n" +
		"<p>Pediste restablecer tu contraseña.</p>\n" +
		`<p><a href="` + html.EscapeString(link) + `">Elegí una contraseña nueva</a></p>` + "\n" +
		"<p>El enlace vence en 60 minutos y sirve una sola vez.</p>\n" +
		"<p>Si no lo pediste, podés ignorar este correo: tu contraseña no cambia.</p>\n" +
		"</body></html>\n"
	return ports.Message{Subject: "Restablece tu contrasena", Text: text, HTML: htmlBody, To: to}
}

// verificationMail es el correo de verificar el email (asunto del contrato D7).
func verificationMail(to, link string) ports.Message {
	text := strings.Join([]string{
		"Hola:",
		"",
		"Para confirmar que este correo es tuyo, abrí este enlace:",
		link,
		"",
		"El enlace vence en 24 horas y sirve una sola vez.",
		"Si no creaste una cuenta, podés ignorar este correo.",
		"",
	}, "\n")
	htmlBody := "<!doctype html>\n" +
		`<html lang="es"><body style="font-family:Arial,Helvetica,sans-serif;line-height:1.5;color:#222">` + "\n" +
		"<p>Hola:</p>\n" +
		"<p>Para confirmar que este correo es tuyo:</p>\n" +
		`<p><a href="` + html.EscapeString(link) + `">Verificá tu email</a></p>` + "\n" +
		"<p>El enlace vence en 24 horas y sirve una sola vez.</p>\n" +
		"<p>Si no creaste una cuenta, podés ignorar este correo.</p>\n" +
		"</body></html>\n"
	return ports.Message{Subject: "Verifica tu email", Text: text, HTML: htmlBody, To: to}
}
