// F2d2 contra Postgres real (SALON_IT=1): la migración 018 (ausencias por
// profesional) y las queries del adaptador, siempre scopeadas por salon_id.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"salon-saas/internal/domain"
)

// S-F2d2-1: la 018 crea time_off, se puede aplicar dos veces y NO hace backfill:
// una base migrada no tiene ausencias hasta que alguien carga una.
func TestMigration018_time_off(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()

	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "018_time_off.sql"))
	if err != nil {
		t.Fatalf("leer la migración 018: %v", err)
	}
	sql := string(raw)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := fmt.Sprintf("f2d2_018_%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("esquema de prueba: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	// Estado pre-018: salón y membresías (sin ausencias).
	for _, q := range []string{
		`CREATE TABLE salons (
		   id   TEXT PRIMARY KEY,
		   slug TEXT NOT NULL UNIQUE,
		   name TEXT NOT NULL
		 )`,
		`CREATE TABLE memberships (
		   user_id  TEXT NOT NULL,
		   salon_id TEXT NOT NULL,
		   role     TEXT NOT NULL,
		   PRIMARY KEY (user_id, salon_id)
		 )`,
		`INSERT INTO salons(id,slug,name) VALUES ('s1','salon-uno','Salón Uno')`,
		`INSERT INTO memberships(user_id,salon_id,role) VALUES
		   ('u-own','s1','owner'),
		   ('u-pro','s1','professional'),
		   ('u-pro2','s2','professional')`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("estado previo: %v", err)
		}
	}

	// Dos pasadas: la segunda no puede fallar ni duplicar nada.
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatalf("aplicar 018 (pasada %d): %v", i+1, err)
		}
	}

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		  WHERE table_schema=$1 AND table_name='time_off')`, schema).Scan(&exists); err != nil {
		t.Fatalf("verificar time_off: %v", err)
	}
	if !exists {
		t.Fatal("falta la tabla time_off")
	}
	var rows int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM time_off`).Scan(&rows); err != nil {
		t.Fatalf("contar time_off: %v", err)
	}
	if rows != 0 {
		t.Fatalf("time_off nace vacía (sin backfill), tiene %d filas", rows)
	}

	// Un motivo vacío es válido y created_at sale solo; el resto de las columnas
	// queda como se escribió.
	if _, err := tx.Exec(ctx,
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at,created_by)
		 VALUES('t-ok','s1','u-pro','2026-12-01T15:00:00Z','2026-12-02T15:00:00Z','u-own')`); err != nil {
		t.Fatalf("ausencia válida: %v", err)
	}
	var reason string
	var createdAt time.Time
	if err := tx.QueryRow(ctx,
		`SELECT reason, created_at FROM time_off WHERE id='t-ok'`).Scan(&reason, &createdAt); err != nil {
		t.Fatalf("leer ausencia: %v", err)
	}
	if reason != "" || createdAt.IsZero() {
		t.Fatalf("defaults: reason=%q created_at=%v", reason, createdAt)
	}

	// Los constraints que sostienen la regla: fin posterior al inicio, tope del
	// motivo, membresía de ESE salón y motivo obligatorio.
	bad := []string{
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at) VALUES('t-bad1','s1','u-pro','2026-12-02T15:00:00Z','2026-12-01T15:00:00Z')`,
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at) VALUES('t-bad2','s1','u-pro','2026-12-01T15:00:00Z','2026-12-01T15:00:00Z')`,
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at,reason) VALUES('t-bad3','s1','u-pro','2026-12-01T15:00:00Z','2026-12-02T15:00:00Z',repeat('a',121))`,
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at) VALUES('t-bad4','s1','u-fantasma','2026-12-01T15:00:00Z','2026-12-02T15:00:00Z')`,
		// Un miembro de OTRO salón no puede tener ausencia acá: la FK es
		// (user_id, salon_id), no solo el usuario.
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at) VALUES('t-bad5','s1','u-pro2','2026-12-01T15:00:00Z','2026-12-02T15:00:00Z')`,
		`INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at,reason) VALUES('t-bad6','s1','u-pro','2026-12-01T15:00:00Z','2026-12-02T15:00:00Z',NULL)`,
	}
	for _, q := range bad {
		if _, err := tx.Exec(ctx, "SAVEPOINT intento"); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		if _, err := tx.Exec(ctx, q); err == nil {
			t.Fatalf("el constraint tiene que rechazar: %s", q)
		}
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT intento"); err != nil {
			t.Fatalf("rollback al savepoint: %v", err)
		}
	}

	// El índice que usa la disponibilidad: (salon_id, user_id, starts_at).
	var hasIndex bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes
		  WHERE schemaname=$1 AND tablename='time_off' AND indexname='time_off_member_idx')`,
		schema).Scan(&hasIndex); err != nil {
		t.Fatalf("verificar índice: %v", err)
	}
	if !hasIndex {
		t.Fatal("falta el índice time_off_member_idx (salon_id, user_id, starts_at)")
	}

	// Borrar la membresía se lleva sus ausencias (ON DELETE CASCADE).
	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE user_id='u-pro' AND salon_id='s1'`); err != nil {
		t.Fatalf("borrar membresía: %v", err)
	}
	var left int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM time_off`).Scan(&left); err != nil {
		t.Fatalf("contar tras borrar la membresía: %v", err)
	}
	if left != 0 {
		t.Fatalf("las ausencias se van con la membresía, quedan %d", left)
	}
}

// S-F2d2-1/5: el adaptador escribe y lee scopeado por salón. La ausencia de otro
// tenant no se lista, no se encuentra y no se puede borrar (ErrNotFound -> 404).
func TestTimeOff_repo_scoped_by_salon(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	applyMigrations(t, pool)
	if err := seedTimeOffSalons(t, pool); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	repo := TimeOff{Pool: pool}
	// Postgres guarda microsegundos: el "ahora" se trunca para comparar con Equal.
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := now.Add(24 * time.Hour)
	end := start.Add(48 * time.Hour)

	off, err := domain.NewTimeOff(domain.TimeOffInput{
		SalonID: timeOffSalonA, UserID: timeOffProA, StartsAt: start, EndsAt: end,
		Reason: "Vacaciones", CreatedBy: timeOffOwnerA,
	}, now)
	if err != nil {
		t.Fatalf("armar ausencia: %v", err)
	}
	if err := repo.Save(off); err != nil {
		t.Fatalf("guardar ausencia: %v", err)
	}

	// La lista del salón A la trae; la del salón B (misma ventana) no.
	for _, c := range []struct {
		salon string
		want  int
	}{{timeOffSalonA, 1}, {timeOffSalonB, 0}} {
		list, err := repo.ListRange(c.salon, start.Add(-time.Hour), end.Add(time.Hour))
		if err != nil {
			t.Fatalf("listar %s: %v", c.salon, err)
		}
		if len(list) != c.want {
			t.Fatalf("salón %s: %d ausencias (esperaba %d): %+v", c.salon, len(list), c.want, list)
		}
	}

	// El rango es [from, to) sobre el período: una ausencia fuera de la ventana
	// no se trae.
	if list, err := repo.ListRange(timeOffSalonA, start.Add(-48*time.Hour), start.Add(-24*time.Hour)); err != nil || len(list) != 0 {
		t.Fatalf("fuera de la ventana: %d %v", len(list), err)
	}

	// FindByID scopeado: la misma id desde el otro salón no existe.
	got, err := repo.FindByID(timeOffSalonA, off.ID)
	if err != nil {
		t.Fatalf("buscar la propia: %v", err)
	}
	if got.Reason != "Vacaciones" || !got.StartsAt.Equal(start) || got.UserID != timeOffProA {
		t.Fatalf("ausencia guardada: %+v", got)
	}
	if _, err := repo.FindByID(timeOffSalonB, off.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v (esperaba ErrNotFound)", err)
	}

	// DELETE de otro salón: no borra nada y devuelve ErrNotFound (404).
	if err := repo.Delete(timeOffSalonB, off.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar desde otro salón: %v (esperaba ErrNotFound)", err)
	}
	if _, err := repo.FindByID(timeOffSalonA, off.ID); err != nil {
		t.Fatalf("la ausencia sigue en su salón: %v", err)
	}

	// DELETE del salón correcto: 204 (sin error) y la fila se va.
	if err := repo.Delete(timeOffSalonA, off.ID); err != nil {
		t.Fatalf("borrar la propia: %v", err)
	}
	if _, err := repo.FindByID(timeOffSalonA, off.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tras borrar: %v (esperaba ErrNotFound)", err)
	}
	// Borrar dos veces es 404, no un 500.
	if err := repo.Delete(timeOffSalonA, off.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar de nuevo: %v", err)
	}
}

// Ids fijos del fixture: las filas quedan en la base scratch y cada corrida las
// reutiliza (upsert / DELETE + INSERT), así no crecen entre ejecuciones.
const (
	timeOffSalonA = "s-f2d2-a"
	timeOffSalonB = "s-f2d2-b"
	timeOffOwnerA = "u-f2d2-own"
	timeOffProA   = "u-f2d2-pro"
	timeOffProB   = "u-f2d2-pro-b"
)

func seedTimeOffSalons(t *testing.T, pool *pgxpool.Pool) error {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES
		   ('u-f2d2-own','Dueña','f2d2own@test.com','x'),
		   ('u-f2d2-pro','Profesional A','f2d2pro@test.com','x'),
		   ('u-f2d2-pro-b','Profesional B','f2d2prob@test.com','x')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO salons(id,slug,name) VALUES
		   ('s-f2d2-a','f2d2-a','Salón A'),('s-f2d2-b','f2d2-b','Salón B')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO memberships(user_id,salon_id,role,attends) VALUES
		   ('u-f2d2-own','s-f2d2-a','owner',true),
		   ('u-f2d2-pro','s-f2d2-a','professional',true),
		   ('u-f2d2-pro-b','s-f2d2-b','professional',true)
		 ON CONFLICT (user_id,salon_id) DO NOTHING`,
		`DELETE FROM time_off WHERE salon_id IN ('s-f2d2-a','s-f2d2-b')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}

// S-F2d2-2 en la DB: dos ausencias de la misma persona que se pisan no entran
// aunque lleguen a la vez (el chequeo del caso de uso no alcanza ante una
// carrera). Pegadas por el borde del período semiabierto [inicio, fin) sí.
func TestTimeOff_db_rejects_overlap_same_person(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	applyMigrations(t, pool)
	if err := seedTimeOffSalons(t, pool); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	repo := TimeOff{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Ventana lejana y al azar: la tabla no se limpia entre corridas y una
	// ausencia de una corrida anterior no tiene que chocar con esta.
	base := now.Add(time.Duration(1000+rand.Int63n(40000)) * 24 * time.Hour)
	mk := func(from, to int) domain.TimeOff {
		t.Helper()
		off, err := domain.NewTimeOff(domain.TimeOffInput{
			SalonID: timeOffSalonA, UserID: timeOffProA,
			StartsAt:  base.Add(time.Duration(from) * time.Hour),
			EndsAt:    base.Add(time.Duration(to) * time.Hour),
			CreatedBy: timeOffOwnerA,
		}, now)
		if err != nil {
			t.Fatalf("armar ausencia: %v", err)
		}
		return off
	}
	if err := repo.Save(mk(0, 10)); err != nil {
		t.Fatalf("primera ausencia: %v", err)
	}
	if err := repo.Save(mk(5, 20)); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("la DB debe rechazar una ausencia que pisa otra de la misma persona, dio %v", err)
	}
	if err := repo.Save(mk(10, 15)); err != nil {
		t.Fatalf("una ausencia pegada por el borde debe entrar: %v", err)
	}
}
