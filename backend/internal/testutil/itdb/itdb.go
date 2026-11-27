// Package itdb es el andamiaje de los tests de integración (SALON_IT=1) de los
// módulos por funcionalidad: el pool, las migraciones aplicadas dos veces (son
// re-ejecutables) y el TestMain que migra antes de cualquier test.
//
// Es la versión compartida de `itPool`, `applyMigrations` y `TestMain` que hoy
// viven en `internal/adapters/postgres/*_it_test.go`: los `_test.go` de un
// paquete no se pueden importar desde otro.
//
// El directorio de migraciones se resuelve relativo al directorio del paquete
// que corre el test (`go test` ejecuta con cwd = directorio del paquete), y todos
// los paquetes `postgres` de una funcionalidad están a la misma profundidad:
// `backend/internal/<feature>/postgres` -> `../../../migrations`.
package itdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsDir es backend/migrations visto desde internal/<feature>/postgres.
const migrationsDir = "../../../migrations"

// migrationLockKey es el advisory lock que serializa las migraciones: `go test`
// corre los paquetes EN PARALELO y varios paquetes IT migran la MISMA base, así
// que sin el lock dos ALTER TABLE simultáneos se pisan ("tuple concurrently
// updated"). Es de test: no toca el runner de producción.
const migrationLockKey = 4001342

// LockMigrations toma el advisory lock de las migraciones y devuelve cómo
// soltarlo. Lo usan los paquetes IT que migran con su propio runner (pgstore).
func LockMigrations(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	if _, err := pool.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return nil, err
	}
	return func() { _, _ = pool.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockKey) }, nil
}

// Pool abre el pool de la base de integración. Sin SALON_IT saltea el test.
func Pool(t *testing.T) *pgxpool.Pool {
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

// ApplyMigrations replica el runner de cmd/api: todos los .sql en orden léxico.
// Se corre DOS veces para probar de paso que son re-ejecutables, que es la
// condición real de cada boot.
func ApplyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	unlock, err := LockMigrations(context.Background(), pool)
	if err != nil {
		t.Fatalf("lock de migraciones: %v", err)
	}
	defer unlock()
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("leer migraciones: %v", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, filepath.Join(migrationsDir, e.Name()))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no hay migraciones en %s", migrationsDir)
	}
	for pass := 1; pass <= 2; pass++ {
		for _, f := range files {
			sql, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("leer %s: %v", f, err)
			}
			if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
				t.Fatalf("aplicar %s (pasada %d): %v", f, pass, err)
			}
		}
	}
}

// Run es el TestMain del paquete: con SALON_IT deja la base migrada antes de
// cualquier test.
func Run(m *testing.M) int {
	if os.Getenv("SALON_IT") != "" {
		if err := migrate(); err != nil {
			fmt.Fprintf(os.Stderr, "migración previa a los tests IT: %v\n", err)
			return 1
		}
	}
	return m.Run()
}

func migrate() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := context.Background()
	unlock, err := LockMigrations(ctx, pool)
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, filepath.Join(migrationsDir, e.Name()))
		}
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("aplicar %s: %w", f, err)
		}
	}
	return nil
}

// Race corre n funciones a la vez, soltadas todas juntas, y espera a todas: la
// forma de probar contra Postgres real que UNIQUE y los UPDATE condicionales
// dejan ganar a uno solo.
func Race(n int, fn func(i int)) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
}

// CountRows ejecuta un SELECT count(*) y devuelve el número; el test falla si la
// consulta no anda. Es el helper compartido de los tests de integración.
func CountRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("contar: %v", err)
	}
	return n
}

// Round2Fixtures crea dos salones limpios (s-it-r2 y s-it-r2b) con usuarios.
func Round2Fixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	ApplyMigrations(t, pool)

	for _, q := range []string{
		`DELETE FROM salons WHERE id IN ('s-it-r2','s-it-r2b')`,
		`DELETE FROM users WHERE id LIKE 'u-it-r2-%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES
			('u-it-r2-owner','Ana Owner','u-it-r2-owner@test.com','x'),
			('u-it-r2-prof','Bruno Prof','u-it-r2-prof@test.com','x'),
			('u-it-r2-owner2','Eva Owner','u-it-r2-owner2@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES
			('s-it-r2','s-it-r2','Salón Ronda 2'),
			('s-it-r2b','s-it-r2b','Salón Ronda 2 B')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	return pool, ctx
}

// MigrationsDir devuelve el directorio de migraciones visto desde
// internal/<feature>/postgres y falla si no existe. Los IT que aplican UNA
// migración concreta (y comprueban que se puede aplicar dos veces) lo usan.
func MigrationsDir(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(migrationsDir); err != nil {
		t.Fatalf("no encuentro las migraciones en %s: %v", migrationsDir, err)
	}
	return migrationsDir
}
