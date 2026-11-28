package httpapi

import "github.com/gin-gonic/gin"

// registerFinanceRoutes: gastos y ganancia neta (F3b).
func registerFinanceRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F3b: gastos y ganancia neta. Solo owner/administración en TODAS las rutas
	// (la autorización vive en el caso de uso); toda consulta lleva salon_id, así
	// que el gasto de otro salón es 404.
	authed.POST("/salons/:id/expenses", audit("expense.create"), d.Expenses.Create)
	authed.GET("/salons/:id/expenses", d.Expenses.List)
	authed.PUT("/salons/:id/expenses/:eid", audit("expense.update"), d.Expenses.Update)
	authed.DELETE("/salons/:id/expenses/:eid", audit("expense.delete"), d.Expenses.Delete)
	authed.GET("/salons/:id/profit", d.Profit.Get)
}
