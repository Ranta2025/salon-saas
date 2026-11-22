// F2d3 contra Postgres real (SALON_IT=1): la transacción del "Reasignar todo"
// (todo o nada, con la EXCLUDE de turnos como árbitro) y la lectura del email de
// un turno para avisar a la clienta. Todas las queries siguen scopeadas por
// salon_id.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// coverageFixture deja el salón, dos profesionales y turnos limpios para el test.
// Usa ids propios (s-it-f2d3) para no tocar los fixtures de las otras pruebas.
func coverageFixture(t *testing.T) (*Bookings, *BookingEmails) {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES
		   ('u-ana-f2d3','Ana','ana-f2d3@test.com','x'),
		   ('u-bea-f2d3','Bea','bea-f2d3@test.com','x'),
		   ('u-out-f2d3','Out','out-f2d3@test.com','x')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO salons(id,slug,name) VALUES
		   ('s-it-f2d3','s-it-f2d3','Salón F2d3'),
		   ('s-it-f2d3-b','s-it-f2d3-b','Otro F2d3')
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO memberships(user_id,salon_id,role,attends) VALUES
		   ('u-ana-f2d3','s-it-f2d3','professional',true),
		   ('u-bea-f2d3','s-it-f2d3','professional',true),
		   ('u-out-f2d3','s-it-f2d3-b','professional',true)
		 ON CONFLICT (user_id,salon_id) DO UPDATE SET role=EXCLUDED.role, attends=EXCLUDED.attends`,
		`DELETE FROM booking_emails WHERE salon_id IN ('s-it-f2d3','s-it-f2d3-b')`,
		`DELETE FROM bookings WHERE salon_id IN ('s-it-f2d3','s-it-f2d3-b')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	return &Bookings{Pool: pool}, &BookingEmails{Pool: pool}
}

// coverageBooking arma un turno reservado del fixture.
func coverageBooking(t *testing.T, id, userID string, start time.Time, minutes int) domain.Booking {
	t.Helper()
	b, err := domain.NewBooking("s-it-f2d3", userID, "Lía", "555", start, start.Add(time.Duration(minutes)*time.Minute))
	if err != nil {
		t.Fatalf("turno %s: %v", id, err)
	}
	b.ID = id
	return b
}

func coverageTime(t *testing.T, day, hour int) time.Time {
	t.Helper()
	return time.Date(2026, 12, day, hour, 0, 0, 0, time.UTC)
}

// S-F2d3-3: si UN turno no se puede mover, la transacción no deja NADA movido.
// La EXCLUDE de la tabla (002) es la que rechaza el segundo: el primero ya se
// había escrito dentro de la transacción.
func TestBookings_InSalonTx_isAllOrNothing(t *testing.T) {
	repo, _ := coverageFixture(t)
	first := coverageBooking(t, "b-f2d3-1", "u-ana-f2d3", coverageTime(t, 1, 10), 45)
	second := coverageBooking(t, "b-f2d3-2", "u-ana-f2d3", coverageTime(t, 1, 11), 45)
	// Bea ya tiene un turno a las 11: a esa hora no entra el segundo.
	busy := coverageBooking(t, "b-f2d3-bea", "u-bea-f2d3", coverageTime(t, 1, 11), 45)
	for _, b := range []domain.Booking{first, second, busy} {
		if err := repo.Save(b); err != nil {
			t.Fatalf("guardar %s: %v", b.ID, err)
		}
	}

	movedFirst, _ := first.Reassign("u-bea-f2d3")
	movedSecond, _ := second.Reassign("u-bea-f2d3")
	err := repo.InSalonTx("s-it-f2d3", func(tx ports.BookingTx) error {
		if err := tx.Update(movedFirst); err != nil {
			return err
		}
		// Este choca con el turno de Bea a las 11: la EXCLUDE lo rechaza.
		return tx.Update(movedSecond)
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("se esperaba el rechazo de la EXCLUDE, dio %v", err)
	}

	// Todo o nada: el PRIMER turno tampoco se movió (rollback real).
	stored, err := repo.FindByID("s-it-f2d3", "b-f2d3-1")
	if err != nil {
		t.Fatalf("leer el primero: %v", err)
	}
	if stored.ProfessionalID != "u-ana-f2d3" {
		t.Fatalf("el primer turno quedó movido: %+v", stored)
	}
	if stored2, _ := repo.FindByID("s-it-f2d3", "b-f2d3-2"); stored2.ProfessionalID != "u-ana-f2d3" {
		t.Fatalf("el segundo turno no puede haberse movido: %+v", stored2)
	}
}

// S-F2d3-2: cuando todo entra, la transacción SÍ mueve todos los turnos y los
// conserva en el mismo horario.
func TestBookings_InSalonTx_movesAllAndKeepsTheTime(t *testing.T) {
	repo, _ := coverageFixture(t)
	first := coverageBooking(t, "b-f2d3-1", "u-ana-f2d3", coverageTime(t, 1, 10), 45)
	second := coverageBooking(t, "b-f2d3-2", "u-ana-f2d3", coverageTime(t, 1, 11), 45)
	for _, b := range []domain.Booking{first, second} {
		if err := repo.Save(b); err != nil {
			t.Fatalf("guardar %s: %v", b.ID, err)
		}
	}

	err := repo.InSalonTx("s-it-f2d3", func(tx ports.BookingTx) error {
		for _, b := range []domain.Booking{first, second} {
			moved, err := b.Reassign("u-bea-f2d3")
			if err != nil {
				return err
			}
			if err := tx.Update(moved); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reasignar todo: %v", err)
	}
	for _, want := range []struct {
		id    string
		start time.Time
	}{{"b-f2d3-1", coverageTime(t, 1, 10)}, {"b-f2d3-2", coverageTime(t, 1, 11)}} {
		stored, err := repo.FindByID("s-it-f2d3", want.id)
		if err != nil {
			t.Fatalf("%s: %v", want.id, err)
		}
		if stored.ProfessionalID != "u-bea-f2d3" {
			t.Fatalf("%s no se movió: %+v", want.id, stored)
		}
		// La clienta conserva la hora.
		if !stored.StartsAt.Equal(want.start) {
			t.Fatalf("%s cambió de horario: %v", want.id, stored.StartsAt)
		}
	}
	if busy, err := repo.FindOverlapping("s-it-f2d3", "u-bea-f2d3", coverageTime(t, 1, 10), coverageTime(t, 1, 12)); err != nil || len(busy) != 2 {
		t.Fatalf("los dos turnos tienen que estar en la agenda de Bea: %d %v", len(busy), err)
	}
	// La agenda de Ana quedó libre: no hay turnos reservados suyos en ese rango.
	if left, err := repo.FindOverlapping("s-it-f2d3", "u-ana-f2d3", coverageTime(t, 1, 9), coverageTime(t, 1, 13)); err != nil || len(left) != 0 {
		t.Fatalf("Ana quedó con turnos: %d %v", len(left), err)
	}
}

// S-F2d3-1/3: el scope por salón es la autorización de la transacción: un turno
// de otro tenant no se toca (ErrNotFound) y la transacción lo deshace.
func TestBookings_InSalonTx_scopesSalon(t *testing.T) {
	repo, _ := coverageFixture(t)
	other := coverageBooking(t, "b-f2d3-out", "u-out-f2d3", coverageTime(t, 1, 10), 45)
	other.SalonID = "s-it-f2d3-b"
	if err := repo.Save(other); err != nil {
		t.Fatalf("guardar el turno del otro salón: %v", err)
	}
	moved, _ := other.Reassign("u-ana-f2d3")
	// La transacción no puede ni leer el turno de otro tenant: FindByID lleva el
	// salon_id de la ruta, así que el plan de reasignación nunca lo incluye.
	err := repo.InSalonTx("s-it-f2d3", func(tx ports.BookingTx) error {
		if _, err := tx.FindByID("s-it-f2d3", "b-f2d3-out"); err != nil {
			return err
		}
		return tx.Update(moved)
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("un turno de otro salón no se toca desde acá: %v", err)
	}
	stored, err := repo.FindByID("s-it-f2d3-b", "b-f2d3-out")
	if err != nil {
		t.Fatalf("leer el turno del otro salón: %v", err)
	}
	if stored.ProfessionalID != "u-out-f2d3" {
		t.Fatalf("el turno del otro salón quedó modificado: %+v", stored)
	}
}

// S-F2d3-4: el email del turno se lee scopeado por salón (el de otro tenant no
// existe acá) para poder avisar a la clienta del cambio.
func TestBookingEmails_FindEmail_scopesSalon(t *testing.T) {
	repo, emails := coverageFixture(t)
	b := coverageBooking(t, "b-f2d3-mail", "u-ana-f2d3", coverageTime(t, 1, 10), 45)
	if err := repo.Save(b); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}
	if err := emails.Save("s-it-f2d3", b.ID, "lia@mail.com"); err != nil {
		t.Fatalf("guardar email: %v", err)
	}
	got, err := emails.FindEmail("s-it-f2d3", b.ID)
	if err != nil || got != "lia@mail.com" {
		t.Fatalf("FindEmail: %q %v", got, err)
	}
	// Otro salón: el turno no existe para esa query.
	if _, err := emails.FindEmail("s-it-f2d3-b", b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("email de otro salón: %v", err)
	}
	// Un turno sin email guardado es ErrNotFound (no hay a quién escribir).
	sin := coverageBooking(t, "b-f2d3-sin-mail", "u-ana-f2d3", coverageTime(t, 1, 12), 45)
	if err := repo.Save(sin); err != nil {
		t.Fatalf("guardar turno sin email: %v", err)
	}
	if _, err := emails.FindEmail("s-it-f2d3", sin.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("turno sin email: %v", err)
	}
}

// S-F2d3-6: covered_from viaja en TODAS las escrituras del turno (alta y el
// UPDATE dentro de la transacción del "Reasignar todo") y se lee en todas las
// SELECT (FindByID y ListRange). Vacío queda en NULL, no en texto vacío.
func TestBookings_covered_from_roundtrip(t *testing.T) {
	repo, _ := coverageFixture(t)

	// Alta sin cobertura: la columna queda NULL y se lee como "".
	fresh := coverageBooking(t, "b-f2d3-cover-1", "u-bea-f2d3", coverageTime(t, 2, 10), 45)
	if err := repo.Save(fresh); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	var isNull bool
	if err := repo.Pool.QueryRow(context.Background(),
		`SELECT covered_from IS NULL FROM bookings WHERE id=$1`, fresh.ID).Scan(&isNull); err != nil {
		t.Fatalf("leer covered_from: %v", err)
	}
	if !isNull {
		t.Fatal("sin cobertura la columna tiene que ser NULL")
	}
	stored, err := repo.FindByID("s-it-f2d3", fresh.ID)
	if err != nil {
		t.Fatalf("leer el turno: %v", err)
	}
	if stored.CoveredFrom != "" {
		t.Fatalf("sin cobertura: %q", stored.CoveredFrom)
	}

	// El UPDATE de la reasignación la persiste (Bea -> Ana: la tenía Bea).
	moved, err := stored.Reassign("u-ana-f2d3")
	if err != nil {
		t.Fatalf("reasignar: %v", err)
	}
	if err := repo.InSalonTx("s-it-f2d3", func(tx ports.BookingTx) error { return tx.Update(moved) }); err != nil {
		t.Fatalf("update en la transacción: %v", err)
	}
	stored, err = repo.FindByID("s-it-f2d3", fresh.ID)
	if err != nil {
		t.Fatalf("releer: %v", err)
	}
	if stored.ProfessionalID != "u-ana-f2d3" || stored.CoveredFrom != "u-bea-f2d3" {
		t.Fatalf("el update tiene que guardar covered_from: %+v", stored)
	}

	// Un alta que ya viene cubierta también lo escribe.
	second := coverageBooking(t, "b-f2d3-cover-2", "u-ana-f2d3", coverageTime(t, 2, 11), 45)
	second.CoveredFrom = "u-bea-f2d3"
	if err := repo.Save(second); err != nil {
		t.Fatalf("guardar la cubierta: %v", err)
	}

	// ListRange (la agenda del salón) lo lee en cada fila.
	list, err := repo.ListRange("s-it-f2d3", coverageTime(t, 2, 0), coverageTime(t, 2, 23))
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	got := map[string]string{}
	for _, b := range list {
		got[b.ID] = b.CoveredFrom
	}
	if got["b-f2d3-cover-1"] != "u-bea-f2d3" || got["b-f2d3-cover-2"] != "u-bea-f2d3" {
		t.Fatalf("covered_from en el listado: %+v", got)
	}
}

// S-F2d3-6: la 019 agrega bookings.covered_from (text, null, SIN FK) y se puede
// aplicar dos veces. Una base que ya tenía turnos los deja en NULL: no hay
// backfill.
func TestMigration019_booking_covered_from(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()

	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "019_booking_covered_from.sql"))
	if err != nil {
		t.Fatalf("leer la migración 019: %v", err)
	}
	sql := string(raw)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := fmt.Sprintf("f2d3_019_%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("esquema de prueba: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	// Estado pre-019: un salón con un turno ya agendado.
	for _, q := range []string{
		`CREATE TABLE bookings (
		   id              TEXT PRIMARY KEY,
		   salon_id        TEXT NOT NULL,
		   professional_id TEXT NOT NULL,
		   starts_at       TIMESTAMPTZ NOT NULL,
		   ends_at         TIMESTAMPTZ NOT NULL,
		   status          TEXT NOT NULL
		 )`,
		`INSERT INTO bookings(id,salon_id,professional_id,starts_at,ends_at,status)
		 VALUES('b-viejo','s1','u-ana','2026-12-01T15:00:00Z','2026-12-01T16:00:00Z','booked')`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("estado previo: %v", err)
		}
	}

	// Dos pasadas: la segunda no puede fallar.
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatalf("aplicar 019 (pasada %d): %v", i+1, err)
		}
	}

	var dataType, isNullable string
	if err := tx.QueryRow(ctx,
		`SELECT data_type, is_nullable FROM information_schema.columns
		  WHERE table_schema=$1 AND table_name='bookings' AND column_name='covered_from'`,
		schema).Scan(&dataType, &isNullable); err != nil {
		t.Fatalf("la columna covered_from: %v", err)
	}
	if dataType != "text" || isNullable != "YES" {
		t.Fatalf("covered_from tiene que ser text null: %s %s", dataType, isNullable)
	}

	// Sin FK: la persona puede dejar el equipo y su nombre queda como historia.
	var fks int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE n.nspname=$1 AND t.relname='bookings' AND c.contype='f'`, schema).Scan(&fks); err != nil {
		t.Fatalf("contar FKs: %v", err)
	}
	if fks != 0 {
		t.Fatalf("covered_from no lleva FK (y la tabla de prueba no tenía): %d", fks)
	}

	// El turno que ya existía queda sin cobertura (sin backfill).
	var covered *string
	if err := tx.QueryRow(ctx, `SELECT covered_from FROM bookings WHERE id='b-viejo'`).Scan(&covered); err != nil {
		t.Fatalf("leer el turno viejo: %v", err)
	}
	if covered != nil {
		t.Fatalf("los turnos existentes quedan en NULL, dio %q", *covered)
	}

	// Se puede escribir y limpiar (la cobertura vuelve a quien la tenía).
	if _, err := tx.Exec(ctx, `UPDATE bookings SET covered_from='u-zoe' WHERE id='b-viejo'`); err != nil {
		t.Fatalf("escribir covered_from: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE bookings SET covered_from=NULL WHERE id='b-viejo'`); err != nil {
		t.Fatalf("limpiar covered_from: %v", err)
	}
}
