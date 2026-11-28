package main

import (
	"time"

	httpapi "salon-saas/internal/adapters/http"
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
	poshttpapi "salon-saas/internal/pos/httpapi"
	productshttpapi "salon-saas/internal/products/httpapi"
	reviewshttpapi "salon-saas/internal/reviews/httpapi"
	scheduleshttpapi "salon-saas/internal/schedules/httpapi"
	settingshttpapi "salon-saas/internal/settings/httpapi"
	staffinghttpapi "salon-saas/internal/staffing/httpapi"
	teamhttpapi "salon-saas/internal/team/httpapi"
	waitlisthttpapi "salon-saas/internal/waitlist/httpapi"
)

// buildDeps arma `httpapi.Deps`: un handler por funcionalidad. Todo lo que
// cruza este borde son interfaces o handlers ya construidos; el paquete HTTP no
// conoce adaptadores concretos (DIP hasta el borde).
func buildDeps(w *wiring) httpapi.Deps {
	return httpapi.Deps{
		Auth:          authhttpapi.AuthHandler{Auth: w.authSvc, Verification: w.verifSvc},
		TwoFactor:     authhttpapi.TwoFactorHandler{Service: w.twoFactorSvc},
		Tenants:       teamhttpapi.TenantsHandler{Tenants: w.tenSvc, Memberships: w.mems},
		Bookings:      bookingshttpapi.BookingHandler{Bookings: w.bookSvc, Memberships: w.mems},
		Services:      staffinghttpapi.ServiceHandler{Services: w.svcSvc, Memberships: w.mems},
		Sales:         poshttpapi.SaleHandler{Sales: w.saleSvc, Memberships: w.mems},
		CashCloses:    poshttpapi.CashCloseHandler{CashCloses: w.cashCloseSvc, Memberships: w.mems},
		Dashboard:     poshttpapi.DashboardHandler{Dashboard: w.dashSvc, Memberships: w.mems},
		Notifications: notificationshttpapi.NotificationHandler{Notifications: w.notifSvc, Memberships: w.mems},
		Clients:       clientshttpapi.ClientHandler{Clients: w.clientSvc, Memberships: w.mems, Photos: w.photoSigner, History: w.clientHistorySvc},
		Me:            authhttpapi.MeHandler{Me: w.meSvc},
		Staff:         teamhttpapi.StaffHandler{Staff: w.staffSvc, Memberships: w.mems},
		Public:        bookingshttpapi.PublicHandler{Avail: w.availabilitySvc, Bookings: w.publicBookingSvc},
		PublicSalon:   settingshttpapi.PublicSalonHandler{Public: w.publicSvc},
		BookingLinks:  bookingshttpapi.BookingLinkHandler{Links: w.bookingLinkSvc},
		Settings:      settingshttpapi.SettingsHandler{Settings: w.settingsSvc, Memberships: w.mems},
		Members:       teamhttpapi.MemberHandler{Members: w.memberSvc, Memberships: w.mems},
		Hours: scheduleshttpapi.HoursHandler{
			Salon: w.salonHoursSvc, Professional: w.professionalHoursSvc, Memberships: w.mems,
		},
		TimeOff:   scheduleshttpapi.TimeOffHandler{TimeOff: w.timeOffSvc, Memberships: w.mems},
		Absences:  scheduleshttpapi.AbsenceHandler{Coverage: w.coverageSvc, Memberships: w.mems},
		Resources: staffinghttpapi.ResourceHandler{Resources: w.resourceSvc, Memberships: w.mems},
		Invitations: teamhttpapi.InvitationHandler{
			Invitations: w.invitationSvc, Public: w.invitationPublicSvc, Memberships: w.mems,
		},
		Payroll: payrollhttpapi.PayrollHandler{PayModels: w.payModelSvc, Payroll: w.payrollSvc, Memberships: w.mems},
		PayrollRuns: payrollhttpapi.PayrollRunHandler{
			Runs: w.payrollRunSvc, Memberships: w.mems,
		},
		Expenses: financehttpapi.ExpenseHandler{Expenses: w.expenseSvc, Memberships: w.mems},
		Profit:   financehttpapi.ProfitHandler{Profit: w.profitSvc, Memberships: w.mems},
		Exports:  exportshttpapi.ExportHandler{Exports: w.exportSvc, Memberships: w.mems},
		// F6: productos e inventario del POS (cinco rutas + `products` en POST /sales).
		Products: productshttpapi.ProductHandler{Products: w.productSvc, Memberships: w.mems},
		// F8: paquetes y bonos prepagos (ocho rutas: catálogo y por clienta).
		Packages: packageshttpapi.PackageHandler{Packages: w.packageSvc, Memberships: w.mems},
		// F9: fidelidad (cinco rutas: programa del salón; ficha, entrega y uso por clienta).
		Loyalty: loyaltyhttpapi.LoyaltyHandler{Loyalty: w.loyaltySvc, Memberships: w.mems},
		// F13: reseñas posvisita (el enlace público y las cinco rutas del panel).
		Reviews: reviewshttpapi.ReviewHandler{Service: w.reviewsSvc, Memberships: w.mems},
		// F10: auditoría (el middleware de las rutas de D2 y GET audit-log).
		Audit: audithttpapi.AuditHandler{Service: w.auditSvc, Memberships: w.mems},
		// F16: perfil público del salón y su galería de fotos.
		Profile: settingshttpapi.SalonProfileHandler{Profile: w.salonProfileSvc, Memberships: w.mems},
		Gallery: settingshttpapi.GalleryHandler{Gallery: w.gallerySvc, Memberships: w.mems},
		// F7: lista de espera (alta pública, baja por enlace y las dos rutas del
		// personal). El barrido periódico se arranca en main con el mismo
		// servicio (deps.Waitlist.Service).
		Waitlist: waitlisthttpapi.WaitlistHandler{Service: w.waitlistSvc, Memberships: w.mems},
		// F11: la política de señas (D3) y la pantalla «Señas» (D10); el callback
		// del banco (D7) y sus dos rutas (D6) existen SOLO con PAYMENT_SIM_SECRET
		// (D12: sin secreto responden 404, porque no se montan).
		DepositPolicy:    depositshttpapi.DepositPolicyHandler{Policy: w.depositPolicySvc, Memberships: w.mems},
		Deposits:         depositshttpapi.DepositHandler{Deposits: w.depositsSvc, Memberships: w.mems},
		PaymentsCallback: w.paymentsCallback,
		SimbankLookup:    w.simbankLookup,
		SimbankPay:       w.simbankPay,
		// Cada ruta nueva tiene SU PROPIO cubo por IP, como el resto de las
		// escrituras sin sesión: una inundación de una no gasta el cupo de otra.
		SimbankLimit:         httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/simbank/"),
		PaymentCallbackLimit: httpapi.RateLimitFor(paymentCallbackRateLimit, time.Minute, "POST", "/api/v1/payments/"),
		// La consulta pública del pago la usa el sondeo de /pago-resultado (cada
		// 3 s hasta 60 s = 20 pedidos por persona): con el cubo de /auth (20/min)
		// el último sondeo quedaría afuera, así que tiene su propio cubo más
		// holgado (60/min por IP).
		PublicDepositLookupLimit: httpapi.RateLimitFor(publicDepositLookupLimit, time.Minute, "POST", "/api/v1/public/deposits/"),
		// F15: recuperar contraseña y verificar email (tres rutas públicas bajo
		// /auth/ y el reenvío autenticado).
		Recovery: authhttpapi.AccountRecoveryHandler{Recovery: w.recoverySvc, Verif: w.verifSvc},
		// La reserva pública es la única escritura sin auth: rate limit por IP
		// como en /auth (el GET de disponibilidad no pasa por acá).
		PublicBookingLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/public/salons/"),
		// F2g1: el lookup y el canje de una invitación son las otras escrituras
		// sin sesión; van bajo el mismo límite por IP que /auth.
		PublicInvitationLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/public/invitations/"),
		// F7/D5: la baja de la lista de espera tiene SU PROPIO cubo (20/min por
		// IP), como el de los enlaces de turno; el alta comparte
		// PublicBookingLimit (D4).
		PublicWaitlistLeaveLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/public/waitlist/"),
		// F1/D7: el enlace del turno tiene su PROPIO cubo (20/min por IP): una
		// inundación de enlaces no puede gastar el cupo de la reserva pública ni el
		// de las invitaciones, ni al revés.
		PublicBookingLinkLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/public/booking-links/"),
		// F13/D4: el enlace de la reseña es una credencial (revela salón, servicio
		// y fecha) y su POST es una escritura sin sesión: cubo PROPIO por IP para
		// las dos, como el del enlace de turno.
		PublicReviewLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "", "/api/v1/public/reviews/"),
		WS:                w.hub.ServeWS,
		TokenIssuer:       w.tokens,
		SessionGate:       w.sessionGate,
	}
}
