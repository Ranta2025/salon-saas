package main

import (
	"log"

	"salon-saas/internal/adapters/mail"
	"salon-saas/internal/ports"
)

// wireMailer arma el adaptador SMTP del puerto MailSender y completa el sender
// de los casos de uso que lo reciben después de construirse (reseñas, lista de
// espera y cobertura de ausencias).
func wireMailer(w *wiring) {
	// Email a clientes (T12): adaptador SMTP del puerto MailSender. Sin
	// SMTP_HOST el sender queda deshabilitado y los casos de uso lo ven como
	// nil (no hay correo en este entorno).
	mailSender, err := mail.New(mail.Config{
		Host: w.cfg.SMTPHost, Port: w.cfg.SMTPPort, User: w.cfg.SMTPUser,
		Pass: w.cfg.SMTPPass, From: w.cfg.SMTPFrom, TLS: w.cfg.SMTPTLS,
	})
	if err != nil {
		log.Fatalf("email: %v", err)
	}
	w.mailSender = mailSender
	var mailer ports.MailSender
	if mailSender.Enabled() {
		mailer = mailSender
	}
	w.mailer = mailer
	// F13: el correo del pedido de reseña sale por el mismo MailSender. Sin email
	// configurado queda nil y el barrido no reclama nada (no se pide una reseña
	// que nunca va a llegar).
	w.reviewsSvc.Mailer = w.mailer
	// F7: el aviso de la lista sale por el mismo MailSender. Sin email
	// configurado queda nil y la tarea no reclama nada (D7).
	w.waitlistSvc.Mailer = w.mailer
	// F2d3 avisa a la clienta del cambio de turno por el mismo email.
	w.coverageSvc.Mailer = w.mailer
	w.coverageSvc.Emails = w.bookingEmails
}
