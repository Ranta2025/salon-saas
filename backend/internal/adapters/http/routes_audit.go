package httpapi

import "github.com/gin-gonic/gin"

// registerAuditRoutes: el registro de auditoría (F10, D3).
func registerAuditRoutes(authed *gin.RouterGroup, d Deps) {
	// F10: el registro de auditoría. Solo owner/admin (el permiso va ANTES de los
	// parámetros, en el caso de uso). Leerlo NO se audita.
	authed.GET("/salons/:id/audit-log", d.Audit.List)
}
