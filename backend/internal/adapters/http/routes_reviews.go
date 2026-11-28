package httpapi

import "github.com/gin-gonic/gin"

// registerReviewsPublicRoutes: el enlace de la reseña (F13, D4). Las dos son
// públicas y el token viaja en la RUTA (es la credencial del fragmento del
// correo).
func registerReviewsPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	// F13: el enlace de la reseña (D4). Las dos son públicas y el token viaja en
	// la RUTA (es la credencial del fragmento del correo); van bajo su propio
	// cubo por IP, como las otras escrituras sin sesión.
	publicReview := func(h gin.HandlerFunc) []gin.HandlerFunc {
		if d.PublicReviewLimit == nil {
			return []gin.HandlerFunc{h}
		}
		return []gin.HandlerFunc{d.PublicReviewLimit, h}
	}
	v1.GET("/public/reviews/:token", publicReview(d.Reviews.GetLink)...)
	v1.POST("/public/reviews/:token", publicReview(d.Reviews.Submit)...)
}

// registerReviewRoutes: las cinco rutas del panel de reseñas (F13, D6).
func registerReviewRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F13: reseñas posvisita (D6). Ver la lista y la configuración:
	// owner/admin/recepción; guardar la configuración y ocultar/mostrar una
	// reseña: solo owner/admin (el permiso va ANTES del cuerpo y de los
	// parámetros). Ocultar, mostrar y guardar la configuración se auditan.
	authed.GET("/salons/:id/reviews", d.Reviews.List)
	authed.GET("/salons/:id/review-settings", d.Reviews.GetSettings)
	authed.PUT("/salons/:id/review-settings", audit("review.settings"), d.Reviews.PutSettings)
	authed.POST("/salons/:id/reviews/:rid/hide", audit("review.hide"), d.Reviews.Hide)
	authed.POST("/salons/:id/reviews/:rid/unhide", audit("review.unhide"), d.Reviews.Unhide)
}
