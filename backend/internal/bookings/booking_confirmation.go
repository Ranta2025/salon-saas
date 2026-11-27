// Confirmación de la reserva pública: el correo que sale apenas el turno queda
// agendado. Va en una goroutine con su propio timeout para que un SMTP lento o
// caído no demore ni tumbe la respuesta del navegador.
package bookings

import (
	"context"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
)

// confirmationTimeout es el techo de la confirmación. Si el SMTP no contesta
// en ese tiempo, la goroutine muere y el turno ya está agendado igual.
const confirmationTimeout = 15 * time.Second

// sendBookingConfirmation dispara la confirmación si hay con qué: sin mailer
// configurado (SMTP_HOST vacío) o sin email de la persona, no hace nada.
//
// `link` es el enlace de un solo uso de ese envío (F1/D1). Vacío = el correo sale
// con la línea de contacto en lugar del enlace (token no emitido o entorno sin
// enlaces).
//
// La reserva ya está guardada cuando esto se llama: ningún error de acá puede
// volver al navegador.
func (s PublicBookingService) sendBookingConfirmation(salonName string, b domain.Booking, email string, settings domain.SalonSettings, link string) {
	if s.Mailer == nil || strings.TrimSpace(email) == "" {
		return
	}
	msg := kernel.BookingMailWithLink(kernel.BookingMailConfirmation, salonName, b.ClientName, b.StartsAt, settings, link)
	msg.To = email
	go func() {
		// Un panic del adaptador no puede tumbar el proceso desde una goroutine
		// suelta.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("reserva pública: panic enviando la confirmación del turno %s: %v", b.ID, r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), confirmationTimeout)
		defer cancel()
		if err := s.Mailer.Send(ctx, msg); err != nil {
			// Sin la dirección en el log: el id del turno y el salón alcanzan.
			log.Printf("reserva pública: enviar la confirmación del turno %s del salón %s: %v", b.ID, b.SalonID, err)
		}
	}()
}
