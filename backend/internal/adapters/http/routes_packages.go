package httpapi

import "github.com/gin-gonic/gin"

// registerPackageRoutes: paquetes y bonos prepagos con saldo por clienta (F8).
func registerPackageRoutes(authed *gin.RouterGroup, d Deps) {
	// F8: paquetes y bonos prepagos con saldo por clienta. Comprar es una venta;
	// consumir una sesión no lo es. Owner/admin/recepción leen, venden y
	// consumen; crear/editar el catálogo y ajustar el saldo, solo owner/admin.
	authed.GET("/salons/:id/packages", d.Packages.ListCatalog)
	authed.POST("/salons/:id/packages", d.Packages.CreateCatalog)
	authed.PUT("/salons/:id/packages/:pid", d.Packages.UpdateCatalog)
	authed.GET("/salons/:id/clients/:cid/packages", d.Packages.ListClient)
	authed.POST("/salons/:id/clients/:cid/packages", d.Packages.Purchase)
	authed.GET("/salons/:id/clients/:cid/packages/:cpid/movements", d.Packages.Movements)
	authed.POST("/salons/:id/clients/:cid/packages/:cpid/redeem", d.Packages.Redeem)
	authed.POST("/salons/:id/clients/:cid/packages/:cpid/adjust", d.Packages.Adjust)
}
