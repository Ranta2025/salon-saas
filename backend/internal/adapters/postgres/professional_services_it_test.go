// F2c contra Postgres real (SALON_IT=1): la migración 016 (oficio, servicios
// por profesional y el servicio del turno) y las queries nuevas, siempre
// scopeadas por salon_id.
package pgstore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	pgstore "salon-saas/internal/adapters/postgres"
	staffingpostgres "salon-saas/internal/staffing/postgres"
	"salon-saas/internal/testutil/itdb"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	bookingspostgres "salon-saas/internal/bookings/postgres"
	"salon-saas/internal/domain"
)

// S-F2c-1: 016 es idempotente y deja el comportamiento de hoy en los datos que
// ya existen: cada miembro reservable hace todos los servicios de SU salón,
// recepción ninguno, los turnos viejos quedan sin servicio (NULL).
//
// Corre en un esquema aislado dentro de una transacción (como el test de la
// 015): el estado "antes de la 016" solo se puede reproducir desde cero. Nada
// queda en la base.
func TestMigration016_professional_services(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)

	raw, err := os.ReadFile(filepath.Join(itdb.MigrationsDir(t), "016_professional_services.sql"))
	if err != nil {
		t.Fatalf("leer la migración 016: %v", err)
	}
	sql := string(raw)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := fmt.Sprintf("f2c016_%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("esquema de prueba: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	// Estado pre-016: memberships con attends (015), servicios y turnos sin
	// service_id. Solo las tablas/columnas que la 016 toca.
	for _, q := range []string{
		`CREATE TABLE memberships (
		   user_id  TEXT NOT NULL,
		   salon_id TEXT NOT NULL,
		   role     TEXT NOT NULL,
		   attends  BOOLEAN NOT NULL DEFAULT false,
		   PRIMARY KEY (user_id, salon_id)
		 )`,
		`CREATE TABLE services (
		   id       TEXT PRIMARY KEY,
		   salon_id TEXT NOT NULL,
		   name     TEXT NOT NULL
		 )`,
		`CREATE TABLE bookings (
		   id       TEXT PRIMARY KEY,
		   salon_id TEXT NOT NULL,
		   starts_at TIMESTAMPTZ NOT NULL,
		   ends_at   TIMESTAMPTZ NOT NULL,
		   status    TEXT NOT NULL DEFAULT 'booked'
		 )`,
		`INSERT INTO memberships(user_id,salon_id,role,attends) VALUES
		   ('u-own',   's1', 'owner',        true),
		   ('u-adm',   's1', 'admin',        true),
		   ('u-pro',   's1', 'professional', true),
		   ('u-rec',   's1', 'reception',    false),
		   ('u-rec2',  's1', 'reception',    true),
		   ('u-cli',   's1', 'client',       false),
		   ('u-own2',  's1', 'owner',        false),
		   ('u-pro2',  's2', 'professional', true)`,
		`INSERT INTO services(id,salon_id,name) VALUES
		   ('svc-a', 's1', 'Corte'),
		   ('svc-b', 's1', 'Keratina'),
		   ('svc-c', 's2', 'Corte')`,
		`INSERT INTO bookings(id,salon_id,starts_at,ends_at) VALUES
		   ('b-1', 's1', '2026-12-01T15:00:00Z', '2026-12-01T15:45:00Z')`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("estado previo (%s): %v", q, err)
		}
	}

	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("aplicar 016: %v", err)
	}

	// Columnas nuevas: trade (not null, default '') y service_id (nullable).
	var nullable, columnDefault string
	if err := tx.QueryRow(ctx, `SELECT is_nullable, COALESCE(column_default,'')
		FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='memberships' AND column_name='trade'`, schema,
	).Scan(&nullable, &columnDefault); err != nil {
		t.Fatalf("falta memberships.trade: %v", err)
	}
	if nullable != "NO" || columnDefault != "''::text" {
		t.Fatalf("trade: nullable=%q default=%q (esperaba NO/''::text)", nullable, columnDefault)
	}
	var serviceNullable string
	if err := tx.QueryRow(ctx, `SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='bookings' AND column_name='service_id'`, schema,
	).Scan(&serviceNullable); err != nil {
		t.Fatalf("falta bookings.service_id: %v", err)
	}
	if serviceNullable != "YES" {
		t.Fatalf("bookings.service_id tiene que aceptar NULL, es %q", serviceNullable)
	}

	// Backfill: cada reservable hace todos los servicios de su salón.
	want := map[string]bool{
		"u-own|svc-a": true, "u-own|svc-b": true,
		"u-adm|svc-a": true, "u-adm|svc-b": true,
		"u-pro|svc-a": true, "u-pro|svc-b": true,
		"u-pro2|svc-c": true,
	}
	rows, err := tx.Query(ctx, `SELECT user_id, service_id FROM professional_services`)
	if err != nil {
		t.Fatalf("leer backfill: %v", err)
	}
	got := map[string]bool{}
	for rows.Next() {
		var user, svc string
		if err := rows.Scan(&user, &svc); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[user+"|"+svc] = true
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("filas del backfill: %+v", got)
	}
	for key := range want {
		if !got[key] {
			t.Fatalf("falta %s en el backfill: %+v", key, got)
		}
	}
	// Recepción (aunque una fila vieja tenga la marca encendida) y cliente no.
	for _, key := range []string{"u-rec|svc-a", "u-rec2|svc-a", "u-cli|svc-a", "u-own2|svc-a", "u-pro|svc-c"} {
		if got[key] {
			t.Fatalf("%s no debería estar en el backfill: %+v", key, got)
		}
	}

	// El turno viejo sigue ahí y su servicio es NULL.
	var bookingService *string
	if err := tx.QueryRow(ctx, `SELECT service_id FROM bookings WHERE id='b-1'`).Scan(&bookingService); err != nil {
		t.Fatalf("turno existente: %v", err)
	}
	if bookingService != nil {
		t.Fatalf("un turno previo no gana servicio: %q", *bookingService)
	}

	// Cambios a mano + segunda pasada: no duplica ni repone lo que se quitó ni
	// pisa el oficio configurado.
	if _, err := tx.Exec(ctx, `DELETE FROM professional_services WHERE user_id='u-pro' AND service_id='svc-b'`); err != nil {
		t.Fatalf("quitar un servicio a mano: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET trade='Barbero' WHERE user_id='u-pro'`); err != nil {
		t.Fatalf("oficio a mano: %v", err)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("segunda pasada: %v", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM professional_services`).Scan(&count); err != nil {
		t.Fatalf("contar tras la segunda pasada: %v", err)
	}
	if count != len(want)-1 {
		t.Fatalf("la segunda pasada duplicó o repuso filas: %d (esperaba %d)", count, len(want)-1)
	}
	var trade string
	if err := tx.QueryRow(ctx, `SELECT trade FROM memberships WHERE user_id='u-pro'`).Scan(&trade); err != nil {
		t.Fatalf("releer oficio: %v", err)
	}
	if trade != "Barbero" {
		t.Fatalf("la segunda pasada pisó el oficio: %q", trade)
	}

	// Un servicio que nace después de la migración no se asigna a nadie: el
	// backfill corre SOLO al crear la tabla (S-F2c-5).
	if _, err := tx.Exec(ctx, `INSERT INTO services(id,salon_id,name) VALUES ('svc-nuevo','s1','Color')`); err != nil {
		t.Fatalf("servicio nuevo: %v", err)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("tercera pasada: %v", err)
	}
	var assigned int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM professional_services WHERE service_id='svc-nuevo'`).Scan(&assigned); err != nil {
		t.Fatalf("contar servicio nuevo: %v", err)
	}
	if assigned != 0 {
		t.Fatalf("un servicio nuevo no se asigna solo: %d filas", assigned)
	}
}

// f2cFixtures deja dos salones limpios con equipo y catálogo para probar las
// queries de F2c (reusa el patrón de las otras suites IT).
func f2cFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)

	for _, q := range []string{
		`DELETE FROM salons WHERE id IN ('s-it-f2c','s-it-f2c-b')`,
		`DELETE FROM users WHERE id LIKE 'u-it-f2c-%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES
		   ('u-it-f2c-own','Ana Owner','u-it-f2c-own@test.com','x'),
		   ('u-it-f2c-adm','Beto Adm','u-it-f2c-adm@test.com','x'),
		   ('u-it-f2c-pro','Caro Prof','u-it-f2c-pro@test.com','x'),
		   ('u-it-f2c-pro2','Eva Prof','u-it-f2c-pro2@test.com','x'),
		   ('u-it-f2c-rec','Dani Recep','u-it-f2c-rec@test.com','x'),
		   ('u-it-f2c-own-b','Fabi Owner','u-it-f2c-own-b@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES
		   ('s-it-f2c','s-it-f2c','Salón F2c'),
		   ('s-it-f2c-b','s-it-f2c-b','Salón F2c B')`,
		`INSERT INTO services(id,salon_id,name,duration_min,price_cents,commission_pct) VALUES
		   ('svc-f2c-corte','s-it-f2c','Corte',45,150000,20),
		   ('svc-f2c-keratina','s-it-f2c','Keratina',60,250000,15),
		   ('svc-f2c-otro','s-it-f2c-b','Corte B',30,90000,10)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mems := pgstore.Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-f2c-own", SalonID: "s-it-f2c", Role: domain.RoleOwner, Attends: true},
		{UserID: "u-it-f2c-adm", SalonID: "s-it-f2c", Role: domain.RoleAdmin},
		{UserID: "u-it-f2c-pro", SalonID: "s-it-f2c", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-it-f2c-pro2", SalonID: "s-it-f2c", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-it-f2c-rec", SalonID: "s-it-f2c", Role: domain.RoleReception},
		{UserID: "u-it-f2c-own-b", SalonID: "s-it-f2c-b", Role: domain.RoleOwner, Attends: true},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membresía %s: %v", m.UserID, err)
		}
	}
	return pool, ctx
}

// S-F2c-2: el adaptador de servicios por profesional. SetServices es un
// reemplazo atómico, las cascadas borran lo que cuelga y toda query está
// scopeada por salon_id.
func TestProfessionalServices_IT(t *testing.T) {
	pool, ctx := f2cFixtures(t)
	repo := staffingpostgres.ProfessionalServices{Pool: pool}

	// Lista inicial vacía (nadie hace nada hasta que se asigna).
	if ids, err := repo.ListServiceIDs("s-it-f2c", "u-it-f2c-pro"); err != nil || len(ids) != 0 {
		t.Fatalf("lista inicial: %v %v", ids, err)
	}
	if err := repo.SetServices("s-it-f2c", "u-it-f2c-pro", []string{"svc-f2c-corte", "svc-f2c-keratina"}); err != nil {
		t.Fatalf("asignar: %v", err)
	}
	ids, err := repo.ListServiceIDs("s-it-f2c", "u-it-f2c-pro")
	if err != nil || len(ids) != 2 || ids[0] != "svc-f2c-corte" || ids[1] != "svc-f2c-keratina" {
		t.Fatalf("lista guardada: %v %v", ids, err)
	}
	// Scopeado: otro salón no ve esta lista y una lista en otro salón no la pisa.
	if other, err := repo.ListServiceIDs("s-it-f2c-b", "u-it-f2c-pro"); err != nil || len(other) != 0 {
		t.Fatalf("otro salón: %v %v", other, err)
	}

	// Reemplazo: la lista nueva reemplaza a la anterior.
	if err := repo.SetServices("s-it-f2c", "u-it-f2c-pro", []string{"svc-f2c-keratina"}); err != nil {
		t.Fatalf("reemplazar: %v", err)
	}
	if ids, _ := repo.ListServiceIDs("s-it-f2c", "u-it-f2c-pro"); len(ids) != 1 || ids[0] != "svc-f2c-keratina" {
		t.Fatalf("reemplazo: %v", ids)
	}

	// Atomicidad: con un id inválido la transacción entera vuelve atrás y la
	// lista anterior queda intacta (no media lista).
	if err := repo.SetServices("s-it-f2c", "u-it-f2c-pro", []string{"svc-f2c-corte", "svc-no-existe"}); err == nil {
		t.Fatal("un servicio inexistente tiene que fallar")
	}
	if ids, _ := repo.ListServiceIDs("s-it-f2c", "u-it-f2c-pro"); len(ids) != 1 || ids[0] != "svc-f2c-keratina" {
		t.Fatalf("la lista quedó a medias: %v", ids)
	}

	// Lista vacía: la persona no hace ninguno.
	if err := repo.SetServices("s-it-f2c", "u-it-f2c-pro", nil); err != nil {
		t.Fatalf("vaciar: %v", err)
	}
	if ids, _ := repo.ListServiceIDs("s-it-f2c", "u-it-f2c-pro"); len(ids) != 0 {
		t.Fatalf("vaciar: %v", ids)
	}

	// ListProfessionalsFor: quien ATIENDE y hace el servicio, y solo del salón.
	if err := repo.SetServices("s-it-f2c", "u-it-f2c-pro", []string{"svc-f2c-corte"}); err != nil {
		t.Fatalf("asignar corte: %v", err)
	}
	if err := repo.SetServices("s-it-f2c", "u-it-f2c-own", []string{"svc-f2c-corte"}); err != nil {
		t.Fatalf("asignar al dueño: %v", err)
	}
	// La administración no atiende: aunque tuviera la fila, no es elegible.
	if _, err := pool.Exec(ctx, `INSERT INTO professional_services(salon_id,user_id,service_id)
		VALUES('s-it-f2c','u-it-f2c-adm','svc-f2c-corte')`); err != nil {
		t.Fatalf("fila de un admin que no atiende: %v", err)
	}
	eligible, err := repo.ListProfessionalsFor("s-it-f2c", "svc-f2c-corte")
	if err != nil {
		t.Fatalf("elegibles: %v", err)
	}
	names := map[string]bool{}
	for _, m := range eligible {
		names[m.ID] = true
		if !m.Bookable() {
			t.Fatalf("%s no atiende y es elegible: %+v", m.ID, eligible)
		}
	}
	if len(names) != 2 || !names["u-it-f2c-pro"] || !names["u-it-f2c-own"] {
		t.Fatalf("elegibles de corte: %+v", eligible)
	}
	if other, err := repo.ListProfessionalsFor("s-it-f2c-b", "svc-f2c-corte"); err != nil || len(other) != 0 {
		t.Fatalf("el otro salón no comparte elegibles: %+v %v", other, err)
	}

	// Cascada por servicios: borrar un servicio se lleva sus filas, y un turno
	// que lo tenía queda sin servicio (SET NULL), no sin turno.
	bookings := bookingspostgres.Bookings{Pool: pool}
	start := time.Date(2026, 12, 2, 15, 0, 0, 0, time.UTC)
	b, err := domain.NewBooking("s-it-f2c", "u-it-f2c-pro", "Lía", "555", start, start.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	b.ServiceID = "svc-f2c-corte"
	if err := bookings.Save(b); err != nil {
		t.Fatalf("guardar turno con servicio: %v", err)
	}
	stored, err := bookings.FindByID("s-it-f2c", b.ID)
	if err != nil || stored.ServiceID != "svc-f2c-corte" {
		t.Fatalf("el turno guarda su servicio: %+v %v", stored, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM services WHERE id='svc-f2c-corte'`); err != nil {
		t.Fatalf("borrar servicio: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM professional_services
		WHERE salon_id='s-it-f2c' AND service_id='svc-f2c-corte'`).Scan(&left); err != nil {
		t.Fatalf("contar tras borrar el servicio: %v", err)
	}
	if left != 0 {
		t.Fatalf("ON DELETE CASCADE no limpió los servicios por profesional: %d", left)
	}
	stored, err = bookings.FindByID("s-it-f2c", b.ID)
	if err != nil {
		t.Fatalf("el turno no se borra con el servicio: %v", err)
	}
	if stored.ServiceID != "" {
		t.Fatalf("ON DELETE SET NULL no limpió el servicio del turno: %q", stored.ServiceID)
	}
	// Y un turno sin servicio (calendario) sigue guardándose y leyéndose.
	plain, _ := domain.NewBooking("s-it-f2c", "u-it-f2c-pro", "Ana", "", start.Add(2*time.Hour), start.Add(3*time.Hour))
	if err := bookings.Save(plain); err != nil {
		t.Fatalf("turno sin servicio: %v", err)
	}

	// Cascada por membresía: si la persona sale del salón, sus servicios se van.
	if err := repo.SetServices("s-it-f2c", "u-it-f2c-pro2", []string{"svc-f2c-keratina"}); err != nil {
		t.Fatalf("asignar a la otra: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM memberships WHERE user_id='u-it-f2c-pro2' AND salon_id='s-it-f2c'`); err != nil {
		t.Fatalf("quitar a la profesional: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM professional_services
		WHERE salon_id='s-it-f2c' AND user_id='u-it-f2c-pro2'`).Scan(&left); err != nil {
		t.Fatalf("contar tras quitar a la persona: %v", err)
	}
	if left != 0 {
		t.Fatalf("ON DELETE CASCADE no limpió los servicios del miembro: %d", left)
	}
}

// S-F2c-2/S-F2c-3: el oficio en las dos lecturas (equipo y agenda), y el turno
// con service_id NULL o con servicio.
func TestMembersTrade_IT(t *testing.T) {
	pool, _ := f2cFixtures(t)
	mems := pgstore.Memberships{Pool: pool}

	if err := mems.SetTrade("u-it-f2c-pro", "s-it-f2c", "Barbero"); err != nil {
		t.Fatalf("guardar oficio: %v", err)
	}
	got, err := mems.Find("u-it-f2c-pro", "s-it-f2c")
	if err != nil || got.Trade != "Barbero" {
		t.Fatalf("el Find tiene que leer el oficio: %+v %v", got, err)
	}
	// Scopeado: el mismo UPDATE sobre otro salón no toca nada.
	if err := mems.SetTrade("u-it-f2c-pro", "s-it-f2c-b", "Otra cosa"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SetTrade de otro salón: %v", err)
	}
	if got, _ := mems.Find("u-it-f2c-pro", "s-it-f2c"); got.Trade != "Barbero" {
		t.Fatalf("el oficio del salón real no se toca: %+v", got)
	}

	members, err := mems.ListMembers("s-it-f2c")
	if err != nil {
		t.Fatalf("listar equipo: %v", err)
	}
	for _, m := range members {
		if m.ID == "u-it-f2c-pro" && m.Trade != "Barbero" {
			t.Fatalf("ListMembers lleva el oficio: %+v", members)
		}
	}
	// ListStaff también, y con la marca de quien atiende.
	staff, err := mems.ListStaff("s-it-f2c")
	if err != nil {
		t.Fatalf("listar staff: %v", err)
	}
	found := false
	for _, m := range staff {
		if m.ID == "u-it-f2c-pro" {
			found = true
			if m.Trade != "Barbero" {
				t.Fatalf("ListStaff lleva el oficio: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("la profesional tiene que estar en staff: %+v", staff)
	}

	// El tope de 40 caracteres vive también en la DB: si otro camino de
	// escritura lo intenta, el CHECK lo frena.
	long := make([]rune, 41)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE memberships SET trade=$3 WHERE user_id=$1 AND salon_id=$2`,
		"u-it-f2c-pro", "s-it-f2c", string(long)); err == nil {
		t.Fatal("el CHECK de 40 caracteres tiene que frenar el UPDATE")
	}
}
