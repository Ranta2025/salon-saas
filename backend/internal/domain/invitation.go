package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// S-F2g1-2: invitación de equipo (contratar por email).
//
// Reglas de seguridad que vive este archivo:
//   - el token son 32 bytes de crypto/rand en base64url SIN relleno: entra en un
//     fragmento de URL ("/invitacion#<token>") y no lleva caracteres que haya
//     que escapar;
//   - el token en claro NO se guarda ni se compara: se guarda su SHA-256 y el
//     canje hashea lo que llega. La comparación es de tiempo constante;
//   - una invitación dura 7 días, se usa una sola vez y tiene cuatro estados
//     excluyentes (pendiente, aceptada, revocada, vencida).
//
// La generación es una función del paquete (no un método del caso de uso) para
// que el test pueda inyectar una semilla; el caso de uso la recibe como campo
// (InvitationService.GenToken) y nunca importa crypto.

// InvitationState son los cuatro estados de una invitación. Los tres últimos
// son la MISMA respuesta pública ("Invitación inválida o vencida"): el contrato
// prohíbe distinguirlos, porque distinguirlos es un oráculo.
type InvitationState string

const (
	InvitationPending  InvitationState = "pending"
	InvitationAccepted InvitationState = "accepted"
	InvitationRevoked  InvitationState = "revoked"
	InvitationExpired  InvitationState = "expired"
)

const (
	// InvitationTTL es la vigencia del enlace (S-F2g1-1/2).
	InvitationTTL = 7 * 24 * time.Hour
	// MaxPendingInvitations es el tope de pendientes por salón (S-F2g1-3).
	MaxPendingInvitations = 50
	// invitationTokenBytes: 32 bytes de entropía. En base64url sin relleno son
	// 43 caracteres (256 bits no entran en menos).
	invitationTokenBytes = 32
)

// Invitation es una invitación a sumarse al equipo de un salón con un rol.
//
// No guarda el token: guarda su hash. AcceptedAt/RevokedAt son punteros porque
// NULL en la DB significa "todavía no" y eso distingue pendiente de aceptada.
type Invitation struct {
	ID         string
	SalonID    string
	Email      string
	Role       Role
	Attends    bool
	TokenHash  string
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	RevokedAt  *time.Time
	AcceptedBy string
	CreatedBy  string
	CreatedAt  time.Time
}

// NewInvitationToken genera el token del enlace: 32 bytes de crypto/rand en
// base64url sin relleno.
func NewInvitationToken() (string, error) {
	var b [invitationTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("invitación: generar el token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// HashInvitationToken es la ÚNICA forma de comparar/buscar un token: se guarda
// y se busca por SHA-256, nunca por el valor en claro. Determinista a propósito
// (el índice único de token_hash es lo que hace atómico el canje).
func HashInvitationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// InvitationTokenMatches dice si el token en claro corresponde al hash
// guardado. Compara en tiempo constante: el hash no se adivina char por char.
func InvitationTokenMatches(hash, token string) bool {
	if hash == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashInvitationToken(token)), []byte(hash)) == 1
}

// NewInvitation valida el alta de una invitación pendiente.
//
// El email se normaliza y se valida con la MISMA regla que una cuenta (no hay
// dos políticas de email en el sistema). El rol tiene que existir y ser
// invitables: `client` no se invita nunca y `owner` tampoco (el dueño nace con
// el salón; ver Role.AddableBy). El hash del token y el salón son obligatorios:
// su ausencia es un bug del caso de uso y no un pedido inválido.
func NewInvitation(salonID, email string, role Role, attends bool, tokenHash, createdBy string, now time.Time) (Invitation, error) {
	if strings.TrimSpace(salonID) == "" || strings.TrimSpace(tokenHash) == "" {
		return Invitation{}, fmt.Errorf("%w: falta el salón o el token de la invitación", ErrValidation)
	}
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return Invitation{}, err
	}
	if err := validateInvitablesRole(role); err != nil {
		return Invitation{}, err
	}
	// La marca de "atiende clientes" se valida con la MISMA regla del PATCH de
	// equipo: recepción no se enciende y un profesional no se apaga. Acá se
	// valida antes de escribir, así una fila incoherente no llega a la DB.
	probe := Invitation{SalonID: salonID, Role: role, Attends: attends}
	if _, err := probe.Membership("validación"); err != nil {
		return Invitation{}, err
	}
	return Invitation{
		ID:        NewID(),
		SalonID:   salonID,
		Email:     email,
		Role:      role,
		Attends:   attends,
		TokenHash: tokenHash,
		ExpiresAt: now.UTC().Add(InvitationTTL),
		CreatedBy: createdBy,
		CreatedAt: now.UTC(),
	}, nil
}

// validateInvitablesRole es la parte de la regla que no depende de quién
// invita: el rol tiene que existir y ser uno que se reparte por invitación.
// El mensaje es el del contrato para los tres casos (S-F2g1-3).
func validateInvitablesRole(role Role) error {
	switch role {
	case RoleAdmin, RoleProfessional, RoleReception:
		return nil
	default:
		// client, owner, "" y cualquier texto: el mismo texto, porque para quien
		// lo lee es la misma situación ("ese rol no se invita").
		return NewMessage(ErrValidation, "No podés invitar con ese rol")
	}
}

// State dice en cuál de los cuatro estados está la invitación. El orden importa:
// aceptada manda (el canje ya pasó), después revocada (alguien la dio de baja) y
// recién al final la vigencia.
func (i Invitation) State(now time.Time) InvitationState {
	if i.AcceptedAt != nil {
		return InvitationAccepted
	}
	if i.RevokedAt != nil {
		return InvitationRevoked
	}
	if i.Expired(now) {
		return InvitationExpired
	}
	return InvitationPending
}

// Pending dice si la invitación se puede usar AHORA: ni aceptada, ni revocada,
// ni vencida. Es la condición del canje y del listado.
func (i Invitation) Pending(now time.Time) bool { return i.State(now) == InvitationPending }

// Expired dice si la vigencia ya pasó. El borde es el mismo que usa el UPDATE
// del canje (expires_at > now()): en el instante exacto del vencimiento ya no
// sirve.
func (i Invitation) Expired(now time.Time) bool { return !now.UTC().Before(i.ExpiresAt) }

// EmailHint es la dirección enmascarada del lookup público: la primera letra de
// la parte local, tres asteriscos y el dominio ("l***@mail.com"). Quien tiene el
// token ya sabe la dirección; quien no, no la aprende de acá.
func (i Invitation) EmailHint() string {
	at := strings.LastIndex(i.Email, "@")
	if at <= 0 {
		// Email sin forma de dirección (no debería existir): se devuelve un
		// texto fijo en vez de filtrar el valor entero.
		return "***"
	}
	local, domain := i.Email[:at], i.Email[at:]
	first, _ := utf8.DecodeRuneInString(local)
	if first == utf8.RuneError {
		return "***" + domain
	}
	return string(first) + "***" + domain
}

// Membership arma la membresía que nace de la invitación: el rol y la marca de
// quién atiende que se guardaron al invitar. Las dos combinaciones imposibles
// (un profesional que no atiende, una recepción que atiende) fallan acá en vez
// de entrar a la DB: la regla es la de Membership.SetAttends.
func (i Invitation) Membership(userID string) (Membership, error) {
	m, err := NewMembership(userID, i.SalonID, i.Role)
	if err != nil {
		return Membership{}, err
	}
	switch {
	case i.Role == RoleProfessional && !i.Attends:
		return Membership{}, NewMessage(ErrValidation, "Las profesionales siempre atienden")
	case i.Role == RoleReception && i.Attends:
		return Membership{}, NewMessage(ErrValidation, "Ese rol no atiende clientes")
	}
	m.Attends = i.Attends
	return m, nil
}

// InvitableBy dice si `actor` puede invitar con el rol del receptor. Es LA
// misma regla de `POST /members` (Role.AddableBy) mirada por el otro lado: un
// solo lugar decide qué combinación actor->rol existe, para que el alta directa
// y la invitación no puedan divergir.
func (r Role) InvitableBy(actor Role) bool { return r.AddableBy(actor) }

// ValidateEmail es la validación de email de las cuentas: la usan el registro y
// la invitación (S-F2g1-2). Se espera el email YA normalizado (NormalizeEmail).
func ValidateEmail(email string) error {
	if !strings.Contains(email, "@") || len(email) < 5 {
		return fmt.Errorf("%w: email inválido", ErrValidation)
	}
	if len(email) > MaxTextEmail {
		return fmt.Errorf("%w: el email no puede superar los %d caracteres", ErrValidation, MaxTextEmail)
	}
	// Forma mínima de una dirección: UN solo "@", algo a cada lado y nada que
	// permita colar una segunda dirección o un encabezado (espacios, saltos de
	// línea, controles, comas, punto y coma, comillas, paréntesis, corchetes).
	// Antes solo se pedía un "@": se guardaban "a b@x.com" o "x@y.com,evil@z" y
	// lo único que las frenaba era el mailer (hallazgo de F2g1).
	if strings.Count(email, "@") != 1 || !utf8.ValidString(email) {
		return fmt.Errorf("%w: email inválido", ErrValidation)
	}
	for _, r := range email {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`,;<>()[]"\`, r) {
			return fmt.Errorf("%w: email inválido", ErrValidation)
		}
	}
	at := strings.IndexByte(email, '@')
	local, host := email[:at], email[at+1:]
	if local == "" || host == "" ||
		strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") ||
		strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return fmt.Errorf("%w: email inválido", ErrValidation)
	}
	return nil
}
