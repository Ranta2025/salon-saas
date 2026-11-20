package domain

import (
	"fmt"
	"strings"
	"time"
)

// NotificationKind cataloga de dónde viene el aviso. Es cerrado a propósito:
// el frontend tiene un ícono por tipo y la DB lo valida con un CHECK, así que
// un tipo nuevo se agrega acá (una sola lista) y no en tres lugares.
type NotificationKind string

const (
	KindInfo    NotificationKind = "info"
	KindBooking NotificationKind = "booking"
	KindSale    NotificationKind = "sale"
	KindCash    NotificationKind = "cash"
)

// Valid dice si el kind está en el catálogo.
func (k NotificationKind) Valid() bool {
	switch k {
	case KindInfo, KindBooking, KindSale, KindCash:
		return true
	default:
		return false
	}
}

// Notification es un aviso persistido de un salón. UserID vacío significa
// "para todo el salón" (la tabla lo guarda como NULL): así un cambio de turno
// o un cierre de caja le llega a quien esté mirando, sin elegir destinatarios.
type Notification struct {
	ID        string
	SalonID   string
	UserID    string // "" = aviso para todo el salón
	Title     string
	Body      string
	Kind      NotificationKind
	ReadAt    *time.Time // nil = no leída
	CreatedAt time.Time
}

// NewNotification valida y crea un aviso no leído.
func NewNotification(salonID, userID, title, body string, kind NotificationKind) (Notification, error) {
	title = strings.TrimSpace(title)
	if salonID == "" {
		return Notification{}, fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	if title == "" {
		return Notification{}, fmt.Errorf("%w: el título es obligatorio", ErrValidation)
	}
	if err := validateText("el título", title, MaxTextTitle); err != nil {
		return Notification{}, err
	}
	body = strings.TrimSpace(body)
	if err := validateText("el cuerpo del aviso", body, MaxTextBody); err != nil {
		return Notification{}, err
	}
	if !kind.Valid() {
		return Notification{}, fmt.Errorf("%w: tipo de aviso desconocido %q", ErrValidation, kind)
	}
	return Notification{
		ID:        NewID(),
		SalonID:   salonID,
		UserID:    strings.TrimSpace(userID),
		Title:     title,
		Body:      body,
		Kind:      kind,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Read dice si el aviso ya fue leído (read_at es la única fuente de verdad).
func (n Notification) Read() bool { return n.ReadAt != nil }

// MarkRead deja la marca la primera vez. Es idempotente: marcar de nuevo no
// mueve la fecha original (el momento en que se leyó es el dato que importa).
func (n *Notification) MarkRead(at time.Time) {
	if n.ReadAt != nil {
		return
	}
	at = at.UTC()
	n.ReadAt = &at
}
