package ports

import "salon-saas/internal/domain"

// ===== F10b: 2FA TOTP (inicio) =====

// TwoFactorRepository: el 2FA de cada usuario (migración 035, D6): la fila de
// user_totp y los códigos de recuperación. Toda operación va scopeada por
// user_id. El secreto viaja SIEMPRE cifrado: este puerto no ve texto claro.
type TwoFactorRepository interface {
	// Find lee el estado. ErrNotFound si el usuario nunca hizo setup.
	Find(userID string) (domain.TwoFactorState, error)
	// SavePending guarda (o REEMPLAZA) el enrolamiento pendiente con un UPSERT
	// que no pisa un 2FA ya activo: ErrAlreadyExists si ya está habilitado.
	SavePending(userID string, secretEnc []byte) error
	// Enable, en UNA transacción, marca enabled=true (solo si sigue pendiente y
	// con ESE secreto), deja last_step=step e inserta los hashes de los códigos
	// de recuperación. ErrNotFound si no hay enrolamiento con ese secreto;
	// ErrAlreadyExists si ya estaba habilitado.
	Enable(userID string, secretEnc []byte, step int64, codeHashes []string) error
	// BeginAttempt reserva un intento de verificar un código con UN solo
	// `UPDATE ... RETURNING` que, si el usuario no está bloqueado, cuenta el
	// intento como fallido (failed_count+1) y al llegar a
	// domain.TwoFactorMaxFailures fija locked_until = now() +
	// domain.TwoFactorLockDuration. Devuelve el secreto cifrado y last_step en
	// ese mismo paso. domain.ErrTooManyAttempts si está bloqueado (ANTES de
	// comparar nada); ErrNotFound si no hay 2FA habilitado.
	BeginAttempt(userID string) (domain.TwoFactorAttempt, error)
	// CompleteStep marca el intento como acierto con un código TOTP del paso
	// `step`: UN UPDATE condicionado a `last_step < step` que además reinicia el
	// contador y el bloqueo. false = el paso ya estaba usado (replay): cuenta
	// como fallo (ya quedó contado en BeginAttempt).
	CompleteStep(userID string, step int64) (bool, error)
	// UseRecoveryCode gasta UN código con un UPDATE condicionado a
	// `used_at IS NULL` y, en la misma sentencia, reinicia el contador. false =
	// no existe o ya se usó: de dos usos simultáneos gana UNO.
	UseRecoveryCode(userID, codeHash string) (bool, error)
	// Disable borra, en UNA transacción, el secreto y los códigos de
	// recuperación. ErrNotFound si no había nada.
	Disable(userID string) error
}

// ChallengeIssuer: el desafío del segundo paso del login (D8). Es un JWT con
// claim typ:"2fa" y 5 minutos de vida que NUNCA sirve de sesión: TokenIssuer.Parse
// lo rechaza, y ParseChallenge rechaza cualquier token sin ese claim.
type ChallengeIssuer interface {
	IssueChallenge(userID string) (string, error)
	// ParseChallenge devuelve el userID de un desafío vigente.
	ParseChallenge(token string) (string, error)
}

// ChallengeClaimsParser es la interfaz OPCIONAL de un ChallengeIssuer que además
// sabe CUÁNDO se emitió el desafío (claim `ia`, milisegundos Unix, el mismo de
// las sesiones). El login lo usa para rechazar un desafío emitido antes del
// último cambio de contraseña (F15, D4). Es opcional a propósito: los fakes de
// ChallengeIssuer de las pruebas existentes no cambian de firma y un emisor sin
// ella se trata como «desafío sin `ia`» (vale solo si la contraseña nunca
// cambió).
type ChallengeClaimsParser interface {
	// ParseChallengeClaims valida igual que ParseChallenge y devuelve además el
	// instante de emisión.
	ParseChallengeClaims(token string) (domain.SessionClaims, error)
}

// TwoFactorLoginGate es lo único que el login necesita saber del 2FA: si la
// cuenta lo exige y, en ese caso, el desafío. required=false => login de
// siempre (sin fila en user_totp o enabled=false).
type TwoFactorLoginGate interface {
	ChallengeFor(userID string) (challenge string, required bool, err error)
}

// ===== F10b: 2FA TOTP (fin) =====
