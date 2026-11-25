package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Audit implementa ports.AuditRepository: el registro de auditoría del salón
// (F10, D1..D3; migración 034). Append-only: no hay UPDATE ni DELETE (la base
// los rechaza con un trigger). Toda query va scopeada por salon_id.
type Audit struct {
	Pool *pgxpool.Pool
	// Timeout acota cada consulta (0 = default). Escribir el evento corre en el
	// camino de la petición (después de c.Next()), así que un tope corto evita
	// que una base lenta demore la respuesta ya armada.
	Timeout time.Duration
}

const defaultAuditTimeout = 3 * time.Second

func (r Audit) ctx() (context.Context, context.CancelFunc) {
	d := r.Timeout
	if d <= 0 {
		d = defaultAuditTimeout
	}
	return context.WithTimeout(context.Background(), d)
}

// Append escribe UN evento. Un nombre de actor o un id de entidad vacío se
// guarda como NULL.
func (r Audit) Append(e domain.AuditEvent) error {
	ctx, cancel := r.ctx()
	defer cancel()
	_, err := r.Pool.Exec(ctx,
		`INSERT INTO audit_events(id,salon_id,actor_user_id,actor_name,action,entity_id,created_at)
		 VALUES($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7)`,
		e.ID, e.SalonID, e.ActorUserID, e.ActorName, e.Action, e.EntityID, e.CreatedAt.UTC())
	return pgError(err)
}

// List lee una página del salón: del más nuevo al más viejo, ESTRICTAMENTE
// anterior al cursor (comparación de fila (created_at, id), que cubre los
// empates de fecha) y con el filtro de acción de domain.AuditActionMatches
// escrito SIN LIKE: igualdad o prefijo `filtro.` (el `_` de `payroll_run` y el
// `%` no son comodines). Los parámetros del cursor van tipados porque con NULL
// Postgres no los puede inferir.
func (r Audit) List(salonID string, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	ctx, cancel := r.ctx()
	defer cancel()
	var beforeAt *time.Time
	var beforeID *string
	if q.Before != nil {
		t := q.Before.CreatedAt.UTC()
		beforeAt, beforeID = &t, &q.Before.ID
	}
	rows, err := r.Pool.Query(ctx,
		`SELECT id,salon_id,actor_user_id,COALESCE(actor_name,''),action,COALESCE(entity_id,''),created_at
		 FROM audit_events
		 WHERE salon_id=$1
		   AND ($2::text = '' OR action = $2::text OR starts_with(action, $2::text || '.'))
		   AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::text))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $5`,
		salonID, q.Action, beforeAt, beforeID, q.Limit)
	if err != nil {
		return nil, pgError(err)
	}
	defer rows.Close()
	out := []domain.AuditEvent{}
	for rows.Next() {
		var e domain.AuditEvent
		if err := rows.Scan(&e.ID, &e.SalonID, &e.ActorUserID, &e.ActorName, &e.Action, &e.EntityID, &e.CreatedAt); err != nil {
			return nil, pgError(err)
		}
		e.CreatedAt = e.CreatedAt.UTC()
		out = append(out, e)
	}
	return out, pgError(rows.Err())
}

var _ ports.AuditRepository = Audit{}
