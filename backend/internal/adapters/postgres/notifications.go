package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// Notifications: bandeja de avisos del salón. Toda query va scopeada por
// salon_id y, cuando corresponde, por destinatario: user_id NULL es un aviso
// para todo el salón y le corresponde a cualquier miembro.
type Notifications struct{ Pool *pgxpool.Pool }

const notificationColumns = `id,salon_id,COALESCE(user_id,''),title,body,kind,read_at,created_at`

func scanNotification(scan func(...interface{}) error) (domain.Notification, error) {
	var n domain.Notification
	var kind string
	if err := scan(&n.ID, &n.SalonID, &n.UserID, &n.Title, &n.Body, &kind, &n.ReadAt, &n.CreatedAt); err != nil {
		return domain.Notification{}, err
	}
	n.Kind = domain.NotificationKind(kind)
	n.CreatedAt = n.CreatedAt.UTC()
	if n.ReadAt != nil {
		at := n.ReadAt.UTC()
		n.ReadAt = &at
	}
	return n, nil
}

// Save persiste un aviso. El user_id vacío se guarda como NULL (aviso del
// salón): así el índice y las queries no tienen dos representaciones del mismo
// concepto.
func (r Notifications) Save(n domain.Notification) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO notifications(id,salon_id,user_id,title,body,kind,read_at,created_at)
		 VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8)
		 ON CONFLICT (id) DO NOTHING`,
		n.ID, n.SalonID, n.UserID, n.Title, n.Body, string(n.Kind), n.ReadAt, n.CreatedAt)
	return pgError(err)
}

// List devuelve los avisos visibles para userID en el salón, más nuevos
// primero: los del salón completo y los dirigidos a esa persona.
func (r Notifications) List(salonID, userID string, limit int) ([]domain.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+notificationColumns+` FROM notifications
		 WHERE salon_id=$1 AND (user_id IS NULL OR user_id=$2)
		 ORDER BY created_at DESC, id DESC
		 LIMIT $3`, salonID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Notification
	for rows.Next() {
		n, err := scanNotification(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkRead marca el aviso como leído. El WHERE es la autorización completa:
// mismo salón, y el aviso es del salón o de esa persona. Si no matchea ninguna
// fila, ErrNotFound (no se distingue "no existe" de "no es tuyo").
func (r Notifications) MarkRead(salonID, userID, id string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE notifications SET read_at = COALESCE(read_at, $4)
		 WHERE id=$1 AND salon_id=$2 AND (user_id IS NULL OR user_id=$3)`,
		id, salonID, userID, time.Now().UTC())
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UnreadCount es el badge del frontend.
func (r Notifications) UnreadCount(salonID, userID string) (int, error) {
	var n int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM notifications
		 WHERE salon_id=$1 AND (user_id IS NULL OR user_id=$2) AND read_at IS NULL`,
		salonID, userID).Scan(&n)
	if err != nil {
		return 0, notFound(err)
	}
	return n, nil
}
