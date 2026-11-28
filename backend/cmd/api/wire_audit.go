package main

import (
	"salon-saas/internal/audit"
	auditpostgres "salon-saas/internal/audit/postgres"
)

// wireAudit arma el registro de auditoría (F10, parte A): lo escribe el
// middleware `Audit(action)` de las rutas sensibles.
func wireAudit(w *wiring) {
	// F10: registro de auditoría (migración 034). Lo escribe el middleware
	// `Audit(action)` de las rutas sensibles; es append-only y de mejor esfuerzo.
	w.auditRepo = auditpostgres.Audit{Pool: w.pool}
	// F10: auditoría. Escribir (middleware, tras un 2xx) resuelve el nombre del
	// actor con el repositorio de usuarios; leer el registro es solo owner/admin.
	w.auditSvc = audit.AuditService{Events: w.auditRepo, Users: w.users, Memberships: w.mems}
}
