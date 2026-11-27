// Command api: composition root. Aquí se CONSTRUYE todo (Factory):
// pool -> repos pg -> hasher/jwt -> casos de uso -> handlers -> rutas.
// main es el único lugar que conoce a todos los adaptadores concretos.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/imaging"
	"salon-saas/internal/adapters/mail"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/adapters/storage"
	"salon-saas/internal/audit"
	audithttpapi "salon-saas/internal/audit/httpapi"
	"salon-saas/internal/auth"
	authhttpapi "salon-saas/internal/auth/httpapi"
	"salon-saas/internal/auth/security"
	"salon-saas/internal/bookings"
	"salon-saas/internal/clients"
	clientshttpapi "salon-saas/internal/clients/httpapi"
	"salon-saas/internal/deposits"
	"salon-saas/internal/domain"
	"salon-saas/internal/exports"
	exportshttpapi "salon-saas/internal/exports/httpapi"
	exportsstore "salon-saas/internal/exports/postgres"
	"salon-saas/internal/finance"
	financehttpapi "salon-saas/internal/finance/httpapi"
	financestore "salon-saas/internal/finance/postgres"
	"salon-saas/internal/loyalty"
	loyaltyhttpapi "salon-saas/internal/loyalty/httpapi"
	loyaltystore "salon-saas/internal/loyalty/postgres"
	notifsvc "salon-saas/internal/notifications"
	notificationshttpapi "salon-saas/internal/notifications/httpapi"
	notifstore "salon-saas/internal/notifications/postgres"
	"salon-saas/internal/packages"
	packageshttpapi "salon-saas/internal/packages/httpapi"
	"salon-saas/internal/payroll"
	payrollhttpapi "salon-saas/internal/payroll/httpapi"
	"salon-saas/internal/ports"
	"salon-saas/internal/pos"
	poshttpapi "salon-saas/internal/pos/httpapi"
	"salon-saas/internal/products"
	productshttpapi "salon-saas/internal/products/httpapi"
	"salon-saas/internal/reviews"
	reviewshttpapi "salon-saas/internal/reviews/httpapi"
	reviewsstore "salon-saas/internal/reviews/postgres"
	"salon-saas/internal/schedules"
	scheduleshttpapi "salon-saas/internal/schedules/httpapi"
	"salon-saas/internal/settings"
	settingshttpapi "salon-saas/internal/settings/httpapi"
	"salon-saas/internal/simbank"
	"salon-saas/internal/staffing"
	staffinghttpapi "salon-saas/internal/staffing/httpapi"
	"salon-saas/internal/team"
	teamhttpapi "salon-saas/internal/team/httpapi"
	teamstore "salon-saas/internal/team/postgres"
	"salon-saas/internal/waitlist"
	waitlisthttpapi "salon-saas/internal/waitlist/httpapi"
	waitliststore "salon-saas/internal/waitlist/postgres"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Timeouts del servidor (B-11): sin ellos un cliente lento ocupa una conexión
// para siempre (slowloris) y el proceso se queda sin file descriptors.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 15 * time.Second
	// maxJSONBody: un payload de esta API no llega ni cerca. El multipart de
	// fotos tiene su propio tope (8 MB) en el handler.
	maxJSONBody = 1 << 20
	// authRateLimit: intentos por IP por minuto en login y registro.
	authRateLimit = 20
	// publicDepositLookupLimit: sondeos por IP por minuto del estado público de
	// un pago (F11, D8): /pago-resultado consulta cada 3 s hasta 60 s.
	publicDepositLookupLimit = 60
	// paymentCallbackRateLimit: entregas del callback del banco por IP por minuto
	// (F11, D7). NO usa el cubo de /auth (20/min): el banco simulado entrega desde
	// 127.0.0.1 con 3 intentos por pago, así que 20/min se agota con ~7 pagos en
	// un minuto y un callback rechazado deja el cobro decidido en el banco y la
	// seña pendiente hasta que vence (dinero perdido de la clienta). La ruta ya
	// está autenticada por la firma HMAC: el límite es solo antiinundación.
	paymentCallbackRateLimit = 120
)

func main() {
	cfg := mustLoadConfig()
	if cfg.GinMode == "" {
		gin.SetMode(gin.ReleaseMode) // producción por defecto; debug se pide explícito
	} else {
		gin.SetMode(cfg.GinMode)
	}

	ctx := context.Background()
	poolCfg, err := pgstore.PoolConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("db no responde: %v", err)
	}

	// Migraciones con lock global (B-10): dos instancias arrancando a la vez no
	// se pisan; la segunda encuentra todo aplicado en schema_migrations.
	dir, err := pgstore.MigrationDir()
	if err != nil {
		log.Fatal(err)
	}
	if err := pgstore.Migrate(ctx, pool, dir); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	router, deps, reminders := buildRouter(cfg, pool)
	log.Printf("media: %s", cfg.MediaDir)
	log.Printf("storage: s3 endpoint=%q bucket=%q ssl=%v", cfg.S3Endpoint, cfg.S3Bucket, cfg.S3UseSSL)
	log.Printf("cors: %v", cfg.CORSOrigins)
	// Una línea para saber en qué modo quedó la IP del cliente (rate limit y
	// logs): sin proxies confiables se ignora X-Forwarded-For.
	if len(cfg.TrustedProxies) == 0 {
		log.Print("trusted proxies: ninguno (la IP del cliente es el peer directo; X-Forwarded-For se ignora)")
	} else {
		log.Printf("trusted proxies: %v", cfg.TrustedProxies)
	}
	// Una sola línea al arrancar: con SMTP_HOST vacío no hay correo y la tarea
	// de recordatorios no reclama nada (los deja para un entorno con email).
	if cfg.SMTPHost == "" {
		log.Print("email deshabilitado (SMTP_HOST vacío)")
	} else {
		log.Printf("email: smtp %s:%s tls=%s", cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTLS)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Shutdown elegante: al recibir SIGINT/SIGTERM el server deja de aceptar
	// conexiones, termina las que están en curso y recién ahí se cierra el pool.
	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Recordatorios de turno (T12): la goroutine comparte el contexto del
	// shutdown. Con email deshabilitado RunOnce no hace nada y no reclama.
	go runReminders(stopCtx, reminders, cfg.ReminderInterval, log.Printf)
	// Lista de espera (F7): su propia goroutine, con el mismo contexto del
	// shutdown. WAITLIST_INTERVAL=0 la deshabilita (el contrato lo pide así).
	if cfg.WaitlistInterval > 0 {
		log.Printf("lista de espera: barrido cada %s", cfg.WaitlistInterval)
		go runWaitlist(stopCtx, deps.Waitlist.Service, cfg.WaitlistInterval, log.Printf)
	} else {
		log.Print("lista de espera: deshabilitada (WAITLIST_INTERVAL=0)")
	}
	// Señas (F11/D9/D12): sin banco configurado el trabajo no corre (no se puede
	// cobrar ni devolver nada) y con DEPOSIT_INTERVAL=0 queda deshabilitado.
	switch {
	case cfg.PaymentSimSecret == "":
		log.Print("señas: deshabilitadas (PAYMENT_SIM_SECRET vacío: NoopGateway)")
	case cfg.DepositInterval <= 0:
		log.Print("señas: barrido deshabilitado (DEPOSIT_INTERVAL=0)")
	default:
		log.Printf("señas: barrido cada %s", cfg.DepositInterval)
		go runDeposits(stopCtx, deps.Deposits.Deposits, cfg.DepositInterval, log.Printf)
	}

	// Reseñas posvisita (F13/D3): el barrido pide la reseña de los turnos
	// atendidos. Sin correo configurado no se pide nada (no habría a dónde
	// mandarlo) y con REVIEW_INTERVAL=0 queda deshabilitado.
	switch {
	case deps.Reviews.Service.Mailer == nil:
		log.Print("reseñas: deshabilitadas (SMTP_HOST vacío)")
	case cfg.ReviewInterval <= 0:
		log.Print("reseñas: barrido deshabilitado (REVIEW_INTERVAL=0)")
	default:
		log.Printf("reseñas: barrido cada %s", cfg.ReviewInterval)
		go runReviews(stopCtx, deps.Reviews.Service, cfg.ReviewInterval, log.Printf)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("api escuchando en :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server: %v", err)
		}
	case <-stopCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		log.Print("api detenida")
	}
}

// photoStorage elige el adaptador de fotos por configuración: S3/MinIO si
// S3_ENDPOINT está definido, filesystem local si no. Aislado para poder probar
// la selección sin arrancar el servidor.
func photoStorage(cfg config) (ports.PhotoStorage, error) {
	if cfg.S3Endpoint == "" {
		return storage.Local{Root: cfg.MediaDir}, nil
	}
	return storage.NewS3(storage.S3Config{
		Endpoint:  cfg.S3Endpoint,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		Bucket:    cfg.S3Bucket,
		UseSSL:    cfg.S3UseSSL,
	})
}

// buildRouter arma los adaptadores y monta las rutas. Devuelve el router y las
// dependencias (los tests de humo lo usan).
func buildRouter(cfg config, pool *pgxpool.Pool) (*gin.Engine, httpapi.Deps, bookings.ReminderService) {
	users := pgstore.Users{Pool: pool}
	salons := pgstore.Salons{Pool: pool}
	mems := pgstore.Memberships{Pool: pool}
	hasher := security.BcryptHasher{}
	tokens := security.JWTIssuer{Secret: cfg.JWTSecret, TTL: 0}

	authSvc := auth.AuthService{Users: users, Hasher: hasher, Tokens: tokens}
	// F15: compuerta de revocación de sesiones (D4). Una consulta por petición
	// autenticada (`SELECT password_changed_at FROM users WHERE id=$1`); un fallo
	// falla CERRADO (401). Los repositorios de restablecer/verificar email guardan
	// SOLO el SHA-256 del token (migración 036).
	recoveryRepo := pgstore.AccountRecovery{Pool: pool}
	sessionGate := pgstore.SessionGate{Pool: pool}
	tenSvc := team.TenantService{Salons: salons, Memberships: mems}
	bookingRepo := pgstore.Bookings{Pool: pool}
	salesRepo := pgstore.Sales{Pool: pool}
	// F6: catálogo de productos (con su libro de stock) y la venta CON productos
	// en una sola transacción con tope de tiempo. Una venta sin productos sigue
	// por salesRepo.Save, intacto.
	productsRepo := pgstore.Products{Pool: pool}
	saleProductsRepo := pgstore.SaleProducts{Pool: pool}
	// F8: catálogo de paquetes y paquetes comprados por cada clienta (con su libro
	// de saldo). Comprar escribe la venta, el paquete y el movimiento en UNA
	// transacción; consumir es un UPDATE condicional más su movimiento.
	packagesRepo := pgstore.Packages{Pool: pool}
	clientPackagesRepo := pgstore.ClientPackages{Pool: pool}
	// F9: fidelidad (programa del salón y vales entregados; migración 033).
	loyaltyProgramsRepo := loyaltystore.LoyaltyPrograms{Pool: pool}
	loyaltyRewardsRepo := loyaltystore.LoyaltyRewards{Pool: pool}
	// F10: registro de auditoría (migración 034). Lo escribe el middleware
	// `Audit(action)` de las rutas sensibles; es append-only y de mejor esfuerzo.
	auditRepo := pgstore.Audit{Pool: pool}
	closesRepo := pgstore.CashCloses{Pool: pool}
	servicesRepo := pgstore.Services{Pool: pool}
	// F2c: qué servicios hace cada quien (disponibilidad, reserva pública y
	// pantalla de equipo).
	professionals := pgstore.ProfessionalServices{Pool: pool}
	// F2d1: horario semanal del salón y de cada profesional (sin filas =
	// comportamiento de siempre).
	weeklyHours := pgstore.WeeklyHours{Pool: pool}
	// F2d2: ausencias (vacaciones y días libres puntuales) de cada profesional.
	timeOffRepo := pgstore.TimeOff{Pool: pool}
	// F2e: puestos y equipamiento con capacidad (tipos, unidades y la unidad que
	// ocupa cada turno). Sin backfill: sin tipos el comportamiento es el de antes.
	resourceRepo := pgstore.Resources{Pool: pool}
	// F2g1: invitaciones de equipo. El adaptador es lectura + transacción (el
	// canje es atómico); el token se guarda solo como SHA-256.
	invitationRepo := teamstore.Invitations{Pool: pool}
	// F3.2: modelo de pago por miembro (member_pay, migración 024) y los
	// agregados de comisión por profesional de una ventana.
	payModelsRepo := pgstore.PayModels{Pool: pool}
	payrollSalesRepo := pgstore.PayrollSales{Pool: pool}
	// F3.3: liquidaciones (migración 025), con la transacción y el advisory lock
	// del salón en el mismo adaptador.
	payrollRunsRepo := pgstore.PayrollRuns{Pool: pool}
	// F3b: gastos (migración 026) y el reporte de ganancia neta, que lee ingresos,
	// gastos y nómina PAGADA en UNA sola transacción REPEATABLE READ de lectura.
	expenseRepo := financestore.Expenses{Pool: pool}
	profitRepo := financestore.Profit{Pool: pool}
	// F4: lecturas masivas del export (ventas con el nombre del profesional y la
	// suma de ítems, cierres por closed_at y clientes por nombre), una sentencia
	// SQL por archivo y con LIMIT.
	exportRepo := exportsstore.Exports{Pool: pool}

	// Realtime: el hub es el EventPublisher de los casos de uso (no saben que
	// hay websockets). Autentica el JWT del query param, valida el Origin con la
	// MISMA allowlist de CORS (B-01) y arma una room por salón.
	hub := realtime.NewHub(tokens, mems, realtime.HubOrigins(cfg.CORSOrigins...), realtime.HubSessionGate(sessionGate))
	notifications := notifstore.Notifications{Pool: pool}
	notifSvc := notifsvc.NotificationService{Notifications: notifications, Salons: mems, Events: hub}

	// Adjuntos: S3 si S3_ENDPOINT está definido; si no, filesystem local. El
	// puerto es el mismo (ports.PhotoStorage): cambiar de adapter es esta línea.
	store, err := photoStorage(cfg)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	// Firma de las URLs de fotos (24 h): clave derivada del secreto del proceso.
	photoSigner := security.NewPhotoSigner(cfg.JWTSecret)

	// Settings: los avisos de turno se formatean en la zona horaria del salón.
	// F5: el vínculo de turnos/ventas con la ficha es un UPDATE aparte (D1), y
	// el historial se lee en UNA transacción REPEATABLE READ con tope de 25 s.
	clientLinks := pgstore.ClientLinks{Pool: pool}
	clientHistoryRepo := pgstore.ClientHistory{Pool: pool}
	clientsRepo := pgstore.Clients{Pool: pool}
	bookSvc := bookings.BookingService{
		Bookings: bookingRepo, Memberships: mems, Settings: salons, Events: hub, Notifier: notifSvc,
		Services: servicesRepo, Resources: resourceRepo,
		ClientLinks: clientLinks,
	}
	svcSvc := staffing.ServiceService{
		Services: servicesRepo, Memberships: mems, Professionals: professionals, Resources: resourceRepo,
	}
	resourceSvc := staffing.ResourceService{
		Resources: resourceRepo, Services: servicesRepo, Memberships: mems,
	}
	saleSvc := pos.SaleService{
		Sales: salesRepo, Services: servicesRepo, Memberships: mems, Events: hub,
		Clients: clientsRepo, ClientLinks: clientLinks,
		Products: productsRepo, ProductSales: saleProductsRepo, Notifier: notifSvc,
	}
	// F6: productos e inventario del POS. Leer owner/admin/recepción; escribir
	// solo owner/admin (D7). El aviso de stock bajo sale DESPUÉS del commit.
	productSvc := products.ProductService{
		Products: productsRepo, Memberships: mems, Events: hub, Notifier: notifSvc,
	}
	// F8: paquetes y bonos prepagos. Comprar es una VENTA (el dinero entra hoy);
	// consumir una sesión no lo es. Vender, consumir y leer: owner/admin/recepción;
	// catálogo (crear/editar) y ajustar el saldo: solo owner/admin (D5/D7/D9/D10).
	packageSvc := packages.PackageService{
		Packages: packagesRepo, ClientPackages: clientPackagesRepo, Services: servicesRepo,
		Clients: clientsRepo, Memberships: mems, Settings: salons, Events: hub,
	}
	// F9: fidelidad «cada N visitas, un servicio de regalo». Usar un vale NO es una
	// venta: el POS, la caja y la nómina no se tocan. Leer el programa: cualquier
	// miembro; guardarlo: owner/admin; ficha, entregar y usar: owner/admin/recepción.
	loyaltySvc := loyalty.LoyaltyService{
		Programs: loyaltyProgramsRepo, Rewards: loyaltyRewardsRepo, Services: servicesRepo,
		Clients: clientsRepo, Memberships: mems, Settings: salons,
	}
	// F10: auditoría. Escribir (middleware, tras un 2xx) resuelve el nombre del
	// actor con el repositorio de usuarios; leer el registro es solo owner/admin.
	auditSvc := audit.AuditService{Events: auditRepo, Users: users, Memberships: mems}
	// F10b: 2FA TOTP opcional (migración 035). El secreto se guarda cifrado con una
	// clave derivada de JWT_SECRET (rotarlo obliga a re-enrolar el 2FA). Un usuario
	// sin 2FA activo entra por el login de siempre; con 2FA, el login devuelve un
	// desafío (typ:"2fa", 5 min) que NO es una sesión. Enrolar y desactivar
	// escriben `auth.2fa_enabled` / `auth.2fa_disabled` en la auditoría.
	totpBox, err := domain.NewTOTPSecretBox(cfg.JWTSecret)
	if err != nil {
		log.Fatalf("2fa: %v", err)
	}
	twoFactorSvc := auth.TwoFactorService{
		Repo: pgstore.TwoFactor{Pool: pool}, Users: users, Hasher: hasher, Tokens: tokens,
		Challenges: tokens, Box: totpBox, Memberships: mems, Audit: auditSvc,
		// F15 D4: un desafío emitido antes del último cambio de contraseña no se
		// canjea (el 2FA no se toca, pero el reinicio mata lo que estaba a medias).
		Gate: sessionGate,
	}
	authSvc.TwoFactor = twoFactorSvc
	// Tx: el cierre corre en una transacción con advisory lock por salón (B-09).
	// Settings: la ventana del cierre es el día LOCAL del salón (su zona), no UTC.
	cashCloseSvc := pos.CashCloseService{
		CashCloses: closesRepo, Sales: salesRepo, Memberships: mems, Tx: closesRepo,
		Settings: salons, Events: hub, Notifier: notifSvc,
	}
	dashSvc := pos.DashboardService{Sales: salesRepo, Agenda: bookingRepo, Staff: mems, Memberships: mems}
	clientSvc := clients.ClientService{
		Clients:     clientsRepo,
		Memberships: mems,
		Photos:      store,                                      // filesystem local
		Images:      imaging.WebP{MaxSide: domain.MaxPhotoSide}, // cwebp
	}
	// F16: perfil público del salón (migración 032) y su galería de fotos. La
	// galería reutiliza el MISMO storage y el MISMO conversor a WebP que las fotos
	// de clientas, pero en su propia tabla: la ruta pública nunca sirve una foto
	// de clienta.
	salonProfileRepo := pgstore.SalonProfiles{Pool: pool}
	salonGalleryRepo := pgstore.SalonGallery{Pool: pool}
	salonProfileSvc := settings.SalonProfileService{Profiles: salonProfileRepo, Memberships: mems}
	gallerySvc := settings.GalleryService{
		Gallery: salonGalleryRepo, Memberships: mems, Salons: salons,
		Photos: store, Images: imaging.WebP{MaxSide: domain.MaxPhotoSide},
	}
	clientHistorySvc := clients.ClientHistoryService{
		Clients: clientsRepo, Source: clientHistoryRepo,
		Memberships: mems, Settings: salons,
	}
	meSvc := auth.MeService{Users: users, Salons: salons, Members: mems}
	staffSvc := team.StaffService{Staff: mems, Memberships: mems}
	// F16 (D4): la vidriera sale del panel: perfil, equipo que atiende, galería y
	// paquetes activos, y la carta se filtra con la MISMA regla que la
	// disponibilidad (ListProfessionalsFor).
	publicSvc := settings.PublicSalonService{
		Salons: salons, Services: servicesRepo, Settings: salons, Hours: weeklyHours,
		Profiles: salonProfileRepo, Staff: mems, Professionals: professionals,
		Gallery: salonGalleryRepo, Packages: packagesRepo, Loyalty: loyaltyProgramsRepo,
	}
	settingsSvc := settings.SettingsService{Settings: salons, Memberships: mems}
	salonHoursSvc := schedules.SalonHoursService{Hours: weeklyHours, Settings: salons, Memberships: mems}
	professionalHoursSvc := schedules.ProfessionalHoursService{Hours: weeklyHours, Memberships: mems}
	// F2d2: las ausencias se cargan desde Equipo y las consultan la agenda (con
	// los turnos en conflicto) y la disponibilidad pública.
	timeOffSvc := schedules.TimeOffService{TimeOff: timeOffRepo, Bookings: bookingRepo, Memberships: mems}
	// F2d3: cubrir una ausencia reasignando los turnos a otra profesional. La
	// transacción con lock por salón es lo que hace el "todo o nada".
	coverageSvc := schedules.CoverageService{
		Bookings: bookingRepo, Memberships: mems, Staff: mems, Professionals: professionals,
		Hours: weeklyHours, TimeOff: timeOffRepo, Services: servicesRepo,
		Settings: salons, Salons: salons, Tx: bookingRepo,
		Events: hub, Notifier: notifSvc,
	}
	memberSvc := team.MemberService{
		Members: mems, Memberships: mems, Users: users,
		Professionals: professionals, Services: servicesRepo,
		// F2g2: la baja corre en la transacción de `mems`: desactivar la membresía
		// y revocar las invitaciones pendientes que emitió commitean juntos o no
		// pasan (mismo patrón que las invitaciones de F2g1).
		Tx: mems,
	}
	// F3.2: nómina. El período se deriva en la zona del salón (settings) y la
	// comisión sale de los agregados de ventas; el fijo se paga una vez cuando el
	// tipo de período coincide con el del miembro (D4).
	payModelSvc := payroll.PayModelService{Pay: payModelsRepo, Memberships: mems}
	payrollSvc := payroll.PayrollService{
		Pay: payModelsRepo, Sales: payrollSalesRepo, Memberships: mems, Settings: salons,
	}
	// F3.3: crear congela la vista previa; ajustar, pagar y borrar solo con la
	// corrida abierta (la DB repite la condición status='open').
	payrollRunSvc := payroll.PayrollRunService{
		Runs: payrollRunsRepo, Tx: payrollRunsRepo,
		PayModels: payModelsRepo, Sales: payrollSalesRepo, Memberships: mems, Settings: salons,
	}
	// F3b: gastos operativos (solo owner/administración) y ganancia neta del
	// período en la zona del salón. Las fechas de los gastos se miden contra el
	// día LOCAL del salón, no UTC.
	expenseSvc := finance.ExpenseService{Expenses: expenseRepo, Memberships: mems, Settings: salons}
	profitSvc := finance.ProfitService{Profit: profitRepo, Memberships: mems, Settings: salons}
	// F4: exportación CSV de ventas, cierres de caja y clientes. Solo dueño y
	// administración (la autorización vive en el caso de uso). Las lecturas son
	// métodos nuevos con LIMIT (una sola sentencia por archivo) y el tope de
	// filas queda en el valor del contrato.
	exportSvc := exports.ExportService{
		Exports: exportRepo, Memberships: mems, Settings: salons,
	}
	availabilitySvc := bookings.AvailabilityService{
		Salons: salons, Services: servicesRepo, Settings: salons, Staff: mems,
		Bookings: bookingRepo, Professionals: professionals, Hours: weeklyHours,
		TimeOff: timeOffRepo, Resources: resourceRepo,
	}
	// F7: lista de espera del salón (migración 030). El barrido periódico reclama
	// avisos con el advisory lock `waitlist-write:<salón>` y manda el email fuera
	// del lock; la pantalla del personal lista y da de baja.
	waitlistRepo := waitliststore.Waitlist{Pool: pool}
	waitlistSvc := waitlist.WaitlistService{
		Waitlist: waitlistRepo, Tx: waitlistRepo,
		Salons: salons, Services: servicesRepo, Professionals: professionals,
		Settings: salons, Availability: availabilitySvc, Memberships: mems,
		// El sender y APP_URL se completan abajo, cuando el mailer existe.
		AppURL: cfg.AppURL,
	}
	// F13: reseñas posvisita (migración 038). Un solo adaptador implementa los
	// pedidos de reseña y las reseñas; el módulo SOLO LEE bookingRepo por su propia
	// consulta (D1) y no toca la agenda. El sender y APP_URL se completan abajo,
	// cuando el mailer existe: sin SMTP el barrido no pide nada.
	reviewsRepo := reviewsstore.Reviews{Pool: pool}
	reviewsSvc := reviews.Service{
		Reviews: reviewsRepo, Requests: reviewsRepo, Memberships: mems,
		AppURL: cfg.AppURL,
	}
	// Email a clientes (T12): adaptador SMTP del puerto MailSender. Sin
	// SMTP_HOST el sender queda deshabilitado y los casos de uso lo ven como
	// nil (no hay correo en este entorno).
	mailSender, err := mail.New(mail.Config{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
		Pass: cfg.SMTPPass, From: cfg.SMTPFrom, TLS: cfg.SMTPTLS,
	})
	if err != nil {
		log.Fatalf("email: %v", err)
	}
	var mailer ports.MailSender
	if mailSender.Enabled() {
		mailer = mailSender
	}
	// F15: recuperar contraseña (D2/D3) y verificar el email (D7). TODO el trabajo
	// de un pedido de restablecer corre DESPUÉS de responder, en el runner
	// asíncrono de producción (goroutine con timeout y recover): el tiempo de la
	// respuesta no revela si el email existe. Con el mailer nil (sin SMTP_HOST) no
	// se emite token ni se envía nada.
	recoverySvc := auth.AccountRecoveryService{
		Users: users, Resets: recoveryRepo, Hasher: hasher,
		Mailer: mailer, AppURL: cfg.AppURL,
	}
	verificationSvc := auth.EmailVerificationService{
		Users: users, Verifs: recoveryRepo, Mailer: mailer, AppURL: cfg.AppURL,
	}
	// La cola de recordatorios vive en su propia tabla (014).
	bookingEmails := pgstore.BookingEmails{Pool: pool}
	// F2g1: invitaciones de equipo (el alta manda el correo y devuelve el enlace
	// una sola vez) y el canje público (cuenta nueva + membresía + invitación
	// aceptada en una transacción). Con el mailer nil la invitación existe igual
	// y la respuesta dice email_sent=false.
	invitationSvc := team.InvitationService{
		Invitations: invitationRepo, Tx: invitationRepo, Memberships: mems, Users: users,
		Salons: salons, Settings: salons, Mailer: mailer, AppURL: cfg.AppURL,
	}
	invitationPublicSvc := team.InvitationPublicService{
		Invitations: invitationRepo, Tx: invitationRepo, Users: users, Salons: salons,
		Hasher: hasher, Tokens: tokens,
	}
	// F13: el correo del pedido de reseña sale por el mismo MailSender. Sin email
	// configurado queda nil y el barrido no reclama nada (no se pide una reseña
	// que nunca va a llegar).
	reviewsSvc.Mailer = mailer
	// F7: el aviso de la lista sale por el mismo MailSender. Sin email
	// configurado queda nil y la tarea no reclama nada (D7).
	waitlistSvc.Mailer = mailer
	// F2d3 avisa a la clienta del cambio de turno por el mismo email.
	coverageSvc.Mailer = mailer
	coverageSvc.Emails = bookingEmails
	// F1: el enlace de un solo uso que viaja en la confirmación, el recordatorio y
	// el aviso de cambio. Guarda solo el SHA-256 del token (migración 027).
	bookingLinks := pgstore.BookingLinks{Pool: pool}
	reminders := bookings.ReminderService{
		Emails: bookingEmails, Mailer: mailer, Settings: salons,
		Links: bookingLinks, AppURL: cfg.AppURL,
	}
	// F11: la seña online (migración 037). El banco simulado SOLO se construye con
	// PAYMENT_SIM_SECRET: sin secreto queda el NoopGateway (D12), no se pueden
	// activar señas, las rutas del banco y el callback no se montan y el barrido
	// no corre. Con secreto, el banco es un adaptador más del mismo puerto.
	depositPoliciesRepo := pgstore.DepositPolicies{Pool: pool}
	depositRepo := pgstore.Deposits{Pool: pool}
	var gateway ports.PaymentGateway = ports.NoopGateway{}
	var simbankHandler *simbank.Handler
	if cfg.PaymentSimSecret != "" {
		bank := &simbank.Bank{Pool: pool, Secret: cfg.PaymentSimSecret, CallbackURL: cfg.PaymentCallbackURL}
		gateway = bank
		simbankHandler = &simbank.Handler{Bank: bank}
	}
	// El barrido cancela la reserva por el camino EXISTENTE de la agenda
	// (SetStatus(cancelled) bajo el lock `booking-write:`): no reimplementa turnos.
	depositsSvc := deposits.DepositService{
		Policies: depositPoliciesRepo, Deposits: depositRepo, Gateway: gateway,
		Bookings: pgstore.BookingCanceller{Bookings: bookingRepo},
		Notices:  depositRepo, Mailer: mailer, Links: bookingLinks,
		Settings: salons, Memberships: mems, AppURL: cfg.AppURL,
	}
	depositPolicySvc := deposits.DepositPolicyService{
		Policies: depositPoliciesRepo, Memberships: mems, Gateway: gateway,
	}
	// El payload público (D11) lee la misma política de señas.
	publicSvc.Deposits = depositPoliciesRepo
	// F13/D7: la vidriera pública suma `reviews` (aditivo, omitido si no hay
	// reseñas visibles). El caso de uso es el mismo que atiende el panel: así una
	// reseña oculta desaparece de la web y del promedio.
	publicSvc.Reviews = reviewsSvc
	var simbankLookup, simbankPay, paymentsCallback gin.HandlerFunc
	if simbankHandler != nil {
		simbankLookup, simbankPay = simbankHandler.Lookup, simbankHandler.Pay
		paymentsCallback = httpapi.PaymentsHandler{Deposits: depositsSvc}.Callback
	}
	publicBookingSvc := bookings.PublicBookingService{
		Salons: salons, Services: servicesRepo, Settings: salons, Staff: mems, Bookings: bookingRepo,
		Professionals: professionals, Hours: weeklyHours, TimeOff: timeOffRepo,
		Resources:   resourceRepo, // el turno ocupa una unidad de equipo (F2e)
		Clients:     clientsRepo,  // cada reserva pública deja la ficha de la clienta (E-03)
		ClientLinks: clientLinks,  // y el turno se vincula con ESA ficha (F5, D3-a)
		Events:      hub, Notifier: notifSvc,
		Mailer: mailer, Emails: bookingEmails,
		// F1: un token nuevo por cada email enviado; solo para turnos con email.
		Links: bookingLinks, AppURL: cfg.AppURL,
	}
	// F11/D8: con seña activa la reserva pública crea el cargo y devuelve
	// `payment`; sin seña responde y manda el correo exactamente como hoy.
	publicBookingSvc.Deposits = depositsSvc
	// F1: las cuatro rutas públicas del enlace (lookup, horarios, cancelar y
	// reprogramar). Toma el MISMO lock `booking-write:<salón>` de Book y Update en
	// UNA transacción: dos pedidos con el mismo token no pueden ganar los dos.
	bookingLinkSvc := bookings.BookingLinkService{
		Links: bookingLinks, Tx: bookingLinks,
		Bookings: bookingRepo, Salons: salons, Services: servicesRepo, Staff: mems,
		Settings: salons, Professionals: professionals, Hours: weeklyHours,
		TimeOff: timeOffRepo, Resources: resourceRepo, Availability: availabilitySvc,
		Events: hub, Notifier: notifSvc, Mailer: mailer, Emails: bookingEmails,
		AppURL: cfg.AppURL,
	}

	router := gin.New()
	// Recuperación de panics + log de acceso con token redactado (B-08).
	router.Use(gin.Recovery(), httpapi.RequestLogger(os.Stdout))
	// CORS con allowlist por env (B-01): sin esto el navegador no puede ni
	// loguearse (el preflight daba 404).
	router.Use(httpapi.CORS(cfg.CORSOrigins...))
	// Tope de 1 MB para JSON (B-11). La subida de la galería del salón (F16)
	// queda exenta por RUTA: tiene su propio tope de 8 MB.
	router.Use(httpapi.MaxJSONBodyExceptGalleryUpload(maxJSONBody))
	// Rate limit por IP solo en auth (B-07).
	router.Use(httpapi.RateLimit(authRateLimit, time.Minute, "/api/v1/auth/"))
	// Proxies confiables (H3): la lista sale de TRUSTED_PROXIES (validada al
	// leer la configuración). Vacía = la IP del cliente es el peer directo y
	// X-Forwarded-For se ignora; detrás de un reverse proxy hay que setearla.
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true, "service": "salon-saas"})
	})

	deps := httpapi.Deps{
		Auth:          authhttpapi.AuthHandler{Auth: authSvc, Verification: verificationSvc},
		TwoFactor:     authhttpapi.TwoFactorHandler{Service: twoFactorSvc},
		Tenants:       teamhttpapi.TenantsHandler{Tenants: tenSvc, Memberships: mems},
		Bookings:      httpapi.BookingHandler{Bookings: bookSvc, Memberships: mems},
		Services:      staffinghttpapi.ServiceHandler{Services: svcSvc, Memberships: mems},
		Sales:         poshttpapi.SaleHandler{Sales: saleSvc, Memberships: mems},
		CashCloses:    poshttpapi.CashCloseHandler{CashCloses: cashCloseSvc, Memberships: mems},
		Dashboard:     poshttpapi.DashboardHandler{Dashboard: dashSvc, Memberships: mems},
		Notifications: notificationshttpapi.NotificationHandler{Notifications: notifSvc, Memberships: mems},
		Clients:       clientshttpapi.ClientHandler{Clients: clientSvc, Memberships: mems, Photos: photoSigner, History: clientHistorySvc},
		Me:            authhttpapi.MeHandler{Me: meSvc},
		Staff:         teamhttpapi.StaffHandler{Staff: staffSvc, Memberships: mems},
		Public:        httpapi.PublicHandler{Avail: availabilitySvc, Bookings: publicBookingSvc},
		PublicSalon:   settingshttpapi.PublicSalonHandler{Public: publicSvc},
		BookingLinks:  httpapi.BookingLinkHandler{Links: bookingLinkSvc},
		Settings:      settingshttpapi.SettingsHandler{Settings: settingsSvc, Memberships: mems},
		Members:       teamhttpapi.MemberHandler{Members: memberSvc, Memberships: mems},
		Hours: scheduleshttpapi.HoursHandler{
			Salon: salonHoursSvc, Professional: professionalHoursSvc, Memberships: mems,
		},
		TimeOff:   scheduleshttpapi.TimeOffHandler{TimeOff: timeOffSvc, Memberships: mems},
		Absences:  scheduleshttpapi.AbsenceHandler{Coverage: coverageSvc, Memberships: mems},
		Resources: staffinghttpapi.ResourceHandler{Resources: resourceSvc, Memberships: mems},
		Invitations: teamhttpapi.InvitationHandler{
			Invitations: invitationSvc, Public: invitationPublicSvc, Memberships: mems,
		},
		Payroll: payrollhttpapi.PayrollHandler{PayModels: payModelSvc, Payroll: payrollSvc, Memberships: mems},
		PayrollRuns: payrollhttpapi.PayrollRunHandler{
			Runs: payrollRunSvc, Memberships: mems,
		},
		Expenses: financehttpapi.ExpenseHandler{Expenses: expenseSvc, Memberships: mems},
		Profit:   financehttpapi.ProfitHandler{Profit: profitSvc, Memberships: mems},
		Exports:  exportshttpapi.ExportHandler{Exports: exportSvc, Memberships: mems},
		// F6: productos e inventario del POS (cinco rutas + `products` en POST /sales).
		Products: productshttpapi.ProductHandler{Products: productSvc, Memberships: mems},
		// F8: paquetes y bonos prepagos (ocho rutas: catálogo y por clienta).
		Packages: packageshttpapi.PackageHandler{Packages: packageSvc, Memberships: mems},
		// F9: fidelidad (cinco rutas: programa del salón; ficha, entrega y uso por clienta).
		Loyalty: loyaltyhttpapi.LoyaltyHandler{Loyalty: loyaltySvc, Memberships: mems},
		// F13: reseñas posvisita (el enlace público y las cinco rutas del panel).
		Reviews: reviewshttpapi.ReviewHandler{Service: reviewsSvc, Memberships: mems},
		// F10: auditoría (el middleware de las rutas de D2 y GET audit-log).
		Audit: audithttpapi.AuditHandler{Service: auditSvc, Memberships: mems},
		// F16: perfil público del salón y su galería de fotos.
		Profile: settingshttpapi.SalonProfileHandler{Profile: salonProfileSvc, Memberships: mems},
		Gallery: settingshttpapi.GalleryHandler{Gallery: gallerySvc, Memberships: mems},
		// F7: lista de espera (alta pública, baja por enlace y las dos rutas del
		// personal). El barrido periódico se arranca en main con el mismo
		// servicio (deps.Waitlist.Service).
		Waitlist: waitlisthttpapi.WaitlistHandler{Service: waitlistSvc, Memberships: mems},
		// F11: la política de señas (D3) y la pantalla «Señas» (D10); el callback
		// del banco (D7) y sus dos rutas (D6) existen SOLO con PAYMENT_SIM_SECRET
		// (D12: sin secreto responden 404, porque no se montan).
		DepositPolicy:    httpapi.DepositPolicyHandler{Policy: depositPolicySvc, Memberships: mems},
		Deposits:         httpapi.DepositHandler{Deposits: depositsSvc, Memberships: mems},
		PaymentsCallback: paymentsCallback,
		SimbankLookup:    simbankLookup,
		SimbankPay:       simbankPay,
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
		Recovery: authhttpapi.AccountRecoveryHandler{Recovery: recoverySvc, Verif: verificationSvc},
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
		WS:                hub.ServeWS,
		TokenIssuer:       tokens,
		SessionGate:       sessionGate,
	}
	httpapi.RegisterRoutes(router, deps)
	return router, deps, reminders
}
