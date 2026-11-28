package httpapi

import "github.com/gin-gonic/gin"

// registerSettingsRoutes: configuración operativa del salón (zona horaria,
// horario, paso).
func registerSettingsRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// Configuración operativa del salón (zona horaria, horario, paso)
	authed.GET("/salons/:id/settings", d.Settings.Get)
	authed.PUT("/salons/:id/settings", audit("settings.update"), d.Settings.Update)
}

// registerPublicSalonRoutes: la vidriera pública del salón (F16). La landing no
// tiene login.
func registerPublicSalonRoutes(v1 *gin.RouterGroup, d Deps) {
	// Vidriera pública del salón: la landing no tiene login.
	v1.GET("/public/salons/:slug", d.PublicSalon.GetSalon)
}

// registerPublicGalleryRoutes: la imagen de la galería se sirve sin firma
// (pública por diseño: es la foto que el salón eligió mostrar).
func registerPublicGalleryRoutes(v1 *gin.RouterGroup, d Deps) {
	// F16: la imagen de la galería se sirve SIN firma (pública por diseño: es la
	// foto que el salón eligió mostrar). Solo lee; no escribe nada sin sesión.
	v1.GET("/public/salons/:slug/gallery/:pid", d.Gallery.PublicImage)
}

// registerProfileGalleryRoutes: perfil público del salón y su galería de fotos
// (F16), incluida la subida multipart con su propio tope de body.
func registerProfileGalleryRoutes(r *gin.Engine, authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F16: perfil público del salón (cualquier miembro lee; owner/admin edita) y
	// galería de fotos (owner/admin). La subida se registra sobre el ENGINE con su
	// ruta completa (RouteGalleryUpload) y el mismo AuthMiddleware: es el string
	// que usa MaxJSONBodyExceptGalleryUpload para dejarla pasar con su tope propio.
	authed.GET("/salons/:id/profile", d.Profile.Get)
	authed.PUT("/salons/:id/profile", audit("profile.update"), d.Profile.Update)
	r.POST(RouteGalleryUpload, AuthMiddleware(d.TokenIssuer, d.SessionGate), d.Gallery.Add)
	authed.GET("/salons/:id/gallery", d.Gallery.List)
	authed.PUT("/salons/:id/gallery/:pid", d.Gallery.SetCaption)
	authed.DELETE("/salons/:id/gallery/:pid", d.Gallery.Delete)
}
