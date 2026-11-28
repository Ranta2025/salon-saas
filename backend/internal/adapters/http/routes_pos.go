package httpapi

import "github.com/gin-gonic/gin"

// registerPOSRoutes: ventas, cierre de caja y panel del día.
func registerPOSRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// POS: ventas, cierre de caja y panel del día
	authed.POST("/salons/:id/sales", d.Sales.Create)
	authed.GET("/salons/:id/sales", d.Sales.List)
	authed.POST("/salons/:id/cash-closes", audit("cash_close.create"), d.CashCloses.Create)
	authed.GET("/salons/:id/cash-closes", d.CashCloses.List)
	authed.GET("/salons/:id/dashboard", d.Dashboard.Get)
}
