// Lista de espera del salón (F7 del contrato de la fase 6): una persona deja su
// contacto para UN servicio, UN día local del salón y, opcionalmente, UNA
// profesional; cuando la tarea periódica ve un hueco libre le manda un email.
//
// Reglas que viven en este archivo:
//   - una entrada = servicio + día LOCAL del salón + (profesional opcional) +
//     contacto. `professional_id` ausente significa «cualquiera» (D2);
//   - el día tiene que estar entre HOY y HOY+30 en la zona del salón (D4): el
//     borde es local, no UTC;
//   - nombre, email y teléfono se validan acá y un NUL es 400, nunca un 500 de
//     Postgres (lección de H20/H21);
//   - el token de la baja son 32 bytes de crypto/rand en base64url y se guarda
//     SOLO su SHA-256 (D5), igual que el enlace de un turno (F1) y las
//     invitaciones (F2g1);
//   - los estados son `waiting`, `notified` y `left` (D3), y el tope de avisos
//     simultáneos por grupo es de 5 huecos (D6).
package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxWaitlistNameRunes es el tope del nombre de quien se anota (120).
	MaxWaitlistNameRunes = MaxTextName
	// MaxWaitlistPhoneRunes es el tope del teléfono (opcional): 40.
	MaxWaitlistPhoneRunes = MaxTextPhone
	// MaxWaitlistEmailLen es el tope de la dirección de contacto (RFC 5321).
	MaxWaitlistEmailLen = MaxTextEmail
	// MaxWaitlistAttempts es el tope de envíos de un aviso (D6): tras 3 fallos
	// la entrada no se reintenta más (la persona se puede volver a anotar).
	MaxWaitlistAttempts = 3
	// MaxWaitlistActivePerEmail es el tope de avisos ACTIVOS por email y salón
	// (D4): 3.
	MaxWaitlistActivePerEmail = 3
	// MaxWaitlistActivePerSalon es el tope de avisos ACTIVOS de un salón (D4):
	// 500. Es la protección contra la saturación de la lista.
	MaxWaitlistActivePerSalon = 500
	// WaitlistDaysAhead es la ventana de días que se puede pedir (D4): hoy y los
	// próximos 30.
	WaitlistDaysAhead = 30
	// WaitlistNoticeSlotsCap es el tope de avisos que se reclaman de un grupo en
	// UNA pasada (D6): 5, aunque el día tenga más huecos.
	WaitlistNoticeSlotsCap = 5
	// WaitlistTokenLength es el largo del token de la baja: 32 bytes en
	// base64url sin relleno entran exactamente en 43 caracteres.
	WaitlistTokenLength = LinkTokenLength
	// WaitlistRetention es la RETENCIÓN BLANDA de un aviso ya enviado (D6): un
	// aviso enviado ocupa un hueco durante 2 horas, para que quien lo recibió
	// tenga tiempo de reservar. No es una reserva: el hueco sigue en el EXCLUDE
	// de turnos y quien reserva primero se lo lleva.
	WaitlistRetention = 2 * time.Hour
	// WaitlistRetryGrace es la gracia de un reclamo sin envío (D6): si el envío
	// falla, la entrada vuelve a estar disponible pasados 5 minutos, con
	// notify_attempts < 3 (igual que los recordatorios).
	WaitlistRetryGrace = 5 * time.Minute
)

// Estados de una entrada (D3). El estado se DERIVA de las fechas: no hay una
// columna `status` que pueda contradecirlas.
const (
	WaitlistStatusWaiting  = "waiting"
	WaitlistStatusNotified = "notified"
	WaitlistStatusLeft     = "left"
)

// Textos exactos del contrato F7 (los que ve la persona).
const (
	// MsgWaitlistSlotsAvailable es el 409 de un día con horarios libres: la
	// lista no sirve para reservar ni para sondear.
	MsgWaitlistSlotsAvailable = "Todavía hay horarios libres ese día"
	// MsgWaitlistEmailLimit es el 409 del tope de 3 avisos activos por email.
	MsgWaitlistEmailLimit = "Ya tenés el máximo de avisos activos"
	// MsgWaitlistSalonFull es el 409 de los 500 avisos activos del salón.
	MsgWaitlistSalonFull = "La lista de espera de este salón está completa"
	// MsgWaitlistNotFound es el ÚNICO 404 de la lista: de un token que no
	// sirve, de una entrada de otro salón o de una que ya se dio de baja.
	MsgWaitlistNotFound = "Aviso no encontrado"
	// MsgWaitlistJoinMissing es el 400 de un alta sin servicio, fecha, nombre o
	// email: nunca se rellena un campo obligatorio por defecto.
	MsgWaitlistJoinMissing = "Faltan datos del aviso: servicio, fecha, nombre y email"
	// MsgWaitlistServiceInvalid es el 400 de un servicio inexistente o inactivo
	// del salón.
	MsgWaitlistServiceInvalid = "El servicio no está disponible en este salón"
	// MsgWaitlistProfessionalInvalid es el 400 de una profesional que no es del
	// salón o no hace ese servicio.
	MsgWaitlistProfessionalInvalid = "Esa persona no atiende ese servicio"
	// Validaciones del contacto.
	MsgWaitlistNameRequired  = "El nombre es obligatorio"
	MsgWaitlistNameTooLong   = "El nombre no puede superar los 120 caracteres"
	MsgWaitlistEmailRequired = "El email es obligatorio"
	MsgWaitlistPhoneTooLong  = "El teléfono no puede superar los 40 caracteres"
	// MsgWaitlistInvalidChars es el 400 de un NUL en cualquier campo de texto.
	MsgWaitlistInvalidChars = MsgProductInvalidChars
	// MsgWaitlistDate es el 400 de una fecha mal formada.
	MsgWaitlistDate = "Fecha inválida: usá AAAA-MM-DD"
	// MsgWaitlistDateRange es el 400 de una fecha fuera de [hoy, hoy+30].
	MsgWaitlistDateRange = "Elegí un día entre hoy y los próximos 30 días"
)

// WaitlistContact es el contacto de quien se anota: el nombre que se saluda en
// el email, la dirección a la que se escribe y el teléfono (opcional, solo para
// que el salón pueda llamar).
type WaitlistContact struct {
	Name  string
	Email string
	Phone string
}

// NewWaitlistContact normaliza y valida el contacto. El email es OBLIGATORIO
// (es el canal del aviso) y se guarda normalizado; el teléfono es opcional.
func NewWaitlistContact(name, email, phone string) (WaitlistContact, error) {
	for _, field := range []string{name, email, phone} {
		if strings.ContainsRune(field, 0) {
			return WaitlistContact{}, NewMessage(ErrValidation, MsgWaitlistInvalidChars)
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return WaitlistContact{}, NewMessage(ErrValidation, MsgWaitlistNameRequired)
	}
	if utf8.RuneCountInString(name) > MaxWaitlistNameRunes {
		return WaitlistContact{}, NewMessage(ErrValidation, MsgWaitlistNameTooLong)
	}
	if strings.TrimSpace(email) == "" {
		return WaitlistContact{}, NewMessage(ErrValidation, MsgWaitlistEmailRequired)
	}
	normalized, err := NormalizeAndValidateEmail(email)
	if err != nil {
		return WaitlistContact{}, err
	}
	if len(normalized) > MaxWaitlistEmailLen {
		return WaitlistContact{}, NewMessage(ErrValidation, "Email inválido")
	}
	phone = strings.TrimSpace(phone)
	if utf8.RuneCountInString(phone) > MaxWaitlistPhoneRunes {
		return WaitlistContact{}, NewMessage(ErrValidation, MsgWaitlistPhoneTooLong)
	}
	return WaitlistContact{Name: name, Email: normalized, Phone: phone}, nil
}

// WaitlistEntry es una persona anotada. No guarda el token: guarda su SHA-256.
// Las fechas NULL (punteros) son la verdad del estado (D3).
type WaitlistEntry struct {
	ID             string
	SalonID        string
	ServiceID      string
	ProfessionalID string // "" = cualquiera
	Day            string // "YYYY-MM-DD" del día LOCAL del salón
	Name           string
	Email          string
	Phone          string
	TokenHash      string
	CreatedAt      time.Time
	// NotifyClaimedAt es el reclamo del aviso (D6): lo setea el UPDATE atómico
	// del barrido y es la retención blanda.
	NotifyClaimedAt *time.Time
	NotifyAttempts  int
	NotifiedAt      *time.Time
	LeftAt          *time.Time
}

// Status deriva el estado de la entrada (D3): `left` gana sobre `notified`
// porque quien pidió la baja ya no recibe nada.
func (e WaitlistEntry) Status() string {
	switch {
	case e.LeftAt != nil:
		return WaitlistStatusLeft
	case e.NotifiedAt != nil:
		return WaitlistStatusNotified
	default:
		return WaitlistStatusWaiting
	}
}

// Active dice si la entrada sigue en la lista (sin baja). Es lo que cuentan los
// topes de D4 y lo que lista el personal (D8).
func (e WaitlistEntry) Active() bool { return e.LeftAt == nil }

// NewWaitlistEntry arma la entrada con el día YA validado (WaitlistDay). Nace
// SIN token: la capability de la baja se emite recién cuando hay un aviso que
// mandar (WithTokenHash), así un aviso que nunca sale no deja un enlace vivo.
// El salón y el servicio son obligatorios: su ausencia es un bug del caso de
// uso, no un pedido inválido.
func NewWaitlistEntry(salonID, serviceID, professionalID, day string, c WaitlistContact, now time.Time) (WaitlistEntry, error) {
	salonID = strings.TrimSpace(salonID)
	serviceID = strings.TrimSpace(serviceID)
	if salonID == "" || serviceID == "" {
		return WaitlistEntry{}, fmt.Errorf("%w: la entrada necesita salón y servicio", ErrValidation)
	}
	if day == "" {
		return WaitlistEntry{}, fmt.Errorf("%w: la entrada necesita un día", ErrValidation)
	}
	if c.Name == "" || c.Email == "" {
		return WaitlistEntry{}, fmt.Errorf("%w: la entrada necesita nombre y email", ErrValidation)
	}
	return WaitlistEntry{
		ID:             NewID(),
		SalonID:        salonID,
		ServiceID:      serviceID,
		ProfessionalID: strings.TrimSpace(professionalID),
		Day:            day,
		Name:           c.Name,
		Email:          c.Email,
		Phone:          c.Phone,
		CreatedAt:      now.UTC(),
	}, nil
}

// WithTokenHash deja el SHA-256 del token de la baja. Es la ÚNICA forma de
// guardar una capability de la lista: el token en claro nunca toca la DB (D5) y
// el hash tiene que ser un SHA-256 en hex (64), el mismo CHECK de la migración
// 030.
func (e WaitlistEntry) WithTokenHash(tokenHash string) (WaitlistEntry, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	if len(tokenHash) != 64 || !isLowerHex(tokenHash) {
		return WaitlistEntry{}, fmt.Errorf("%w: el token del aviso no está hasheado", ErrValidation)
	}
	e.TokenHash = tokenHash
	return e, nil
}

// isLowerHex dice si el texto es hex en minúsculas: la forma de un SHA-256
// (hex.EncodeToString siempre produce minúsculas).
func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

// WaitlistDay valida el día pedido y lo devuelve normalizado ("YYYY-MM-DD").
// El rango es [HOY, HOY+30] en la ZONA DEL SALÓN: un domingo 23:30 en CDMX ya es
// lunes en UTC y sigue siendo domingo para el salón.
func WaitlistDay(day string, now time.Time, loc *time.Location) (string, error) {
	day = strings.TrimSpace(day)
	if loc == nil {
		loc = time.UTC
	}
	parsed, err := time.ParseInLocation(dateLayout, day, loc)
	if err != nil {
		return "", NewMessage(ErrValidation, MsgWaitlistDate)
	}
	// La comparación es de FECHAS locales, no de instantes: hoy entero es válido
	// aunque la hora ya haya pasado.
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	if parsed.Before(today) || parsed.After(today.AddDate(0, 0, WaitlistDaysAhead)) {
		return "", NewMessage(ErrValidation, MsgWaitlistDateRange)
	}
	return parsed.Format(dateLayout), nil
}

// WaitlistNoticesToClaim aplica D6: se reclaman `libres - retenidos` avisos,
// nunca más de WaitlistNoticeSlotsCap y nunca un número negativo.
func WaitlistNoticesToClaim(libres, retained int) int {
	if libres > WaitlistNoticeSlotsCap {
		libres = WaitlistNoticeSlotsCap
	}
	if retained < 0 {
		retained = 0
	}
	n := libres - retained
	if n < 0 {
		return 0
	}
	return n
}

// NewWaitlistToken genera el token de la baja: 32 bytes de crypto/rand en
// base64url sin relleno. Reusa el generador de F1/F2g1 a propósito: un solo
// lugar decide cuánta entropía y qué alfabeto tiene una capability del sistema.
func NewWaitlistToken() (string, error) { return NewBookingLinkToken() }

// HashWaitlistToken es la ÚNICA forma de guardar/buscar el token de la baja:
// SHA-256 en hex. Determinista porque el índice único de token_hash es lo que
// hace inequívoco el reclamo.
func HashWaitlistToken(token string) string { return HashBookingLinkToken(token) }

// WaitlistTokenMatches dice si el token en claro corresponde al hash guardado,
// en tiempo constante.
func WaitlistTokenMatches(hash, token string) bool { return BookingLinkTokenMatches(hash, token) }

// ValidWaitlistToken valida la FORMA del token antes de tocar la DB: 43
// caracteres del alfabeto base64url.
func ValidWaitlistToken(token string) bool { return ValidBookingLinkToken(token) }

// WaitlistGroup es un grupo del barrido (D6): salón + servicio + profesional
// (vacío = cualquiera) + día local. Lo que la tarea periódica consulta en la
// disponibilidad antes de reclamar avisos.
type WaitlistGroup struct {
	SalonID   string
	SalonSlug string
	SalonName string
	ServiceID string
	// ServiceName es el nombre del servicio para el email (leído en la misma
	// consulta del barrido).
	ServiceName string
	// ProfessionalID es "" cuando la entrada es «cualquiera».
	ProfessionalID string
	Day            string
}

// WaitlistStaffEntry es la fila de la pantalla del personal (D8): la entrada más
// el nombre del servicio y el de la profesional. Los nombres son solo para
// mostrar: si el servicio o la persona ya no existen, quedan vacíos.
type WaitlistStaffEntry struct {
	WaitlistEntry
	ServiceName      string
	ProfessionalName string
}
