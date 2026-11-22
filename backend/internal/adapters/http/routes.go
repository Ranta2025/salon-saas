package httpapi

import (
	"github.com/gin-gonic/gin"
	"salon-saas/internal/ports"
)

// Deps agrupa lo que HTTP necesita (todo interfaces: DIP hasta el borde).
type Deps struct {
	Auth          AuthHandler
	Tenants       TenantsHandler
	Bookings      BookingHandler
	Services      ServiceHandler
	Sales         SaleHandler
	CashCloses    CashCloseHandler
	Dashboard     DashboardHandler
	Notifications NotificationHandler
	Clients       ClientHandler
	Me            MeHandler
	Staff         StaffHandler
	Public        PublicHandler
	Settings      SettingsHandler
	Members       MemberHandler
	// Hours es el horario semanal del salón y de cada profesional (F2d1).
	Hours HoursHandler
	// PublicBookingLimit limita por IP el POST público de reservas (nil = sin
	// límite, como en los tests unitarios del router).
	PublicBookingLimit gin.HandlerFunc
	// WS es el handler del websocket (el hub de realtime). Va como
	// gin.HandlerFunc para que este paquete no conozca gorilla/websocket:
	// el composition root monta el adaptador concreto.
	WS          gin.HandlerFunc
	TokenIssuer ports.TokenIssuer
}

// RegisterRoutes monta /api/v1. Las protegidas cuelgan del middleware.
//
// Las rutas públicas (auth, vidriera del salón, foto por capability, WS) no
// llevan Bearer; el resto sí.
func RegisterRoutes(r *gin.Engine, d Deps) {
	v1 := r.Group("/api/v1")
	v1.POST("/auth/register", d.Auth.Register)
	v1.POST("/auth/login", d.Auth.Login)

	// Vidriera pública del salón: la landing no tiene login.
	v1.GET("/public/salons/:slug", d.Public.GetSalon)
	v1.GET("/public/salons/:slug/availability", d.Public.Availability)
	// La reserva pública se limita por IP (como /auth): es la única escritura
	// sin autenticación. El GET de arriba no pasa por el limiter.
	publicBooking := []gin.HandlerFunc{d.Public.CreateBooking}
	if d.PublicBookingLimit != nil {
		publicBooking = append([]gin.HandlerFunc{d.PublicBookingLimit}, publicBooking...)
	}
	v1.POST("/public/salons/:slug/bookings", publicBooking...)

	authed := v1.Group("", AuthMiddleware(d.TokenIssuer))
	// Quién soy y en qué salones estoy (el frontend elige el salón activo).
	authed.GET("/me", d.Me.Get)
	authed.POST("/salons", d.Tenants.CreateSalon)

	// Agenda
	authed.POST("/salons/:id/bookings", d.Bookings.Create)
	authed.GET("/salons/:id/bookings", d.Bookings.List)
	authed.PATCH("/salons/:id/bookings/:bid", d.Bookings.Update)

	// Configuración operativa del salón (zona horaria, horario, paso)
	authed.GET("/salons/:id/settings", d.Settings.Get)
	authed.PUT("/salons/:id/settings", d.Settings.Update)

	// Equipo del salón (alta/baja de miembros, solo owner/admin)
	authed.GET("/salons/:id/members", d.Members.List)
	authed.POST("/salons/:id/members", d.Members.Add)
	authed.DELETE("/salons/:id/members/:uid", d.Members.Remove)
	// F2a/F2c: "atiende clientes" y oficio del miembro (el dueño-barbero), y
	// qué servicios hace (PUT, lista completa).
	authed.PATCH("/salons/:id/members/:uid", d.Members.Update)
	authed.PUT("/salons/:id/members/:uid/services", d.Members.SetServices)

	// F2d1: horario semanal del salón y de cada profesional.
	authed.GET("/salons/:id/hours", d.Hours.GetSalon)
	authed.PUT("/salons/:id/hours", d.Hours.UpdateSalon)
	authed.GET("/salons/:id/members/:uid/hours", d.Hours.GetMember)
	authed.PUT("/salons/:id/members/:uid/hours", d.Hours.UpdateMember)

	// Equipo del salón (select de profesional de la agenda)
	authed.GET("/salons/:id/staff", d.Staff.List)

	// Catálogo de servicios
	authed.GET("/salons/:id/services", d.Services.List)
	authed.POST("/salons/:id/services", d.Services.Create)
	authed.PUT("/salons/:id/services/:service_id", d.Services.Update)

	// POS: ventas, cierre de caja y panel del día
	authed.POST("/salons/:id/sales", d.Sales.Create)
	authed.GET("/salons/:id/sales", d.Sales.List)
	authed.POST("/salons/:id/cash-closes", d.CashCloses.Create)
	authed.GET("/salons/:id/cash-closes", d.CashCloses.List)
	authed.GET("/salons/:id/dashboard", d.Dashboard.Get)

	// Bandeja de avisos. Listar va por salón; marcar leída no (el aviso ya sabe
	// de qué salón es y el alcance sale de las memberships del usuario).
	authed.GET("/salons/:id/notifications", d.Notifications.List)
	authed.POST("/notifications/:nid/read", d.Notifications.MarkRead)

	// Clientes y adjuntos. La subida de fotos se registra sobre el ENGINE con su
	// ruta completa (routePhotoUpload) y el mismo AuthMiddleware: es el string
	// que usa MaxJSONBody para saber que la ruta tiene tope de body propio.
	authed.GET("/salons/:id/clients", d.Clients.List)
	authed.POST("/salons/:id/clients", d.Clients.Create)
	r.POST(routePhotoUpload, AuthMiddleware(d.TokenIssuer), d.Clients.AddPhoto)
	authed.GET("/salons/:id/clients/:cid/photos", d.Clients.ListPhotos)
	authed.DELETE("/salons/:id/clients/:cid/photos/:pid", d.Clients.DeletePhoto)

	// La foto se sirve sin Bearer: <img src> no manda headers, así que el id
	// aleatorio es la capability (ver ClientHandler.StreamPhoto).
	v1.GET("/photos/:pid", d.Clients.StreamPhoto)

	// Realtime: el navegador tampoco puede mandar Authorization en un WS, así
	// que el token viaja por query param y el hub lo valida antes del upgrade.
	if d.WS != nil {
		v1.GET("/ws", d.WS)
	}
}
