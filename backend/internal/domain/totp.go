// 2FA TOTP opcional por usuario (F10, parte B del contrato de la fase 6, D5 y
// D6): RFC 6238 con la stdlib (HMAC-SHA1, 6 dígitos, paso de 30 s, ventana ±1),
// códigos de recuperación y la caja que cifra el secreto en reposo
// (AES-256-GCM). El dominio no sabe de HTTP ni de SQL: solo reglas puras.
package domain

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 define HMAC-SHA1; no es un hash de contraseñas.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// TOTPDigits, TOTPPeriodSeconds y TOTPWindow son los parámetros de D5.
	TOTPDigits        = 6
	TOTPPeriodSeconds = 30
	// TOTPWindow: se aceptan el paso actual y ±1 (reloj desfasado).
	TOTPWindow = 1
	// TOTPSecretBytes: 20 bytes aleatorios (160 bits, el largo de la clave SHA1).
	TOTPSecretBytes = 20
	// RecoveryCodeCount: códigos de recuperación que se entregan al activar.
	RecoveryCodeCount = 8
	// TwoFactorMaxFailures: tras tantos códigos fallidos seguidos el usuario
	// queda bloqueado TwoFactorLockDuration (D9).
	TwoFactorMaxFailures  = 5
	TwoFactorLockDuration = 15 * time.Minute
	// TwoFactorChallengeTTL: vida del desafío del segundo paso del login (D8).
	TwoFactorChallengeTTL = 5 * time.Minute
	// TOTPIssuer es el emisor que muestra la app autenticadora.
	TOTPIssuer = "Salon"

	// totpKeyLabel es la etiqueta de la derivación de la clave de la caja (D6).
	totpKeyLabel = "salon-saas/totp/v1"
	// recoveryAlphabet es base32 (A-Z, 2-7): sin 0/1/8/9 que se confunden.
	recoveryAlphabet   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	recoveryCodeLength = 10
	gcmNonceSize       = 12
)

// Textos exactos del contrato F10 (D7, D8, D9), sin tildes tal cual están
// escritos. Los devuelve la API tal cual.
const (
	MsgTwoFactorLocked         = "Demasiados intentos. Proba de nuevo en unos minutos"
	MsgTwoFactorBadCode        = "Codigo incorrecto"
	MsgTwoFactorChallenge      = "Codigo incorrecto o desafio vencido"
	MsgTwoFactorBadCredentials = "Credenciales invalidas"
	MsgTwoFactorAlreadyEnabled = "La verificacion en dos pasos ya esta activada"
	MsgTwoFactorNoSetup        = "Primero genera el codigo con la configuracion"
	// No está literal en el contrato (disable de una cuenta sin 2FA): sigue el
	// estilo de los otros 409.
	MsgTwoFactorNotEnabled = "La verificacion en dos pasos no esta activada"
)

// ErrTooManyAttempts es el bloqueo por fuerza bruta (429). La capa HTTP lo
// distingue de los demás errores de dominio.
var ErrTooManyAttempts = errors.New("demasiados intentos")

// ErrTwoFactorCrypto cubre la caja de secretos: clave ausente, texto cifrado
// corto o manipulado. NO lleva detalle del contenido.
var ErrTwoFactorCrypto = errors.New("no se pudo abrir el secreto de la verificación en dos pasos")

// TwoFactorState es lo que el repositorio guarda de un usuario (sin contadores:
// esos los maneja el UPDATE atómico del adaptador).
type TwoFactorState struct {
	SecretEnc []byte
	Enabled   bool
	LastStep  int64
}

// TwoFactorAttempt es lo que devuelve reservar un intento: el secreto cifrado
// y el último paso usado, leídos en el MISMO UPDATE que cuenta el intento.
type TwoFactorAttempt struct {
	SecretEnc []byte
	LastStep  int64
}

// --- TOTP (RFC 6238 sobre HOTP, RFC 4226) ---

// NewTOTPSecret genera un secreto aleatorio de TOTPSecretBytes bytes.
func NewTOTPSecret() ([]byte, error) {
	b := make([]byte, TOTPSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// EncodeTOTPSecret lo pasa a base32 sin relleno (lo que pegan las apps).
func EncodeTOTPSecret(secret []byte) string { return totpBase32.EncodeToString(secret) }

// TOTPStepAt es el contador de 30 s de un instante (T0 = época Unix).
func TOTPStepAt(t time.Time) int64 { return t.Unix() / TOTPPeriodSeconds }

// TOTPCode es el código de `digits` dígitos del paso `step` (truncamiento
// dinámico de RFC 4226). `digits` 6 es el real; 8 existe para los vectores del
// RFC.
func TOTPCode(secret []byte, step int64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// NormalizeTOTPCode descarta todo espacio («123 456» pegado).
func NormalizeTOTPCode(s string) string { return strings.Join(strings.Fields(s), "") }

// IsTOTPCodeFormat dice si es exactamente de 6 dígitos ASCII.
func IsTOTPCodeFormat(s string) bool {
	if len(s) != TOTPDigits {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// MatchTOTP busca `code` en la ventana ±TOTPWindow de `now` y devuelve el paso
// que coincidió. Anti-replay (D5): solo cuenta un paso ESTRICTAMENTE mayor que
// lastStep. Las comparaciones son de tiempo constante y se recorren los tres
// pasos siempre (sin salir en el primer acierto).
func MatchTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(secret) == 0 || !IsTOTPCodeFormat(code) {
		return 0, false
	}
	base := TOTPStepAt(now)
	var matched int64
	found := false
	for step := base - TOTPWindow; step <= base+TOTPWindow; step++ {
		want := TOTPCode(secret, step, TOTPDigits)
		eq := subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1
		if eq && step > lastStep {
			matched, found = step, true
		}
	}
	return matched, found
}

// OTPAuthURL arma la URL que lee una app autenticadora. El email va escapado
// como segmento de ruta (el `@` queda tal cual; un espacio y un `+` van
// como %20 y %2B: algunas apps leen el `+` como espacio).
func OTPAuthURL(email, secretB32 string) string {
	label := strings.ReplaceAll(url.PathEscape(email), "+", "%2B")
	return "otpauth://totp/" + TOTPIssuer + ":" + label +
		"?secret=" + url.QueryEscape(secretB32) + "&issuer=" + url.QueryEscape(TOTPIssuer)
}

// --- Códigos de recuperación ---

// NewRecoveryCodes genera n códigos distintos «XXXXX-XXXXX» (10 caracteres
// base32 = 50 bits de entropía, aleatorios: alcanza un SHA-256 sin sal).
func NewRecoveryCodes(n int) ([]string, error) {
	out := make([]string, 0, n)
	seen := make(map[string]bool, n)
	for len(out) < n {
		var raw [recoveryCodeLength]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		var sb strings.Builder
		for i, b := range raw {
			if i == recoveryCodeLength/2 {
				sb.WriteByte('-')
			}
			// 32 símbolos y 256 valores: `& 31` es uniforme (sin sesgo).
			sb.WriteByte(recoveryAlphabet[b&31])
		}
		code := sb.String()
		if seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out, nil
}

// NormalizeRecoveryCode deja el código en 10 caracteres base32 en mayúsculas
// (sin espacios ni guion). false si no tiene esa forma.
func NormalizeRecoveryCode(s string) (string, bool) {
	var sb strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
		case r >= 'A' && r <= 'Z', r >= '2' && r <= '7':
			sb.WriteRune(r)
		default:
			return "", false
		}
	}
	if sb.Len() != recoveryCodeLength {
		return "", false
	}
	return sb.String(), true
}

// HashRecoveryCode es el SHA-256 en hex del código ya normalizado (lo único que
// se guarda).
func HashRecoveryCode(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// --- Caja del secreto (D6) ---

// TOTPSecretBox cifra el secreto TOTP con AES-256-GCM. La clave sale de
// JWT_SECRET con HMAC-SHA256 y la etiqueta `salon-saas/totp/v1`; rotar
// JWT_SECRET invalida los 2FA ya enrolados (limitación aceptada). El valor cero
// NO sirve: falla cerrado.
type TOTPSecretBox struct {
	aead cipher.AEAD
}

// NewTOTPSecretBox deriva la clave. Sin secreto es un error de configuración.
func NewTOTPSecretBox(jwtSecret string) (TOTPSecretBox, error) {
	if jwtSecret == "" {
		return TOTPSecretBox{}, errors.New("JWT_SECRET no configurado: no se puede derivar la clave del 2FA")
	}
	mac := hmac.New(sha256.New, []byte(jwtSecret))
	mac.Write([]byte(totpKeyLabel))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return TOTPSecretBox{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return TOTPSecretBox{}, err
	}
	return TOTPSecretBox{aead: aead}, nil
}

// Seal devuelve nonce(12 aleatorios) || texto cifrado || tag. El id del usuario
// va como dato autenticado: el texto cifrado de una fila no sirve en otra.
func (b TOTPSecretBox) Seal(plain []byte, userID string) ([]byte, error) {
	if b.aead == nil {
		return nil, ErrTwoFactorCrypto
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, []byte(userID)), nil
}

// Open descifra y autentica. Un texto corto, manipulado, de otra clave o de
// otro usuario devuelve ErrTwoFactorCrypto (nunca entra en pánico).
func (b TOTPSecretBox) Open(blob []byte, userID string) ([]byte, error) {
	if b.aead == nil || len(blob) < gcmNonceSize+b.aead.Overhead() {
		return nil, ErrTwoFactorCrypto
	}
	plain, err := b.aead.Open(nil, blob[:gcmNonceSize], blob[gcmNonceSize:], []byte(userID))
	if err != nil {
		return nil, ErrTwoFactorCrypto
	}
	return plain, nil
}
