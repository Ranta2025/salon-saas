// ReminderService: la tarea periódica que manda el recordatorio de los turnos
// que empiezan en las próximas 24 h. Una pasada = reclamar (atómico, en la DB)
// y enviar. La idempotencia real vive en la tabla booking_emails: lo reclamado
// queda con dueño por 5 minutos y lo enviado no vuelve nunca.
package app

import (
	"context"
	"log"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

const (
	// reminderBatchLimit acota cuántos correos manda una pasada: un lote
	// enorme no puede ocupar el proceso ni el servidor SMTP de una vez.
	reminderBatchLimit = 50
	// reminderSendTimeout es el techo de UN envío: un SMTP colgado no puede
	// frenar el resto del lote ni el siguiente tick.
	reminderSendTimeout = 15 * time.Second
)

// ReminderService.RunOnce envía los recordatorios que vencen en [now, now+24h).
// Devuelve cuántos salieron y cuántos fallaron. Un fallo de envío deja el
// recordatorio sin marcar (vuelve en un próximo tick, respetando los 3 intentos
// y la gracia de 5 minutos del claim) y NUNCA frena al resto del lote.
type ReminderService struct {
	Emails   ports.BookingEmailRepository
	Mailer   ports.MailSender
	Settings ports.SalonSettingsRepository
	// Links emite el enlace de un solo uso del recordatorio (F1/D1): un token
	// NUEVO por cada intento de envío, guardado solo como SHA-256. nil = el
	// recordatorio sale sin enlace (con la línea de contacto).
	Links ports.BookingLinkRepository
	// AppURL es la base del enlace (APP_URL).
	AppURL string
}

func (s ReminderService) RunOnce(ctx context.Context, now time.Time) (sent, failed int) {
	// Sin repositorio o sin sender (email deshabilitado) no hay nada que hacer;
	// ni siquiera se reclama: los recordatorios quedan intactos para cuando el
	// entorno tenga correo.
	if s.Emails == nil || s.Mailer == nil || ctx.Err() != nil {
		return 0, 0
	}
	due, err := s.Emails.ClaimDueReminders(now, reminderBatchLimit)
	if err != nil {
		log.Printf("recordatorios: reclamar vencidos: %v", err)
		return 0, 0
	}
	for _, d := range due {
		// Apagado en medio del lote: lo reclamado y no enviado vuelve solo
		// después de la gracia del claim.
		if ctx.Err() != nil {
			return sent, failed
		}
		// La hora del correo es la del salón. Si su configuración no se puede
		// leer, el recordatorio sale igual en UTC (mejor una hora con zona que
		// ningún recordatorio).
		settings, err := kernel.LoadSalonSettings(s.Settings, d.SalonID)
		if err != nil {
			log.Printf("recordatorios: settings del salón %s: %v", d.SalonID, err)
			settings = domain.SalonSettings{SalonID: d.SalonID, Timezone: "UTC"}
		}
		// El enlace del recordatorio es SUYO (token nuevo): los otros tokens vivos
		// del turno siguen valiendo hasta que se usen (D2). Si no se puede emitir,
		// el recordatorio sale igual, con la línea de contacto.
		link := ""
		if s.Links != nil {
			if _, token, err := mintBookingLink(s.Links, d.SalonID, d.BookingID, now); err != nil {
				// Sin el token en el log: el id del turno y el salón alcanzan.
				log.Printf("recordatorios: emitir el enlace del turno %s del salón %s: %v", d.BookingID, d.SalonID, err)
			} else {
				link = bookingLinkURL(s.AppURL, token)
			}
		}
		msg := bookingMailWithLink(bookingMailReminder, d.SalonName, d.ClientName, d.StartsAt, settings, link)
		msg.To = d.Email

		sendCtx, cancel := context.WithTimeout(ctx, reminderSendTimeout)
		err = s.Mailer.Send(sendCtx, msg)
		cancel()
		if err != nil {
			failed++
			// Sin dirección ni cuerpo en el log: counts y el id del turno
			// alcanzan para diagnosticar.
			log.Printf("recordatorios: enviar el turno %s del salón %s: %v", d.BookingID, d.SalonID, err)
			continue
		}
		if err := s.Emails.MarkReminderSent(d.BookingID); err != nil {
			// El correo ya salió; se cuenta como enviado. Si el marcado falló,
			// tras la gracia podría repetirse: se prefiere un recordatorio
			// repetido a uno perdido.
			log.Printf("recordatorios: marcar enviado el turno %s: %v", d.BookingID, err)
		}
		sent++
	}
	return sent, failed
}
