// Integración con Postgres real del 2FA (F10, D6 y D9). Se saltea sin
// SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la DB: la migración 035
// dos veces sin tocar datos previos, cada CHECK/FK/UNIQUE, y que cada operación
// del adaptador haga lo que dice su sentencia SQL (UPDATE atómico del intento,
// `last_step < paso`, `used_at IS NULL`, transacciones de enable y disable).
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

const (
	tfUser  = "u-it-tf"
	tfUser2 = "u-it-tf2"
)

type tfFix struct {
	pool *pgxpool.Pool
	ctx  context.Context
	repo TwoFactor
}

func tfFixtures(t *testing.T) *tfFix {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)
	for _, q := range []string{
		`DELETE FROM users WHERE id LIKE 'u-it-tf%'`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-tf','Tefe Uno','tf-it-1@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-tf2','Tefe Dos','tf-it-2@test.com','x')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id LIKE 'u-it-tf%'`) })
	return &tfFix{pool: pool, ctx: ctx, repo: TwoFactor{Pool: pool}}
}

func (f *tfFix) hashes(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = domain.HashRecoveryCode(fmt.Sprintf("%s%05d", prefix, i))
	}
	return out
}

// enabled deja a tfUser con el 2FA activo y n códigos de recuperación.
func (f *tfFix) enabled(t *testing.T, user string, step int64, codes int) []string {
	t.Helper()
	if err := f.repo.SavePending(user, []byte("cifrado-"+user)); err != nil {
		t.Fatalf("SavePending: %v", err)
	}
	h := f.hashes(codes, user)
	if err := f.repo.Enable(user, []byte("cifrado-"+user), step, h); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	return h
}

func (f *tfFix) row(t *testing.T, user string) (enabled bool, lastStep int64, failed int, locked bool) {
	t.Helper()
	err := f.pool.QueryRow(f.ctx,
		`SELECT enabled, last_step, failed_count, locked_until IS NOT NULL FROM user_totp WHERE user_id=$1`, user).
		Scan(&enabled, &lastStep, &failed, &locked)
	if err != nil {
		t.Fatalf("leer fila: %v", err)
	}
	return
}

func (f *tfFix) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// D6: la migración 035 dos veces no toca datos previos (md5 de users,
// memberships, sales y audit_events idénticos antes y después) y las tablas
// quedan como el contrato.
func TestTwoFactor_migrationTwiceKeepsData(t *testing.T) {
	f := tfFixtures(t)
	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "035_two_factor.sql"))
	if err != nil {
		t.Fatal(err)
	}
	digest := func() string {
		var out string
		err := f.pool.QueryRow(f.ctx, `SELECT
		  (SELECT md5(COALESCE(string_agg(u::text, '|' ORDER BY u.id), '')) FROM users u) || '/' ||
		  (SELECT md5(COALESCE(string_agg(m::text, '|' ORDER BY m.user_id, m.salon_id), '')) FROM memberships m) || '/' ||
		  (SELECT md5(COALESCE(string_agg(s::text, '|' ORDER BY s.id), '')) FROM sales s) || '/' ||
		  (SELECT md5(COALESCE(string_agg(a::text, '|' ORDER BY a.id), '')) FROM audit_events a)`).Scan(&out)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := digest()
	// Estado de 2FA existente: la re-aplicación tampoco lo pisa.
	f.enabled(t, tfUser, 7, 3)
	for pass := 1; pass <= 2; pass++ {
		if _, err := f.pool.Exec(f.ctx, string(sql)); err != nil {
			t.Fatalf("pasada %d de 035: %v", pass, err)
		}
	}
	if enabled, step, _, _ := f.row(t, tfUser); !enabled || step != 7 {
		t.Errorf("la re-aplicación pisó el 2FA: enabled=%v step=%d", enabled, step)
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser); n != 3 {
		t.Errorf("la re-aplicación tocó los códigos: %d", n)
	}
	// users cambió solo por el fixture? No: 035 no toca users; el digest de las 4
	// tablas debe ser idéntico (la fila de tfUser2/tfUser ya estaba en `before`).
	if after := digest(); after != before {
		t.Errorf("035 cambió datos previos:\nantes   %s\ndespués %s", before, after)
	}
	// Columnas y defaults del contrato.
	var enabled bool
	var step int64
	var failed int
	var lockedNull, enabledAtNull bool
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO user_totp(user_id, secret_enc) VALUES($1, '\x01')`, tfUser2); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT enabled,last_step,failed_count,locked_until IS NULL,enabled_at IS NULL FROM user_totp WHERE user_id=$1`, tfUser2).
		Scan(&enabled, &step, &failed, &lockedNull, &enabledAtNull); err != nil {
		t.Fatal(err)
	}
	if enabled || step != 0 || failed != 0 || !lockedNull || !enabledAtNull {
		t.Errorf("defaults: %v %d %d %v %v", enabled, step, failed, lockedNull, enabledAtNull)
	}
}

// D6: cada CHECK, FK y UNIQUE probado a mano.
func TestTwoFactor_constraints(t *testing.T) {
	f := tfFixtures(t)
	exec := func(q string, args ...any) error { _, err := f.pool.Exec(f.ctx, q, args...); return err }
	wantCode := func(name, code string, err error) {
		t.Helper()
		if got := pgCode(err); got != code {
			t.Errorf("%s: código %q, esperado %q (err=%v)", name, got, code, err)
		}
	}
	wantCode("secreto vacío", "23514", exec(`INSERT INTO user_totp(user_id, secret_enc) VALUES($1, '\x')`, tfUser))
	wantCode("last_step negativo", "23514", exec(`INSERT INTO user_totp(user_id, secret_enc, last_step) VALUES($1, '\x01', -1)`, tfUser))
	wantCode("failed_count negativo", "23514", exec(`INSERT INTO user_totp(user_id, secret_enc, failed_count) VALUES($1, '\x01', -1)`, tfUser))
	wantCode("enabled sin enabled_at", "23514", exec(`INSERT INTO user_totp(user_id, secret_enc, enabled) VALUES($1, '\x01', true)`, tfUser))
	wantCode("usuario inexistente", "23503", exec(`INSERT INTO user_totp(user_id, secret_enc) VALUES('no-existe', '\x01')`))
	wantCode("sin secreto (NULL)", "23502", exec(`INSERT INTO user_totp(user_id, secret_enc) VALUES($1, NULL)`, tfUser))
	if n := f.count(t, `SELECT count(*) FROM user_totp WHERE user_id=$1`, tfUser); n != 0 {
		t.Fatalf("un INSERT inválido dejó fila: %d", n)
	}
	if err := exec(`INSERT INTO user_totp(user_id, secret_enc, enabled, enabled_at) VALUES($1, '\x01', true, now())`, tfUser); err != nil {
		t.Fatalf("válido: %v", err)
	}
	wantCode("PK user_id duplicada", "23505", exec(`INSERT INTO user_totp(user_id, secret_enc) VALUES($1, '\x02')`, tfUser))
	wantCode("UPDATE a enabled sin enabled_at", "23514", exec(`UPDATE user_totp SET enabled_at=NULL WHERE user_id=$1`, tfUser))

	h := domain.HashRecoveryCode("ABCDEFGHIJ")
	wantCode("hash corto", "23514", exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('r1',$1,'abc')`, tfUser))
	wantCode("hash largo", "23514", exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('r1',$1,$2)`, tfUser, h+"0"))
	wantCode("usuario inexistente (código)", "23503", exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('r1','no-existe',$1)`, h))
	wantCode("sin hash (NULL)", "23502", exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('r1',$1,NULL)`, tfUser))
	if err := exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('it-tf-r1',$1,$2)`, tfUser, h); err != nil {
		t.Fatalf("válido: %v", err)
	}
	wantCode("UNIQUE (user_id, code_hash)", "23505", exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('it-tf-r2',$1,$2)`, tfUser, h))
	wantCode("PK id duplicada", "23505", exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('it-tf-r1',$1,$2)`, tfUser, domain.HashRecoveryCode("ZZZZZZZZZZ")))
	// El mismo hash para OTRO usuario sí se puede.
	if err := exec(`INSERT INTO user_recovery_codes(id,user_id,code_hash) VALUES('it-tf-r3',$1,$2)`, tfUser2, h); err != nil {
		t.Fatalf("mismo hash en otro usuario: %v", err)
	}
	var usedNull bool
	if err := f.pool.QueryRow(f.ctx, `SELECT used_at IS NULL FROM user_recovery_codes WHERE id='it-tf-r1'`).Scan(&usedNull); err != nil || !usedNull {
		t.Errorf("used_at por defecto NULL: %v %v", usedNull, err)
	}
	// ON DELETE CASCADE de users: borra la fila y los códigos.
	if err := exec(`DELETE FROM users WHERE id=$1`, tfUser); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM user_totp WHERE user_id=$1`, tfUser) + f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser); n != 0 {
		t.Errorf("la cascada de users no borró el 2FA: %d", n)
	}
}

func TestTwoFactor_findAndSavePending(t *testing.T) {
	f := tfFixtures(t)
	if _, err := f.repo.Find(tfUser); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sin fila: %v", err)
	}
	if err := f.repo.SavePending(tfUser, []byte("uno")); err != nil {
		t.Fatal(err)
	}
	st, err := f.repo.Find(tfUser)
	if err != nil || st.Enabled || st.LastStep != 0 || string(st.SecretEnc) != "uno" {
		t.Fatalf("pendiente: %+v %v", st, err)
	}
	// Un pendiente se REEMPLAZA y reinicia los contadores.
	if _, err := f.pool.Exec(f.ctx, `UPDATE user_totp SET failed_count=3, last_step=9 WHERE user_id=$1`, tfUser); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SavePending(tfUser, []byte("dos")); err != nil {
		t.Fatal(err)
	}
	st, _ = f.repo.Find(tfUser)
	if string(st.SecretEnc) != "dos" || st.LastStep != 0 {
		t.Errorf("reemplazo: %+v", st)
	}
	if _, _, failed, _ := f.row(t, tfUser); failed != 0 {
		t.Errorf("el reemplazo reinicia failed_count: %d", failed)
	}
	// Activo: NO se pisa.
	f.enabled(t, tfUser, 5, 2)
	if err := f.repo.SavePending(tfUser, []byte("tres")); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("setup sobre activo: %v", err)
	}
	st, _ = f.repo.Find(tfUser)
	if !st.Enabled || string(st.SecretEnc) != "cifrado-"+tfUser || st.LastStep != 5 {
		t.Errorf("el setup rechazado pisó el activo: %+v", st)
	}
	// Usuario inexistente: error de dominio, no SQL crudo.
	if err := f.repo.SavePending("no-existe", []byte("x")); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("FK: %v", err)
	}
}

func TestTwoFactor_enable(t *testing.T) {
	f := tfFixtures(t)
	// Sin enrolamiento.
	if err := f.repo.Enable(tfUser, []byte("x"), 1, f.hashes(8, "a")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sin setup: %v", err)
	}
	if err := f.repo.SavePending(tfUser, []byte("sec")); err != nil {
		t.Fatal(err)
	}
	// Secreto distinto (el setup se reemplazó entre medio): no activa y no deja códigos.
	if err := f.repo.Enable(tfUser, []byte("otro"), 1, f.hashes(8, "a")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("secreto distinto: %v", err)
	}
	if en, _, _, _ := f.row(t, tfUser); en {
		t.Error("no debía activar")
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser); n != 0 {
		t.Errorf("códigos sin activar: %d", n)
	}
	// Transacción: un hash duplicado rompe el INSERT y NO deja activado.
	dup := f.hashes(8, "a")
	dup[7] = dup[0]
	if err := f.repo.Enable(tfUser, []byte("sec"), 4, dup); err == nil {
		t.Fatal("hashes duplicados deberían fallar")
	}
	if en, step, _, _ := f.row(t, tfUser); en || step != 0 {
		t.Errorf("la transacción no se deshizo: enabled=%v step=%d", en, step)
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser); n != 0 {
		t.Errorf("códigos a medias: %d", n)
	}
	// Bien.
	good := f.hashes(8, "b")
	if err := f.repo.Enable(tfUser, []byte("sec"), 4, good); err != nil {
		t.Fatal(err)
	}
	st, _ := f.repo.Find(tfUser)
	if !st.Enabled || st.LastStep != 4 {
		t.Errorf("estado: %+v", st)
	}
	if n := f.count(t, `SELECT count(*) FROM user_totp WHERE user_id=$1 AND enabled_at IS NOT NULL AND failed_count=0 AND locked_until IS NULL`, tfUser); n != 1 {
		t.Error("enabled_at fijado y contadores en cero")
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND used_at IS NULL`, tfUser); n != 8 {
		t.Errorf("8 códigos sin usar, hay %d", n)
	}
	// Los códigos se guardan solo como hash (64 hex), no en claro.
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND code_hash ~ '^[0-9a-f]{64}$'`, tfUser); n != 8 {
		t.Errorf("hashes sha256 hex: %d", n)
	}
	// Otra vez: 409 y los códigos siguen siendo los mismos 8.
	if err := f.repo.Enable(tfUser, []byte("sec"), 9, f.hashes(8, "c")); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("segundo enable: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND code_hash = ANY($2)`, tfUser, good); n != 8 {
		t.Errorf("el segundo enable cambió los códigos: %d", n)
	}
	if _, step, _, _ := f.row(t, tfUser); step != 4 {
		t.Errorf("last_step cambió: %d", step)
	}
}

func TestTwoFactor_beginAttempt(t *testing.T) {
	f := tfFixtures(t)
	if _, err := f.repo.BeginAttempt(tfUser); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sin fila: %v", err)
	}
	if err := f.repo.SavePending(tfUser, []byte("sec")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.BeginAttempt(tfUser); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("pendiente no es 2FA activo: %v", err)
	}
	if _, _, failed, _ := f.row(t, tfUser); failed != 0 {
		t.Errorf("un pendiente no cuenta intentos: %d", failed)
	}
	f.enabled(t, tfUser, 11, 1)
	for i := 1; i <= 4; i++ {
		att, err := f.repo.BeginAttempt(tfUser)
		if err != nil {
			t.Fatalf("intento %d: %v", i, err)
		}
		if string(att.SecretEnc) != "cifrado-"+tfUser || att.LastStep != 11 {
			t.Errorf("devuelve secreto y last_step: %+v", att)
		}
		if _, _, failed, locked := f.row(t, tfUser); failed != i || locked {
			t.Errorf("tras %d: failed=%d locked=%v", i, failed, locked)
		}
	}
	// El 5.º se reserva y deja el bloqueo de 15 minutos puesto.
	if _, err := f.repo.BeginAttempt(tfUser); err != nil {
		t.Fatalf("el 5.º se reserva: %v", err)
	}
	var secs float64
	if err := f.pool.QueryRow(f.ctx, `SELECT extract(epoch FROM (locked_until - now())) FROM user_totp WHERE user_id=$1`, tfUser).Scan(&secs); err != nil {
		t.Fatal(err)
	}
	if secs < 14*60+50 || secs > 15*60+1 {
		t.Errorf("locked_until = now()+15min, faltan %.0fs", secs)
	}
	// Bloqueado: no cuenta y no devuelve el secreto.
	for i := 0; i < 3; i++ {
		att, err := f.repo.BeginAttempt(tfUser)
		if !errors.Is(err, domain.ErrTooManyAttempts) || att.SecretEnc != nil {
			t.Fatalf("bloqueado: %v %+v", err, att)
		}
	}
	if _, _, failed, _ := f.row(t, tfUser); failed != 5 {
		t.Errorf("bloqueado no incrementa: %d", failed)
	}
	// Bloqueo vencido: el contador arranca de cero y el bloqueo se limpia.
	if _, err := f.pool.Exec(f.ctx, `UPDATE user_totp SET locked_until = now() - interval '1 second' WHERE user_id=$1`, tfUser); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.BeginAttempt(tfUser); err != nil {
		t.Fatalf("bloqueo vencido: %v", err)
	}
	if _, _, failed, locked := f.row(t, tfUser); failed != 1 || locked {
		t.Errorf("tras vencer: failed=%d locked=%v", failed, locked)
	}
	// Otro usuario no se ve afectado.
	f.enabled(t, tfUser2, 0, 0)
	if _, _, failed, _ := f.row(t, tfUser2); failed != 0 {
		t.Errorf("aislamiento por usuario: %d", failed)
	}
}

func TestTwoFactor_completeStepAndReplay(t *testing.T) {
	f := tfFixtures(t)
	f.enabled(t, tfUser, 10, 1)
	_, _ = f.repo.BeginAttempt(tfUser)
	_, _ = f.repo.BeginAttempt(tfUser)
	if won, err := f.repo.CompleteStep(tfUser, 10); err != nil || won {
		t.Fatalf("paso == last_step es replay: %v %v", won, err)
	}
	if won, _ := f.repo.CompleteStep(tfUser, 9); won {
		t.Fatal("paso < last_step es replay")
	}
	if _, step, failed, _ := f.row(t, tfUser); step != 10 || failed != 2 {
		t.Errorf("un replay no cambia nada: step=%d failed=%d", step, failed)
	}
	if won, err := f.repo.CompleteStep(tfUser, 11); err != nil || !won {
		t.Fatalf("paso nuevo: %v %v", won, err)
	}
	if _, step, failed, locked := f.row(t, tfUser); step != 11 || failed != 0 || locked {
		t.Errorf("acierto: step=%d failed=%d locked=%v", step, failed, locked)
	}
	if won, _ := f.repo.CompleteStep(tfUser, 11); won {
		t.Error("el mismo paso dos veces")
	}
	// Sin 2FA activo: nada.
	if won, _ := f.repo.CompleteStep("no-existe", 99); won {
		t.Error("usuario sin fila")
	}
}

func TestTwoFactor_useRecoveryCode(t *testing.T) {
	f := tfFixtures(t)
	h := f.enabled(t, tfUser, 0, 3)
	f.enabled(t, tfUser2, 0, 3) // mismos hashes por prefijo distinto: no chocan
	for i := 0; i < 3; i++ {
		_, _ = f.repo.BeginAttempt(tfUser)
	}
	if won, err := f.repo.UseRecoveryCode(tfUser, domain.HashRecoveryCode("NOEXISTE00")); err != nil || won {
		t.Fatalf("código inexistente: %v %v", won, err)
	}
	if _, _, failed, _ := f.row(t, tfUser); failed != 3 {
		t.Errorf("un código inexistente no reinicia: %d", failed)
	}
	if won, err := f.repo.UseRecoveryCode(tfUser, h[0]); err != nil || !won {
		t.Fatalf("primer uso: %v %v", won, err)
	}
	if _, _, failed, locked := f.row(t, tfUser); failed != 0 || locked {
		t.Errorf("el acierto reinicia el contador: %d %v", failed, locked)
	}
	if won, _ := f.repo.UseRecoveryCode(tfUser, h[0]); won {
		t.Fatal("un código sirve UNA vez")
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND used_at IS NOT NULL`, tfUser); n != 1 {
		t.Errorf("solo uno marcado usado: %d", n)
	}
	// Scope por usuario: el código de otro no sirve.
	if won, _ := f.repo.UseRecoveryCode(tfUser, f.hashes(1, tfUser2)[0]); won {
		t.Error("el código de otro usuario no sirve")
	}
}

func TestTwoFactor_disable(t *testing.T) {
	f := tfFixtures(t)
	if err := f.repo.Disable(tfUser); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sin 2FA: %v", err)
	}
	f.enabled(t, tfUser, 3, 4)
	f.enabled(t, tfUser2, 3, 4)
	if err := f.repo.Disable(tfUser); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Find(tfUser); !errors.Is(err, domain.ErrNotFound) {
		t.Error("se borra el secreto")
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser); n != 0 {
		t.Errorf("se borran los códigos: %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser2); n != 4 {
		t.Errorf("el otro usuario no se toca: %d", n)
	}
	if err := f.repo.Disable(tfUser); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("segundo disable: %v", err)
	}
}

// Disable es UNA transacción: si falla el borrado del secreto, los códigos de
// recuperación NO se pierden (un trigger de prueba hace fallar ese DELETE).
func TestTwoFactor_disableIsOneTransaction(t *testing.T) {
	f := tfFixtures(t)
	f.enabled(t, tfUser, 3, 4)
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION it_tf_fail_delete() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'it: falla el borrado'; END $$ LANGUAGE plpgsql`,
		`CREATE TRIGGER it_tf_fail_delete BEFORE DELETE ON user_totp FOR EACH ROW EXECUTE FUNCTION it_tf_fail_delete()`,
	} {
		if _, err := f.pool.Exec(f.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS it_tf_fail_delete ON user_totp`)
		_, _ = f.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS it_tf_fail_delete()`)
	})
	if err := f.repo.Disable(tfUser); err == nil {
		t.Fatal("el borrado debía fallar")
	}
	if n := f.count(t, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1`, tfUser); n != 4 {
		t.Errorf("la transacción no se deshizo: quedan %d códigos de 4", n)
	}
	if en, _, _, _ := f.row(t, tfUser); !en {
		t.Error("el 2FA debía seguir activo")
	}
}
