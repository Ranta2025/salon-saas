package ports

import "salon-saas/internal/domain"

// --- Notificaciones ---

// NotificationRepository: avisos persistidos del salón. Las queries del salón
// (List/MarkRead/UnreadCount) reciben userID porque un aviso puede ser para una
// persona o para todo el salón (user_id NULL).
type NotificationRepository interface {
	Save(n domain.Notification) error
	List(salonID, userID string, limit int) ([]domain.Notification, error)
	MarkRead(salonID, userID, id string) error
	UnreadCount(salonID, userID string) (int, error)
}
