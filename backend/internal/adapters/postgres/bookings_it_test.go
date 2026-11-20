// Carrera real contra Postgres (requiere SALON_IT=1 y DB migrada).
// Prueba la garantía dura: dos inserts concurrentes al mismo hueco -> uno gana,
// el otro vuelve como ErrAlreadyExists (EXCLUDE 23P01). Corre con:
// SALON_IT=1 go test ./internal/adapters/postgres/ -run TestRace -v
package pgstore

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
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

func TestRace_sameSlot_oneWins(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Fixture mínima: usuario profesional + salón (upsert para re-ejecución).
	_, _ = pool.Exec(ctx, `INSERT INTO users(id,name,email,pass_hash) VALUES('prof-it','Prof IT','profit@test.com','x') ON CONFLICT (id) DO NOTHING`)
	_, _ = pool.Exec(ctx, `INSERT INTO salons(id,slug,name) VALUES('s-it','s-it','Salón IT') ON CONFLICT (id) DO NOTHING`)
	_, _ = pool.Exec(ctx, `DELETE FROM bookings WHERE salon_id='s-it'`)
	// 002 debe estar aplicada (migrate del main la aplica al arrancar).
	var hasExclusion bool
	_ = pool.QueryRow(ctx, `SELECT true FROM pg_constraint WHERE conname='bookings_no_overlap'`).Scan(&hasExclusion)
	if !hasExclusion {
		t.Fatal("falta migración 002 (constraint bookings_no_overlap)")
	}

	repo := Bookings{Pool: pool}
	day := time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)
	start, end := day.Add(10*time.Hour), day.Add(11*time.Hour)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := domain.NewBooking("s-it", "prof-it", "Cliente", "", start, end)
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = repo.Save(b)
		}(i)
	}
	wg.Wait()

	wins, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case isAlreadyExists(err):
			conflicts++
		default:
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("esperaba 1 ganador + 1 conflicto 409, dio wins=%d conflicts=%d errs=%v", wins, conflicts, errs)
	}
}

func isAlreadyExists(err error) bool {
	return err != nil && (err == domain.ErrAlreadyExists ||
		// compara por igualdad ya traducido en Save; esto es cinturón extra.
		err.Error() == domain.ErrAlreadyExists.Error())
}
