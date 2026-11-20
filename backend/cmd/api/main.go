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
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/adapters/storage"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
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

	router, deps := buildRouter(cfg, pool)
	log.Printf("media: %s", cfg.MediaDir)
	log.Printf("cors: %v", cfg.CORSOrigins)
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

// buildRouter arma los adaptadores y monta las rutas. Devuelve el router y las
// dependencias (los tests de humo lo usan).
func buildRouter(cfg config, pool *pgxpool.Pool) (*gin.Engine, httpapi.Deps) {
	users := pgstore.Users{Pool: pool}
	salons := pgstore.Salons{Pool: pool}
	mems := pgstore.Memberships{Pool: pool}
	hasher := security.BcryptHasher{}
	tokens := security.JWTIssuer{Secret: cfg.JWTSecret, TTL: 0}

	authSvc := app.AuthService{Users: users, Hasher: hasher, Tokens: tokens}
	tenSvc := app.TenantService{Salons: salons, Memberships: mems}
	bookings := pgstore.Bookings{Pool: pool}
	salesRepo := pgstore.Sales{Pool: pool}
	closesRepo := pgstore.CashCloses{Pool: pool}
	servicesRepo := pgstore.Services{Pool: pool}

	// Realtime: el hub es el EventPublisher de los casos de uso (no saben que
	// hay websockets). Autentica el JWT del query param, valida el Origin con la
	// MISMA allowlist de CORS (B-01) y arma una room por salón.
	hub := realtime.NewHub(tokens, mems, realtime.HubOrigins(cfg.CORSOrigins...))
	notifications := pgstore.Notifications{Pool: pool}
	notifSvc := app.NotificationService{Notifications: notifications, Salons: mems, Events: hub}

	// Adjuntos: filesystem local detrás de ports.PhotoStorage (misma interfaz
	// que usaría S3: cambiar de adapter es esta línea y nada más).
	store := storage.Local{Root: cfg.MediaDir}

	bookSvc := app.BookingService{Bookings: bookings, Memberships: mems, Events: hub, Notifier: notifSvc}
	svcSvc := app.ServiceService{Services: servicesRepo, Memberships: mems}
	saleSvc := app.SaleService{Sales: salesRepo, Services: servicesRepo, Memberships: mems, Events: hub}
	// Tx: el cierre corre en una transacción con advisory lock por salón (B-09).
	cashCloseSvc := app.CashCloseService{
		CashCloses: closesRepo, Sales: salesRepo, Memberships: mems, Tx: closesRepo,
		Events: hub, Notifier: notifSvc,
	}
	dashSvc := app.DashboardService{Sales: salesRepo, Agenda: bookings, Staff: mems, Memberships: mems}
	clientSvc := app.ClientService{
		Clients:     pgstore.Clients{Pool: pool},
		Memberships: mems,
		Photos:      store,                                      // filesystem local
		Images:      imaging.WebP{MaxSide: domain.MaxPhotoSide}, // cwebp
	}
	meSvc := app.MeService{Users: users, Salons: salons, Members: mems}
	staffSvc := app.StaffService{Staff: mems, Memberships: mems}
	publicSvc := app.PublicSalonService{Salons: salons, Services: servicesRepo}

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
	// Los proxies confiables son las redes privadas del compose: el rate limit
	// usa la IP real del cliente y no la del nginx. Sin esto, X-Forwarded-For
	// sería una cabecera que cualquiera puede inventar.
	if err := router.SetTrustedProxies([]string{"127.0.0.1", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}); err != nil {
		log.Printf("trusted proxies: %v", err)
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
		Clients:       httpapi.ClientHandler{Clients: clientSvc, Memberships: mems},
		Me:            httpapi.MeHandler{Me: meSvc},
		Staff:         httpapi.StaffHandler{Staff: staffSvc, Memberships: mems},
		Public:        httpapi.PublicHandler{Public: publicSvc},
		WS:            hub.ServeWS,
		TokenIssuer:   tokens,
	}
	httpapi.RegisterRoutes(router, deps)
	return router, deps
}
