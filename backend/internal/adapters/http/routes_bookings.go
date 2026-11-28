package httpapi

import "github.com/gin-gonic/gin"

// registerBookingsPublicRoutes: la vidriera disponible sin login y la reserva
// pública (la única escritura sin sesión que además crea turnos).
func registerBookingsPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	v1.GET("/public/salons/:slug/availability", d.Public.Availability)
	// La reserva pública se limita por IP (como /auth): es la única escritura
	// sin autenticación. El GET de arriba no pasa por el limiter.
	publicBooking := []gin.HandlerFunc{d.Public.CreateBooking}
	if d.PublicBookingLimit != nil {
		publicBooking = append([]gin.HandlerFunc{d.PublicBookingLimit}, publicBooking...)
	}
	v1.POST("/public/salons/:slug/bookings", publicBooking...)
}

// registerBookingsRoutes: la agenda del panel.
func registerBookingsRoutes(authed *gin.RouterGroup, d Deps) {
	// Agenda
	authed.POST("/salons/:id/bookings", d.Bookings.Create)
	authed.GET("/salons/:id/bookings", d.Bookings.List)
	authed.PATCH("/salons/:id/bookings/:bid", d.Bookings.Update)
}

// registerBookingLinksPublicRoutes: las cuatro rutas públicas del enlace del
// turno (F1), con su propio cubo por IP.
func registerBookingLinksPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	// F1: el enlace firmado de un solo uso del turno. Las cuatro son POST con el
	// token en el CUERPO (nunca en la ruta ni en el query) y van bajo un límite
	// por IP propio (D7).
	publicLink := func(h gin.HandlerFunc) []gin.HandlerFunc {
		if d.PublicBookingLinkLimit == nil {
			return []gin.HandlerFunc{h}
		}
		return []gin.HandlerFunc{d.PublicBookingLinkLimit, h}
	}
	v1.POST("/public/booking-links/lookup", publicLink(d.BookingLinks.Lookup)...)
	v1.POST("/public/booking-links/slots", publicLink(d.BookingLinks.Slots)...)
	v1.POST("/public/booking-links/cancel", publicLink(d.BookingLinks.Cancel)...)
	v1.POST("/public/booking-links/reschedule", publicLink(d.BookingLinks.Reschedule)...)
}
