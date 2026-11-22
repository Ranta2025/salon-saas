package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Ausencias de una profesional (F2d2): vacaciones y días libres puntuales.
//
// Una ausencia es un período [starts_at, ends_at) en UTC de UNA persona
// reservable (F2a). La UI convierte desde la zona del salón; un día completo es
// de 00:00 a 00:00 del día siguiente en esa zona.
//
// El período es semiabierto por la misma razón que los turnos: una ausencia que
// termina a las 12:00 no bloquea el turno que arranca a las 12:00, y una que
// empieza a las 12:00 no bloquea la que termina a esa hora.
const (
	// MaxTimeOffDays es el tope de duración de una ausencia. Noventa días es
	// una licencia; más que eso es un error de tipeo en las fechas.
	MaxTimeOffDays = 90
	// MaxTimeOffRangeDays es el ancho máximo del rango que se puede LISTAR
	// (S-F2d2-5): la pantalla de equipo pide semanas o meses, no años.
	MaxTimeOffRangeDays = 92
	// MaxTextTimeOffReason es el tope del motivo ("vacaciones", "médico").
	MaxTextTimeOffReason = 120
)

// Mensajes exactos del contrato (S-F2d2-5): los ve la persona en Equipo, así que
// son parte de la API.
const (
	MsgTimeOffOrder   = "El fin debe ser posterior al inicio"
	MsgTimeOffTooLong = "Una ausencia no puede durar más de 90 días"
	MsgTimeOffPast    = "No se puede cargar una ausencia que ya terminó"
	MsgTimeOffOverlap = "Ya hay una ausencia en ese período"
	MsgTimeOffRole    = "Ese rol no tiene ausencias"

	// Errores de la CONSULTA de la lista (?from&to), no de una ausencia.
	MsgTimeOffRangeMissing = "Indicá el rango de fechas (from y to)"
	MsgTimeOffRangeOrder   = "Rango inválido: el fin debe ser posterior al inicio"
	MsgTimeOffRangeTooLong = "El rango no puede superar los 92 días"
)

// ValidateTimeOffRange valida el rango [from, to) de la lista de ausencias
// (GET /salons/:id/time-off). La pantalla de equipo pide semanas o meses, no
// años: un rango enorme es un error de la consulta, no una lista larga.
func ValidateTimeOffRange(from, to time.Time) error {
	if from.IsZero() || to.IsZero() {
		return NewMessage(ErrValidation, MsgTimeOffRangeMissing)
	}
	if !to.After(from) {
		return NewMessage(ErrValidation, MsgTimeOffRangeOrder)
	}
	if to.Sub(from) > MaxTimeOffRangeDays*24*time.Hour {
		return NewMessage(ErrValidation, MsgTimeOffRangeTooLong)
	}
	return nil
}

// TimeOff es una ausencia cargada de una profesional. Reason es el motivo
// visible solo para dueño/administración y para la propia persona.
type TimeOff struct {
	ID        string
	SalonID   string
	UserID    string
	StartsAt  time.Time
	EndsAt    time.Time
	Reason    string
	CreatedBy string
	CreatedAt time.Time
}

// TimeOffInput es el pedido de alta ya parseado (el handler traduce el cuerpo).
type TimeOffInput struct {
	SalonID   string
	UserID    string
	StartsAt  time.Time
	EndsAt    time.Time
	Reason    string
	CreatedBy string
}

// NewTimeOff valida y crea una ausencia (S-F2d2-2). `now` se recibe y no se lee
// del reloj para que la regla del pasado sea testeable y determinista.
//
// Las validaciones van en este orden porque es el orden en que la persona las
// entiende: primero las fechas coherentes, después la duración, después "esto ya
// pasó" y al final el motivo.
func NewTimeOff(in TimeOffInput, now time.Time) (TimeOff, error) {
	if strings.TrimSpace(in.SalonID) == "" || strings.TrimSpace(in.UserID) == "" {
		return TimeOff{}, NewMessage(ErrValidation, "Falta la persona de la ausencia")
	}
	start, end := in.StartsAt.UTC(), in.EndsAt.UTC()
	if !end.After(start) {
		return TimeOff{}, NewMessage(ErrValidation, MsgTimeOffOrder)
	}
	if end.Sub(start) > MaxTimeOffDays*24*time.Hour {
		return TimeOff{}, NewMessage(ErrValidation, MsgTimeOffTooLong)
	}
	// Una ausencia que ya terminó no bloquea nada: cargarla sería mentirle a la
	// agenda. Una EN CURSO sí se acepta (hoy es el día libre de hoy).
	if !end.After(now.UTC()) {
		return TimeOff{}, NewMessage(ErrValidation, MsgTimeOffPast)
	}
	reason, err := normalizeTimeOffReason(in.Reason)
	if err != nil {
		return TimeOff{}, err
	}
	return TimeOff{
		ID:        NewID(),
		SalonID:   in.SalonID,
		UserID:    in.UserID,
		StartsAt:  start,
		EndsAt:    end,
		Reason:    reason,
		CreatedBy: strings.TrimSpace(in.CreatedBy),
		CreatedAt: now.UTC(),
	}, nil
}

// Overlaps dice si la ausencia pisa [start, end). Semiabierto en los dos lados:
// tocarse en el borde no es pisarse.
func (t TimeOff) Overlaps(start, end time.Time) bool {
	return t.StartsAt.Before(end) && start.Before(t.EndsAt)
}

// normalizeTimeOffReason recorta, rechaza saltos de línea y acota el motivo. Va
// aparte porque el alta y cualquier camino futuro lo comparten.
func normalizeTimeOffReason(reason string) (string, error) {
	// El salto se busca en el texto CRUDO: recortar primero dejaría pasar un
	// "me voy\n" (el recorte se lleva el salto final y el motivo queda limpio).
	if strings.ContainsAny(reason, "\r\n") {
		return "", NewMessage(ErrValidation, "El motivo no puede tener saltos de línea")
	}
	clean := strings.TrimSpace(reason)
	if utf8.RuneCountInString(clean) > MaxTextTimeOffReason {
		return "", NewMessage(ErrValidation,
			"El motivo no puede superar los 120 caracteres")
	}
	return clean, nil
}

// HasTimeOff dice si esta persona puede tener ausencias: solo quien atiende
// clientes (F2a). Recepción y cliente no, y un dueño/administración sin la marca
// tampoco: no tiene turnos que bloquear.
func (m Membership) HasTimeOff() bool { return m.Bookable() }
