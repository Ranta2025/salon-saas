// Canje público de una invitación (F2g1): lookup y aceptación.
//
// Dos caminos:
//   - sin sesión (`AcceptNew`): la persona crea su cuenta con la política de
//     contraseña vigente, la membresía con el rol de la invitación y la
//     invitación queda aceptada en la MISMA transacción. Devuelve la sesión,
//     igual que el login.
//   - con sesión (`AcceptExisting`): una cuenta que ya existe se une al salón
//     solo si su email es el de la invitación (si no, 403).
//
// Sin oráculo: token desconocido, vencido, revocado y ya usado devuelven el
// MISMO error ("Invitación inválida o vencida"), y el lookup solo revela
// `has_account` para un token válido.
package team

import (
	"errors"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// InvitationInvalidText es la ÚNICA respuesta de un token que no sirve. El
// mensaje es genérico a propósito: distinguir "no existe" de "venció" o "ya se
// usó" le diría a un atacante qué tokens existieron.
const InvitationInvalidText = "Invitación inválida o vencida"

// invitationInvalid es el error de dominio del token que no sirve: 404 con el
// texto genérico (ErrNotFound mantiene el mapeo HTTP).
func invitationInvalid() error { return domain.NewMessage(domain.ErrNotFound, InvitationInvalidText) }

// InvitationPublicService: las dos rutas públicas y el canje autenticado.
type InvitationPublicService struct {
	Invitations ports.InvitationRepository
	Tx          ports.InvitationTxRunner
	Users       ports.UserRepository
	Salons      ports.SalonRepository
	Hasher      ports.PasswordHasher
	Tokens      ports.TokenIssuer
	// Now es el reloj del caso de uso; nil = time.Now.
	Now func() time.Time
}

// InvitationLookup es lo que ve quien abre el enlace, sin sesión.
type InvitationLookup struct {
	SalonName  string
	Role       domain.Role
	RoleLabel  string
	EmailHint  string
	ExpiresAt  time.Time
	HasAccount bool
}

// Lookup describe la invitación de un token válido. La invitación vive en un
// salón real: si el salón no se puede leer es un error de infraestructura.
func (s InvitationPublicService) Lookup(token string) (InvitationLookup, error) {
	inv, err := s.pendingByToken(token)
	if err != nil {
		return InvitationLookup{}, err
	}
	salon, err := s.Salons.FindByID(inv.SalonID)
	if err != nil {
		return InvitationLookup{}, err
	}
	hasAccount, err := s.hasAccount(inv.Email)
	if err != nil {
		return InvitationLookup{}, err
	}
	return InvitationLookup{
		SalonName:  salon.Name,
		Role:       inv.Role,
		RoleLabel:  inv.Role.Label(),
		EmailHint:  inv.EmailHint(),
		ExpiresAt:  inv.ExpiresAt,
		HasAccount: hasAccount,
	}, nil
}

// AcceptNew crea la cuenta de quien acepta y la une al salón. Devuelve el JWT de
// la sesión nueva (el mismo emisor y la misma forma que el login).
//
// El orden importa: la política de contraseña corre ANTES del hash (B-06) y el
// hash antes de abrir la transacción (bcrypt es caro a propósito: no se sostiene
// una transacción durante ~100 ms de CPU).
func (s InvitationPublicService) AcceptNew(token, name, password string) (string, error) {
	inv, err := s.pendingByToken(token)
	if err != nil {
		return "", err
	}
	if has, err := s.hasAccount(inv.Email); err != nil {
		return "", err
	} else if has {
		return "", domain.NewMessage(domain.ErrAlreadyExists,
			"Ya tenés cuenta: iniciá sesión para aceptar la invitación")
	}
	if err := domain.ValidatePassword(password); err != nil {
		return "", err
	}
	// El nombre también se valida antes de gastar bcrypt: NewUser lo rechaza, así
	// que se arma el usuario DESPUÉS de hashear y un nombre inválido no tiene que
	// pagar el hash. Para eso se valida primero con un usuario descartable.
	if err := validateNewUserName(name); err != nil {
		return "", err
	}
	hash, err := s.Hasher.Hash(password)
	if err != nil {
		return "", err
	}
	user, err := domain.NewUser(name, inv.Email, hash)
	if err != nil {
		return "", err
	}
	// El canje: reclamar la invitación + crear la cuenta + crear la membresía, en
	// una sola transacción. Si la membresía falla, la cuenta no queda.
	if _, err := s.claimByID(inv.ID, user.ID, func(tx ports.InvitationTx) error {
		if err := tx.SaveUser(user); err != nil {
			// El UNIQUE de users decide la carrera: otra persona (u otro canje de
			// otro salón) creó la cuenta con este email entre el chequeo de arriba
			// y este INSERT. Mismo mensaje que cuando la cuenta ya existía de antes.
			if errors.Is(err, domain.ErrAlreadyExists) {
				return domain.NewMessage(domain.ErrAlreadyExists,
					"Ya tenés cuenta: iniciá sesión para aceptar la invitación")
			}
			return err
		}
		return nil
	}); err != nil {
		return "", err
	}
	// La sesión se emite después del commit: nunca hay un JWT de una cuenta que
	// no quedó guardada.
	return s.Tokens.Issue(user.ID)
}

// AcceptExisting une a una cuenta que YA existe al salón de la invitación. Solo
// si el email coincide: una invitación es de una dirección, no de un usuario.
func (s InvitationPublicService) AcceptExisting(userID, token string) (domain.Salon, domain.Invitation, error) {
	empty := domain.Salon{}
	user, err := s.Users.FindByID(userID)
	if err != nil {
		// Token válido o no, sin usuario no hay sesión: 401.
		return empty, domain.Invitation{}, domain.ErrUnauthorized
	}
	inv, err := s.pendingByToken(token)
	if err != nil {
		return empty, domain.Invitation{}, err
	}
	if domain.NormalizeEmail(user.Email) != inv.Email {
		return empty, domain.Invitation{}, domain.NewMessage(domain.ErrForbidden,
			"Esta invitación es para otra cuenta")
	}
	salon, err := s.Salons.FindByID(inv.SalonID)
	if err != nil {
		return empty, domain.Invitation{}, err
	}
	claimed, err := s.claimByID(inv.ID, user.ID, nil)
	if err != nil {
		return empty, domain.Invitation{}, err
	}
	return salon, claimed, nil
}

// claimByID ejecuta el canje completo: Claim (UPDATE condicional) + cuenta
// opcional + membresía. Si algo falla, el runner revierte todo.
func (s InvitationPublicService) claimByID(invitationID, userID string, saveUser func(tx ports.InvitationTx) error) (domain.Invitation, error) {
	var claimed domain.Invitation
	err := s.Tx.InInvitationTx(func(tx ports.InvitationTx) error {
		// El reclamo es lo PRIMERO: dos canjes simultáneos del mismo token chocan
		// acá (row lock del UPDATE) y el que pierde recibe ErrNotFound.
		got, err := tx.Claim(invitationID, userID)
		if err != nil {
			return err
		}
		claimed = got
		exists, err := tx.MembershipExists(got.SalonID, userID)
		if err != nil {
			return err
		}
		if exists {
			return domain.NewMessage(domain.ErrAlreadyExists, "Esa persona ya es parte del equipo")
		}
		if saveUser != nil {
			if err := saveUser(tx); err != nil {
				return err
			}
		}
		membership, err := got.Membership(userID)
		if err != nil {
			return err
		}
		return tx.SaveMembership(membership)
	})
	if err != nil {
		// Un token que dejó de servir en la carrera responde el 404 genérico, no
		// el error crudo del repositorio.
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Invitation{}, invitationInvalid()
		}
		return domain.Invitation{}, err
	}
	return claimed, nil
}

// pendingByToken resuelve el token hasheado y exige que esté pendiente AHORA.
// Cualquier otra cosa es el 404 genérico.
func (s InvitationPublicService) pendingByToken(token string) (domain.Invitation, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return domain.Invitation{}, invitationInvalid()
	}
	inv, err := s.Invitations.FindByTokenHash(domain.HashInvitationToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Invitation{}, invitationInvalid()
		}
		return domain.Invitation{}, err
	}
	// Defensa en profundidad: el hash del token tiene que corresponder al que se
	// buscó (comparación de tiempo constante) y la invitación tiene que seguir
	// pendiente (ni vencida, ni revocada, ni usada).
	if !domain.InvitationTokenMatches(inv.TokenHash, token) {
		return domain.Invitation{}, invitationInvalid()
	}
	if !inv.Pending(s.now()) {
		return domain.Invitation{}, invitationInvalid()
	}
	return inv, nil
}

// hasAccount dice si la dirección de la invitación ya tiene cuenta. Es lo ÚNICO
// que el lookup revela de las cuentas, y solo con un token válido.
func (s InvitationPublicService) hasAccount(email string) (bool, error) {
	_, err := s.Users.FindByEmail(email)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, domain.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

func (s InvitationPublicService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// validateNewUserName aplica la validación de nombre del alta sin hashear nada.
func validateNewUserName(name string) error {
	_, err := domain.NewUser(name, "validacion@salon.local", "validación")
	return err
}
