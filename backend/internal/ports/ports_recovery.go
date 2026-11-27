package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// ===== F15: recuperar contraseña, revocar sesiones y verificar email =====

// PasswordResetRepository guarda los pedidos de restablecer contraseña
// (migración 036). El token en claro nunca entra: solo su SHA-256.
type PasswordResetRepository interface {
	// IssueReset emite el pedido en UNA transacción con el usuario bloqueado: si
	// domain.ResetThrottled lo frena (r.CreatedAt es el «ahora» del caso de uso)
	// devuelve issued=false y no escribe nada; si no, invalida los pedidos
	// anteriores sin usar del usuario y guarda este. Un solo vigente por usuario.
	IssueReset(r domain.PasswordReset) (issued bool, err error)
	// FindReset resuelve un pedido por el hash de su token (ErrNotFound si no
	// existe). Es solo lectura: no consume nada.
	FindReset(tokenHash string) (domain.PasswordReset, error)
	// ConsumeReset es el canje, TODO en UNA transacción: UPDATE condicional de
	// used_at (used_at IS NULL AND expires_at > now()), nueva pass_hash,
	// password_changed_at igual al used_at (UN solo valor del reloj de la base),
	// email_verified_at si era NULL, e invalidación de los demás pedidos del
	// usuario. No toca el 2FA ni emite sesión. ErrNotFound si el pedido no se
	// pudo canjear (desconocido, usado o vencido): de 30 canjes paralelos del
	// mismo token gana exactamente UNO.
	ConsumeReset(tokenHash, newPassHash string) error
}

// EmailVerificationRepository guarda los pedidos de verificar email (036).
type EmailVerificationRepository interface {
	// IssueVerification guarda el pedido bajo el lock del usuario. Con
	// minInterval > 0, si ya hay uno creado hace menos de minInterval devuelve
	// issued=false y no escribe nada (el reenvío: 429). minInterval = 0 no
	// limita (el primer correo del registro).
	IssueVerification(v domain.EmailVerification, minInterval time.Duration) (issued bool, err error)
	// ConsumeVerification marca el email como verificado con UN UPDATE
	// condicional (used_at IS NULL AND expires_at > now()) en la misma
	// transacción que users.email_verified_at. Idempotente: un token ya usado
	// cuyo usuario ya está verificado es nil. ErrNotFound en cualquier otro caso
	// (desconocido, vencido, usado sin verificar).
	ConsumeVerification(tokenHash string) error
}

// VerificationSender lo usa el registro para disparar el correo de verificación
// (asíncrono: nunca demora ni falla el registro).
type VerificationSender interface {
	SendVerification(u domain.User)
}

// SessionTokenParser es la interfaz OPCIONAL de un TokenIssuer que sabe leer el
// claim `ia` (D4). Es opcional a propósito: los fakes de TokenIssuer de las
// pruebas existentes no cambian de firma; sin ella un token se trata como «sin
// ia».
type SessionTokenParser interface {
	// ParseSession valida igual que TokenIssuer.Parse (método, firma,
	// vencimiento, tipo: un desafío de 2FA nunca es sesión) y devuelve además el
	// instante de emisión.
	ParseSession(token string) (domain.SessionClaims, error)
}

// SessionGate es la compuerta de revocación (D4): cuándo cambió por última vez
// la contraseña del usuario. nil = nunca. Un error es un fallo de la consulta:
// quien la usa FALLA CERRADO (401).
type SessionGate interface {
	PasswordChangedAt(userID string) (*time.Time, error)
}

// ===== F15 (fin) =====
