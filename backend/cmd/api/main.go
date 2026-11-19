// Command api: composition root. Aquí se CONSTRUYE todo (Factory):
// pool -> repos pg -> hasher/jwt -> casos de uso -> handlers -> rutas.
// main es el único lugar que conoce a todos los adaptadores concretos.
package main

import (
	"context"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/postgres"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
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
	candidates := []string{"migrations/001_init.sql", "/migrations/001_init.sql", "backend/migrations/001_init.sql"}
	var sql []byte
	var err error
	for _, p := range candidates {
		sql, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.Printf("migrate: no leí migraciones (%v), sigo", err)
		return
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		log.Fatalf("migrate: %v", err)
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

	r := gin.Default()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true, "service": "salon-saas"})
	})
	httpapi.RegisterRoutes(r, httpapi.Deps{
		Auth:        httpapi.AuthHandler{Auth: authSvc},
		Tenants:     httpapi.TenantsHandler{Tenants: tenSvc, Memberships: mems},
		TokenIssuer: tokens,
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
