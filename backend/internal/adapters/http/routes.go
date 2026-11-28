// routes.go: el esqueleto del router. Acá viven `Deps` (todo lo que HTTP
// necesita), la creación de los grupos `/api/v1` y autenticado, y el ORDEN en
// que se monta cada funcionalidad. Las rutas de cada funcionalidad están en su
// `routes_<funcionalidad>.go`; el orden de las llamadas es el de siempre, porque
// gin resuelve el árbol por prefijo (las rutas estáticas antes que los
// parámetros del mismo prefijo).
package httpapi

import (
	audithttpapi "salon-saas/internal/audit/httpapi"
	authhttpapi "salon-saas/internal/auth/httpapi"
	bookingshttpapi "salon-saas/internal/bookings/httpapi"
	clientshttpapi "salon-saas/internal/clients/httpapi"
	depositshttpapi "salon-saas/internal/deposits/httpapi"
	exportshttpapi "salon-saas/internal/exports/httpapi"
	financehttpapi "salon-saas/internal/finance/httpapi"
	loyaltyhttpapi "salon-saas/internal/loyalty/httpapi"
	notificationshttpapi "salon-saas/internal/notifications/httpapi"
	packageshttpapi "salon-saas/internal/packages/httpapi"
	payrollhttpapi "salon-saas/internal/payroll/httpapi"
	"salon-saas/internal/ports"
	poshttpapi "salon-saas/internal/pos/httpapi"
	productshttpapi "salon-saas/internal/products/httpapi"
	reviewshttpapi "salon-saas/internal/reviews/httpapi"
	scheduleshttpapi "salon-saas/internal/schedules/httpapi"
	settingshttpapi "salon-saas/internal/settings/httpapi"
	staffinghttpapi "salon-saas/internal/staffing/httpapi"
	teamhttpapi "salon-saas/internal/team/httpapi"
	waitlisthttpapi "salon-saas/internal/waitlist/httpapi"

	"github.com/gin-gonic/gin"
)

// Deps agrupa lo que HTTP necesita (todo interfaces: DIP hasta el borde).
type Deps struct {
	Auth          authhttpapi.AuthHandler
	Tenants       teamhttpapi.TenantsHandler
	Bookings      bookingshttpapi.BookingHandler
	Services      staffinghttpapi.ServiceHandler
	Sales         poshttpapi.SaleHandler
	CashCloses    poshttpapi.CashCloseHandler
	Dashboard     poshttpapi.DashboardHandler
	Notifications notificationshttpapi.NotificationHandler
	Clients       clientshttpapi.ClientHandler
	Me            authhttpapi.MeHandler
	Staff         teamhttpapi.StaffHandler
	Public        bookingshttpapi.PublicHandler
	// PublicSalon es la vidriera pública del salón (F16, D4): la landing no
	// tiene login y lee lo que el dueño edita en el panel.
	PublicSalon settingshttpapi.PublicSalonHandler
	Settings    settingshttpapi.SettingsHandler
	Members     teamhttpapi.MemberHandler
	// Hours es el horario semanal del salón y de cada profesional (F2d1).
	Hours scheduleshttpapi.HoursHandler
	// TimeOff son las ausencias de las profesionales (F2d2).
	TimeOff scheduleshttpapi.TimeOffHandler
	// Absences cubre una ausencia reasignando sus turnos (F2d3).
	Absences scheduleshttpapi.AbsenceHandler
	// Resources es el equipamiento con capacidad (F2e).
	Resources staffinghttpapi.ResourceHandler
	// Invitations son las invitaciones de equipo (F2g1): alta, listado y
	// revocación del salón, más el lookup y el canje públicos.
	Invitations teamhttpapi.InvitationHandler
	// Payroll es la nómina de F3.2: modelo de pago por miembro, vista previa del
	// período y «Mis comisiones». Los permisos de D7 los aplica el caso de uso.
	Payroll payrollhttpapi.PayrollHandler
	// PayrollRuns son las liquidaciones de F3.3 (crear, listar, detalle, ajuste,
	// pago, borrado y CSV). Solo owner/admin: lo aplica el caso de uso.
	PayrollRuns payrollhttpapi.PayrollRunHandler
	// Expenses son los gastos operativos de F3b (F3b del contrato Fase 6).
	Expenses financehttpapi.ExpenseHandler
	// Profit es el reporte de ganancia neta de F3b (ingresos - gastos - nómina
	// pagada). Solo owner/admin: lo aplica el caso de uso.
	Profit financehttpapi.ProfitHandler
	// Exports son las tres exportaciones CSV de F4 (ventas, cierres de caja y
	// clientes). Solo owner/admin: lo aplica el caso de uso.
	Exports exportshttpapi.ExportHandler
	// Products es el catálogo de productos y su inventario (F6): cinco rutas.
	// La autorización vive en el caso de uso (leer owner/admin/recepción;
	// escribir y ver movimientos solo owner/admin).
	Products productshttpapi.ProductHandler
	// Packages son los paquetes y bonos prepagos (F8): el catálogo del salón y,
	// por clienta, comprar, ver el saldo, descontar una sesión y ajustarlo. La
	// autorización vive en el caso de uso.
	Packages packageshttpapi.PackageHandler
	// Loyalty es la fidelidad «cada N visitas, un servicio de regalo» (F9): el
	// programa del salón y, por clienta, la ficha, entregar y usar un vale. La
	// autorización vive en el caso de uso.
	Loyalty loyaltyhttpapi.LoyaltyHandler
	// Audit es el registro de auditoría (F10): el middleware `Audit(action)` que
	// cuelga de las rutas sensibles y `GET /salons/:id/audit-log`. Sin servicio
	// configurado el middleware no hace nada (los routers de los tests unitarios
	// siguen igual). La autorización de la lectura vive en el caso de uso.
	Audit audithttpapi.AuditHandler
	// TwoFactor es el 2FA TOTP opcional (F10, parte B): el segundo paso del login
	// (`POST /auth/login/2fa`, público y bajo el límite por IP de /auth/) y las
	// cuatro rutas de la cuenta propia (`/me/2fa`). Sin 2FA activo el login es el
	// de siempre.
	TwoFactor authhttpapi.TwoFactorHandler
	// Profile es el perfil público del salón (F16, D1) y Gallery su galería de
	// fotos (F16, D3): la autorización vive en el caso de uso.
	Profile settingshttpapi.SalonProfileHandler
	Gallery settingshttpapi.GalleryHandler
	// BookingLinks son las cuatro rutas públicas del enlace firmado de un turno
	// (F1): lookup, horarios, cancelar y reprogramar. Sin sesión y con el token
	// en el cuerpo.
	BookingLinks bookingshttpapi.BookingLinkHandler
	// Waitlist es la lista de espera (F7): el alta pública, la baja por enlace y
	// las dos rutas del personal (listar y quitar). La autorización vive en el
	// caso de uso.
	Waitlist waitlisthttpapi.WaitlistHandler
	// Recovery es F15: recuperar contraseña (olvidé / canjear) y verificar el email
	// (canjear el enlace y reenviarlo). El anti-enumeración y la seguridad del
	// token viven en el caso de uso.
	Recovery authhttpapi.AccountRecoveryHandler
	// DepositPolicy es la política de señas del salón (F11, D3): GET cualquier
	// miembro activo, PUT solo owner/admin.
	DepositPolicy depositshttpapi.DepositPolicyHandler
	// Deposits es la pantalla «Señas» (F11, D10) y la consulta pública del estado
	// de un pago (D8). La autorización vive en el caso de uso.
	Deposits depositshttpapi.DepositHandler
	// Reviews son las reseñas posvisita (F13): las dos rutas públicas del enlace
	// (lookup y envío), la lista del panel, su configuración y ocultar/mostrar.
	// La autorización, las validaciones y la paginación viven en el caso de uso.
	Reviews reviewshttpapi.ReviewHandler
	// PublicReviewLimit limita por IP las dos rutas públicas del enlace (el
	// token es una credencial: un GET revela salón, servicio y fecha). nil = sin
	// límite (tests unitarios del router).
	PublicReviewLimit gin.HandlerFunc
	// PaymentsCallback es el POST público y firmado del banco (F11, D7). nil = sin
	// banco configurado: la ruta NO existe y responde 404 (D12).
	PaymentsCallback gin.HandlerFunc
	// SimbankLookup y SimbankPay son las dos rutas públicas del banco simulado
	// (F11, D6). nil = sin banco: no existen (D12).
	SimbankLookup gin.HandlerFunc
	SimbankPay    gin.HandlerFunc
	// SimbankLimit limita por IP las rutas del banco (cubo propio). nil = sin
	// límite (tests unitarios del router).
	SimbankLimit gin.HandlerFunc
	// PaymentCallbackLimit limita por IP el callback del banco (cubo propio: un
	// reintento del banco no puede gastar el cupo de otra ruta).
	PaymentCallbackLimit gin.HandlerFunc
	// PublicDepositLookupLimit limita por IP la consulta pública del pago (cubo
	// propio, como el resto de las escrituras sin sesión).
	PublicDepositLookupLimit gin.HandlerFunc
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
	// SessionGate es la compuerta de revocación de sesiones de F15 (D4): con ella,
	// un token emitido antes del último cambio de contraseña da 401. nil = sin
	// compuerta (los routers de las pruebas que no la cablean no cambian).
	SessionGate ports.SessionGate
}

// auditFunc es el middleware `Audit(action)` de F10: envuelve una ruta y escribe
// el evento DESPUÉS de un 2xx. Cada `routes_<funcionalidad>.go` lo recibe ya
// resuelto desde Deps.
type auditFunc = func(action string) gin.HandlerFunc

// RegisterRoutes monta /api/v1. Las protegidas cuelgan del middleware.
//
// Las rutas públicas (auth, vidriera del salón, foto por capability, WS) no
// llevan Bearer; el resto sí.
func RegisterRoutes(r *gin.Engine, d Deps) {
	v1 := r.Group("/api/v1")
	// Públicas (sin sesión). Las escrituras van bajo rate limit por IP.
	registerAuthPublicRoutes(v1, d)
	registerPublicSalonRoutes(v1, d)
	registerBookingsPublicRoutes(v1, d)
	// F16: la imagen de la galería se sirve SIN firma (pública por diseño).
	registerPublicGalleryRoutes(v1, d)
	// F11: banco simulado (D6), callback del banco (D7) y estado público del pago
	// (D8). Sin banco configurado las dos primeras NO existen (D12).
	registerSimbankPublicRoutes(v1, d)
	registerPaymentsCallbackRoute(v1, d)
	registerDepositsPublicRoutes(v1, d)
	// F13: el enlace de la reseña (D4).
	registerReviewsPublicRoutes(v1, d)
	// F7: alta y baja de la lista de espera.
	registerWaitlistPublicRoutes(v1, d)
	// F2g1: lookup y canje públicos de una invitación.
	registerInvitationsPublicRoutes(v1, d)
	// F1: el enlace firmado de un solo uso del turno.
	registerBookingLinksPublicRoutes(v1, d)

	authed := v1.Group("", AuthMiddleware(d.TokenIssuer, d.SessionGate))
	// F10: `audit("<acción>")` registra la acción DESPUÉS de que la ruta responda
	// 2xx (nunca cambia status, cuerpo ni errores de la ruta). Solo las rutas de
	// la lista cerrada de D2 lo llevan; la ruta de POS (ventas) NO se audita.
	audit := d.Audit.Audit
	registerMeRoutes(authed, d)
	registerTenantRoutes(authed, d)
	// Agenda
	registerBookingsRoutes(authed, d)
	// Configuración operativa del salón (zona horaria, horario, paso)
	registerSettingsRoutes(authed, d, audit)
	// Equipo del salón: miembros, invitaciones y el select de profesional.
	registerMemberRoutes(authed, d, audit)
	registerInvitationRoutes(authed, d, audit)
	// Horarios, ausencias y cobertura.
	registerHoursRoutes(authed, d)
	registerTimeOffRoutes(authed, d)
	registerAbsenceRoutes(authed, d)
	registerStaffRoutes(authed, d)
	registerResourceRoutes(authed, d)
	// F16: perfil público del salón y su galería de fotos.
	registerProfileGalleryRoutes(r, authed, d, audit)
	// Catálogo de servicios
	registerServiceRoutes(authed, d)
	registerProductRoutes(authed, d, audit)
	registerPackageRoutes(authed, d)
	registerLoyaltyRoutes(authed, d, audit)
	registerWaitlistRoutes(authed, d)
	// POS: ventas, cierre de caja y panel del día
	registerPOSRoutes(authed, d, audit)
	registerPayrollRoutes(authed, d, audit)
	registerFinanceRoutes(authed, d, audit)
	registerExportRoutes(authed, d, audit)
	registerAuditRoutes(authed, d)
	registerDepositRoutes(authed, d, audit)
	registerReviewRoutes(authed, d, audit)
	registerNotificationRoutes(authed, d)
	registerClientRoutes(r, authed, v1, d)
	// Realtime: el navegador tampoco puede mandar Authorization en un WS, así
	// que el token viaja por query param y el hub lo valida antes del upgrade.
	registerRealtimeRoutes(v1, d)
}
