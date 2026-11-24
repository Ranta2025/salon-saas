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
	// TimeOff son las ausencias de las profesionales (F2d2).
	TimeOff TimeOffHandler
	// Absences cubre una ausencia reasignando sus turnos (F2d3).
	Absences AbsenceHandler
	// Resources es el equipamiento con capacidad (F2e).
	Resources ResourceHandler
	// Invitations son las invitaciones de equipo (F2g1): alta, listado y
	// revocación del salón, más el lookup y el canje públicos.
	Invitations InvitationHandler
	// Payroll es la nómina de F3.2: modelo de pago por miembro, vista previa del
	// período y «Mis comisiones». Los permisos de D7 los aplica el caso de uso.
	Payroll PayrollHandler
	// PayrollRuns son las liquidaciones de F3.3 (crear, listar, detalle, ajuste,
	// pago, borrado y CSV). Solo owner/admin: lo aplica el caso de uso.
	PayrollRuns PayrollRunHandler
	// Expenses son los gastos operativos de F3b (F3b del contrato Fase 6).
	Expenses ExpenseHandler
	// Profit es el reporte de ganancia neta de F3b (ingresos - gastos - nómina
	// pagada). Solo owner/admin: lo aplica el caso de uso.
	Profit ProfitHandler
	// Exports son las tres exportaciones CSV de F4 (ventas, cierres de caja y
	// clientes). Solo owner/admin: lo aplica el caso de uso.
	Exports ExportHandler
	// Products es el catálogo de productos y su inventario (F6): cinco rutas.
	// La autorización vive en el caso de uso (leer owner/admin/recepción;
	// escribir y ver movimientos solo owner/admin).
	Products ProductHandler
	// Packages son los paquetes y bonos prepagos (F8): el catálogo del salón y,
	// por clienta, comprar, ver el saldo, descontar una sesión y ajustarlo. La
	// autorización vive en el caso de uso.
	Packages PackageHandler
	// Profile es el perfil público del salón (F16, D1) y Gallery su galería de
	// fotos (F16, D3): la autorización vive en el caso de uso.
	Profile SalonProfileHandler
	Gallery GalleryHandler
	// BookingLinks son las cuatro rutas públicas del enlace firmado de un turno
	// (F1): lookup, horarios, cancelar y reprogramar. Sin sesión y con el token
	// en el cuerpo.
	BookingLinks BookingLinkHandler
	// Waitlist es la lista de espera (F7): el alta pública, la baja por enlace y
	// las dos rutas del personal (listar y quitar). La autorización vive en el
	// caso de uso.
	Waitlist WaitlistHandler
	// PublicWaitlistLeaveLimit limita por IP la baja de la lista (D5): tiene SU
	// PROPIO cubo, con el mismo valor que /auth, como el de los enlaces de turno
	// (nil = sin límite, como en los tests unitarios del router).
	PublicWaitlistLeaveLimit gin.HandlerFunc
	// PublicBookingLinkLimit limita por IP el prefijo de los enlaces (D7): tiene
	// SU PROPIO cubo, con el mismo valor que /auth, para que una inundación de
	// enlaces no gaste el cupo de la reserva pública ni de las invitaciones.
	PublicBookingLinkLimit gin.HandlerFunc
	// PublicInvitationLimit limita por IP el lookup y el canje públicos (nil =
	// sin límite, como en los tests unitarios del router). Van bajo el mismo
	// cubo que la reserva pública: son las escrituras sin sesión.
	PublicInvitationLimit gin.HandlerFunc
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
	// F16: la imagen de la galería se sirve SIN firma (pública por diseño: es la
	// foto que el salón eligió mostrar). Solo lee; no escribe nada sin sesión.
	v1.GET("/public/salons/:slug/gallery/:pid", d.Gallery.PublicImage)
	// La reserva pública se limita por IP (como /auth): es la única escritura
	// sin autenticación. El GET de arriba no pasa por el limiter.
	publicBooking := []gin.HandlerFunc{d.Public.CreateBooking}
	if d.PublicBookingLimit != nil {
		publicBooking = append([]gin.HandlerFunc{d.PublicBookingLimit}, publicBooking...)
	}
	v1.POST("/public/salons/:slug/bookings", publicBooking...)

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

	// F2g1: el canje de una invitación también es público (la persona todavía no
	// tiene sesión) y va bajo el rate limit por IP, como /auth. El token viaja en
	// el CUERPO: nunca en la ruta ni en el query, así no queda en ningún log.
	publicInvitation := func(h gin.HandlerFunc) []gin.HandlerFunc {
		if d.PublicInvitationLimit == nil {
			return []gin.HandlerFunc{h}
		}
		return []gin.HandlerFunc{d.PublicInvitationLimit, h}
	}
	v1.POST("/public/invitations/lookup", publicInvitation(d.Invitations.Lookup)...)
	v1.POST("/public/invitations/accept", publicInvitation(d.Invitations.Accept)...)

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
	// F2g2: `DELETE` da de baja (ended_at, no borra la fila) y esta ruta deshace
	// la baja conservando rol, marca, oficio y servicios.
	authed.POST("/salons/:id/members/:uid/reactivate", d.Members.Reactivate)
	// F2a/F2c: "atiende clientes" y oficio del miembro (el dueño-barbero), y
	// qué servicios hace (PUT, lista completa).
	authed.PATCH("/salons/:id/members/:uid", d.Members.Update)
	authed.PUT("/salons/:id/members/:uid/services", d.Members.SetServices)

	// F2g1: invitaciones de equipo (alta, listado y revocación). Solo
	// dueño/administración; la regla de qué rol puede invitar cada quien vive en
	// el caso de uso (la misma de `POST /members`).
	authed.POST("/salons/:id/invitations", d.Invitations.Create)
	authed.GET("/salons/:id/invitations", d.Invitations.List)
	authed.DELETE("/salons/:id/invitations/:iid", d.Invitations.Revoke)
	// Canje para una cuenta que YA existe: la sesión es la del login, la ruta no
	// es pública y la invitación tiene que ser de SU email.
	authed.POST("/invitations/accept", d.Invitations.AcceptAs)

	// F2d1: horario semanal del salón y de cada profesional.
	authed.GET("/salons/:id/hours", d.Hours.GetSalon)
	authed.PUT("/salons/:id/hours", d.Hours.UpdateSalon)
	authed.GET("/salons/:id/members/:uid/hours", d.Hours.GetMember)
	authed.PUT("/salons/:id/members/:uid/hours", d.Hours.UpdateMember)

	// F2d2: ausencias (vacaciones y días libres puntuales) de cada profesional.
	// Cualquier miembro las lee; solo dueño/administración las carga y borra.
	authed.GET("/salons/:id/time-off", d.TimeOff.List)
	authed.POST("/salons/:id/members/:uid/time-off", d.TimeOff.Create)
	authed.DELETE("/salons/:id/time-off/:tid", d.TimeOff.Delete)

	// F2d3: cubrir una ausencia. Solo dueño/administración (el motivo es privado).
	// Reasignar es un PATCH de professional_id apoyado en la EXCLUDE de turnos: no
	// hay tabla nueva ni endpoint de alta.
	authed.GET("/salons/:id/absences", d.Absences.List)
	authed.POST("/salons/:id/absences/:tid/reassign", d.Absences.ReassignAbsence)
	authed.POST("/salons/:id/bookings/:bid/reassign", d.Absences.ReassignBooking)

	// Equipo del salón (select de profesional de la agenda)
	authed.GET("/salons/:id/staff", d.Staff.List)

	// F2e: puestos y equipamiento con capacidad. Cualquier miembro lee; solo
	// dueño/administración escribe (la autorización vive en el caso de uso).
	authed.GET("/salons/:id/resources", d.Resources.List)
	authed.POST("/salons/:id/resources", d.Resources.Create)
	authed.PATCH("/salons/:id/resources/:tid", d.Resources.Rename)
	authed.DELETE("/salons/:id/resources/:tid", d.Resources.DeleteType)
	authed.POST("/salons/:id/resources/:tid/units", d.Resources.AddUnits)
	authed.PATCH("/salons/:id/resource-units/:uid", d.Resources.UpdateUnit)

	// F16: perfil público del salón (cualquier miembro lee; owner/admin edita) y
	// galería de fotos (owner/admin). La subida se registra sobre el ENGINE con su
	// ruta completa (RouteGalleryUpload) y el mismo AuthMiddleware: es el string
	// que usa MaxJSONBodyExceptGalleryUpload para dejarla pasar con su tope propio.
	authed.GET("/salons/:id/profile", d.Profile.Get)
	authed.PUT("/salons/:id/profile", d.Profile.Update)
	r.POST(RouteGalleryUpload, AuthMiddleware(d.TokenIssuer), d.Gallery.Add)
	authed.GET("/salons/:id/gallery", d.Gallery.List)
	authed.PUT("/salons/:id/gallery/:pid", d.Gallery.SetCaption)
	authed.DELETE("/salons/:id/gallery/:pid", d.Gallery.Delete)

	// Catálogo de servicios
	authed.GET("/salons/:id/services", d.Services.ListDescribed)
	authed.POST("/salons/:id/services", d.Services.CreateDescribed)
	authed.PUT("/salons/:id/services/:service_id", d.Services.UpdateDescribed)

	// F6: productos e inventario del POS (stock y alerta de stock bajo).
	authed.GET("/salons/:id/products", d.Products.List)
	authed.POST("/salons/:id/products", d.Products.Create)
	authed.PUT("/salons/:id/products/:pid", d.Products.Update)
	authed.POST("/salons/:id/products/:pid/adjust", d.Products.Adjust)
	authed.GET("/salons/:id/products/:pid/movements", d.Products.Movements)

	// F8: paquetes y bonos prepagos con saldo por clienta. Comprar es una venta;
	// consumir una sesión no lo es. Owner/admin/recepción leen, venden y
	// consumen; crear/editar el catálogo y ajustar el saldo, solo owner/admin.
	authed.GET("/salons/:id/packages", d.Packages.ListCatalog)
	authed.POST("/salons/:id/packages", d.Packages.CreateCatalog)
	authed.PUT("/salons/:id/packages/:pid", d.Packages.UpdateCatalog)
	authed.GET("/salons/:id/clients/:cid/packages", d.Packages.ListClient)
	authed.POST("/salons/:id/clients/:cid/packages", d.Packages.Purchase)
	authed.GET("/salons/:id/clients/:cid/packages/:cpid/movements", d.Packages.Movements)
	authed.POST("/salons/:id/clients/:cid/packages/:cpid/redeem", d.Packages.Redeem)
	authed.POST("/salons/:id/clients/:cid/packages/:cpid/adjust", d.Packages.Adjust)

	// F7: lista de espera del salón (D8). Owner, administración y recepción; la
	// autorización vive en el caso de uso y toda consulta lleva salon_id.
	authed.GET("/salons/:id/waitlist", d.Waitlist.List)
	authed.DELETE("/salons/:id/waitlist/:eid", d.Waitlist.Remove)

	// POS: ventas, cierre de caja y panel del día
	authed.POST("/salons/:id/sales", d.Sales.Create)
	authed.GET("/salons/:id/sales", d.Sales.List)
	authed.POST("/salons/:id/cash-closes", d.CashCloses.Create)
	authed.GET("/salons/:id/cash-closes", d.CashCloses.List)
	authed.GET("/salons/:id/dashboard", d.Dashboard.Get)

	// F3.2: nómina y comisiones. El modelo de pago y la vista previa son de
	// dueño/administración; «Mis comisiones» es la única que ve un profesional
	// (y solo lo suyo). La autorización de cada rol vive en el caso de uso.
	authed.GET("/salons/:id/pay-models", d.Payroll.ListPayModels)
	authed.PUT("/salons/:id/members/:uid/pay-model", d.Payroll.UpdatePayModel)
	authed.POST("/salons/:id/payroll/preview", d.Payroll.Preview)
	authed.GET("/salons/:id/payroll/me", d.Payroll.Me)

	// F3.3: liquidaciones de nómina. Todas owner/admin (la autorización vive en
	// el caso de uso); el CSV baja con fetch autenticado, no con un enlace.
	authed.POST("/salons/:id/payroll/runs", d.PayrollRuns.Create)
	authed.GET("/salons/:id/payroll/runs", d.PayrollRuns.List)
	authed.GET("/salons/:id/payroll/runs/:rid", d.PayrollRuns.Get)
	authed.PATCH("/salons/:id/payroll/runs/:rid/lines/:uid", d.PayrollRuns.AdjustLine)
	authed.POST("/salons/:id/payroll/runs/:rid/pay", d.PayrollRuns.Pay)
	authed.DELETE("/salons/:id/payroll/runs/:rid", d.PayrollRuns.Delete)
	authed.GET("/salons/:id/payroll/runs/:rid/export.csv", d.PayrollRuns.ExportCSV)

	// F3b: gastos y ganancia neta. Solo owner/administración en TODAS las rutas
	// (la autorización vive en el caso de uso); toda consulta lleva salon_id, así
	// que el gasto de otro salón es 404.
	authed.POST("/salons/:id/expenses", d.Expenses.Create)
	authed.GET("/salons/:id/expenses", d.Expenses.List)
	authed.PUT("/salons/:id/expenses/:eid", d.Expenses.Update)
	authed.DELETE("/salons/:id/expenses/:eid", d.Expenses.Delete)
	authed.GET("/salons/:id/profit", d.Profit.Get)

	// F4: exportación CSV de ventas, cierres de caja y clientes. Salida MASIVA de
	// datos personales y de dinero: solo dueño/administración (la autorización
	// vive en el caso de uso). El CSV baja con fetch autenticado (Bearer), no con
	// un enlace directo, y no se cachea.
	authed.GET("/salons/:id/export/sales.csv", d.Exports.Sales)
	authed.GET("/salons/:id/export/cash-closes.csv", d.Exports.CashCloses)
	authed.GET("/salons/:id/export/clients.csv", d.Exports.Clients)

	// Bandeja de avisos. Listar va por salón; marcar leída no (el aviso ya sabe
	// de qué salón es y el alcance sale de las memberships del usuario).
	authed.GET("/salons/:id/notifications", d.Notifications.List)
	authed.POST("/notifications/:nid/read", d.Notifications.MarkRead)

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
