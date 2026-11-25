// Integración con Postgres real del registro de auditoría (F10, D1..D3). Se
// saltea sin SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la DB: la
// migración 034 dos veces sin tocar datos previos, los CHECK, el trigger de
// append-only (UPDATE/DELETE directos fallan, el borrado en cascada de un salón
// funciona), el orden y el cursor con empates, el filtro de acción (igual a la
// regla del dominio) y el aislamiento por salón.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

const (
	audSalon  = "s-it-aud"
	audSalon2 = "s-it-aud2"
	audUser   = "u-it-aud"
)

type audFix struct {
	pool *pgxpool.Pool
	ctx  context.Context
	repo Audit
}

func audFixtures(t *testing.T) *audFix {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-aud%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-aud%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-aud','Ana Auditada','audit-it@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-it-aud','s-it-aud','Salón Auditoría IT')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-it-aud2','s-it-aud2','Salón Auditoría IT 2')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		// Borrar un salón con eventos tiene que funcionar (cascada permitida).
		if _, err := pool.Exec(context.Background(), `DELETE FROM salons WHERE id LIKE 's-it-aud%'`); err != nil {
			t.Errorf("borrar el salón con su auditoría: %v", err)
		}
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id LIKE 'u-it-aud%'`)
	})
	return &audFix{pool: pool, ctx: ctx, repo: Audit{Pool: pool}}
}

func (f *audFix) event(t *testing.T, salon, action, entity string, at time.Time) domain.AuditEvent {
	t.Helper()
	e, err := domain.NewAuditEvent(salon, audUser, "Ana Auditada", action, entity, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Append(e); err != nil {
		t.Fatalf("Append(%s): %v", action, err)
	}
	return e
}

func (f *audFix) count(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT COUNT(*)::int FROM audit_events WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// --- Migración 034 dos veces ----------------------------------------------------

// md5Of resume el contenido de una tabla en un hash: si la migración tocara una
// fila, cambiaría.
func (f *audFix) md5Of(t *testing.T, table string) string {
	t.Helper()
	var h *string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT md5(COALESCE(string_agg(x::text, '|' ORDER BY x::text), '')) FROM `+table+` x`).Scan(&h); err != nil {
		t.Fatalf("md5 %s: %v", table, err)
	}
	return *h
}

func TestAuditIT_migration034TwiceKeepsPriorData(t *testing.T) {
	f := audFixtures(t)
	// Datos previos en users, memberships y sales (más un evento ya escrito).
	for _, q := range []string{
		`INSERT INTO memberships(user_id,salon_id,role) VALUES('u-it-aud','s-it-aud','owner')`,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method)
		 VALUES('sale-it-aud','s-it-aud','u-it-aud','Clienta',1000,'cash')`,
	} {
		if _, err := f.pool.Exec(f.ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	f.event(t, audSalon, "member.add", "m1", time.Now().UTC())
	before := map[string]string{}
	tables := []string{"users", "memberships", "sales", "audit_events"}
	for _, tb := range tables {
		before[tb] = f.md5Of(t, tb)
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "034_audit_events.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := f.pool.Exec(f.ctx, string(sql)); err != nil {
			t.Fatalf("034 pasada %d: %v", pass, err)
		}
	}
	for _, tb := range tables {
		if got := f.md5Of(t, tb); got != before[tb] {
			t.Errorf("la migración cambió datos de %s", tb)
		}
	}
	// Quedó UN solo trigger y UN solo índice (no se duplican al reaplicar).
	var triggers, indexes int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*)::int FROM pg_trigger WHERE tgrelid='audit_events'::regclass AND NOT tgisinternal`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*)::int FROM pg_indexes WHERE tablename='audit_events' AND indexname='audit_events_salon_created_idx'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if triggers != 1 || indexes != 1 {
		t.Fatalf("triggers=%d índices=%d, quería 1 y 1", triggers, indexes)
	}
	// Y el trigger sigue vigente tras reaplicar.
	if _, err := f.pool.Exec(f.ctx, `UPDATE audit_events SET action='x.y' WHERE salon_id='s-it-aud'`); err == nil {
		t.Fatal("el trigger debe seguir rechazando el UPDATE tras reaplicar la migración")
	}
}

// --- CHECK y columnas --------------------------------------------------------------

func TestAuditIT_checksAndNullable(t *testing.T) {
	f := audFixtures(t)
	ins := func(id, action string, entity any) error {
		_, err := f.pool.Exec(f.ctx,
			`INSERT INTO audit_events(id,salon_id,actor_user_id,action,entity_id) VALUES($1,'s-it-aud','u-it-aud',$2,$3)`, id, action, entity)
		return err
	}
	if err := ins("c-ok60", strings.Repeat("a", 60), strings.Repeat("e", 100)); err != nil {
		t.Fatalf("60/100 deben valer: %v", err)
	}
	if err := ins("c-null", "a.b", nil); err != nil {
		t.Fatalf("entity_id NULL debe valer: %v", err)
	}
	for name, err := range map[string]error{
		"acción vacía":   ins("c-0", "", nil),
		"acción de 61":   ins("c-61", strings.Repeat("a", 61), nil),
		"entidad de 101": ins("c-e101", "a.b", strings.Repeat("e", 101)),
		"id duplicado":   ins("c-ok60", "a.b", nil),
	} {
		var pe *pgconn.PgError
		if err == nil || !errors.As(err, &pe) || pe.Code[:2] != "23" {
			t.Errorf("%s: esperaba violación de integridad, obtuve %v", name, err)
		}
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO audit_events(id,salon_id,action) VALUES('c-noactor','s-it-aud','a.b')`); err == nil {
		t.Error("actor_user_id es NOT NULL")
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO audit_events(id,salon_id,actor_user_id,action) VALUES('c-nosalon','no-existe','u-it-aud','a.b')`); err == nil {
		t.Error("el salón debe existir (FK)")
	}
	var created time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT created_at FROM audit_events WHERE id='c-null'`).Scan(&created); err != nil || time.Since(created) > time.Minute {
		t.Errorf("created_at por defecto now(): %v %v", created, err)
	}
}

// --- Append-only ---------------------------------------------------------------------

func TestAuditIT_appendOnlyTrigger(t *testing.T) {
	f := audFixtures(t)
	e := f.event(t, audSalon, "member.add", "m1", time.Now().UTC())
	for name, q := range map[string]string{
		"UPDATE de una columna": `UPDATE audit_events SET action='otra.cosa' WHERE id=$1`,
		"UPDATE del nombre":     `UPDATE audit_events SET actor_name='Otra' WHERE id=$1`,
		"UPDATE del created_at": `UPDATE audit_events SET created_at=now() WHERE id=$1`,
		"DELETE directo":        `DELETE FROM audit_events WHERE id=$1`,
	} {
		_, err := f.pool.Exec(f.ctx, q, e.ID)
		var pe *pgconn.PgError
		if err == nil || !errors.As(err, &pe) || pe.Code != "23001" || !strings.Contains(pe.Message, "solo escritura") {
			t.Errorf("%s: esperaba el rechazo del trigger (23001), obtuve %v", name, err)
		}
	}
	// DELETE masivo del salón entero tampoco pasa mientras el salón existe.
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM audit_events WHERE salon_id=$1`, audSalon); err == nil {
		t.Error("borrar todos los eventos de un salón que existe debe fallar")
	}
	// Y el evento quedó intacto.
	got, err := f.repo.List(audSalon, domain.AuditQuery{Limit: 10})
	if err != nil || len(got) != 1 || got[0].Action != "member.add" || got[0].ActorName != "Ana Auditada" {
		t.Fatalf("el evento debe quedar intacto: %+v %v", got, err)
	}
}

func TestAuditIT_deletingSalonCascadesEvents(t *testing.T) {
	f := audFixtures(t)
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		f.event(t, audSalon, "member.add", fmt.Sprintf("m%d", i), now)
	}
	f.event(t, audSalon2, "member.add", "otro", now)
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM salons WHERE id=$1`, audSalon); err != nil {
		t.Fatalf("borrar un salón con eventos debe funcionar: %v", err)
	}
	if n := f.count(t, `salon_id=$1`, audSalon); n != 0 {
		t.Fatalf("los eventos del salón borrado deben irse en cascada: quedan %d", n)
	}
	if n := f.count(t, `salon_id=$1`, audSalon2); n != 1 {
		t.Fatalf("los eventos de otro salón no se tocan: %d", n)
	}
	// Dentro de una transacción con el evento recién escrito también funciona.
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err := tx.Exec(f.ctx,
		`INSERT INTO audit_events(id,salon_id,actor_user_id,action) VALUES('tx-1',$1,'u-it-aud','a.b')`, audSalon2); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx, `DELETE FROM salons WHERE id=$1`, audSalon2); err != nil {
		t.Fatalf("cascada con evento de la misma transacción: %v", err)
	}
	var n int
	if err := tx.QueryRow(f.ctx, `SELECT COUNT(*)::int FROM audit_events WHERE salon_id=$1`, audSalon2).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tras la cascada no quedan eventos: %d %v", n, err)
	}
}

// --- Repositorio ----------------------------------------------------------------------

func TestAuditIT_appendStoresSnapshotAndNulls(t *testing.T) {
	f := audFixtures(t)
	at := time.Date(2026, 5, 1, 10, 0, 0, 123456000, time.UTC)
	e1 := f.event(t, audSalon, "settings.update", "", at)
	noName, err := domain.NewAuditEvent(audSalon, audUser, "", "member.update", "m9", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Append(noName); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.List(audSalon, domain.AuditQuery{Limit: 10})
	if err != nil || len(got) != 2 {
		t.Fatalf("esperaba 2 eventos: %+v %v", got, err)
	}
	if got[0].Action != "member.update" || got[0].ActorName != "" || got[0].EntityID != "m9" {
		t.Errorf("sin nombre: %+v", got[0])
	}
	if got[1].ID != e1.ID || got[1].EntityID != "" || got[1].ActorName != "Ana Auditada" ||
		got[1].ActorUserID != audUser || got[1].SalonID != audSalon || !got[1].CreatedAt.Equal(at) {
		t.Errorf("con nombre y sin entidad: %+v", got[1])
	}
	var nameNull, entNull bool
	if err := f.pool.QueryRow(f.ctx,
		`SELECT actor_name IS NULL, entity_id IS NULL FROM audit_events WHERE id=$1`, noName.ID).Scan(&nameNull, &entNull); err != nil || !nameNull || entNull {
		t.Errorf("nombre vacío = NULL: %v %v %v", nameNull, entNull, err)
	}
}

func TestAuditIT_listOrderPaginationAndTies(t *testing.T) {
	f := audFixtures(t)
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	// 12 eventos: 4 con la MISMA marca de tiempo (empate) y 8 distintos.
	var all []domain.AuditEvent
	for i := 0; i < 8; i++ {
		all = append(all, f.event(t, audSalon, "sale.x", fmt.Sprintf("d%d", i), base.Add(time.Duration(i)*time.Minute)))
	}
	for i := 0; i < 4; i++ {
		all = append(all, f.event(t, audSalon, "sale.x", fmt.Sprintf("t%d", i), base.Add(30*time.Minute)))
	}
	// Eventos de otro salón mezclados en el tiempo: nunca aparecen.
	for i := 0; i < 3; i++ {
		f.event(t, audSalon2, "sale.x", "ajeno", base.Add(time.Duration(i)*time.Minute))
	}
	var seen []domain.AuditEvent
	var cursor *domain.AuditCursor
	pages := 0
	for {
		rows, err := f.repo.List(audSalon, domain.AuditQuery{Limit: 5, Before: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		pages++
		seen = append(seen, rows...)
		last := rows[len(rows)-1]
		cursor = &domain.AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		if pages > 10 {
			t.Fatal("no termina")
		}
	}
	if len(seen) != 12 || pages != 3 {
		t.Fatalf("esperaba 12 eventos en 3 páginas, vi %d en %d", len(seen), pages)
	}
	ids := map[string]bool{}
	for i, e := range seen {
		if ids[e.ID] {
			t.Fatalf("evento repetido %s", e.ID)
		}
		ids[e.ID] = true
		if e.SalonID != audSalon {
			t.Fatalf("fuga entre salones: %+v", e)
		}
		if i > 0 {
			p := seen[i-1]
			if e.CreatedAt.After(p.CreatedAt) || (e.CreatedAt.Equal(p.CreatedAt) && e.ID >= p.ID) {
				t.Fatalf("orden roto en %d: %v/%s tras %v/%s", i, e.CreatedAt, e.ID, p.CreatedAt, p.ID)
			}
		}
	}
	for _, e := range all {
		if !ids[e.ID] {
			t.Errorf("falta %s", e.EntityID)
		}
	}
	// Límite exacto.
	rows, err := f.repo.List(audSalon, domain.AuditQuery{Limit: 3})
	if err != nil || len(rows) != 3 {
		t.Fatalf("límite 3: %d %v", len(rows), err)
	}
	// Salón sin eventos.
	rows, err = f.repo.List("s-no-existe", domain.AuditQuery{Limit: 3})
	if err != nil || len(rows) != 0 {
		t.Fatalf("salón sin eventos: %d %v", len(rows), err)
	}
}

// El filtro de SQL tiene que coincidir con la regla del dominio, incluidos los
// comodines de LIKE (% y _), que NO son comodines acá.
func TestAuditIT_actionFilterMatchesDomainRule(t *testing.T) {
	f := audFixtures(t)
	actions := []string{"member.add", "member.update", "memberx.add", "member", "payroll_run.pay", "payrollXrun.pay", "export.sales", "a%b.c", "a.b.c"}
	now := time.Now().UTC()
	for i, a := range actions {
		f.event(t, audSalon, a, "", now.Add(time.Duration(i)*time.Second))
	}
	filters := []string{"", "member", "member.add", "mem", "member.", "payroll_run", "payroll_run.pay", "p%", "%", "_", "a", "a.b", "a%b", "export", "a.b.c", "Member"}
	for _, flt := range filters {
		rows, err := f.repo.List(audSalon, domain.AuditQuery{Limit: 100, Action: flt})
		if err != nil {
			t.Fatalf("filtro %q: %v", flt, err)
		}
		got := map[string]bool{}
		for _, r := range rows {
			got[r.Action] = true
		}
		for _, a := range actions {
			if want := domain.AuditActionMatches(flt, a); got[a] != want {
				t.Errorf("filtro %q sobre %q: SQL=%v dominio=%v", flt, a, got[a], want)
			}
		}
	}
}
