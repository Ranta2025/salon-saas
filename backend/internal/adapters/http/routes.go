package httpapi

import (
	"github.com/gin-gonic/gin"
	"salon-saas/internal/ports"
)

// Deps agrupa lo que HTTP necesita (todo interfaces: DIP hasta el borde).
type Deps struct {
	Auth        AuthHandler
	Tenants     TenantsHandler
	TokenIssuer ports.TokenIssuer
}

// RegisterRoutes monta /api/v1. Las protegidas cuelgan del middleware.
func RegisterRoutes(r *gin.Engine, d Deps) {
	v1 := r.Group("/api/v1")
	v1.POST("/auth/register", d.Auth.Register)
	v1.POST("/auth/login", d.Auth.Login)

	authed := v1.Group("", AuthMiddleware(d.TokenIssuer))
	authed.POST("/salons", d.Tenants.CreateSalon)
}
