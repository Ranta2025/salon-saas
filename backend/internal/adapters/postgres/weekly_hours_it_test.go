// F2d1 contra Postgres real (SALON_IT=1): la migración 017 (horario semanal del
// salón y de cada profesional) y las queries nuevas, siempre scopeadas por
// salon_id.
package pgstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"salon-saas/internal/domain"
)

// S-F2d1-1: la 017 crea las dos tablas, se puede aplicar dos veces y NO hace
// backfill: los datos que ya existen quedan como están (sin filas de horario),
// así el comportamiento de hoy no cambia.
func TestMigration017_weekly_hours(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()

	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "017_weekly_hours.sql"))
	if err != nil {
		t.Fatalf("leer la migración 017: %v", err)
	}
	sql := string(raw)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := fmt.Sprintf("f2d1_017_%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("esquema de prueba: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	// Estado pre-017: salón con settings y membresías (sin horario semanal).
	for _, q := range []string{
		`CREATE TABLE salons (
		   id   TEXT PRIMARY KEY,
		   slug TEXT NOT NULL UNIQUE,
		   name TEXT NOT NULL,
		   open_time TEXT NOT NULL DEFAULT '09:00',
		   close_time TEXT NOT NULL DEFAULT '19:00'
		 )`,
		`CREATE TABLE memberships (
		   user_id  TEXT NOT NULL,
		   salon_id TEXT NOT NULL,
		   role     TEXT NOT NULL,
		   attends  BOOLEAN NOT NULL DEFAULT false,
		   trade    TEXT NOT NULL DEFAULT '',
		   PRIMARY KEY (user_id, salon_id)
		 )`,
		`INSERT INTO salons(id,slug,name) VALUES ('s1','salon-uno','Salón Uno')`,
		`INSERT INTO memberships(user_id,salon_id,role,attends) VALUES
		   ('u-own','s1','owner',true),
		   ('u-pro','s1','professional',true)`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("estado previo: %v", err)
		}
	}

	// Se aplica dos veces: la segunda no puede fallar ni duplicar nada.
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatalf("aplicar 017 (pasada %d): %v", i+1, err)
		}
	}

	for _, table := range []string{"salon_weekly_hours", "professional_hours"} {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			  WHERE table_schema=$1 AND table_name=$2)`, schema, table).Scan(&exists); err != nil {
			t.Fatalf("verificar %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("falta la tabla %s", table)
		}
		// Sin backfill: nadie tiene horario propio después de migrar.
		var rows int
		if err := tx.QueryRow(ctx, "SELECT COUNT(*)::int FROM "+table).Scan(&rows); err != nil {
			t.Fatalf("contar %s: %v", table, err)
		}
		if rows != 0 {
			t.Fatalf("%s tiene que nacer vacía (sin backfill), tiene %d filas", table, rows)
		}
	}

	// Los constraints que sostienen la regla: día 1..7, inicio antes del fin y la
	// membresía que autoriza el horario de una persona.
	bad := []string{
		`INSERT INTO salon_weekly_hours(salon_id,weekday,open_time,close_time) VALUES ('s1',1,'19:00','09:00')`,
		`INSERT INTO salon_weekly_hours(salon_id,weekday,open_time,close_time) VALUES ('s1',8,'09:00','19:00')`,
		`INSERT INTO professional_hours(salon_id,user_id,weekday,start_time,end_time) VALUES ('s1','u-pro',2,'14:00','13:00')`,
		`INSERT INTO professional_hours(salon_id,user_id,weekday,start_time,end_time) VALUES ('s1','u-pro',0,'09:00','13:00')`,
		// Un miembro inexistente en ESE salón no puede tener horario.
		`INSERT INTO professional_hours(salon_id,user_id,weekday,start_time,end_time) VALUES ('s1','u-fantasma',2,'09:00','13:00')`,
	}
	for _, q := range bad {
		// Cada insert inválido va en su propio savepoint: el error aborta la
		// transacción y el savepoint la deja usable para el resto del test.
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

	// Una ventana por día: el segundo insert del mismo día no entra.
	if _, err := tx.Exec(ctx, `INSERT INTO salon_weekly_hours(salon_id,weekday,open_time,close_time) VALUES ('s1',1,'09:00','13:00')`); err != nil {
		t.Fatalf("horario válido: %v", err)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT repetido"); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO salon_weekly_hours(salon_id,weekday,open_time,close_time) VALUES ('s1',1,'14:00','19:00')`); err == nil {
		t.Fatal("un día no puede tener dos ventanas: PK (salon_id, weekday)")
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT repetido"); err != nil {
		t.Fatalf("rollback al savepoint: %v", err)
	}
}

// fixtureMember deja un salón y una persona del equipo en la base scratch.
func fixtureMember(t *testing.T, pool *pgxpool.Pool, userID, salonID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users(id,name,email,pass_hash) VALUES($1,$1,$1||'@test.com','x')
		 ON CONFLICT (id) DO NOTHING`, userID); err != nil {
		t.Fatalf("usuario %s: %v", userID, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO salons(id,slug,name) VALUES($1,$1,$1) ON CONFLICT (id) DO NOTHING`,
		salonID); err != nil {
		t.Fatalf("salón %s: %v", salonID, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO memberships(user_id,salon_id,role,attends) VALUES($1,$2,'professional',true)
		 ON CONFLICT (user_id,salon_id) DO UPDATE SET role='professional'`, userID, salonID); err != nil {
		t.Fatalf("membresía %s: %v", userID, err)
	}
}

// S-F2d1-4: el horario del salón se guarda por reemplazo (lo viejo no queda) y
// una lista vacía vuelve al horario único. Siempre scopeado por salón.
func TestWeeklyHours_salon(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	repo := WeeklyHours{Pool: pool}
	fixtureMember(t, pool, "f2d1-own", "f2d1-s1")
	fixtureMember(t, pool, "f2d1-own2", "f2d1-s2")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM salon_weekly_hours WHERE salon_id IN ('f2d1-s1','f2d1-s2')`)
	})

	if err := repo.SaveSalonHours("f2d1-s1", []domain.WeekdayHours{
		{Weekday: 1, Open: "09:00", Close: "18:00"},
		{Weekday: 2, Open: "10:00", Close: "14:00"},
	}); err != nil {
		t.Fatalf("guardar horario: %v", err)
	}
	got, err := repo.SalonHours("f2d1-s1")
	if err != nil {
		t.Fatalf("leer horario: %v", err)
	}
	if len(got) != 2 || got[0] != (domain.WeekdayHours{Weekday: 1, Open: "09:00", Close: "18:00"}) ||
		got[1] != (domain.WeekdayHours{Weekday: 2, Open: "10:00", Close: "14:00"}) {
		t.Fatalf("horario guardado: %+v", got)
	}

	// Reemplazo: lo que no vino en la lista nueva no queda.
	if err := repo.SaveSalonHours("f2d1-s1", []domain.WeekdayHours{{Weekday: 5, Open: "11:00", Close: "20:00"}}); err != nil {
		t.Fatalf("reemplazar horario: %v", err)
	}
	got, err = repo.SalonHours("f2d1-s1")
	if err != nil {
		t.Fatalf("leer horario: %v", err)
	}
	if len(got) != 1 || got[0].Weekday != 5 {
		t.Fatalf("el reemplazo tiene que dejar solo la fila nueva: %+v", got)
	}

	// Aislamiento: el horario del otro salón no se toca ni se ve.
	if err := repo.SaveSalonHours("f2d1-s2", []domain.WeekdayHours{{Weekday: 3, Open: "08:00", Close: "12:00"}}); err != nil {
		t.Fatalf("horario del otro salón: %v", err)
	}
	if got, _ := repo.SalonHours("f2d1-s1"); len(got) != 1 || got[0].Weekday != 5 {
		t.Fatalf("el salón 1 no ve lo del salón 2: %+v", got)
	}

	// Lista vacía = volver al horario único.
	if err := repo.SaveSalonHours("f2d1-s1", nil); err != nil {
		t.Fatalf("volver al horario único: %v", err)
	}
	if got, _ := repo.SalonHours("f2d1-s1"); len(got) != 0 {
		t.Fatalf("sin filas propias: %+v", got)
	}
	if got, _ := repo.SalonHours("f2d1-s2"); len(got) != 1 {
		t.Fatal("limpiar el salón 1 no puede limpiar el salón 2")
	}
}

// S-F2d1-4: los tramos de una profesional. Reemplazo atómico (un tramo que la DB
// rechaza deja la lista anterior intacta), scope por salón y CASCADE cuando la
// persona sale del equipo.
func TestWeeklyHours_member(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := WeeklyHours{Pool: pool}
	fixtureMember(t, pool, "f2d1-pro", "f2d1-s1")
	fixtureMember(t, pool, "f2d1-pro2", "f2d1-s1")
	fixtureMember(t, pool, "f2d1-ajeno", "f2d1-s2")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM professional_hours WHERE salon_id IN ('f2d1-s1','f2d1-s2')`)
	})

	segments := []domain.HoursSegment{
		{Weekday: 2, Start: "09:00", End: "13:00"},
		{Weekday: 2, Start: "14:00", End: "18:00"},
		{Weekday: 4, Start: "10:00", End: "12:00"},
	}
	if err := repo.SaveMemberHours("f2d1-s1", "f2d1-pro", segments); err != nil {
		t.Fatalf("guardar tramos: %v", err)
	}
	got, err := repo.MemberHours("f2d1-s1", "f2d1-pro")
	if err != nil {
		t.Fatalf("leer tramos: %v", err)
	}
	if len(got) != 3 || got[0] != segments[0] || got[1] != segments[1] || got[2] != segments[2] {
		t.Fatalf("tramos guardados: %+v", got)
	}

	// La segunda persona ve los suyos (nadie más tiene todavía).
	if got, _ := repo.MemberHours("f2d1-s1", "f2d1-pro2"); len(got) != 0 {
		t.Fatalf("la otra profesional no tiene tramos: %+v", got)
	}

	// Todo el salón en una query, indexado por persona.
	all, err := repo.AllMemberHours("f2d1-s1")
	if err != nil {
		t.Fatalf("tramos del salón: %v", err)
	}
	if len(all) != 1 || len(all["f2d1-pro"]) != 3 {
		t.Fatalf("tramos del salón: %+v", all)
	}

	// Atomicidad: un tramo con el inicio después del fin lo rechaza el CHECK de
	// la DB a mitad del reemplazo; la lista anterior tiene que quedar intacta.
	broken := []domain.HoursSegment{{Weekday: 3, Start: "09:00", End: "10:00"}, {Weekday: 3, Start: "12:00", End: "11:00"}}
	if err := repo.SaveMemberHours("f2d1-s1", "f2d1-pro", broken); err == nil {
		t.Fatal("un tramo inválido tiene que fallar")
	}
	if got, _ := repo.MemberHours("f2d1-s1", "f2d1-pro"); len(got) != 3 {
		t.Fatalf("el reemplazo fallido no puede tocar los tramos anteriores: %+v", got)
	}

	// Aislamiento entre salones: la persona de otro salón no tiene tramos acá y
	// un tramo suyo no aparece en este salón.
	_ = repo.SaveMemberHours("f2d1-s2", "f2d1-ajeno", []domain.HoursSegment{{Weekday: 1, Start: "09:00", End: "10:00"}})
	if got, _ := repo.MemberHours("f2d1-s1", "f2d1-ajeno"); len(got) != 0 {
		t.Fatalf("tramos de otro salón en este salón: %+v", got)
	}
	if got, _ := repo.SalonHours("f2d1-s1"); len(got) != 0 {
		t.Fatalf("el salón 1 no tiene horario propio: %+v", got)
	}
	all, _ = repo.AllMemberHours("f2d1-s1")
	if _, ok := all["f2d1-ajeno"]; ok {
		t.Fatalf("AllMemberHours no puede traer gente de otro salón: %+v", all)
	}

	// ON DELETE CASCADE: cuando la persona sale del equipo, su horario se va.
	if _, err := pool.Exec(ctx, `DELETE FROM memberships WHERE user_id='f2d1-pro' AND salon_id='f2d1-s1'`); err != nil {
		t.Fatalf("quitar miembro: %v", err)
	}
	if got, _ := repo.MemberHours("f2d1-s1", "f2d1-pro"); len(got) != 0 {
		t.Fatalf("el horario se va con la membresía: %+v", got)
	}
}
