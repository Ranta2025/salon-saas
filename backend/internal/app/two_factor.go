// 2FA TOTP opcional por usuario (F10, parte B del contrato de la fase 6, D5..D9
// y D4 para la auditoría). Reglas que sostienen este archivo:
//
//   - El secreto solo existe en claro dentro de este servicio, un instante, y
//     en la respuesta del setup. Se guarda cifrado (domain.TOTPSecretBox) y NUNCA
//     se loguea ni vuelve a salir.
//   - Fuerza bruta (D9): verifyCode reserva el intento con
//     TwoFactorRepository.BeginAttempt (UN UPDATE atómico que cuenta el intento y
//     aplica el bloqueo) ANTES de comparar el código. Un acierto reinicia el
//     contador; un fallo no tiene que hacer nada más (ya quedó contado). Así 30
//     intentos paralelos nunca pasan de 5 verificaciones.
//   - Anti-replay (D5): un paso solo se acepta si el UPDATE `last_step < paso`
//     gana. Un código de recuperación se gasta con un UPDATE `used_at IS NULL`.
//   - El login con 2FA devuelve un error uniforme (401) para desafío inválido,
//     vencido o código malo; solo el bloqueo es distinto (429).
package app

import (
	"errors"
	"log"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Acciones de auditoría que escribe ESTE servicio (D4); las de D2 las escribe el
// middleware.
const (
	AuditAction2FAEnabled  = "auth.2fa_enabled"
	AuditAction2FADisabled = "auth.2fa_disabled"
)

// TwoFactorSetup es lo único que sale del secreto: la respuesta del setup.
type TwoFactorSetup struct {
	Secret     string
	OTPAuthURL string
}

type TwoFactorService struct {
	Repo ports.TwoFactorRepository
	// Users resuelve el email (otpauth) y la contraseña (disable).
	Users      ports.UserRepository
	Hasher     ports.PasswordHasher
	Tokens     ports.TokenIssuer
	Challenges ports.ChallengeIssuer
	Box        domain.TOTPSecretBox
	// Memberships da los salones ACTIVOS del usuario para la auditoría (D4).
	Memberships ports.MembershipLister
	// Audit: el servicio de auditoría existente (Events nil = apagada).
	Audit AuditService
	// Now es el reloj (nil = time.Now); solo para el paso TOTP: el bloqueo lo
	// calcula la base con now().
	Now func() time.Time
}

var (
	errTwoFactorBadCode   = domain.NewMessage(domain.ErrValidation, domain.MsgTwoFactorBadCode)
	errTwoFactorChallenge = domain.NewMessage(domain.ErrUnauthorized, domain.MsgTwoFactorChallenge)
	errTwoFactorLocked    = domain.NewMessage(domain.ErrTooManyAttempts, domain.MsgTwoFactorLocked)
	errTwoFactorCreds     = domain.NewMessage(domain.ErrUnauthorized, domain.MsgTwoFactorBadCredentials)
	errTwoFactorAlready   = domain.NewMessage(domain.ErrAlreadyExists, domain.MsgTwoFactorAlreadyEnabled)
	errTwoFactorNoSetup   = domain.NewMessage(domain.ErrAlreadyExists, domain.MsgTwoFactorNoSetup)
	errTwoFactorNotOn     = domain.NewMessage(domain.ErrAlreadyExists, domain.MsgTwoFactorNotEnabled)
	// errCodeMismatch es interno: el código no coincidió (el llamador lo traduce).
	errCodeMismatch = errors.New("código incorrecto")
)

func (s TwoFactorService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Status dice si el 2FA está ACTIVADO (un enrolamiento pendiente no cuenta).
func (s TwoFactorService) Status(userID string) (bool, error) {
	st, err := s.Repo.Find(userID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return st.Enabled, nil
}

// Setup genera un secreto nuevo y lo guarda PENDIENTE (reemplaza uno pendiente
// anterior). Ya activado = 409.
func (s TwoFactorService) Setup(userID string) (TwoFactorSetup, error) {
	user, err := s.Users.FindByID(userID)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	st, err := s.Repo.Find(userID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return TwoFactorSetup{}, err
	}
	if err == nil && st.Enabled {
		return TwoFactorSetup{}, errTwoFactorAlready
	}
	secret, err := domain.NewTOTPSecret()
	if err != nil {
		return TwoFactorSetup{}, err
	}
	enc, err := s.Box.Seal(secret, userID)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if err := s.Repo.SavePending(userID, enc); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) { // activado entre medio
			return TwoFactorSetup{}, errTwoFactorAlready
		}
		return TwoFactorSetup{}, err
	}
	b32 := domain.EncodeTOTPSecret(secret)
	return TwoFactorSetup{Secret: b32, OTPAuthURL: domain.OTPAuthURL(user.Email, b32)}, nil
}

// Enable confirma el enrolamiento con un código TOTP del secreto pendiente y
// devuelve los 8 códigos de recuperación (UNA vez: solo se guarda su hash).
func (s TwoFactorService) Enable(userID, code string) ([]string, error) {
	st, err := s.Repo.Find(userID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errTwoFactorNoSetup
	}
	if err != nil {
		return nil, err
	}
	if st.Enabled {
		return nil, errTwoFactorAlready
	}
	secret, err := s.Box.Open(st.SecretEnc, userID)
	if err != nil {
		return nil, err
	}
	step, ok := domain.MatchTOTP(secret, domain.NormalizeTOTPCode(code), s.now(), st.LastStep)
	if !ok {
		return nil, errTwoFactorBadCode
	}
	codes, err := domain.NewRecoveryCodes(domain.RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(codes))
	for _, c := range codes {
		n, _ := domain.NormalizeRecoveryCode(c)
		hashes = append(hashes, domain.HashRecoveryCode(n))
	}
	if err := s.Repo.Enable(userID, st.SecretEnc, step, hashes); err != nil {
		switch {
		case errors.Is(err, domain.ErrAlreadyExists):
			return nil, errTwoFactorAlready
		case errors.Is(err, domain.ErrNotFound): // el setup se reemplazó entre medio
			return nil, errTwoFactorNoSetup
		}
		return nil, err
	}
	s.audit(userID, AuditAction2FAEnabled)
	return codes, nil
}

// Disable desactiva el 2FA: primero la contraseña (401), después el código TOTP
// o de recuperación (400, con la misma contabilidad de fuerza bruta que el
// login). Borra el secreto y los códigos en UNA transacción.
func (s TwoFactorService) Disable(userID, password, code string) error {
	user, err := s.Users.FindByID(userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.Hasher.DummyCompare(password)
			return errTwoFactorCreds
		}
		return err
	}
	if !s.Hasher.Compare(user.PasswordHash, password) {
		return errTwoFactorCreds
	}
	if err := s.verifyCode(userID, code); err != nil {
		switch {
		case errors.Is(err, errCodeMismatch):
			return errTwoFactorBadCode
		case errors.Is(err, domain.ErrTooManyAttempts):
			return errTwoFactorLocked
		case errors.Is(err, domain.ErrNotFound):
			return errTwoFactorNotOn
		}
		return err
	}
	if err := s.Repo.Disable(userID); err != nil {
		if errors.Is(err, domain.ErrNotFound) { // otro disable ganó
			return errTwoFactorNotOn
		}
		return err
	}
	s.audit(userID, AuditAction2FADisabled)
	return nil
}

// ChallengeFor es el gancho del login (ports.TwoFactorLoginGate): sin fila o con
// enabled=false no hay desafío (login de siempre); con 2FA activo, el desafío.
func (s TwoFactorService) ChallengeFor(userID string) (string, bool, error) {
	st, err := s.Repo.Find(userID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !st.Enabled {
		return "", false, nil
	}
	ch, err := s.Challenges.IssueChallenge(userID)
	if err != nil {
		return "", false, err
	}
	return ch, true, nil
}

// VerifyLogin es el segundo paso del login: desafío + código (TOTP o de
// recuperación) -> token de sesión. Desafío inválido, vencido o código malo dan
// el MISMO 401; un usuario bloqueado, 429 aun con el código correcto.
func (s TwoFactorService) VerifyLogin(challenge, code string) (string, error) {
	userID, err := s.Challenges.ParseChallenge(challenge)
	if err != nil || userID == "" {
		return "", errTwoFactorChallenge
	}
	if err := s.verifyCode(userID, code); err != nil {
		switch {
		case errors.Is(err, errCodeMismatch), errors.Is(err, domain.ErrNotFound):
			return "", errTwoFactorChallenge
		case errors.Is(err, domain.ErrTooManyAttempts):
			return "", errTwoFactorLocked
		}
		return "", err
	}
	return s.Tokens.Issue(userID)
}

// verifyCode reserva el intento (bloqueo ANTES de comparar) y verifica el código.
// nil = acierto y contador reiniciado. errCodeMismatch = no coincide o ya estaba
// usado (replay). ErrTooManyAttempts = bloqueado. ErrNotFound = no hay 2FA
// activo. Cualquier otro error (base caída, secreto manipulado) falla cerrado.
func (s TwoFactorService) verifyCode(userID, code string) error {
	att, err := s.Repo.BeginAttempt(userID)
	if err != nil {
		return err
	}
	if tc := domain.NormalizeTOTPCode(code); domain.IsTOTPCodeFormat(tc) {
		secret, err := s.Box.Open(att.SecretEnc, userID)
		if err != nil {
			return err
		}
		step, ok := domain.MatchTOTP(secret, tc, s.now(), att.LastStep)
		if !ok {
			return errCodeMismatch
		}
		won, err := s.Repo.CompleteStep(userID, step)
		if err != nil {
			return err
		}
		if !won {
			return errCodeMismatch
		}
		return nil
	}
	if rc, ok := domain.NormalizeRecoveryCode(code); ok {
		won, err := s.Repo.UseRecoveryCode(userID, domain.HashRecoveryCode(rc))
		if err != nil {
			return err
		}
		if won {
			return nil
		}
	}
	return errCodeMismatch
}

// audit escribe `action` en cada salón de las membresías ACTIVAS del usuario
// (D4). Mejor esfuerzo: un fallo se loguea (sin datos del 2FA) y no rompe la
// petición. Sin salones no se escribe nada.
func (s TwoFactorService) audit(userID, action string) {
	if s.Memberships == nil || !s.Audit.Enabled() {
		return
	}
	mems, err := s.Memberships.ListByUser(userID)
	if err != nil {
		log.Printf("auditoria 2fa: no se pudieron listar los salones de %s: %v", userID, err)
		return
	}
	for _, m := range mems {
		if err := s.Audit.Record(m.SalonID, userID, action, ""); err != nil {
			log.Printf("auditoria 2fa: no se pudo registrar %s en %s: %v", action, m.SalonID, err)
		}
	}
}
