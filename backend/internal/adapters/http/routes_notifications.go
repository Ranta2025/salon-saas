package httpapi

import "github.com/gin-gonic/gin"

// registerNotificationRoutes: la bandeja de avisos.
func registerNotificationRoutes(authed *gin.RouterGroup, d Deps) {
	// Bandeja de avisos. Listar va por salón; marcar leída no (el aviso ya sabe
	// de qué salón es y el alcance sale de las memberships del usuario).
	authed.GET("/salons/:id/notifications", d.Notifications.List)
	authed.POST("/notifications/:nid/read", d.Notifications.MarkRead)
}
