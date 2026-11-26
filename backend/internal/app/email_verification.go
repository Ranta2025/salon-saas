// Verificar el email (F15, parte B, D7). Verificar NO bloquea nada (D9): solo
// informa y se muestra en el
// panel. Mismas reglas de token que el restablecer: 32 bytes base64url, solo el
// SHA-256 guardado, enlace en el fragmento, un solo uso por UPDATE condicional, y
// el correo sale por el runner asíncrono sin loguear direcciones ni tokens.
package app

import (
	"context"
	"errors"
	"log"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// EmailVerificationService: mandar, reenviar y canjear la verificación.
type EmailVerificationService struct {
	Users  ports.UserRepository
	Verifs ports.EmailVerificationRepository
	// Mailer nil = SMTP deshabilitado: no se emite ni se envía nada.
	Mailer ports.MailSender
	AppURL string
	// Run es el runner asíncrono; nil = GoAsync.
	Run AsyncRunner
	Now func() time.Time
	// GenToken: nil = domain.NewRecoveryToken.
	GenToken func() (string, error)
}

func (s EmailVerificationService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s EmailVerificationService) token() (string, error) {
	if s.GenToken != nil {
		return s.GenToken()
	}
	return domain.NewRecoveryToken()
}

// SendVerification manda el correo de verificación de una cuenta recién creada.
// TODO el trabajo (emitir el token y enviar) corre en el runner asíncrono:
// nunca demora ni hace fallar el registro. Sin SMTP no hace nada.
func (s EmailVerificationService) SendVerification(u domain.User) {
	runAsync(s.Run, func(ctx context.Context) {
		if s.Mailer == nil {
			return
		}
		token, v, err := s.newVerification(u)
		if err != nil {
			log.Printf("verificación: armar el pedido de la cuenta %s falló (%T)", u.ID, err)
			return
		}
		issued, err := s.Verifs.IssueVerification(v, 0)
		if err != nil {
			log.Printf("verificación: guardar el pedido de la cuenta %s falló (%T)", u.ID, err)
			return
		}
		if issued {
			s.mail(ctx, u, token)
		}
	})
}

// newVerification genera el token y arma el pedido (con el hash, nunca el token).
func (s EmailVerificationService) newVerification(u domain.User) (string, domain.EmailVerification, error) {
	token, err := s.token()
	if err != nil {
		return "", domain.EmailVerification{}, err
	}
	v, err := domain.NewEmailVerification(u.ID, u.Email, domain.HashRecoveryToken(token), s.now())
	return token, v, err
}

func (s EmailVerificationService) mail(ctx context.Context, u domain.User, token string) {
	msg := verificationMail(u.Email, recoveryLink(s.AppURL, "/verificar-email", token))
	if err := s.Mailer.Send(ctx, msg); err != nil {
		log.Printf("verificación: enviar el correo de la cuenta %s falló (%T)", u.ID, err)
	}
}

// Resend es el reenvío autenticado. El estado se decide AL RESPONDER (409 si ya
// está verificado, 429 si pidió otro hace menos de un minuto, ambos con el texto
// del contrato); solo el envío del correo es asíncrono.
func (s EmailVerificationService) Resend(userID string) error {
	u, err := s.Users.FindByID(userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrUnauthorized // el token ya no representa a nadie
		}
		return err
	}
	if u.EmailVerifiedAt != nil {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgEmailAlreadyVerified)
	}
	if s.Mailer == nil {
		return nil // SMTP deshabilitado: no hay nada que enviar
	}
	token, v, err := s.newVerification(u)
	if err != nil {
		return err
	}
	issued, err := s.Verifs.IssueVerification(v, domain.VerificationResendInterval)
	if err != nil {
		return err
	}
	if !issued {
		return domain.NewMessage(domain.ErrTooManyAttempts, domain.MsgVerificationThrottled)
	}
	runAsync(s.Run, func(ctx context.Context) { s.mail(ctx, u, token) })
	return nil
}

// Verify canjea el token: marca el email como verificado y consume el pedido en
// UNA transacción del repositorio. Idempotente: un token ya usado de una cuenta ya
// verificada es nil. Cualquier otro caso es el texto uniforme del contrato.
func (s EmailVerificationService) Verify(token string) error {
	if !domain.ValidRecoveryTokenShape(token) {
		return errLinkInvalid()
	}
	if err := s.Verifs.ConsumeVerification(domain.HashRecoveryToken(token)); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return errLinkInvalid()
		}
		return err
	}
	return nil
}
