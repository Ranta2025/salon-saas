package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// DefaultCORSOrigins son los orígenes del frontend en desarrollo (Vite y su
// preview de build). En producción se configuran con CORS_ORIGINS.
const DefaultCORSOrigins = "http://localhost:5173,http://localhost:4173"

// ParseOrigins convierte "a,b, c" en una allowlist normalizada. Un valor vacío
// devuelve la lista por defecto: sin CORS el navegador no puede ni loguearse.
func ParseOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		raw = DefaultCORSOrigins
	}
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		if o := strings.TrimSpace(part); o != "" {
			out = append(out, strings.TrimRight(o, "/"))
		}
	}
	return out
}

// CORS permite que el navegador llame a la API solo desde los orígenes
// configurados (B-01).
//
//   - Preflight (OPTIONS) de un origen permitido: 204 con Allow-Origin,
//     Allow-Methods y Allow-Headers. Sin esto el navegador corta el login: el
//     preflight daba 404 y la petición real nunca salía.
//   - Origen NO permitido: se responde igual, pero sin headers CORS, así el
//     navegador bloquea la lectura. El preflight se corta con 403.
//   - Sin header Origin (curl, server-to-server, tests) no se toca nada.
//
// Allow-Headers es una lista fija y no el eco de Access-Control-Request-Headers:
// el contrato de la API es conocido (Authorization y Content-Type) y no queremos
// que un pedido arbitrario se convierta en un permiso nuevo.
func CORS(origins ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		allowed[strings.TrimRight(strings.TrimSpace(o), "/")] = struct{}{}
	}
	const (
		allowMethods = "GET, POST, PUT, DELETE, OPTIONS"
		allowHeaders = "Authorization, Content-Type"
		maxAge       = "600"
	)
	return func(c *gin.Context) {
		origin := strings.TrimRight(strings.TrimSpace(c.GetHeader("Origin")), "/")
		// Vary siempre: la respuesta depende del origen (cachés intermedias).
		c.Writer.Header().Add("Vary", "Origin")
		if origin == "" {
			c.Next()
			return
		}
		_, ok := allowed[origin]
		if c.Request.Method == http.MethodOptions {
			if !ok {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origen no permitido"})
				return
			}
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", allowMethods)
			h.Set("Access-Control-Allow-Headers", allowHeaders)
			h.Set("Access-Control-Max-Age", maxAge)
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		if ok {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Length")
		}
		c.Next()
	}
}
