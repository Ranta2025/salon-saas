package httpapi

import "github.com/gin-gonic/gin"

// registerLoyaltyRoutes: fidelidad (programa del salón; ficha, entrega y uso por
// clienta) (F9).
func registerLoyaltyRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F9: fidelidad (programa del salón; ficha, entrega y uso por clienta).
	authed.GET("/salons/:id/loyalty-program", d.Loyalty.GetProgram)
	authed.PUT("/salons/:id/loyalty-program", audit("loyalty.program"), d.Loyalty.PutProgram)
	authed.GET("/salons/:id/clients/:cid/loyalty", d.Loyalty.GetClient)
	authed.POST("/salons/:id/clients/:cid/loyalty/grant", d.Loyalty.Grant)
	authed.POST("/salons/:id/clients/:cid/loyalty/rewards/:rid/redeem", d.Loyalty.Redeem)
}
