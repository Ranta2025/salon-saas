// La migración 021 (invitaciones) vive con el resto de las migraciones: este
// test la corre en un esquema aislado y no usa el adaptador.
package pgstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// S-F2g1-1: la migración 021 es idempotente y crea lo que el contrato pide.
// Se corre en un esquema aislado (la tabla real ya existe) con un `salons`
// mínimo, y se revierte al final.
func TestMigration021_invitations(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	applyMigrations(t, pool)

	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "021_invitations.sql"))
	if err != nil {
		t.Fatalf("leer la migración 021: %v", err)
	}
	sql := string(raw)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := fmt.Sprintf("f2g1_021_%d", time.Now().UnixNano())
	for _, q := range []string{
		"CREATE SCHEMA " + schema,
		"SET LOCAL search_path TO " + schema,
		`CREATE TABLE salons (id TEXT PRIMARY KEY)`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}

	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("aplicar 021: %v", err)
	}
	// Columnas del contrato (S-F2g1-1).
	want := map[string]string{
		"id": "NO", "salon_id": "NO", "email": "NO", "role": "NO", "attends": "NO",
		"token_hash": "NO", "expires_at": "NO", "accepted_at": "YES", "accepted_by": "NO",
		"revoked_at": "YES", "created_by": "NO", "created_at": "NO",
	}
	rows, err := tx.Query(ctx, `SELECT column_name, is_nullable FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='invitations'`, schema)
	if err != nil {
		t.Fatalf("columnas: %v", err)
	}
	got := map[string]string{}
	for rows.Next() {
		var name, nullable string
		if err := rows.Scan(&name, &nullable); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = nullable
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("columnas de invitations: %+v", got)
	}
	for name, nullable := range want {
		if got[name] != nullable {
			t.Fatalf("columna %s: is_nullable=%q (esperaba %q)", name, got[name], nullable)
		}
	}

	// Índices: único de token_hash y único PARCIAL de la pendiente por email.
	var tokenUnique, pendingUnique, pendingPredicate string
	if err := tx.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
		WHERE schemaname=$1 AND indexname='invitations_token_hash_key'`, schema).Scan(&tokenUnique); err != nil {
		t.Fatalf("índice de token_hash: %v", err)
	}
	if !containsAll(tokenUnique, "UNIQUE", "token_hash") {
		t.Fatalf("token_hash tiene que ser único: %q", tokenUnique)
	}
	if err := tx.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
		WHERE schemaname=$1 AND indexname='invitations_pending_email_idx'`, schema).Scan(&pendingUnique); err != nil {
		t.Fatalf("índice de pendientes: %v", err)
	}
	if !containsAll(pendingUnique, "UNIQUE", "lower(email)", "accepted_at IS NULL", "revoked_at IS NULL") {
		t.Fatalf("índice de la pendiente por email: %q", pendingUnique)
	}
	_ = pendingPredicate

	// La FK del salón borra sus invitaciones con el salón.
	var deleteRule string
	if err := tx.QueryRow(ctx, `SELECT rc.delete_rule FROM information_schema.referential_constraints rc
		JOIN information_schema.table_constraints tc ON tc.constraint_name = rc.constraint_name
		WHERE tc.table_schema=$1 AND tc.table_name='invitations'`, schema).Scan(&deleteRule); err != nil {
		t.Fatalf("FK del salón: %v", err)
	}
	if deleteRule != "CASCADE" {
		t.Fatalf("la FK del salón tiene que ser ON DELETE CASCADE: %q", deleteRule)
	}

	// Idempotente: una segunda pasada no falla y no duplica índices.
	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("segunda pasada: %v", err)
	}
	var indexes int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM pg_indexes
		WHERE schemaname=$1 AND tablename='invitations'`, schema).Scan(&indexes); err != nil {
		t.Fatalf("contar índices: %v", err)
	}
	if indexes != 3 { // primary key + token_hash + pendiente por email
		t.Fatalf("índices después de reaplicar: %d (esperaba 3)", indexes)
	}
}

// containsAll exige que el texto traiga todas las partes (el DDL se compara por
// fragmentos porque el formato del dump cambia).
func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		found := false
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
