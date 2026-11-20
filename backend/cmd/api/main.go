// Command api: composition root. Aquí se CONSTRUYE todo (Factory):
// pool -> repos pg -> hasher/jwt -> casos de uso -> handlers -> rutas.
// main es el único lugar que conoce a todos los adaptadores concretos.
package main

import (
	"context"
	"log"
	"os"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/imaging"
	"salon-saas/internal/adapters/postgres"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/adapters/storage"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

func mustPool() *pgxpool.Pool {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@localhost:5432/salon?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	return pool
}

func migrate(pool *pgxpool.Pool) {
	// Aplica todas las migraciones en orden léxico desde el primer dir que exista.
	dirs := []string{"migrations", "/migrations", "backend/migrations"}
	var files []string
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && len(e.Name()) > 4 && e.Name()[len(e.Name())-4:] == ".sql" {
				files = append(files, d+"/"+e.Name())
			}
		}
		if len(files) > 0 {
			break
		}
	}
	if len(files) == 0 {
		log.Print("migrate: sin migraciones, sigo")
		return
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			log.Fatalf("migrate: leer %s: %v", f, err)
		}
		if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
			log.Fatalf("migrate: aplicar %s: %v", f, err)
		}
		log.Printf("migrate: aplicada %s", f)
	}
}

func main() {
	pool := mustPool()
	defer pool.Close()
	migrate(pool)

	users := pgstore.Users{Pool: pool}
	salons := pgstore.Salons{Pool: pool}
	mems := pgstore.Memberships{Pool: pool}
	hasher := security.BcryptHasher{}
	tokens := security.JWTIssuer{Secret: envOr("JWT_SECRET", "dev-secret-cambiala"), TTL: 0}

	authSvc := app.AuthService{Users: users, Hasher: hasher, Tokens: tokens}
	tenSvc := app.TenantService{Salons: salons, Memberships: mems}
	bookings := pgstore.Bookings{Pool: pool}
	salesRepo := pgstore.Sales{Pool: pool}
	closesRepo := pgstore.CashCloses{Pool: pool}
	servicesRepo := pgstore.Services{Pool: pool}

	// Realtime: el hub es el EventPublisher de los casos de uso (no saben que
	// hay websockets). Autentica el JWT del query param y arma una room por
	// salón a partir de las memberships del usuario.
	hub := realtime.NewHub(tokens, mems)
	notifications := pgstore.Notifications{Pool: pool}
	notifSvc := app.NotificationService{Notifications: notifications, Salons: mems, Events: hub}

	// Adjuntos: filesystem local detrás de ports.PhotoStorage (misma interfaz
	// que usaría S3: cambiar de adapter es esta línea y nada más).
	mediaDir := envOr("MEDIA_DIR", "./media")
	store := storage.Local{Root: mediaDir}
	log.Printf("media: %s", mediaDir)

	bookSvc := app.BookingService{Bookings: bookings, Memberships: mems, Events: hub, Notifier: notifSvc}
	svcSvc := app.ServiceService{Services: servicesRepo, Memberships: mems}
	saleSvc := app.SaleService{Sales: salesRepo, Services: servicesRepo, Memberships: mems, Events: hub}
	cashCloseSvc := app.CashCloseService{CashCloses: closesRepo, Sales: salesRepo, Memberships: mems, Events: hub, Notifier: notifSvc}
	dashSvc := app.DashboardService{Sales: salesRepo, Agenda: bookings, Staff: mems, Memberships: mems}
	clientSvc := app.ClientService{
		Clients:     pgstore.Clients{Pool: pool},
		Memberships: mems,
		Photos:      store,                                      // filesystem local
		Images:      imaging.WebP{MaxSide: domain.MaxPhotoSide}, // cwebp
	}

	r := gin.Default()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true, "service": "salon-saas"})
	})
	httpapi.RegisterRoutes(r, httpapi.Deps{
		Auth:          httpapi.AuthHandler{Auth: authSvc},
		Tenants:       httpapi.TenantsHandler{Tenants: tenSvc, Memberships: mems},
		Bookings:      httpapi.BookingHandler{Bookings: bookSvc, Memberships: mems},
		Services:      httpapi.ServiceHandler{Services: svcSvc, Memberships: mems},
		Sales:         httpapi.SaleHandler{Sales: saleSvc, Memberships: mems},
		CashCloses:    httpapi.CashCloseHandler{CashCloses: cashCloseSvc, Memberships: mems},
		Dashboard:     httpapi.DashboardHandler{Dashboard: dashSvc, Memberships: mems},
		Notifications: httpapi.NotificationHandler{Notifications: notifSvc, Memberships: mems},
		Clients:       httpapi.ClientHandler{Clients: clientSvc, Memberships: mems},
		WS:            hub.ServeWS,
		TokenIssuer:   tokens,
	})

	port := envOr("PORT", "8080")
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
