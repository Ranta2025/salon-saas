package httpapi

import "github.com/gin-gonic/gin"

// registerServiceRoutes: catálogo de servicios del salón.
func registerServiceRoutes(authed *gin.RouterGroup, d Deps) {
	// Catálogo de servicios
	authed.GET("/salons/:id/services", d.Services.ListDescribed)
	authed.POST("/salons/:id/services", d.Services.CreateDescribed)
	authed.PUT("/salons/:id/services/:service_id", d.Services.UpdateDescribed)
}

// registerResourceRoutes: puestos y equipamiento con capacidad (F2e).
func registerResourceRoutes(authed *gin.RouterGroup, d Deps) {
	// F2e: puestos y equipamiento con capacidad. Cualquier miembro lee; solo
	// dueño/administración escribe (la autorización vive en el caso de uso).
	authed.GET("/salons/:id/resources", d.Resources.List)
	authed.POST("/salons/:id/resources", d.Resources.Create)
	authed.PATCH("/salons/:id/resources/:tid", d.Resources.Rename)
	authed.DELETE("/salons/:id/resources/:tid", d.Resources.DeleteType)
	authed.POST("/salons/:id/resources/:tid/units", d.Resources.AddUnits)
	authed.PATCH("/salons/:id/resource-units/:uid", d.Resources.UpdateUnit)
}
