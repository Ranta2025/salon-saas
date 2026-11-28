package httpapi

import "github.com/gin-gonic/gin"

// registerProductRoutes: productos e inventario del POS (F6).
func registerProductRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F6: productos e inventario del POS (stock y alerta de stock bajo).
	authed.GET("/salons/:id/products", d.Products.List)
	authed.POST("/salons/:id/products", d.Products.Create)
	authed.PUT("/salons/:id/products/:pid", d.Products.Update)
	authed.POST("/salons/:id/products/:pid/adjust", audit("product.adjust"), d.Products.Adjust)
	authed.GET("/salons/:id/products/:pid/movements", d.Products.Movements)
}
