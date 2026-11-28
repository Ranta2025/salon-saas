// wire.go: el estado del cableado. Un único struct de datos (`wiring`) junta lo
// que los `wire_<funcionalidad>.go` construyen, para que buildRouter solo llame
// funciones en orden y arme `Deps` al final. Acá no hay lógica: solo campos.
package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"salon-saas/internal/adapters/mail"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/audit"
	auditpostgres "salon-saas/internal/audit/postgres"
	"salon-saas/internal/auth"
	authpostgres "salon-saas/internal/auth/postgres"
	"salon-saas/internal/auth/security"
	"salon-saas/internal/bookings"
	bookingspostgres "salon-saas/internal/bookings/postgres"
	"salon-saas/internal/clients"
	clientspostgres "salon-saas/internal/clients/postgres"
	"salon-saas/internal/deposits"
	depositspostgres "salon-saas/internal/deposits/postgres"
	"salon-saas/internal/exports"
	exportsstore "salon-saas/internal/exports/postgres"
	"salon-saas/internal/finance"
	financestore "salon-saas/internal/finance/postgres"
	"salon-saas/internal/loyalty"
	loyaltystore "salon-saas/internal/loyalty/postgres"
	notifsvc "salon-saas/internal/notifications"
	"salon-saas/internal/packages"
	packagespostgres "salon-saas/internal/packages/postgres"
	"salon-saas/internal/payroll"
	payrollpostgres "salon-saas/internal/payroll/postgres"
	"salon-saas/internal/ports"
	"salon-saas/internal/pos"
	pospostgres "salon-saas/internal/pos/postgres"
	"salon-saas/internal/products"
	productspostgres "salon-saas/internal/products/postgres"
	"salon-saas/internal/reviews"
	reviewsstore "salon-saas/internal/reviews/postgres"
	"salon-saas/internal/schedules"
	schedulespostgres "salon-saas/internal/schedules/postgres"
	"salon-saas/internal/settings"
	settingspostgres "salon-saas/internal/settings/postgres"
	"salon-saas/internal/simbank"
	"salon-saas/internal/staffing"
	staffingpostgres "salon-saas/internal/staffing/postgres"
	"salon-saas/internal/team"
	teamstore "salon-saas/internal/team/postgres"
	"salon-saas/internal/waitlist"
	waitliststore "salon-saas/internal/waitlist/postgres"

	"github.com/gin-gonic/gin"
)

// wiring junta todo lo construido por los `wire_*.go`. Es un struct de datos:
// los `wire_*` escriben sus campos y buildRouter arma `Deps` con ellos.
type wiring struct {
	cfg  config
	pool *pgxpool.Pool

	// Núcleo: usuarios, salones y memberships los comparten casi todos los casos
	// de uso; hasher y tokens son los primitivos de seguridad del borde.
	users  pgstore.Users
	salons pgstore.Salons
	mems   pgstore.Memberships
	hasher security.BcryptHasher
	tokens security.JWTIssuer

	// Autenticación y sesión (F10b, F15).
	recoveryRepo authpostgres.AccountRecovery
	sessionGate  authpostgres.SessionGate
	authSvc      auth.AuthService
	twoFactorSvc auth.TwoFactorService
	recoverySvc  auth.AccountRecoveryService
	verifSvc     auth.EmailVerificationService
	meSvc        auth.MeService

	// Equipo e invitaciones.
	tenSvc              team.TenantService
	invitationRepo      teamstore.Invitations
	invitationSvc       team.InvitationService
	invitationPublicSvc team.InvitationPublicService
	staffSvc            team.StaffService
	memberSvc           team.MemberService

	// Notificaciones y tiempo real (el hub es el EventPublisher).
	hub      *realtime.Hub
	notifSvc notifsvc.NotificationService

	// Media: adjuntos (S3 o filesystem local) y la firma de sus URLs.
	store       ports.PhotoStorage
	photoSigner security.PhotoSigner

	// Email a clientes (T12): nil si no hay SMTP configurado.
	mailSender mail.Sender
	mailer     ports.MailSender

	// Agenda, disponibilidad, enlaces públicos y recordatorios.
	bookingRepo      bookingspostgres.Bookings
	bookSvc          bookings.BookingService
	availabilitySvc  bookings.AvailabilityService
	bookingEmails    bookingspostgres.BookingEmails
	bookingLinks     bookingspostgres.BookingLinks
	reminders        bookings.ReminderService
	bookingLinkSvc   bookings.BookingLinkService
	publicBookingSvc bookings.PublicBookingService

	// Catálogo de servicios, profesionales y equipamiento.
	servicesRepo  staffingpostgres.Services
	professionals staffingpostgres.ProfessionalServices
	resourceRepo  staffingpostgres.Resources
	svcSvc        staffing.ServiceService
	resourceSvc   staffing.ResourceService

	// Horarios, ausencias y cobertura.
	weeklyHours          schedulespostgres.WeeklyHours
	timeOffRepo          schedulespostgres.TimeOff
	salonHoursSvc        schedules.SalonHoursService
	professionalHoursSvc schedules.ProfessionalHoursService
	timeOffSvc           schedules.TimeOffService
	coverageSvc          schedules.CoverageService

	// Clientes, fichas e historial.
	clientLinks       clientspostgres.ClientLinks
	clientHistoryRepo clientspostgres.ClientHistory
	clientsRepo       clientspostgres.Clients
	clientSvc         clients.ClientService
	clientHistorySvc  clients.ClientHistoryService

	// Ajustes, perfil público del salón y galería.
	salonProfileRepo settingspostgres.SalonProfiles
	salonGalleryRepo settingspostgres.SalonGallery
	settingsSvc      settings.SettingsService
	salonProfileSvc  settings.SalonProfileService
	gallerySvc       settings.GalleryService
	publicSvc        settings.PublicSalonService

	// POS: ventas, productos, cierres de caja y panel del día.
	salesRepo        pospostgres.Sales
	saleProductsRepo pospostgres.SaleProducts
	closesRepo       pospostgres.CashCloses
	saleSvc          pos.SaleService
	cashCloseSvc     pos.CashCloseService
	dashSvc          pos.DashboardService

	// Productos e inventario.
	productsRepo productspostgres.Products
	productSvc   products.ProductService

	// Paquetes y bonos prepagos.
	packagesRepo       packagespostgres.Packages
	clientPackagesRepo packagespostgres.ClientPackages
	packageSvc         packages.PackageService

	// Fidelidad.
	loyaltyProgramsRepo loyaltystore.LoyaltyPrograms
	loyaltyRewardsRepo  loyaltystore.LoyaltyRewards
	loyaltySvc          loyalty.LoyaltyService

	// Auditoría.
	auditRepo auditpostgres.Audit
	auditSvc  audit.AuditService

	// Nómina y comisiones.
	payModelsRepo    payrollpostgres.PayModels
	payrollSalesRepo payrollpostgres.PayrollSales
	payrollRunsRepo  payrollpostgres.PayrollRuns
	payModelSvc      payroll.PayModelService
	payrollSvc       payroll.PayrollService
	payrollRunSvc    payroll.PayrollRunService

	// Gastos y ganancia neta.
	expenseRepo financestore.Expenses
	profitRepo  financestore.Profit
	expenseSvc  finance.ExpenseService
	profitSvc   finance.ProfitService

	// Exportes CSV.
	exportRepo exportsstore.Exports
	exportSvc  exports.ExportService

	// Lista de espera.
	waitlistRepo waitliststore.Waitlist
	waitlistSvc  waitlist.WaitlistService

	// Reseñas posvisita.
	reviewsRepo reviewsstore.Reviews
	reviewsSvc  reviews.Service

	// Señas online y el banco simulado.
	depositPoliciesRepo depositspostgres.DepositPolicies
	depositRepo         depositspostgres.Deposits
	gateway             ports.PaymentGateway
	simbankHandler      *simbank.Handler
	depositsSvc         deposits.DepositService
	depositPolicySvc    deposits.DepositPolicyService
	simbankLookup       gin.HandlerFunc
	simbankPay          gin.HandlerFunc
	paymentsCallback    gin.HandlerFunc
}

// newWiring construye el núcleo compartido: el pool, los tres repositorios de
// identidad y los primitivos de seguridad (bcrypt y JWT). Todo lo demás lo
// agregan los `wire_*.go`.
func newWiring(cfg config, pool *pgxpool.Pool) *wiring {
	w := &wiring{cfg: cfg, pool: pool}
	w.users = pgstore.Users{Pool: pool}
	w.salons = pgstore.Salons{Pool: pool}
	w.mems = pgstore.Memberships{Pool: pool}
	w.hasher = security.BcryptHasher{}
	w.tokens = security.JWTIssuer{Secret: cfg.JWTSecret, TTL: 0}
	return w
}
