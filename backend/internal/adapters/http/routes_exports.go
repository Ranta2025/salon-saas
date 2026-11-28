package httpapi

import "github.com/gin-gonic/gin"

// registerExportRoutes: exportación CSV de ventas, cierres de caja y clientes
// (F4).
func registerExportRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F4: exportación CSV de ventas, cierres de caja y clientes. Salida MASIVA de
	// datos personales y de dinero: solo dueño/administración (la autorización
	// vive en el caso de uso). El CSV baja con fetch autenticado (Bearer), no con
	// un enlace directo, y no se cachea.
	authed.GET("/salons/:id/export/sales.csv", audit("export.sales"), d.Exports.Sales)
	authed.GET("/salons/:id/export/cash-closes.csv", d.Exports.CashCloses)
	authed.GET("/salons/:id/export/clients.csv", audit("export.clients"), d.Exports.Clients)
}
