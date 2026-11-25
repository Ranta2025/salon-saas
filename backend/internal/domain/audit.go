// Registro de auditoría (F10, parte A del contrato de la fase 6): quién hizo
// qué, sobre qué id y cuándo. Nada más: sin valores antes/después ni datos
// personales (D1).
//
// El registro lo escribe un middleware de HTTP después de una respuesta 2xx y
// es de MEJOR ESFUERZO (D2): un fallo al escribir se loguea y no rompe la
// petición. El dominio no sabe de HTTP ni de SQL: valida el evento, el cursor
// de paginación y el filtro de acción, y dice cuándo una acción coincide con un
// filtro.
package domain

import (
	"encoding/base64"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxAuditActionLen y MaxAuditEntityLen son los CHECK de la migración 034.
	MaxAuditActionLen = 60
	MaxAuditEntityLen = 100
	// MaxAuditActorNameLen acota el snapshot del nombre del actor (la tabla no lo
	// limita, pero no hay motivo para guardar más).
	MaxAuditActorNameLen = 120
	// DefaultAuditLimit y MaxAuditLimit son el tamaño de página de GET audit-log.
	DefaultAuditLimit = 50
	MaxAuditLimit     = 100
	// maxAuditCursorLen acota el cursor antes de decodificarlo: uno legítimo mide
	// ~60 caracteres (fecha + id de 32).
	maxAuditCursorLen = 300
)

// Textos exactos del contrato F10 (D3), sin tildes tal cual están escritos.
const (
	MsgAuditLimitRange    = "El limite debe ser entre 1 y 100"
	MsgAuditCursorInvalid = "Cursor invalido"
	// No está literal en el contrato (solo dice «400»): sigue el estilo de los
	// otros dos.
	MsgAuditActionInvalid = "Accion invalida"
)

// AuditEvent es una fila del registro. EntityID vacío = NULL (la acción no
// tiene un id propio en la ruta, p. ej. `member.add`).
type AuditEvent struct {
	ID          string
	SalonID     string
	ActorUserID string
	// ActorName es una FOTO del nombre al momento del evento (vacío si no se
	// pudo resolver: nunca hace fallar el registro).
	ActorName string
	Action    string
	EntityID  string
	CreatedAt time.Time
}

// AuditCursor es la posición de una página: el último evento ya devuelto. El
// orden es (created_at DESC, id DESC), así que la página siguiente son los
// eventos ESTRICTAMENTE anteriores a (CreatedAt, ID).
type AuditCursor struct {
	CreatedAt time.Time
	ID        string
}

// AuditQuery es una consulta ya validada de GET audit-log.
type AuditQuery struct {
	Limit  int
	Before *AuditCursor
	// Action vacío = sin filtro; si no, la acción exacta o un prefijo hasta un
	// punto (ver AuditActionMatches).
	Action string
}

// cleanAuditText dice si un texto es válido para guardarlo: UTF-8 y sin
// caracteres de control (NUL, saltos de línea, tabulaciones...).
func cleanAuditText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// NewAuditEvent valida y arma un evento. La acción lleva 1..60 caracteres y el
// id de la entidad (opcional) hasta 100, sin caracteres de control. El nombre
// del actor NO falla nunca: se limpia (sin control) y se acota, porque una
// auditoría no debe perderse por el nombre de quien actuó.
func NewAuditEvent(salonID, actorUserID, actorName, action, entityID string, now time.Time) (AuditEvent, error) {
	if salonID == "" || actorUserID == "" {
		return AuditEvent{}, NewMessage(ErrValidation, "evento de auditoría sin salón o sin actor")
	}
	if n := utf8.RuneCountInString(action); n < 1 || n > MaxAuditActionLen || !cleanAuditText(action) {
		return AuditEvent{}, NewMessage(ErrValidation, MsgAuditActionInvalid)
	}
	if utf8.RuneCountInString(entityID) > MaxAuditEntityLen || !cleanAuditText(entityID) {
		return AuditEvent{}, NewMessage(ErrValidation, "id de entidad inválido")
	}
	return AuditEvent{
		ID: NewID(), SalonID: salonID, ActorUserID: actorUserID,
		ActorName: cleanAuditName(actorName),
		Action:    action, EntityID: entityID, CreatedAt: now,
	}, nil
}

func cleanAuditName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > MaxAuditActorNameLen {
		name = string(r[:MaxAuditActorNameLen])
	}
	return name
}

// EncodeAuditCursor arma el cursor opaco: base64url sin relleno de
// «<created_at RFC3339Nano UTC>|<id>».
func EncodeAuditCursor(c AuditCursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeAuditCursor valida un cursor. Solo acepta la forma que produce
// EncodeAuditCursor (base64url sin relleno, fecha canónica, id limpio): todo lo
// demás es 400 «Cursor invalido».
func DecodeAuditCursor(s string) (AuditCursor, error) {
	invalid := NewMessage(ErrValidation, MsgAuditCursorInvalid)
	if s == "" || len(s) > maxAuditCursorLen {
		return AuditCursor{}, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return AuditCursor{}, invalid
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" || utf8.RuneCountInString(id) > MaxAuditEntityLen || !cleanAuditText(id) {
		return AuditCursor{}, invalid
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return AuditCursor{}, invalid
	}
	c := AuditCursor{CreatedAt: at.UTC(), ID: id}
	if EncodeAuditCursor(c) != s {
		return AuditCursor{}, invalid
	}
	return c, nil
}

// ParseAuditQuery valida los tres parámetros de GET audit-log (los textos tal
// cual llegan; vacío = no se pasó). El orden de las reglas es: límite, cursor,
// acción. Límite: entero de 1 a 100 (por defecto 50).
func ParseAuditQuery(limit, before, action string) (AuditQuery, error) {
	q := AuditQuery{Limit: DefaultAuditLimit}
	if limit != "" {
		n, ok := parseAuditLimit(limit)
		if !ok {
			return AuditQuery{}, NewMessage(ErrValidation, MsgAuditLimitRange)
		}
		q.Limit = n
	}
	if before != "" {
		c, err := DecodeAuditCursor(before)
		if err != nil {
			return AuditQuery{}, err
		}
		q.Before = &c
	}
	// Solo se recortan espacios: un salto de línea al final es un 400, no se
	// descarta en silencio.
	action = strings.Trim(action, " ")
	if action != "" {
		if utf8.RuneCountInString(action) > MaxAuditActionLen || !cleanAuditText(action) {
			return AuditQuery{}, NewMessage(ErrValidation, MsgAuditActionInvalid)
		}
		q.Action = action
	}
	return q, nil
}

// parseAuditLimit acepta solo dígitos ASCII (sin signo ni espacios ni
// exponente) y de 1 a 100.
func parseAuditLimit(s string) (int, bool) {
	if len(s) > 4 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n < 1 || n > MaxAuditLimit {
		return 0, false
	}
	return n, true
}

// AuditActionMatches es la ÚNICA regla del filtro `action`: coincide la acción
// completa o un prefijo que termina justo antes de un punto («member» coincide
// con «member.add» pero «mem» no, y «member.» tampoco). Vacío = todas. El
// adaptador Postgres la repite en SQL sin LIKE (el `_` de `payroll_run` no es
// comodín) y su test de integración la compara con esta.
func AuditActionMatches(filter, action string) bool {
	if filter == "" {
		return true
	}
	return action == filter || strings.HasPrefix(action, filter+".")
}
