package httpapi

import "github.com/gin-gonic/gin"

// registerClientRoutes: fichas de clientas, su historial y las fotos. La subida
// de fotos se registra sobre el ENGINE con su ruta completa y la foto se sirve
// sin Bearer (el id aleatorio es la capability).
func registerClientRoutes(r *gin.Engine, authed *gin.RouterGroup, v1 *gin.RouterGroup, d Deps) {
	// Clientes y adjuntos. La subida de fotos se registra sobre el ENGINE con su
	// ruta completa (routePhotoUpload) y el mismo AuthMiddleware: es el string
	// que usa MaxJSONBody para saber que la ruta tiene tope de body propio.
	authed.GET("/salons/:id/clients", d.Clients.List)
	authed.POST("/salons/:id/clients", d.Clients.Create)
	// F5: la ficha por id y su historial. GET lo ve cualquier miembro activo; el
	// PUT solo owner/admin/recepción (la autorización vive en el caso de uso) y
	// el DINERO del historial solo quien puede ver la plata. Una ficha de otro
	// salón es 404 «Ficha no encontrada».
	authed.GET("/salons/:id/clients/:cid", d.Clients.Get)
	authed.PUT("/salons/:id/clients/:cid", d.Clients.Update)
	authed.GET("/salons/:id/clients/:cid/history", d.Clients.ClientHistory)
	r.POST(routePhotoUpload, AuthMiddleware(d.TokenIssuer, d.SessionGate), d.Clients.AddPhoto)
	authed.GET("/salons/:id/clients/:cid/photos", d.Clients.ListPhotos)
	authed.DELETE("/salons/:id/clients/:cid/photos/:pid", d.Clients.DeletePhoto)

	// La foto se sirve sin Bearer: <img src> no manda headers, así que el id
	// aleatorio es la capability (ver ClientHandler.StreamPhoto).
	v1.GET("/photos/:pid", d.Clients.StreamPhoto)
}
