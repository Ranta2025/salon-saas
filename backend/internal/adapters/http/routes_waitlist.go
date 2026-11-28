package httpapi

import "github.com/gin-gonic/gin"

// registerWaitlistPublicRoutes: el alta de la lista de espera (comparte el cubo
// de la reserva pública) y la baja por enlace (cubo propio) (F7, D4/D5).
func registerWaitlistPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	// F7: el alta de la lista de espera comparte el limitador de la reserva
	// pública (D4: es la misma escritura sin sesión bajo el prefijo
	// /public/salons/) y la baja tiene SU PROPIO cubo (D5).
	publicWaitlistJoin := []gin.HandlerFunc{d.Waitlist.Join}
	if d.PublicBookingLimit != nil {
		publicWaitlistJoin = append([]gin.HandlerFunc{d.PublicBookingLimit}, publicWaitlistJoin...)
	}
	v1.POST("/public/salons/:slug/waitlist", publicWaitlistJoin...)
	publicWaitlistLeave := []gin.HandlerFunc{d.Waitlist.Leave}
	if d.PublicWaitlistLeaveLimit != nil {
		publicWaitlistLeave = append([]gin.HandlerFunc{d.PublicWaitlistLeaveLimit}, publicWaitlistLeave...)
	}
	v1.POST("/public/waitlist/leave", publicWaitlistLeave...)
}

// registerWaitlistRoutes: la pantalla del personal (F7, D8).
func registerWaitlistRoutes(authed *gin.RouterGroup, d Deps) {
	// F7: lista de espera del salón (D8). Owner, administración y recepción; la
	// autorización vive en el caso de uso y toda consulta lleva salon_id.
	authed.GET("/salons/:id/waitlist", d.Waitlist.List)
	authed.DELETE("/salons/:id/waitlist/:eid", d.Waitlist.Remove)
}
