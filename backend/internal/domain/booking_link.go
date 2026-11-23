package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// F1: enlace firmado de un solo uso para cancelar o reprogramar un turno desde
// el email (contrato F1 de la fase 6).
//
// Reglas de seguridad que vive este archivo:
//   - el token son 32 bytes de crypto/rand en base64url SIN relleno (43
//     caracteres). Es el MISMO generador de las invitaciones de F2g1: un solo
//     lugar decide cómo se genera un token de capability;
//   - el token en claro NO se guarda ni se compara: se guarda su SHA-256 y el
//     canje hashea lo que llega. La comparación es de tiempo constante;
//   - el token se usa UNA vez (el `used_at` lo marca el UPDATE condicional del
//     reclamo) y no vence por fecha: la vigencia la decide el TURNO (D3). Si el
//     personal lo cancela, el enlace muere; si lo mueve, el enlace vale contra
//     el horario nuevo.
//   - el enlace viaja en el FRAGMENTO de la URL (`/turno#<token>`): lo que va
//     después del "#" no se manda al servidor, así no queda en ningún log.

// LinkMinNoticeHours es la anticipación mínima para cambiar o cancelar desde el
// enlace (D3): con menos horas la persona tiene que hablar con el salón. Es una
// constante de dominio y no una política por salón (eso quedó como seguimiento).
const LinkMinNoticeHours = 2

// LinkTokenLength es el largo del token en caracteres: 32 bytes en base64url sin
// relleno entran exactamente en 43.
const LinkTokenLength = 43

// Mensajes exactos del contrato F1 (D5/D6). La pantalla los muestra verbatim.
const (
	// MsgBookingLinkInvalid es la ÚNICA respuesta de un token que no sirve
	// (inexistente, usado, mal formado o de un turno que ya no está vigente):
	// distinguir el motivo sería un oráculo.
	MsgBookingLinkInvalid = "Enlace inválido o vencido"
	// MsgBookingLinkMinNotice es el 409 de un turno dentro de las 2 horas.
	MsgBookingLinkMinNotice = "Faltan menos de 2 horas para el turno: comunicate con el salón para cambiarlo"
	// MsgBookingLinkSlotTaken es el 409 de un horario nuevo que ya no entra.
	MsgBookingLinkSlotTaken = "Ese horario ya no está disponible: elegí otro"
	// MsgBookingLinkSameSlot es el 400 de reprogramar al mismo instante.
	MsgBookingLinkSameSlot = "Elegí un horario distinto al actual"
	// MsgBookingLinkDate es el 400 de una fecha mal formada en los horarios.
	MsgBookingLinkDate = "Fecha inválida: usá AAAA-MM-DD"
)

// BookingLink es un enlace emitido para un turno. No guarda el token: guarda su
// SHA-256. `UsedAt` NULL significa "todavía sirve"; el turno decide el resto.
type BookingLink struct {
	ID        string
	SalonID   string
	BookingID string
	TokenHash string
	CreatedAt time.Time
	UsedAt    *time.Time
}

// Used dice si el enlace ya se consumió (D2).
func (l BookingLink) Used() bool { return l.UsedAt != nil }

// NewBookingLinkToken genera el token del enlace: 32 bytes de crypto/rand en
// base64url sin relleno. Reusa el generador de F2g1 a propósito (un solo lugar
// decide cuánta entropía y qué alfabeto tiene una capability del sistema).
func NewBookingLinkToken() (string, error) {
	return NewInvitationToken()
}

// HashBookingLinkToken es la ÚNICA forma de guardar/buscar un token de enlace:
// SHA-256 en hex. Determinista a propósito (el índice único de `token_hash` es
// lo que hace inequívoco el reclamo).
func HashBookingLinkToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// BookingLinkTokenMatches dice si el token en claro corresponde al hash
// guardado. Compara en tiempo constante: el hash no se adivina char por char.
func BookingLinkTokenMatches(hash, token string) bool { return InvitationTokenMatches(hash, token) }

// ValidBookingLinkToken valida la FORMA del token antes de tocar la DB: 43
// caracteres del alfabeto base64url (A-Z a-z 0-9 - _). Un token que no puede
// existir no merece una consulta.
func ValidBookingLinkToken(token string) bool {
	if len(token) != LinkTokenLength {
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

// NewBookingLink valida el alta de un enlace. El salón, el turno y el hash son
// obligatorios: su ausencia es un bug del caso de uso, no un pedido inválido. El
// hash tiene que ser un SHA-256 en hex (64), igual que el CHECK de la migración
// 027.
func NewBookingLink(salonID, bookingID, tokenHash string, now time.Time) (BookingLink, error) {
	salonID = strings.TrimSpace(salonID)
	bookingID = strings.TrimSpace(bookingID)
	tokenHash = strings.TrimSpace(tokenHash)
	if salonID == "" || bookingID == "" || tokenHash == "" {
		return BookingLink{}, fmt.Errorf("%w: el enlace necesita salón, turno y token", ErrValidation)
	}
	if len(tokenHash) != 64 {
		return BookingLink{}, fmt.Errorf("%w: el token del enlace no está hasheado", ErrValidation)
	}
	return BookingLink{
		ID:        NewID(),
		SalonID:   salonID,
		BookingID: bookingID,
		TokenHash: tokenHash,
		CreatedAt: now.UTC(),
	}, nil
}

// LinkLive dice si el turno sigue vigente para su enlace (D3): `booked` y con
// el comienzo en el futuro. Un turno cancelado, hecho, no-show o pasado mata el
// enlace aunque el token no se haya usado.
func (b Booking) LinkLive(now time.Time) bool {
	return b.Status == BookingBooked && b.StartsAt.After(now.UTC())
}

// LinkCanChange dice si el enlace además alcanza para CAMBIAR algo (D3): faltan
// LinkMinNoticeHours o más. El turno se sigue mostrando (vigente) pero la
// pantalla oculta las acciones.
func (b Booking) LinkCanChange(now time.Time) bool {
	return b.LinkLive(now) && b.StartsAt.Sub(now.UTC()) >= LinkMinNoticeHours*time.Hour
}
