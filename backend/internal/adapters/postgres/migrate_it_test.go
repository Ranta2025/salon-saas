// Migraciones del adaptador Postgres (B-10): ordenadas, idempotentes y con un
// lock global para que dos instancias que arrancan a la vez no se pisen.
//
// Corre con: SALON_IT=1 DATABASE_URL=postgres://salon:salon@127.0.0.1:5432/salon_w_a?sslmode=disable \
//
//	go test ./internal/adapters/postgres/ -run TestMigrate -v
package pgstore

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func migrationsDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "migrations")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("no encuentro las migraciones en %s: %v", dir, err)
	}
	return dir
}

// La primera pasada sobre una DB fresca aplica todo; la segunda no hace nada
// (schema_migrations) y tampoco falla: es la condición real de cada boot.
func TestMigrate_idempotente(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	dir := migrationsDir(t)

	if err := Migrate(ctx, pool, dir); err != nil {
		t.Fatalf("primera pasada: %v", err)
	}
	if err := Migrate(ctx, pool, dir); err != nil {
		t.Fatalf("segunda pasada sobre una DB ya migrada: %v", err)
	}

	// Quedó registro de cada archivo y las tablas necesarias existen.
	var applied int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("leer schema_migrations: %v", err)
	}
	if applied < 9 {
		t.Fatalf("esperaba al menos 9 migraciones registradas, hay %d", applied)
	}
	for _, table := range []string{"users", "salons", "memberships", "bookings", "services", "sales", "sale_items", "cash_closes", "notifications", "clients", "client_photos"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
			table).Scan(&exists); err != nil {
			t.Fatalf("verificar tabla %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("falta la tabla %s después de migrar", table)
		}
	}
}

// Dos instancias arrancando al mismo tiempo: una toma el lock y migra, la otra
// espera y después encuentra todo aplicado. Ambas tienen que terminar sin error
// (antes, el que perdía la carrera moría con "relation already exists").
func TestMigrate_concurrente(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	dir := migrationsDir(t)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = Migrate(ctx, pool, dir)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("instancia %d falló migrando en paralelo: %v", i, err)
		}
	}
}

// Una versión ya registrada no se vuelve a aplicar (el archivo puede incluso
// haber cambiado: la DB es la fuente de verdad).
func TestMigrate_salta_versiones_aplicadas(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	dir := migrationsDir(t)
	if err := Migrate(ctx, pool, dir); err != nil {
		t.Fatalf("migrar: %v", err)
	}

	// Un archivo con error de sintaxis que YA está registrado no rompe el boot.
	scratch := t.TempDir()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("leer migraciones: %v", err)
	}
	var last string
	for _, e := range files {
		if filepath.Ext(e.Name()) == ".sql" {
			last = e.Name()
			src, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("leer %s: %v", e.Name(), err)
			}
			if err := os.WriteFile(filepath.Join(scratch, e.Name()), src, 0o600); err != nil {
				t.Fatalf("copiar %s: %v", e.Name(), err)
			}
		}
	}
	if last == "" {
		t.Fatal("no hay migraciones")
	}
	if err := os.WriteFile(filepath.Join(scratch, last), []byte("ESTO NO ES SQL;"), 0o600); err != nil {
		t.Fatalf("escribir archivo roto: %v", err)
	}
	if err := Migrate(ctx, pool, scratch); err != nil {
		t.Fatalf("una versión ya aplicada no debe re-ejecutarse: %v", err)
	}

	// El archivo roto con una versión NUEVA sí falla (no se traga el error).
	if err := os.WriteFile(filepath.Join(scratch, "999_rota.sql"), []byte("ESTO NO ES SQL;"), 0o600); err != nil {
		t.Fatalf("escribir archivo roto: %v", err)
	}
	if err := Migrate(ctx, pool, scratch); err == nil {
		t.Fatal("una migración nueva con error debe fallar")
	}
}

// El runner aplica en orden léxico (001 antes que 002...) y deja la versión
// registrada: se verifica con una lista de archivos controlada.
func TestMigrate_orden_y_registro(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()

	dir := t.TempDir()
	for name, sql := range map[string]string{
		"001_a.sql": "CREATE TABLE IF NOT EXISTS mig_order_a (id TEXT PRIMARY KEY);",
		"002_b.sql": "CREATE TABLE IF NOT EXISTS mig_order_b (id TEXT PRIMARY KEY REFERENCES mig_order_a(id));",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o600); err != nil {
			t.Fatalf("escribir %s: %v", name, err)
		}
	}
	if err := Migrate(ctx, pool, dir); err != nil {
		t.Fatalf("migrar: %v", err)
	}
	var versions []string
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations WHERE version LIKE '%_a.sql' OR version LIKE '%_b.sql' ORDER BY version`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		versions = append(versions, v)
	}
	if len(versions) != 2 || !sort.StringsAreSorted(versions) {
		t.Fatalf("versiones registradas: %v", versions)
	}
	// Limpieza de las tablas de prueba (no de las migraciones reales).
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS mig_order_b`); err != nil {
		t.Fatalf("drop b: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS mig_order_a`); err != nil {
		t.Fatalf("drop a: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version IN ('001_a.sql','002_b.sql')`); err != nil {
		t.Fatalf("limpiar versiones: %v", err)
	}
}

// El pool que usa main tiene que ser configurable (B-11): con la config pedida
// el pool respeta los límites en vez de los defaults.
func TestPoolConfig(t *testing.T) {
	cfg, err := PoolConfig("postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable")
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}
	if cfg.MaxConns <= 0 || cfg.MinConns < 0 || cfg.MaxConnLifetime <= 0 {
		t.Fatalf("config incompleta: %+v", cfg)
	}
	if cfg.MaxConnIdleTime <= 0 || cfg.HealthCheckPeriod <= 0 {
		t.Fatalf("faltan timeouts de pool: %+v", cfg)
	}
	var _ *pgxpool.Config = cfg
}
