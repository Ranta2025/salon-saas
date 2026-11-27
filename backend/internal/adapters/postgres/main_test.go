// TestMain deja la base de integración migrada antes de cualquier test del
// paquete: así el orden de archivos no importa y una DB recién creada (scratch)
// sirve igual que la de desarrollo.
package pgstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/testutil/itdb"
)

func TestMain(m *testing.M) {
	if os.Getenv("SALON_IT") != "" {
		if err := migrateForTests(); err != nil {
			fmt.Fprintf(os.Stderr, "migración previa a los tests IT: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func migrateForTests() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable"
	}
	cfg, err := PoolConfig(url)
	if err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := context.Background()
	// Varios paquetes IT migran la MISMA base en paralelo: el lock evita que dos
	// ALTER TABLE simultáneos se pisen.
	unlock, err := itdb.LockMigrations(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	return Migrate(ctx, pool, filepath.Join("..", "..", "..", "migrations"))
}
