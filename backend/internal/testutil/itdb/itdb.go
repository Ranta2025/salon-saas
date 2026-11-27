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
		if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
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
