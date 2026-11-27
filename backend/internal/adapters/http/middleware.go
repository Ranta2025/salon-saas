package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/auth"
	"salon-saas/internal/ports"
)

// AuthMiddleware valida "Authorization: Bearer <jwt>" y guarda userID en contexto.
// Patrón Middleware/Chain: la ruta decide si lo usa; el handler ya recibe userID listo.
//
// F15 (D4): `gate` es la compuerta de revocación de sesiones. Con compuerta, un
// token emitido ANTES del último cambio de contraseña ya no vale (401 «token
// inválido», el mismo mensaje de siempre) y un fallo de la consulta FALLA CERRADO
// también con 401. Sin compuerta (los routers de las pruebas que no la cablean) el
// comportamiento es exactamente el de antes: solo la firma y el vencimiento.
func AuthMiddleware(tokens ports.TokenIssuer, gate ports.SessionGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		tok := strings.TrimPrefix(h, "Bearer ")
		if h == tok || tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "falta token"})
			return
		}
		uid, err := auth.AuthenticateSession(tokens, gate, tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token inválido"})
			return
		}
		c.Set("userID", uid)
		c.Next()
	}
}
