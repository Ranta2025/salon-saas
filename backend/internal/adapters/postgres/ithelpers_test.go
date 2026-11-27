// Helpers de los tests de integración de este paquete. Viven acá (y no en el
// archivo de un adaptador concreto) porque los usan TODOS los IT del paquete.
package pgstore

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/testutil/itdb"
)

func itPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SALON_IT") == "" {
		t.Skip("SALON_IT no definido: test de integración salteado")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	return pool
}

func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	itdb.ApplyMigrations(t, pool)
}
