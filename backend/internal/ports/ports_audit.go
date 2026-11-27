package ports

import "salon-saas/internal/domain"

// ===== F10: registro de auditoría (inicio) =====

// AuditRepository: el registro de auditoría del salón (migración 034, D1). Es
// APPEND-ONLY: la base rechaza UPDATE y DELETE sobre audit_events (solo se borra
// en cascada con el salón), así que el puerto no tiene nada para modificar ni
// borrar. Toda consulta va scopeada por salon_id.
type AuditRepository interface {
	// Append escribe UN evento. El middleware lo llama después de una respuesta
	// 2xx: un error acá se loguea y no rompe la petición (D2).
	Append(e domain.AuditEvent) error
	// List trae, del más nuevo al más viejo (created_at DESC, id DESC), a lo
	// sumo q.Limit eventos del salón, ESTRICTAMENTE anteriores a q.Before (si
	// viene) y que cumplan domain.AuditActionMatches(q.Action, action).
	List(salonID string, q domain.AuditQuery) ([]domain.AuditEvent, error)
}

// ===== F10: registro de auditoría (fin) =====
