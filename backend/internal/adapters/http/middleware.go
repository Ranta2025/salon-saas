package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/ports"
)

// AuthMiddleware valida "Authorization: Bearer <jwt>" y guarda userID en contexto.
// Patrón Middleware/Chain: la ruta decide si lo usa; el handler ya recibe userID listo.
func AuthMiddleware(tokens ports.TokenIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		tok := strings.TrimPrefix(h, "Bearer ")
		if h == tok || tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "falta token"})
			return
		}
		uid, err := tokens.Parse(tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token inválido"})
			return
		}
		c.Set("userID", uid)
		c.Next()
	}
}
