package domain

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// F15: recuperar contraseña, revocar sesiones y verificar email.
//
// Reglas de seguridad que vive este archivo (las mismas del token de invitación,
// domain/invitation.go):
//   - el token son 32 bytes de crypto/rand en base64url SIN relleno (43
//     caracteres): entra en un fragmento de URL ("/restablecer#<token>") y no
//     lleva nada que haya que escapar;
//   - el token en claro NO se guarda: se guarda su SHA-256 (hex, 64 caracteres)
//     y el canje hashea lo que llega. La comparación es de tiempo constante;
//   - la FORMA del token se chequea antes de tocar la base: un valor que no
//     puede ser un token nunca genera una consulta;
//   - un token se usa una sola vez (lo garantiza el UPDATE condicional del
//     adaptador, no este archivo).

const (
	// PasswordResetTTL es la vigencia del enlace de restablecer (D2).
	PasswordResetTTL = 60 * time.Minute
	// EmailVerificationTTL es la vigencia del enlace de verificar el email (D7).
	EmailVerificationTTL = 24 * time.Hour
	// ResetMinInterval y ResetMaxPerHour son el throttle POR USUARIO del pedido
	// de restablecer (D2): si el último se creó hace menos de 60 s, o ya hay 5 en
	// la última hora, no se emite ni se envía nada (silencioso).
	ResetMinInterval = 60 * time.Second
	ResetMaxPerHour  = 5
	// VerificationResendInterval es el mínimo entre dos pedidos de correo de
	// verificación del mismo usuario (D7: «más de 1 pedido por minuto» es 429).
	VerificationResendInterval = 60 * time.Second

	// recoveryTokenLen es el largo de 32 bytes en base64url sin relleno.
	recoveryTokenLen   = 43
	recoveryTokenBytes = 32
)

// Textos EXACTOS del contrato (D3/D7). Van sin tildes a propósito: son los
// textos que el contrato fija y que el frontend muestra tal cual.
const (
	MsgLinkInvalid           = "Enlace invalido o vencido"
	MsgEmailAlreadyVerified  = "El email ya esta verificado"
	MsgVerificationThrottled = "Espera un minuto antes de pedir otro correo"
)

// PasswordReset es un pedido de restablecer contraseña. No guarda el token:
// guarda su hash. UsedAt NULL = todavía no se usó (ni se invalidó por otro más
// nuevo).
type PasswordReset struct {
	ID        string
	UserID    string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// EmailVerification es un pedido de verificar el email de una cuenta. Guarda el
// email al que se mandó: solo ese email queda verificado.
type EmailVerification struct {
	ID        string
	UserID    string
	Email     string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// SessionClaims es lo que el emisor de sesiones sabe de un token ya validado
// (firma, método, vencimiento y tipo): el usuario y el instante de emisión.
// HasIA=false es un token anterior a F15, sin claim `ia`.
type SessionClaims struct {
	UserID string
	// IssuedAtMs es el claim `ia`: milisegundos Unix del instante de emisión.
	IssuedAtMs int64
	HasIA      bool
}

// NewRecoveryToken genera el token del enlace: 32 bytes de crypto/rand en
// base64url sin relleno.
func NewRecoveryToken() (string, error) {
	var b [recoveryTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("recuperación: generar el token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// HashRecoveryToken es la ÚNICA forma de guardar/buscar un token: SHA-256 en
// hex, el mismo del token de invitación (un solo hash de tokens en el sistema).
func HashRecoveryToken(token string) string { return HashInvitationToken(token) }

// RecoveryTokenMatches compara en tiempo constante el token en claro contra el
// hash guardado.
func RecoveryTokenMatches(hash, token string) bool { return InvitationTokenMatches(hash, token) }

// ValidRecoveryTokenShape dice si el valor PUEDE ser un token: exactamente 43
// caracteres del alfabeto base64url. Se chequea antes de consultar la base, así
// un valor basura (vacío, largo, con NUL, con relleno) nunca llega a una query.
func ValidRecoveryTokenShape(token string) bool {
	if len(token) != recoveryTokenLen {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// NewPasswordReset arma el pedido pendiente: vence a los 60 minutos.
func NewPasswordReset(userID, tokenHash string, now time.Time) (PasswordReset, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tokenHash) == "" {
		return PasswordReset{}, fmt.Errorf("%w: falta el usuario o el token del pedido", ErrValidation)
	}
	return PasswordReset{
		ID:        NewID(),
		UserID:    userID,
		TokenHash: tokenHash,
		CreatedAt: now.UTC(),
		ExpiresAt: now.UTC().Add(PasswordResetTTL),
	}, nil
}

// NewEmailVerification arma el pedido pendiente: vence a las 24 horas. El email
// se normaliza y se valida con la regla de las cuentas.
func NewEmailVerification(userID, email, tokenHash string, now time.Time) (EmailVerification, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tokenHash) == "" {
		return EmailVerification{}, fmt.Errorf("%w: falta el usuario o el token del pedido", ErrValidation)
	}
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return EmailVerification{}, err
	}
	return EmailVerification{
		ID:        NewID(),
		UserID:    userID,
		Email:     email,
		TokenHash: tokenHash,
		CreatedAt: now.UTC(),
		ExpiresAt: now.UTC().Add(EmailVerificationTTL),
	}, nil
}

// Usable dice si el pedido se puede canjear AHORA: sin usar y vigente. El borde
// es el del UPDATE del canje (expires_at > now()): en el instante exacto del
// vencimiento ya no sirve.
func (r PasswordReset) Usable(now time.Time) bool {
	return r.UsedAt == nil && now.UTC().Before(r.ExpiresAt)
}

// Usable: ver PasswordReset.Usable.
func (v EmailVerification) Usable(now time.Time) bool {
	return v.UsedAt == nil && now.UTC().Before(v.ExpiresAt)
}

// ResetThrottled aplica el throttle de D2: `last` es la creación del último
// pedido del usuario (nil si no hay) e `inLastHour` cuántos se crearon en la
// última hora. Hace falta ESPERAR 60 s completos (a los 60 s exactos ya se
// puede) y el tope de la hora es 5 (el quinto ya cuenta: no hay sexto).
func ResetThrottled(last *time.Time, inLastHour int, now time.Time) bool {
	if inLastHour >= ResetMaxPerHour {
		return true
	}
	return last != nil && now.Sub(*last) < ResetMinInterval
}

// VerificationThrottled es el throttle del reenvío del correo de verificación:
// menos de 60 s desde el último pedido del usuario.
func VerificationThrottled(last *time.Time, now time.Time) bool {
	return last != nil && now.Sub(*last) < VerificationResendInterval
}

// SessionValid es la regla de revocación de sesiones (D4).
//
// Un token vale solo si la contraseña nunca cambió (passwordChangedAt == nil) O
// el token trae `ia` y `ia` es ESTRICTAMENTE posterior al cambio, comparados en
// MILISEGUNDOS (la base guarda microsegundos: se trunca al milisegundo hacia
// abajo, igual que el `ia` del token).
//
// Borde (conservador a propósito): un token emitido en el MISMO milisegundo que
// el cambio se rechaza. Un token anterior al cambio nunca sobrevive, ni siquiera
// por un milisegundo de empate; el costo es que quien inicie sesión en el mismo
// milisegundo exacto del cambio tiene que volver a iniciar (el reinicio de
// contraseña no emite sesión, así que en la práctica no ocurre). Los tokens sin
// `ia` (anteriores a F15) valen solo mientras la contraseña nunca cambió.
func SessionValid(passwordChangedAt *time.Time, issuedAtMs int64, hasIA bool) bool {
	if passwordChangedAt == nil {
		return true
	}
	return hasIA && issuedAtMs > passwordChangedAt.UnixMilli()
}
