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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/imaging"
	"salon-saas/internal/adapters/mail"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/adapters/storage"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
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
	_ = deps

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
func buildRouter(cfg config, pool *pgxpool.Pool) (*gin.Engine, httpapi.Deps, app.ReminderService) {
	users := pgstore.Users{Pool: pool}
	salons := pgstore.Salons{Pool: pool}
	mems := pgstore.Memberships{Pool: pool}
	hasher := security.BcryptHasher{}
	tokens := security.JWTIssuer{Secret: cfg.JWTSecret, TTL: 0}

	authSvc := app.AuthService{Users: users, Hasher: hasher, Tokens: tokens}
	tenSvc := app.TenantService{Salons: salons, Memberships: mems}
	bookings := pgstore.Bookings{Pool: pool}
	salesRepo := pgstore.Sales{Pool: pool}
	// F6: catálogo de productos (con su libro de stock) y la venta CON productos
	// en una sola transacción con tope de tiempo. Una venta sin productos sigue
	// por salesRepo.Save, intacto.
	productsRepo := pgstore.Products{Pool: pool}
	saleProductsRepo := pgstore.SaleProducts{Pool: pool}
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
	invitationRepo := pgstore.Invitations{Pool: pool}
	// F3.2: modelo de pago por miembro (member_pay, migración 024) y los
	// agregados de comisión por profesional de una ventana.
	payModelsRepo := pgstore.PayModels{Pool: pool}
	payrollSalesRepo := pgstore.PayrollSales{Pool: pool}
	// F3.3: liquidaciones (migración 025), con la transacción y el advisory lock
	// del salón en el mismo adaptador.
	payrollRunsRepo := pgstore.PayrollRuns{Pool: pool}
	// F3b: gastos (migración 026) y el reporte de ganancia neta, que lee ingresos,
	// gastos y nómina PAGADA en UNA sola transacción REPEATABLE READ de lectura.
	expenseRepo := pgstore.Expenses{Pool: pool}
	profitRepo := pgstore.Profit{Pool: pool}
	// F4: lecturas masivas del export (ventas con el nombre del profesional y la
	// suma de ítems, cierres por closed_at y clientes por nombre), una sentencia
	// SQL por archivo y con LIMIT.
	exportRepo := pgstore.Exports{Pool: pool}

	// Realtime: el hub es el EventPublisher de los casos de uso (no saben que
	// hay websockets). Autentica el JWT del query param, valida el Origin con la
	// MISMA allowlist de CORS (B-01) y arma una room por salón.
	hub := realtime.NewHub(tokens, mems, realtime.HubOrigins(cfg.CORSOrigins...))
	notifications := pgstore.Notifications{Pool: pool}
	notifSvc := app.NotificationService{Notifications: notifications, Salons: mems, Events: hub}

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
	bookSvc := app.BookingService{
		Bookings: bookings, Memberships: mems, Settings: salons, Events: hub, Notifier: notifSvc,
		Services: servicesRepo, Resources: resourceRepo,
		ClientLinks: clientLinks,
	}
	svcSvc := app.ServiceService{
		Services: servicesRepo, Memberships: mems, Professionals: professionals, Resources: resourceRepo,
	}
	resourceSvc := app.ResourceService{
		Resources: resourceRepo, Services: servicesRepo, Memberships: mems,
	}
	saleSvc := app.SaleService{
		Sales: salesRepo, Services: servicesRepo, Memberships: mems, Events: hub,
		Clients: clientsRepo, ClientLinks: clientLinks,
		Products: productsRepo, ProductSales: saleProductsRepo, Notifier: notifSvc,
	}
	// F6: productos e inventario del POS. Leer owner/admin/recepción; escribir
	// solo owner/admin (D7). El aviso de stock bajo sale DESPUÉS del commit.
	productSvc := app.ProductService{
		Products: productsRepo, Memberships: mems, Events: hub, Notifier: notifSvc,
	}
	// Tx: el cierre corre en una transacción con advisory lock por salón (B-09).
	// Settings: la ventana del cierre es el día LOCAL del salón (su zona), no UTC.
	cashCloseSvc := app.CashCloseService{
		CashCloses: closesRepo, Sales: salesRepo, Memberships: mems, Tx: closesRepo,
		Settings: salons, Events: hub, Notifier: notifSvc,
	}
	dashSvc := app.DashboardService{Sales: salesRepo, Agenda: bookings, Staff: mems, Memberships: mems}
	clientSvc := app.ClientService{
		Clients:     clientsRepo,
		Memberships: mems,
		Photos:      store,                                      // filesystem local
		Images:      imaging.WebP{MaxSide: domain.MaxPhotoSide}, // cwebp
	}
	clientHistorySvc := app.ClientHistoryService{
		Clients: clientsRepo, Source: clientHistoryRepo,
		Memberships: mems, Settings: salons,
	}
	meSvc := app.MeService{Users: users, Salons: salons, Members: mems}
	staffSvc := app.StaffService{Staff: mems, Memberships: mems}
	publicSvc := app.PublicSalonService{
		Salons: salons, Services: servicesRepo, Settings: salons, Hours: weeklyHours,
	}
	settingsSvc := app.SettingsService{Settings: salons, Memberships: mems}
	salonHoursSvc := app.SalonHoursService{Hours: weeklyHours, Settings: salons, Memberships: mems}
	professionalHoursSvc := app.ProfessionalHoursService{Hours: weeklyHours, Memberships: mems}
	// F2d2: las ausencias se cargan desde Equipo y las consultan la agenda (con
	// los turnos en conflicto) y la disponibilidad pública.
	timeOffSvc := app.TimeOffService{TimeOff: timeOffRepo, Bookings: bookings, Memberships: mems}
	// F2d3: cubrir una ausencia reasignando los turnos a otra profesional. La
	// transacción con lock por salón es lo que hace el "todo o nada".
	coverageSvc := app.CoverageService{
		Bookings: bookings, Memberships: mems, Staff: mems, Professionals: professionals,
		Hours: weeklyHours, TimeOff: timeOffRepo, Services: servicesRepo,
		Settings: salons, Salons: salons, Tx: bookings,
		Events: hub, Notifier: notifSvc,
	}
	memberSvc := app.MemberService{
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
	payModelSvc := app.PayModelService{Pay: payModelsRepo, Memberships: mems}
	payrollSvc := app.PayrollService{
		Pay: payModelsRepo, Sales: payrollSalesRepo, Memberships: mems, Settings: salons,
	}
	// F3.3: crear congela la vista previa; ajustar, pagar y borrar solo con la
	// corrida abierta (la DB repite la condición status='open').
	payrollRunSvc := app.PayrollRunService{
		Runs: payrollRunsRepo, Tx: payrollRunsRepo,
		PayModels: payModelsRepo, Sales: payrollSalesRepo, Memberships: mems, Settings: salons,
	}
	// F3b: gastos operativos (solo owner/administración) y ganancia neta del
	// período en la zona del salón. Las fechas de los gastos se miden contra el
	// día LOCAL del salón, no UTC.
	expenseSvc := app.ExpenseService{Expenses: expenseRepo, Memberships: mems, Settings: salons}
	profitSvc := app.ProfitService{Profit: profitRepo, Memberships: mems, Settings: salons}
	// F4: exportación CSV de ventas, cierres de caja y clientes. Solo dueño y
	// administración (la autorización vive en el caso de uso). Las lecturas son
	// métodos nuevos con LIMIT (una sola sentencia por archivo) y el tope de
	// filas queda en el valor del contrato.
	exportSvc := app.ExportService{
		Exports: exportRepo, Memberships: mems, Settings: salons,
	}
	availabilitySvc := app.AvailabilityService{
		Salons: salons, Services: servicesRepo, Settings: salons, Staff: mems,
		Bookings: bookings, Professionals: professionals, Hours: weeklyHours,
		TimeOff: timeOffRepo, Resources: resourceRepo,
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
	// La cola de recordatorios vive en su propia tabla (014).
	bookingEmails := pgstore.BookingEmails{Pool: pool}
	// F2g1: invitaciones de equipo (el alta manda el correo y devuelve el enlace
	// una sola vez) y el canje público (cuenta nueva + membresía + invitación
	// aceptada en una transacción). Con el mailer nil la invitación existe igual
	// y la respuesta dice email_sent=false.
	invitationSvc := app.InvitationService{
		Invitations: invitationRepo, Tx: invitationRepo, Memberships: mems, Users: users,
		Salons: salons, Settings: salons, Mailer: mailer, AppURL: cfg.AppURL,
	}
	invitationPublicSvc := app.InvitationPublicService{
		Invitations: invitationRepo, Tx: invitationRepo, Users: users, Salons: salons,
		Hasher: hasher, Tokens: tokens,
	}
	// F2d3 avisa a la clienta del cambio de turno por el mismo email.
	coverageSvc.Mailer = mailer
	coverageSvc.Emails = bookingEmails
	// F1: el enlace de un solo uso que viaja en la confirmación, el recordatorio y
	// el aviso de cambio. Guarda solo el SHA-256 del token (migración 027).
	bookingLinks := pgstore.BookingLinks{Pool: pool}
	reminders := app.ReminderService{
		Emails: bookingEmails, Mailer: mailer, Settings: salons,
		Links: bookingLinks, AppURL: cfg.AppURL,
	}
	publicBookingSvc := app.PublicBookingService{
		Salons: salons, Services: servicesRepo, Settings: salons, Staff: mems, Bookings: bookings,
		Professionals: professionals, Hours: weeklyHours, TimeOff: timeOffRepo,
		Resources:   resourceRepo, // el turno ocupa una unidad de equipo (F2e)
		Clients:     clientsRepo,  // cada reserva pública deja la ficha de la clienta (E-03)
		ClientLinks: clientLinks,  // y el turno se vincula con ESA ficha (F5, D3-a)
		Events:      hub, Notifier: notifSvc,
		Mailer: mailer, Emails: bookingEmails,
		// F1: un token nuevo por cada email enviado; solo para turnos con email.
		Links: bookingLinks, AppURL: cfg.AppURL,
	}
	// F1: las cuatro rutas públicas del enlace (lookup, horarios, cancelar y
	// reprogramar). Toma el MISMO lock `booking-write:<salón>` de Book y Update en
	// UNA transacción: dos pedidos con el mismo token no pueden ganar los dos.
	bookingLinkSvc := app.BookingLinkService{
		Links: bookingLinks, Tx: bookingLinks,
		Bookings: bookings, Salons: salons, Services: servicesRepo, Staff: mems,
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
	// Tope de 1 MB para JSON (B-11).
	router.Use(httpapi.MaxJSONBody(maxJSONBody))
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
		Auth:          httpapi.AuthHandler{Auth: authSvc},
		Tenants:       httpapi.TenantsHandler{Tenants: tenSvc, Memberships: mems},
		Bookings:      httpapi.BookingHandler{Bookings: bookSvc, Memberships: mems},
		Services:      httpapi.ServiceHandler{Services: svcSvc, Memberships: mems},
		Sales:         httpapi.SaleHandler{Sales: saleSvc, Memberships: mems},
		CashCloses:    httpapi.CashCloseHandler{CashCloses: cashCloseSvc, Memberships: mems},
		Dashboard:     httpapi.DashboardHandler{Dashboard: dashSvc, Memberships: mems},
		Notifications: httpapi.NotificationHandler{Notifications: notifSvc, Memberships: mems},
		Clients:       httpapi.ClientHandler{Clients: clientSvc, Memberships: mems, Photos: photoSigner, History: clientHistorySvc},
		Me:            httpapi.MeHandler{Me: meSvc},
		Staff:         httpapi.StaffHandler{Staff: staffSvc, Memberships: mems},
		Public:        httpapi.PublicHandler{Public: publicSvc, Avail: availabilitySvc, Bookings: publicBookingSvc},
		BookingLinks:  httpapi.BookingLinkHandler{Links: bookingLinkSvc},
		Settings:      httpapi.SettingsHandler{Settings: settingsSvc, Memberships: mems},
		Members:       httpapi.MemberHandler{Members: memberSvc, Memberships: mems},
		Hours: httpapi.HoursHandler{
			Salon: salonHoursSvc, Professional: professionalHoursSvc, Memberships: mems,
		},
		TimeOff:   httpapi.TimeOffHandler{TimeOff: timeOffSvc, Memberships: mems},
		Absences:  httpapi.AbsenceHandler{Coverage: coverageSvc, Memberships: mems},
		Resources: httpapi.ResourceHandler{Resources: resourceSvc, Memberships: mems},
		Invitations: httpapi.InvitationHandler{
			Invitations: invitationSvc, Public: invitationPublicSvc, Memberships: mems,
		},
		Payroll: httpapi.PayrollHandler{PayModels: payModelSvc, Payroll: payrollSvc, Memberships: mems},
		PayrollRuns: httpapi.PayrollRunHandler{
			Runs: payrollRunSvc, Memberships: mems,
		},
		Expenses: httpapi.ExpenseHandler{Expenses: expenseSvc, Memberships: mems},
		Profit:   httpapi.ProfitHandler{Profit: profitSvc, Memberships: mems},
		Exports:  httpapi.ExportHandler{Exports: exportSvc, Memberships: mems},
		// F6: productos e inventario del POS (cinco rutas + `products` en POST /sales).
		Products: httpapi.ProductHandler{Products: productSvc, Memberships: mems},
		// La reserva pública es la única escritura sin auth: rate limit por IP
		// como en /auth (el GET de disponibilidad no pasa por acá).
		PublicBookingLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/public/salons/"),
		// F2g1: el lookup y el canje de una invitación son las otras escrituras
		// sin sesión; van bajo el mismo límite por IP que /auth.
		PublicInvitationLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/public/invitations/"),
		// F1/D7: el enlace del turno tiene su PROPIO cubo (20/min por IP): una
		// inundación de enlaces no puede gastar el cupo de la reserva pública ni el
		// de las invitaciones, ni al revés.
		PublicBookingLinkLimit: httpapi.RateLimitFor(authRateLimit, time.Minute, "POST", "/api/v1/public/booking-links/"),
		WS:                     hub.ServeWS,
		TokenIssuer:            tokens,
	}
	httpapi.RegisterRoutes(router, deps)
	return router, deps, reminders
}
