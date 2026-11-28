package httpapi

import "github.com/gin-gonic/gin"

// registerRealtimeRoutes: el websocket del hub de tiempo real (el token viaja
// por query param porque el navegador no manda Authorization en un WS).
func registerRealtimeRoutes(v1 *gin.RouterGroup, d Deps) {
	// Realtime: el navegador tampoco puede mandar Authorization en un WS, así
	// que el token viaja por query param y el hub lo valida antes del upgrade.
	if d.WS != nil {
		v1.GET("/ws", d.WS)
	}
}
