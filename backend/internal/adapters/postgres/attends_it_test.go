// F2a contra Postgres real (SALON_IT=1): la migración 015 y la regla de quién
// atiende en las queries (siempre scopeadas por salon_id).
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
	"salon-saas/internal/testutil/itdb"
)

// S-F2a-1: 015_membership_attends.sql agrega memberships.attends (not null,
// default false) y deja el comportamiento de hoy en las filas existentes:
// owner/admin/professional atendiendo, el resto no.
//
// Se ejecuta dentro de una transacción con SU PROPIO esquema y se revierte al
// final: la tabla memberships real ya tiene la columna, así que la migración
// solo se puede probar desde cero en un esquema aislado. Nada queda en la DB.
func TestMigration015_attends(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	applyMigrations(t, pool)

	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "015_membership_attends.sql"))
	if err != nil {
		t.Fatalf("leer la migración 015: %v", err)
	}
	sql := string(raw)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := fmt.Sprintf("f2a015_%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("esquema de prueba: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	// Estado pre-F2a: memberships sin la columna, con un rol por persona.
	if _, err := tx.Exec(ctx, `CREATE TABLE memberships (
		user_id  TEXT NOT NULL,
		salon_id TEXT NOT NULL,
		role     TEXT NOT NULL,
		PRIMARY KEY (user_id, salon_id)
	)`); err != nil {
		t.Fatalf("tabla previa: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships(user_id,salon_id,role) VALUES
		('u-own','s1','owner'),
		('u-adm','s1','admin'),
		('u-pro','s1','professional'),
		('u-rec','s1','reception'),
		('u-cli','s1','client')`); err != nil {
		t.Fatalf("filas previas: %v", err)
	}

	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("aplicar 015: %v", err)
	}

	// La columna existe, es NOT NULL y su default es false.
	var nullable, columnDefault string
	if err := tx.QueryRow(ctx, `SELECT is_nullable, COALESCE(column_default,'')
		FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='memberships' AND column_name='attends'`, schema,
	).Scan(&nullable, &columnDefault); err != nil {
		t.Fatalf("la columna attends no existe después de la 015: %v", err)
	}
	if nullable != "NO" || columnDefault != "false" {
		t.Fatalf("attends: nullable=%q default=%q (esperaba NO/false)", nullable, columnDefault)
	}

	// Backfill: no cambia lo que hay hoy.
	want := map[string]bool{
		"u-own": true, "u-adm": true, "u-pro": true,
		"u-rec": false, "u-cli": false,
	}
	rows, err := tx.Query(ctx, `SELECT user_id, attends FROM memberships`)
	if err != nil {
		t.Fatalf("leer backfill: %v", err)
	}
	got := map[string]bool{}
	for rows.Next() {
		var id string
		var attends bool
		if err := rows.Scan(&id, &attends); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[id] = attends
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("filas: %+v", got)
	}
	for id, attends := range want {
		if got[id] != attends {
			t.Fatalf("%s: attends=%v, esperaba %v (backfill: %+v)", id, got[id], attends, got)
		}
	}

	// Idempotente: una segunda pasada no pisa lo que la persona configuró (el
	// dueño que se apagó desde la pantalla de equipo sigue apagado).
	if _, err := tx.Exec(ctx, `UPDATE memberships SET attends=false WHERE user_id='u-own'`); err != nil {
		t.Fatalf("apagar al dueño: %v", err)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("segunda pasada: %v", err)
	}
	var ownerAttends bool
	if err := tx.QueryRow(ctx, `SELECT attends FROM memberships WHERE user_id='u-own'`).Scan(&ownerAttends); err != nil {
		t.Fatalf("releer dueño: %v", err)
	}
	if ownerAttends {
		t.Fatal("la segunda pasada no debe volver a encender al dueño")
	}
	// Y una fila nueva nace en false: la marca la pone el alta, no la migración.
	if _, err := tx.Exec(ctx, `INSERT INTO memberships(user_id,salon_id,role) VALUES ('u-nuevo','s1','owner')`); err != nil {
		t.Fatalf("fila nueva: %v", err)
	}
	var nuevo bool
	if err := tx.QueryRow(ctx, `SELECT attends FROM memberships WHERE user_id='u-nuevo'`).Scan(&nuevo); err != nil {
		t.Fatalf("releer fila nueva: %v", err)
	}
	if nuevo {
		t.Fatal("una fila insertada sin attends debe nacer en false")
	}
}

// S-F2a-3: el adaptador Postgres. ListStaff devuelve role='professional' OR
// attends; Find lee la marca; SetAttends y ListMembers van scopeados por salón.
func TestMemberships_IT_attends(t *testing.T) {
	pool, ctx := itdb.Round2Fixtures(t)
	mems := Memberships{Pool: pool}
	mustExec := func(q string) {
		t.Helper()
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture %q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users(id,name,email,pass_hash) VALUES
		('u-it-r2-adm','Carla Adm','u-it-r2-adm@test.com','x'),
		('u-it-r2-rec','Dani Recep','u-it-r2-rec@test.com','x')
		ON CONFLICT (id) DO NOTHING`)

	// Save/Find: la marca viaja y el Find está scopeado por salón.
	if err := mems.Save(domain.Membership{UserID: "u-it-r2-owner", SalonID: "s-it-r2", Role: domain.RoleOwner}); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if err := mems.Save(domain.Membership{UserID: "u-it-r2-owner2", SalonID: "s-it-r2", Role: domain.RoleOwner, Attends: true}); err != nil {
		t.Fatalf("owner-barbero: %v", err)
	}
	if err := mems.Save(domain.Membership{UserID: "u-it-r2-prof", SalonID: "s-it-r2", Role: domain.RoleProfessional, Attends: true}); err != nil {
		t.Fatalf("profesional: %v", err)
	}
	if err := mems.Save(domain.Membership{UserID: "u-it-r2-adm", SalonID: "s-it-r2", Role: domain.RoleAdmin}); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if err := mems.Save(domain.Membership{UserID: "u-it-r2-rec", SalonID: "s-it-r2", Role: domain.RoleReception, Attends: true}); err != nil {
		t.Fatalf("recepción: %v", err)
	}

	prof, err := mems.Find("u-it-r2-prof", "s-it-r2")
	if err != nil {
		t.Fatalf("find profesional: %v", err)
	}
	if !prof.Attends || !prof.Bookable() {
		t.Fatalf("el profesional atiende por rol: %+v", prof)
	}
	owner, err := mems.Find("u-it-r2-owner", "s-it-r2")
	if err != nil {
		t.Fatalf("find owner: %v", err)
	}
	if owner.Attends || owner.Bookable() {
		t.Fatalf("el owner guardado con attends=false no atiende: %+v", owner)
	}
	if _, err := mems.Find("u-it-r2-prof", "s-it-r2b"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el find tiene que estar scopeado por salón: %v", err)
	}

	// ListStaff: solo quien atiende (profesional, owner-barbero). Fuera el owner
	// apagado, la administración y recepción (aunque tenga la marca).
	staff, err := mems.ListStaff("s-it-r2")
	if err != nil {
		t.Fatalf("listar staff: %v", err)
	}
	ids := make([]string, 0, len(staff))
	for _, m := range staff {
		ids = append(ids, m.ID)
		if !m.Bookable() {
			t.Fatalf("%s no atiende y aparece en la agenda: %+v", m.ID, staff)
		}
	}
	if len(ids) != 2 || ids[0] != "u-it-r2-prof" || ids[1] != "u-it-r2-owner2" {
		t.Fatalf("staff (por nombre): %v", ids)
	}
	if other, err := mems.ListStaff("s-it-r2b"); err != nil || len(other) != 0 {
		t.Fatalf("el otro salón no ve este equipo: %+v %v", other, err)
	}

	// SetAttends enciende al owner y lo vuelve a poner en la agenda.
	if err := mems.SetAttends("u-it-r2-owner", "s-it-r2", true); err != nil {
		t.Fatalf("encender al owner: %v", err)
	}
	if got, _ := mems.Find("u-it-r2-owner", "s-it-r2"); !got.Attends {
		t.Fatalf("la marca no se persistió: %+v", got)
	}
	if staff, _ := mems.ListStaff("s-it-r2"); len(staff) != 3 {
		t.Fatalf("el owner que atiende vuelve a la agenda: %+v", staff)
	}
	// Scopeado: el mismo UPDATE sobre otro salón no toca nada.
	if err := mems.SetAttends("u-it-r2-owner", "s-it-r2b", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SetAttends de otro salón: %v", err)
	}
	if got, _ := mems.Find("u-it-r2-owner", "s-it-r2"); !got.Attends {
		t.Fatalf("la marca del salón real no se toca: %+v", got)
	}

	// ListMembers lleva attends y sigue scopeado.
	members, err := mems.ListMembers("s-it-r2")
	if err != nil {
		t.Fatalf("listar miembros: %v", err)
	}
	attends := map[string]bool{}
	for _, m := range members {
		attends[m.ID] = m.Attends
	}
	// ListMembers muestra la marca cruda de la fila (la recepción quedó encendida
	// a mano en este fixture a propósito: ListStaff la ignora igual).
	if len(attends) != 5 || !attends["u-it-r2-owner"] || attends["u-it-r2-adm"] || !attends["u-it-r2-rec"] {
		t.Fatalf("attends del equipo: %+v", attends)
	}
	if other, err := mems.ListMembers("s-it-r2b"); err != nil || len(other) != 0 {
		t.Fatalf("el otro salón no ve este equipo: %+v %v", other, err)
	}
}
