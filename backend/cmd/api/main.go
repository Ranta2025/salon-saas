// Command api: composition root. Aquí se CONSTRUYE todo (Factory):
// pool -> repos pg -> hasher/jwt -> casos de uso -> handlers -> rutas.
// main es el único lugar que conoce a todos los adaptadores concretos.
//
// El cableado está partido por funcionalidad en los `wire_<feature>.go` de este
// mismo package: cada uno arma los repositorios, los casos de uso y —cuando
// aplica— el banco/almacenamiento de UNA funcionalidad. Este archivo conserva el
// orden de alto nivel (núcleo, funcionalidades, servidor) y arma las rutas.
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
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/bookings"
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

// buildRouter arma los adaptadores y monta las rutas. Devuelve el router, las
// dependencias (los tests de humo lo usan) y la cola de recordatorios.
//
// El ORDEN importa: primero el núcleo (identidad y compuerta de sesión), después
// los repositorios y casos de uso de cada funcionalidad (hub -> media -> casos
// de uso que publican eventos), y al final los que necesitan el mailer y el
// banco. El orden de los constructores con efecto (storage, 2FA, SMTP, banco) es
// el mismo de siempre.
func buildRouter(cfg config, pool *pgxpool.Pool) (*gin.Engine, httpapi.Deps, bookings.ReminderService) {
	w := newWiring(cfg, pool)          // núcleo: usuarios, salones, memberships, hasher, jwt
	wireAuth(w)                        // login de siempre + compuerta de revocación de sesiones
	wireTenants(w)                     // alta de salón
	wireBookingRepos(w)                // turnos
	wirePOSRepos(w)                    // ventas, venta con productos y cierres de caja
	wireStaffing(w)                    // servicios, profesionales y equipamiento
	wireSchedules(w)                   // horarios, ausencias y cobertura
	wireAudit(w)                       // registro de auditoría
	wirePayroll(w)                     // nómina y comisiones
	wireFinance(w)                     // gastos y ganancia neta
	wireExports(w)                     // exportes CSV
	wireTeamRepos(w)                   // invitaciones de equipo
	wireClientRepos(w)                 // fichas, vínculo con la ficha e historial
	wireNotifications(w)               // hub de tiempo real y bandeja de avisos
	wireMedia(w)                       // adjuntos (S3 o filesystem) y firma de URLs
	wireBookingServices(w)             // agenda y disponibilidad
	wireClients(w)                     // fichas, fotos e historial
	wireProducts(w)                    // productos e inventario
	wirePackages(w)                    // paquetes y bonos prepagos
	wireLoyalty(w)                     // fidelidad
	wirePOSServices(w)                 // ventas, cierre de caja y panel del día
	wireSettings(w)                    // ajustes, perfil público y galería
	wireMe(w)                          // /me
	wireTeamServices(w)                // staff y miembros del salón
	wireTwoFactor(w)                   // 2FA opcional (secreto cifrado)
	wireWaitlist(w)                    // lista de espera
	wireReviews(w)                     // reseñas posvisita
	wireMailer(w)                      // SMTP + inyección posterior del sender
	wireAccountRecovery(w)             // recuperar contraseña y verificar email
	wireInvitations(w)                 // invitaciones de equipo (alta y canje público)
	wireBookingLinks(w)                // recordatorios y enlaces del turno
	wireDeposits(w)                    // señas online y banco simulado
	wireSimbankRoutes(w)               // handlers del banco y del callback
	wireBookingPublic(w)               // reserva pública y enlace del turno
	w.publicSvc.Reviews = w.reviewsSvc // F13/D7: la vidriera suma las reseñas

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

	deps := buildDeps(w)
	httpapi.RegisterRoutes(router, deps)
	return router, deps, w.reminders
}
