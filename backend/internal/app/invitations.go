// Invitaciones de equipo (F2g1), lado del salón: emitir, listar y revocar.
//
// Reglas que viven acá:
//   - solo owner/admin del salón (la misma autorización del alta directa);
//   - el rol sigue la MISMA regla AddableBy de `POST /members` (nadie invita
//     clientes ni un segundo dueño, y un admin no invita admins);
//   - máximo 50 pendientes por salón;
//   - reinvitar a un email revoca la invitación anterior y emite un token nuevo
//     (el viejo deja de servir: es de un solo uso por emisión);
//   - el enlace se devuelve UNA sola vez, en esta respuesta, y no se puede
//     recuperar después (no se guarda: en la DB queda su SHA-256).
package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// DefaultAppURL es la base del enlace cuando APP_URL no está configurada.
// cmd/api siempre la define (ver docs/ops/environment.md); esto es el respaldo
// para los tests y para un caso de uso usado desde otro binario.
const DefaultAppURL = "http://localhost:8081"

// invitationMailTimeout acota el envío del correo. El envío es SÍNCRONO porque
// la respuesta necesita saber si salió (email_sent); el tope evita que un SMTP
// mudo demore la respuesta más que eso.
const invitationMailTimeout = 10 * time.Second

// InvitationService agrupa el manejo de invitaciones del salón.
type InvitationService struct {
	Invitations ports.InvitationRepository
	// Tx es la transacción: emitir (contar, revocar y guardar), revocar y
	// canjear nunca quedan a medias.
	Tx ports.InvitationTxRunner
	// Memberships es el directorio del equipo: la lectura activa compartida y la
	// que también ve a quien fue dado de baja (FindMember). La invitación la
	// necesita para no confundir una baja con alguien nuevo (F2g2).
	Memberships ports.MemberDirectory
	Users       ports.UserRepository
	Salons      ports.SalonRepository
	// Settings da la zona del salón para escribir la fecha de vencimiento del
	// correo en la fecha local (no en UTC).
	Settings ports.SalonSettingsRepository
	// Mailer nil = email deshabilitado (SMTP_HOST vacío): la invitación existe
	// igual y email_sent=false.
	Mailer ports.MailSender
	// AppURL es la base del enlace (APP_URL, por ejemplo http://localhost:8081).
	AppURL string
	// GenToken genera el token del enlace; nil = domain.NewInvitationToken. Es un
	// campo para que el test pueda fijarlo (nunca se adivina un token).
	GenToken func() (string, error)
	// Now es el reloj del caso de uso; nil = time.Now.
	Now func() time.Time
}

// InvitationInput es el cuerpo de `POST /salons/:id/invitations`.
type InvitationInput struct {
	Email string
	Role  domain.Role
	// Attends nil = el default del rol (professional atiende; el resto no), la
	// misma regla del alta directa (S-F2a-4).
	Attends *bool
}

// CreatedInvitation es lo que devuelve el alta: la invitación, el enlace con el
// token (única vez que se muestra) y si el correo salió.
type CreatedInvitation struct {
	Invitation domain.Invitation
	Link       string
	EmailSent  bool
}

// Invite emite una invitación y manda el correo (best effort).
func (s InvitationService) Invite(actor domain.Membership, salonID string, in InvitationInput) (CreatedInvitation, error) {
	m, err := manageActor(s.Memberships, actor, salonID)
	if err != nil {
		return CreatedInvitation{}, err
	}
	// El rol tiene que existir y ser invitables, y el actor tiene que poder darlo:
	// el mensaje es el mismo en los dos casos (para quien lo lee es la misma
	// situación). Distinto de "sin permiso" (403), que es cuando el actor no es
	// owner/admin del salón.
	if !in.Role.InvitableBy(m.Role) {
		return CreatedInvitation{}, domain.NewMessage(domain.ErrValidation, "No podés invitar con ese rol")
	}
	email := domain.NormalizeEmail(in.Email)
	if err := domain.ValidateEmail(email); err != nil {
		return CreatedInvitation{}, domain.NewMessage(domain.ErrValidation, "Email inválido")
	}
	// Quien ya es parte del equipo no se invita: se le cambia el rol desde
	// Equipo. (El canje también lo rechaza, por si la membresía apareció después.)
	// Una persona DADA DE BAJA sí se puede invitar si el actor podría reactivarla:
	// el canje la reactiva con el rol de la invitación.
	if err := s.ensureNotMember(m, salonID, email); err != nil {
		return CreatedInvitation{}, err
	}
	attends := defaultAttends(in.Role)
	if in.Attends != nil {
		attends = *in.Attends
	}
	token, err := s.newToken()
	if err != nil {
		return CreatedInvitation{}, err
	}
	now := s.now()
	inv, err := domain.NewInvitation(salonID, email, in.Role, attends, domain.HashInvitationToken(token), m.UserID, now)
	if err != nil {
		return CreatedInvitation{}, err
	}
	// El nombre del salón se lee ANTES de escribir: si el salón no se puede leer
	// es un error de infraestructura y la invitación no debe quedar creada a
	// medias.
	salon, err := s.Salons.FindByID(salonID)
	if err != nil {
		return CreatedInvitation{}, err
	}
	if err := s.Tx.InInvitationTx(func(tx ports.InvitationTx) error {
		// Reinvidar la misma dirección revoca la anterior y libera el lugar del
		// tope (una revocada no cuenta).
		if err := tx.RevokePendingByEmail(salonID, email, now); err != nil {
			return err
		}
		n, err := tx.CountPending(salonID, now)
		if err != nil {
			return err
		}
		if n >= domain.MaxPendingInvitations {
			return domain.NewMessage(domain.ErrValidation, "Máximo 50 invitaciones pendientes")
		}
		return tx.Save(inv)
	}); err != nil {
		return CreatedInvitation{}, err
	}
	link := s.link(token)
	emailSent := s.sendInvitationEmail(salon, inv, link, s.expiryLabel(salonID, inv.ExpiresAt))
	return CreatedInvitation{Invitation: inv, Link: link, EmailSent: emailSent}, nil
}

// List devuelve las invitaciones PENDIENTES del salón. Nunca incluye el token
// (no existe en la DB) y el handler tampoco expone el hash.
func (s InvitationService) List(actor domain.Membership, salonID string) ([]domain.Invitation, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return nil, err
	}
	list, err := s.Invitations.ListPending(salonID, s.now())
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.Invitation{}
	}
	return list, nil
}

// Revoke da de baja una invitación pendiente (DELETE). El token revocado pasa a
// responder el 404 genérico.
func (s InvitationService) Revoke(actor domain.Membership, salonID, invitationID string) error {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return err
	}
	if strings.TrimSpace(invitationID) == "" {
		return domain.ErrNotFound
	}
	now := s.now()
	return s.Tx.InInvitationTx(func(tx ports.InvitationTx) error {
		return tx.Revoke(salonID, invitationID, now)
	})
}

// ensureNotMember rechaza invitar a quien ya está en el equipo.
//
// La lectura es FindMember, no Find: desde F2g2 `Memberships.Find` devuelve solo
// activas, así que una baja parecía alguien nuevo y el canje le cambiaba el rol
// al que eligiera el actor. Si la fila existe y está dada de baja, la
// autorización es la de la baja/reactivación sobre el ROL GUARDADO
// (Role.RemovableBy): la administración no emite una invitación que le cambiaría
// el rol a un admin o a un dueño (403); el dueño sí, y ahí manda el rol de la
// invitación.
func (s InvitationService) ensureNotMember(actor domain.Membership, salonID, email string) error {
	user, err := s.Users.FindByEmail(email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // no tiene cuenta: es exactamente a quien se invita
		}
		return err
	}
	existing, err := s.Memberships.FindMember(user.ID, salonID)
	switch {
	case err == nil && existing.Active():
		return domain.NewMessage(domain.ErrAlreadyExists, "Esa persona ya es parte del equipo")
	case err == nil:
		if !existing.Role.RemovableBy(actor.Role) {
			return domain.ErrForbidden
		}
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return nil
	default:
		return err
	}
}

// link arma el enlace con el token en el FRAGMENTO: lo que va después del "#" no
// se manda al servidor, así no queda en ningún log de la API ni de nginx.
func (s InvitationService) link(token string) string {
	base := strings.TrimSpace(s.AppURL)
	if base == "" {
		base = DefaultAppURL
	}
	return strings.TrimRight(base, "/") + "/invitacion#" + token
}

// sendInvitationEmail manda el correo (best effort) y dice si salió. Un SMTP
// caído NO falla el alta: la invitación ya está guardada y el enlace se puede
// compartir a mano.
//
// El log NUNCA lleva el token ni el enlace: solo el id de la invitación y el
// salón alcanzan para diagnosticar.
func (s InvitationService) sendInvitationEmail(salon domain.Salon, inv domain.Invitation, link, expires string) bool {
	if s.Mailer == nil {
		return false
	}
	msg := invitationMail(salon.Name, inv, link, expires)
	ctx, cancel := context.WithTimeout(context.Background(), invitationMailTimeout)
	defer cancel()
	if err := s.Mailer.Send(ctx, msg); err != nil {
		log.Printf("invitación %s del salón %s: no se pudo enviar el correo: %v", inv.ID, inv.SalonID, err)
		return false
	}
	return true
}

// newToken genera el token del enlace. El token en claro vive solo en esta
// llamada y en la respuesta del alta: la fila guardada tiene su SHA-256, así que
// el enlace no se puede reconstruir desde la DB.
func (s InvitationService) newToken() (string, error) {
	if s.GenToken != nil {
		return s.GenToken()
	}
	return domain.NewInvitationToken()
}

// expiryLabel es la fecha de vencimiento en el día local del salón. Si la
// configuración no se puede leer, el correo sale igual en UTC: un dato de
// formato no puede voltear el alta de la invitación.
func (s InvitationService) expiryLabel(salonID string, t time.Time) string {
	if s.Settings != nil {
		if settings, err := s.Settings.Settings(salonID); err == nil {
			return expiryLabel(settings, t)
		}
	}
	return expiryLabel(domain.SalonSettings{}, t)
}

func (s InvitationService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// defaultAttends es la marca con la que nace cada rol (S-F2a-4): un profesional
// atiende siempre, el resto no hasta que el alta lo pida.
func defaultAttends(role domain.Role) bool { return role == domain.RoleProfessional }
