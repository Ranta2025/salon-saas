package pgstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationLockKey es la clave del advisory lock global de migraciones. Es un
// número fijo (no un hash del nombre de la DB) para que dos procesos que
// apuntan a la misma base se serialicen entre sí.
const migrationLockKey int64 = 721_002_600

// Migrate aplica los .sql de dir en orden léxico UNA sola vez cada uno,
// registrando la versión en schema_migrations (B-10).
//
// Por qué el lock: docker-compose (o un deploy con dos réplicas) arranca dos
// procesos a la vez. Sin serialización, ambos intentan CREATE TABLE y uno muere
// con "relation already exists". Con pg_advisory_lock uno espera al otro y el
// segundo encuentra todo aplicado.
//
// El lock es de sesión y se toma en una conexión dedicada: tiene que sostenerse
// hasta el último COMMIT, y se libera siempre (defer), incluso si una migración
// falla.
func Migrate(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	files, err := migrationFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("migrate: no hay archivos .sql en %s", dir)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return err
	}
	defer func() {
		// Best effort: si el unlock falla, la conexión se libera y el lock de
		// sesión muere con ella.
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}

	for _, file := range files {
		version := filepath.Base(file)
		var applied bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("migrate: leer %s: %w", version, err)
		}
		// Cada archivo es atómico con su registro: si falla, ni el cambio ni la
		// versión quedan a medias.
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: aplicar %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: registrar %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate: commit %s: %w", version, err)
		}
	}
	return nil
}

// migrationFiles lista los .sql de dir en orden léxico.
func migrationFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: leer %s: %w", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// MigrationDir busca el directorio de migraciones probando las ubicaciones
// desde las que corre el binario: local (`go run ./cmd/api`), dentro de la
// imagen (WORKDIR /app) y desde la raíz del repo.
func MigrationDir() (string, error) {
	for _, dir := range []string{"migrations", "/migrations", "backend/migrations"} {
		if files, err := migrationFiles(dir); err == nil && len(files) > 0 {
			return dir, nil
		}
	}
	return "", fmt.Errorf("migrate: no encuentro el directorio de migraciones")
}

// PoolConfig arma la configuración del pool con límites explícitos (B-11): el
// default de pgxpool abre hasta max(4, CPUs) conexiones y no las recicla, lo
// que en un contenedor con límite de conexiones de Postgres termina en
// "too many clients".
func PoolConfig(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	return cfg, nil
}
