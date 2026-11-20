// NotificationService: avisos del salón (los persistidos, los que quedan en la
// bandeja cuando el navegador estaba cerrado). El event publisher avisa en el
// momento; esto es lo que sobrevive al refresh.
package app

import (
	"errors"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

const (
	defaultNotificationLimit = 50
	maxNotificationLimit     = 200
)

type NotificationService struct {
	Notifications ports.NotificationRepository
	// Salons resuelve a qué salones pertenece un usuario. La ruta de "marcar
	// leída" no trae :salon_id (el aviso ya sabe de qué salón es), así que el
	// alcance se deriva de las memberships reales del usuario, nunca del body.
	Salons ports.MembershipLister
	Events ports.EventPublisher
}

// Create deja un aviso y lo publica. Es de uso interno (después de una venta,
// un turno, un cierre): no recibe actor porque quien llama ya validó permisos.
// userID vacío = aviso para todo el salón.
func (s NotificationService) Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error) {
	n, err := domain.NewNotification(salonID, userID, title, body, kind)
	if err != nil {
		return domain.Notification{}, err
	}
	if err := s.Notifications.Save(n); err != nil {
		return domain.Notification{}, err
	}
	// El evento `notify` lleva la notificación ya persistida (con su id): el
	// frontend la pinta al instante y después la puede marcar leída.
	publish(s.Events, ports.Event{
		Type:    "notify",
		SalonID: n.SalonID,
		Data:    map[string]any{"notification": NotificationPayload(n)},
		TS:      time.Now().UTC(),
	})
	return n, nil
}

// List trae los avisos visibles para el actor en el salón: los del salón
// completo y los dirigidos a él. Cualquier miembro puede leer.
func (s NotificationService) List(actor domain.Membership, salonID string, limit int) ([]domain.Notification, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return nil, err
	}
	return s.Notifications.List(salonID, actor.UserID, clampLimit(limit))
}

// UnreadCount alimenta el badge del frontend.
func (s NotificationService) UnreadCount(actor domain.Membership, salonID string) (int, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return 0, err
	}
	return s.Notifications.UnreadCount(salonID, actor.UserID)
}

// MarkRead marca un aviso como leído. La ruta no trae el salón, así que se
// recorre la lista de salones del usuario: el repo solo acepta el aviso si es
// de ese salón y visible para esa persona. Un aviso de otro tenant simplemente
// no existe (ErrNotFound), no se filtra ni su id.
func (s NotificationService) MarkRead(actor domain.Membership, id string) error {
	if actor.UserID == "" {
		return domain.ErrUnauthorized
	}
	if id == "" {
		return domain.ErrNotFound
	}
	salons, err := s.Salons.ListByUser(actor.UserID)
	if err != nil {
		return err
	}
	for _, m := range salons {
		err := s.Notifications.MarkRead(m.SalonID, actor.UserID, id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return domain.ErrNotFound
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultNotificationLimit
	case limit > maxNotificationLimit:
		return maxNotificationLimit
	default:
		return limit
	}
}

// member valida la membership real contra la DB: el rol que trae el actor no
// se usa como permiso (podría estar viejo o ser inventado).
func (s NotificationService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.UserID == "" {
		return domain.Membership{}, domain.ErrUnauthorized
	}
	salons, err := s.Salons.ListByUser(actor.UserID)
	if err != nil {
		return domain.Membership{}, err
	}
	for _, m := range salons {
		if m.SalonID == salonID {
			return m, nil
		}
	}
	return domain.Membership{}, domain.ErrForbidden
}

// NotificationPayload es la forma única de serializar un aviso: la usan el
// evento `notify` del websocket y el listado HTTP, así el frontend parsea lo
// mismo venga por donde venga.
func NotificationPayload(n domain.Notification) map[string]any {
	out := map[string]any{
		"id":         n.ID,
		"salon_id":   n.SalonID,
		"title":      n.Title,
		"body":       n.Body,
		"kind":       string(n.Kind),
		"read":       n.Read(),
		"read_at":    nil,
		"created_at": n.CreatedAt.UTC().Format(time.RFC3339),
	}
	if n.UserID != "" {
		out["user_id"] = n.UserID
	}
	if n.ReadAt != nil {
		out["read_at"] = n.ReadAt.UTC().Format(time.RFC3339)
	}
	return out
}
