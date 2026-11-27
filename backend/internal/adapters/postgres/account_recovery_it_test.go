// Integración con Postgres real de F15 (recuperar contraseña, revocación de
// sesiones y verificar email). Se saltea sin SALON_IT=1. Lo que se prueba acá
// SOLO se prueba contra la DB: la migración 036 dos veces con md5 y relleno,
// cada CHECK/FK/UNIQUE a mano, y que cada operación del adaptador haga lo que
// dice su sentencia SQL.
package pgstore

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/auth"
	"salon-saas/internal/auth/security"
	"salon-saas/internal/domain"
)

const (
	f15Ana   = "u-f15-ana"
	f15Bea   = "u-f15-bea"
	f15Salon = "s-f15"
)

type f15Fix struct {
	pool *pgxpool.Pool
	ctx  context.Context
	repo AccountRecovery
}

func f15Fixtures(t *testing.T) *f15Fix {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)
	clean := func() {
		for _, q := range []string{
			`DELETE FROM memberships WHERE user_id LIKE 'u-f15-%'`,
			`DELETE FROM invitations WHERE salon_id='s-f15'`,
			`DELETE FROM salons WHERE id='s-f15'`,
			`DELETE FROM users WHERE id LIKE 'u-f15-%'`,
		} {
			if _, err := pool.Exec(context.Background(), q); err != nil {
				t.Fatalf("limpieza: %v", err)
			}
		}
	}
	clean()
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-f15-ana','Ana','f15-ana@test.com','h-vieja-ana')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-f15-bea','Bea','f15-bea@test.com','h-vieja-bea')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-f15','s-f15','Salón F15')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	t.Cleanup(clean)
	return &f15Fix{pool: pool, ctx: ctx, repo: AccountRecovery{Pool: pool}}
}

func (f *f15Fix) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (f *f15Fix) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// reset crea un pedido de restablecer y devuelve el token en claro.
func (f *f15Fix) reset(t *testing.T, user string, created time.Time) (token string, r domain.PasswordReset) {
	t.Helper()
	token, _ = domain.NewRecoveryToken()
	r, err := domain.NewPasswordReset(user, domain.HashRecoveryToken(token), created)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := f.repo.IssueReset(r)
	if err != nil || !ok {
		t.Fatalf("IssueReset: %v %v", ok, err)
	}
	return token, r
}

// --- D1: migración ---

// migFreshDBPool devuelve un pool sobre una base de datos NUEVA y vacía con las
// migraciones todavía sin aplicar.
//
// Por qué una base y no un esquema: los guardas de las migraciones viejas miran
// catálogos GLOBALES (`pg_constraint WHERE conname = ...` no filtra por esquema),
// así que dentro de una base que ya tiene las tablas migradas en `public` el
// guarda de 008 salta el UNIQUE de `clients` y la FK compuesta de
// `client_photos` falla. Una base propia es el único aislamiento real.
//
// El nombre es fijo para que el test sea re-ejecutable: se borra el resto de la
// corrida anterior antes de crear.
func migFreshDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := itPool(t) // saltea sin SALON_IT=1
	ctx := context.Background()
	t.Cleanup(base.Close)
	const name = "salon_f15_mig"
	if _, err := base.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Fatalf("limpiar la base de la migración: %v", err)
	}
	if _, err := base.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("crear la base de la migración: %v", err)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close() // primero se suelta la base…
		if _, err := base.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("no se pudo borrar la base de la migración: %v", err)
		}
	})
	return pool
}

func migFiles(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files
}

func applySQLFile(t *testing.T, pool *pgxpool.Pool, file string) {
	t.Helper()
	sql, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("aplicar %s: %v", filepath.Base(file), err)
	}
}

// D1: sobre un esquema con 001..035 y datos, aplicar 036 DOS veces agrega las
// dos columnas, rellena email_verified_at = created_at de TODOS los usuarios
// existentes, no reescribe nada más (md5 de users sin las columnas nuevas,
// memberships, sales, audit_events y user_totp idéntico) y una segunda pasada no
// re-verifica a quien nació después de la primera.
func TestF15_migration036_twiceKeepsDataAndBackfills(t *testing.T) {
	pool := migFreshDBPool(t)
	ctx := context.Background()
	var pre, mig []string
	for _, f := range migFiles(t) {
		if strings.HasPrefix(filepath.Base(f), "036_") {
			mig = append(mig, f)
		} else if filepath.Base(f) < "036" {
			pre = append(pre, f)
		}
	}
	if len(mig) != 1 {
		t.Fatalf("esperaba UNA migración 036, hay %d", len(mig))
	}
	for _, f := range pre {
		applySQLFile(t, pool, f)
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash,created_at) VALUES
		   ('m-u1','Uno','m1@test.com','h1','2024-01-02 03:04:05+00'),
		   ('m-u2','Dos','m2@test.com','h2','2025-06-07 08:09:10+00')`,
		`INSERT INTO salons(id,slug,name) VALUES('m-s1','m-s1','Salón')`,
		`INSERT INTO memberships(user_id,salon_id,role) VALUES('m-u1','m-s1','owner')`,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method) VALUES('m-sale','m-s1','m-u1','C',1000,'cash')`,
		`INSERT INTO audit_events(id,salon_id,actor_user_id,action) VALUES('m-a1','m-s1','m-u1','x')`,
		`INSERT INTO user_totp(user_id,secret_enc) VALUES('m-u1','\x0102')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	digest := func(table, order string, dropCols ...string) string {
		expr := "x::text"
		if len(dropCols) > 0 {
			expr = "(to_jsonb(x)"
			for _, c := range dropCols {
				expr += " - '" + c + "'"
			}
			expr += ")::text"
		}
		var h string
		err := pool.QueryRow(ctx, `SELECT md5(COALESCE(string_agg(`+expr+`, '|' ORDER BY `+order+`), '')) FROM `+table+` x`).Scan(&h)
		if err != nil {
			t.Fatalf("digest %s: %v", table, err)
		}
		return h
	}
	// Antes de 036 users no tiene las columnas: el digest sin columnas es el
	// del texto completo en jsonb.
	before := map[string]string{
		"users":       digest("users", "x.id", "password_changed_at", "email_verified_at"),
		"memberships": digest("memberships", "x.user_id, x.salon_id"),
		"sales":       digest("sales", "x.id"),
		"audit":       digest("audit_events", "x.id"),
		"totp":        digest("user_totp", "x.user_id"),
	}
	applySQLFile(t, pool, mig[0])
	after := func() map[string]string {
		return map[string]string{
			"users":       digest("users", "x.id", "password_changed_at", "email_verified_at"),
			"memberships": digest("memberships", "x.user_id, x.salon_id"),
			"sales":       digest("sales", "x.id"),
			"audit":       digest("audit_events", "x.id"),
			"totp":        digest("user_totp", "x.user_id"),
		}
	}
	for k, v := range after() {
		if v != before[k] {
			t.Errorf("036 cambió datos previos de %s", k)
		}
	}
	// Relleno: TODOS los usuarios existentes verificados en su created_at.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email_verified_at IS NOT DISTINCT FROM created_at AND password_changed_at IS NULL`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("relleno: %d de 2 usuarios con email_verified_at=created_at (%v)", n, err)
	}
	// Un usuario NUEVO (sin verificar) no se re-verifica en la segunda pasada.
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,pass_hash) VALUES('m-u3','Tres','m3@test.com','h3')`); err != nil {
		t.Fatal(err)
	}
	applySQLFile(t, pool, mig[0])
	applySQLFile(t, pool, mig[0])
	var nullVerified bool
	if err := pool.QueryRow(ctx, `SELECT email_verified_at IS NULL FROM users WHERE id='m-u3'`).Scan(&nullVerified); err != nil || !nullVerified {
		t.Fatalf("la re-aplicación re-verificó a un usuario nuevo: null=%v err=%v", nullVerified, err)
	}
	for k, v := range after() {
		if k == "users" {
			continue // m-u3 cambió el conjunto de usuarios
		}
		if v != before[k] {
			t.Errorf("la re-aplicación cambió %s", k)
		}
	}
	// Las tres columnas/tablas existen con el tipo del contrato.
	for _, q := range []string{
		`SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='users' AND column_name='password_changed_at' AND data_type='timestamp with time zone' AND is_nullable='YES'`,
		`SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='users' AND column_name='email_verified_at' AND data_type='timestamp with time zone' AND is_nullable='YES'`,
		`SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='password_resets'`,
		`SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='email_verifications'`,
		`SELECT 1 FROM pg_indexes WHERE schemaname='public' AND tablename='password_resets' AND indexdef ILIKE '%(user_id, created_at DESC)%'`,
		`SELECT 1 FROM pg_indexes WHERE schemaname='public' AND tablename='email_verifications' AND indexdef ILIKE '%(user_id, created_at DESC)%'`,
	} {
		var one int
		if err := pool.QueryRow(ctx, q).Scan(&one); err != nil {
			t.Errorf("no cumple el contrato (%v): %s", err, q)
		}
	}
}

// D1: cada CHECK, FK y UNIQUE probado a mano.
func TestF15_constraints(t *testing.T) {
	f := f15Fixtures(t)
	exec := func(q string, args ...any) error { _, err := f.pool.Exec(f.ctx, q, args...); return err }
	want := func(name, code string, err error) {
		t.Helper()
		if got := pgCode(err); got != code {
			t.Errorf("%s: código %q, esperado %q (err=%v)", name, got, code, err)
		}
	}
	h := domain.HashRecoveryToken("a")
	h2 := domain.HashRecoveryToken("b")
	exp := time.Now().Add(time.Hour)
	for _, table := range []struct{ name, cols, vals string }{
		{"password_resets", "id,user_id,token_hash,expires_at", ""},
		{"email_verifications", "id,user_id,email,token_hash,expires_at", ""},
	} {
		ins := func(id, user, hash string) error {
			if table.name == "password_resets" {
				return exec(`INSERT INTO password_resets(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, id, user, hash, exp)
			}
			return exec(`INSERT INTO email_verifications(id,user_id,email,token_hash,expires_at) VALUES($1,$2,'a@b.com',$3,$4)`, id, user, hash, exp)
		}
		n := table.name
		want(n+": hash corto", "23514", ins("it-f15-1", f15Ana, "abc"))
		want(n+": hash largo", "23514", ins("it-f15-1", f15Ana, h+"0"))
		want(n+": usuario inexistente", "23503", ins("it-f15-1", "no-existe", h))
		want(n+": hash NULL", "23502", exec(`INSERT INTO `+n+`(id,user_id,token_hash,expires_at) VALUES('it-f15-1',$1,NULL,$2)`, f15Ana, exp))
		want(n+": expires_at NULL", "23502", exec(`INSERT INTO `+n+`(id,user_id,token_hash,expires_at) VALUES('it-f15-1',$1,$2,NULL)`, f15Ana, h))
		if f.count(t, `SELECT count(*) FROM `+n) != 0 {
			t.Fatalf("%s: un INSERT inválido dejó fila", n)
		}
		if err := ins("it-f15-1", f15Ana, h); err != nil {
			t.Fatalf("%s válido: %v", n, err)
		}
		want(n+": UNIQUE token_hash", "23505", ins("it-f15-2", f15Bea, h))
		want(n+": PK id duplicada", "23505", ins("it-f15-1", f15Ana, h2))
		var usedNull bool
		var created time.Time
		if err := f.pool.QueryRow(f.ctx, `SELECT used_at IS NULL, created_at FROM `+n+` WHERE id='it-f15-1'`).Scan(&usedNull, &created); err != nil || !usedNull || time.Since(created) > time.Minute {
			t.Errorf("%s: defaults used_at=NULL y created_at=now(): %v %v %v", n, usedNull, created, err)
		}
		// ON DELETE CASCADE de users.
		f.exec(t, `DELETE FROM users WHERE id=$1`, f15Ana)
		if f.count(t, `SELECT count(*) FROM `+n) != 0 {
			t.Errorf("%s: borrar el usuario debe borrar sus pedidos", n)
		}
		f.exec(t, `INSERT INTO users(id,name,email,pass_hash) VALUES('u-f15-ana','Ana','f15-ana@test.com','h-vieja-ana')`)
	}
}

// Users guarda y lee email_verified_at (nil = sin verificar, y es lo que deja el
// registro abierto).
func TestF15_usersRoundTripEmailVerifiedAt(t *testing.T) {
	f := f15Fixtures(t)
	users := Users{Pool: f.pool}
	u, _ := domain.NewUser("Nuevo", "f15-nuevo@test.com", "h")
	u.ID = "u-f15-nuevo"
	if err := users.Save(u); err != nil {
		t.Fatal(err)
	}
	got, err := users.FindByID(u.ID)
	if err != nil || got.EmailVerifiedAt != nil {
		t.Fatalf("registro abierto debe quedar sin verificar: %+v %v", got.EmailVerifiedAt, err)
	}
	v, _ := domain.NewUser("Verif", "f15-verif@test.com", "h")
	v.ID = "u-f15-verif"
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	v.EmailVerifiedAt = &at
	if err := users.Save(v); err != nil {
		t.Fatal(err)
	}
	for name, find := range map[string]func() (domain.User, error){
		"FindByID":    func() (domain.User, error) { return users.FindByID(v.ID) },
		"FindByEmail": func() (domain.User, error) { return users.FindByEmail(v.Email) },
	} {
		got, err := find()
		if err != nil || got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(at) {
			t.Errorf("%s: %v %v", name, got.EmailVerifiedAt, err)
		}
	}
}

// --- D2: emitir ---

func TestF15_IssueReset_hashOnlyOnePendingAndThrottle(t *testing.T) {
	f := f15Fixtures(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	tok1, r1 := f.reset(t, f15Ana, now.Add(-30*time.Minute))
	// Solo el hash está guardado.
	if f.count(t, `SELECT count(*) FROM password_resets WHERE token_hash=$1`, domain.HashRecoveryToken(tok1)) != 1 ||
		f.count(t, `SELECT count(*) FROM password_resets WHERE token_hash=$1 OR id=$1 OR user_id=$1`, tok1) != 0 {
		t.Fatal("el token en claro no puede estar guardado")
	}
	var exp time.Time
	f.pool.QueryRow(f.ctx, `SELECT expires_at FROM password_resets WHERE id=$1`, r1.ID).Scan(&exp)
	if !exp.Equal(r1.ExpiresAt) {
		t.Errorf("expires_at: %v vs %v", exp, r1.ExpiresAt)
	}
	// Dentro de los 60 s: throttle silencioso y no escribe nada.
	t2, _ := domain.NewRecoveryToken()
	r2, _ := domain.NewPasswordReset(f15Ana, domain.HashRecoveryToken(t2), now.Add(-30*time.Minute).Add(59*time.Second))
	if ok, err := f.repo.IssueReset(r2); err != nil || ok {
		t.Fatalf("a los 59 s: ok=%v err=%v", ok, err)
	}
	if f.count(t, `SELECT count(*) FROM password_resets WHERE user_id=$1`, f15Ana) != 1 {
		t.Fatal("un pedido frenado escribió")
	}
	// A los 60 s: se emite, el anterior queda invalidado.
	r3, _ := domain.NewPasswordReset(f15Ana, domain.HashRecoveryToken("tres"), r1.CreatedAt.Add(60*time.Second))
	if ok, err := f.repo.IssueReset(r3); err != nil || !ok {
		t.Fatalf("a los 60 s: ok=%v err=%v", ok, err)
	}
	if f.count(t, `SELECT count(*) FROM password_resets WHERE user_id=$1 AND used_at IS NULL`, f15Ana) != 1 {
		t.Fatal("debe haber UN solo vigente por usuario")
	}
	if err := f.repo.ConsumeReset(domain.HashRecoveryToken(tok1), "h-nueva"); err != domain.ErrNotFound {
		t.Fatalf("el anterior quedó invalidado: %v", err)
	}
	// Otro usuario no se ve afectado.
	f.reset(t, f15Bea, now)
	// 5 en la hora para Ana: el sexto se frena.
	base := r3.CreatedAt
	for i := 1; i <= 2; i++ { // r1, r3 + 2 = 4
		r, _ := domain.NewPasswordReset(f15Ana, domain.HashRecoveryToken("x"+string(rune('a'+i))), base.Add(time.Duration(i)*61*time.Second))
		if ok, err := f.repo.IssueReset(r); err != nil || !ok {
			t.Fatalf("emisión %d: %v %v", i, ok, err)
		}
	}
	r5, _ := domain.NewPasswordReset(f15Ana, domain.HashRecoveryToken("quinto"), base.Add(3*61*time.Second))
	if ok, err := f.repo.IssueReset(r5); err != nil || !ok {
		t.Fatalf("5.º: %v %v", ok, err)
	}
	r6, _ := domain.NewPasswordReset(f15Ana, domain.HashRecoveryToken("sexto"), base.Add(4*61*time.Second))
	if ok, err := f.repo.IssueReset(r6); err != nil || ok {
		t.Fatalf("6.º en la hora: ok=%v err=%v", ok, err)
	}
	// Usuario inexistente: error, sin escribir.
	rx, _ := domain.NewPasswordReset("u-f15-no", domain.HashRecoveryToken("x"), now)
	if _, err := f.repo.IssueReset(rx); err == nil {
		t.Error("un usuario inexistente debe fallar")
	}
}

func TestF15_FindReset(t *testing.T) {
	f := f15Fixtures(t)
	tok, r := f.reset(t, f15Ana, time.Now().UTC())
	got, err := f.repo.FindReset(domain.HashRecoveryToken(tok))
	if err != nil || got.ID != r.ID || got.UserID != f15Ana || got.UsedAt != nil || !got.Usable(time.Now()) {
		t.Fatalf("FindReset: %+v %v", got, err)
	}
	if _, err := f.repo.FindReset(domain.HashRecoveryToken("desconocido")); err != domain.ErrNotFound {
		t.Fatalf("desconocido: %v", err)
	}
}

// --- D3: canje ---

func TestF15_ConsumeReset_effects(t *testing.T) {
	f := f15Fixtures(t)
	f.exec(t, `UPDATE users SET email_verified_at=NULL WHERE id=$1`, f15Ana) // sin verificar
	f.exec(t, `UPDATE users SET email_verified_at=$2 WHERE id=$1`, f15Bea, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	tok, _ := f.reset(t, f15Ana, time.Now().UTC())
	// Otro pedido pendiente de Ana insertado a mano (el invalidar los demás).
	f.exec(t, `INSERT INTO password_resets(id,user_id,token_hash,expires_at) VALUES('it-f15-other',$1,$2,now()+interval '1 hour')`,
		f15Ana, domain.HashRecoveryToken("otro"))
	beaBefore := f.count(t, `SELECT count(*) FROM users WHERE id=$1 AND pass_hash='h-vieja-bea'`, f15Bea)

	if err := f.repo.ConsumeReset(domain.HashRecoveryToken(tok), "h-nueva-ana"); err != nil {
		t.Fatalf("ConsumeReset: %v", err)
	}
	var hash string
	var changed, verified, usedAt *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT pass_hash, password_changed_at, email_verified_at FROM users WHERE id=$1`, f15Ana).
		Scan(&hash, &changed, &verified); err != nil {
		t.Fatal(err)
	}
	if hash != "h-nueva-ana" || changed == nil || verified == nil {
		t.Fatalf("users: hash=%q changed=%v verified=%v", hash, changed, verified)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT used_at FROM password_resets WHERE token_hash=$1`, domain.HashRecoveryToken(tok)).Scan(&usedAt); err != nil || usedAt == nil {
		t.Fatalf("used_at: %v %v", usedAt, err)
	}
	// UN solo valor del reloj de la base para used_at y password_changed_at (la
	// comparación D4 es consistente).
	if !usedAt.Equal(*changed) || !verified.Equal(*changed) {
		t.Errorf("used_at=%v password_changed_at=%v email_verified_at=%v: deben ser el mismo instante", usedAt, changed, verified)
	}
	if time.Since(*changed) > time.Minute || changed.After(time.Now().Add(time.Second)) {
		t.Errorf("el instante no es el del reloj de la base: %v", changed)
	}
	if f.count(t, `SELECT count(*) FROM password_resets WHERE user_id=$1 AND used_at IS NULL`, f15Ana) != 0 {
		t.Error("los demás pedidos del usuario deben quedar invalidados")
	}
	if f.count(t, `SELECT count(*) FROM users WHERE id=$1 AND pass_hash='h-vieja-bea' AND password_changed_at IS NULL`, f15Bea) != beaBefore {
		t.Error("el canje tocó a otro usuario")
	}
	// Segundo canje: no hay nada que canjear y la clave no cambia.
	if err := f.repo.ConsumeReset(domain.HashRecoveryToken(tok), "h-otra"); err != domain.ErrNotFound {
		t.Fatalf("reuso: %v", err)
	}
	f.pool.QueryRow(f.ctx, `SELECT pass_hash FROM users WHERE id=$1`, f15Ana).Scan(&hash)
	if hash != "h-nueva-ana" {
		t.Fatalf("el reuso cambió la clave: %q", hash)
	}
	// Un email ya verificado conserva SU fecha (COALESCE).
	tokB, _ := f.reset(t, f15Bea, time.Now().UTC())
	if err := f.repo.ConsumeReset(domain.HashRecoveryToken(tokB), "h-nueva-bea"); err != nil {
		t.Fatal(err)
	}
	var vb time.Time
	f.pool.QueryRow(f.ctx, `SELECT email_verified_at FROM users WHERE id=$1`, f15Bea).Scan(&vb)
	if !vb.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("pisó email_verified_at de una cuenta ya verificada: %v", vb)
	}
}

func TestF15_ConsumeReset_expiredAndUnknownLeaveEverythingIntact(t *testing.T) {
	f := f15Fixtures(t)
	f.exec(t, `INSERT INTO password_resets(id,user_id,token_hash,created_at,expires_at) VALUES('it-f15-exp',$1,$2,now()-interval '2 hours',now()-interval '1 second')`,
		f15Ana, domain.HashRecoveryToken("vencido"))
	if err := f.repo.ConsumeReset(domain.HashRecoveryToken("vencido"), "h-x"); err != domain.ErrNotFound {
		t.Fatalf("vencido: %v", err)
	}
	if err := f.repo.ConsumeReset(domain.HashRecoveryToken("nadie"), "h-x"); err != domain.ErrNotFound {
		t.Fatalf("desconocido: %v", err)
	}
	if f.count(t, `SELECT count(*) FROM users WHERE id=$1 AND pass_hash='h-vieja-ana' AND password_changed_at IS NULL`, f15Ana) != 1 ||
		f.count(t, `SELECT count(*) FROM password_resets WHERE id='it-f15-exp' AND used_at IS NULL`) != 1 {
		t.Fatal("un canje fallido modificó datos")
	}
}

// D5: el reinicio NO toca user_totp ni user_recovery_codes, no reinicia
// failed_count/locked_until, no quita el 2FA y no emite sesión: tras reiniciar, el
// login con la contraseña nueva sigue devolviendo el DESAFÍO (sin token).
func TestF15_ConsumeReset_leavesTwoFactorAloneAndLoginStillAsksForTheCode(t *testing.T) {
	f := f15Fixtures(t)
	tf := TwoFactor{Pool: f.pool}
	if err := tf.SavePending(f15Ana, []byte("cifrado")); err != nil {
		t.Fatal(err)
	}
	codes := []string{domain.HashRecoveryCode("AAAAAAAAAA"), domain.HashRecoveryCode("BBBBBBBBBB")}
	if err := tf.Enable(f15Ana, []byte("cifrado"), 7, codes); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE user_totp SET failed_count=3, locked_until=now()+interval '10 minutes' WHERE user_id=$1`, f15Ana)
	snap := func() string {
		var s string
		if err := f.pool.QueryRow(f.ctx, `SELECT
		  (SELECT md5(string_agg(t::text,'|' ORDER BY t.user_id)) FROM user_totp t WHERE t.user_id=$1) || '/' ||
		  (SELECT md5(string_agg(c::text,'|' ORDER BY c.id)) FROM user_recovery_codes c WHERE c.user_id=$1)`, f15Ana).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snap()

	hasher := security.BcryptHasher{Cost: 4}
	newHash, _ := hasher.Hash("claveNueva1")
	tok, _ := f.reset(t, f15Ana, time.Now().UTC())
	if err := f.repo.ConsumeReset(domain.HashRecoveryToken(tok), newHash); err != nil {
		t.Fatal(err)
	}
	if after := snap(); after != before {
		t.Fatalf("el reinicio tocó user_totp/user_recovery_codes:\n%s\n%s", before, after)
	}
	// Login real: la contraseña nueva pasa, el 2FA sigue activo => desafío.
	jwtIss := security.JWTIssuer{Secret: "test-secret"}
	box, _ := domain.NewTOTPSecretBox("test-secret")
	auth := auth.AuthService{
		Users: Users{Pool: f.pool}, Hasher: hasher, Tokens: jwtIss,
		TwoFactor: auth.TwoFactorService{Repo: tf, Challenges: jwtIss, Box: box},
	}
	res, err := auth.Authenticate("f15-ana@test.com", "claveNueva1")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !res.TwoFactorRequired || res.Challenge == "" || res.Token != "" {
		t.Fatalf("el login con 2FA debe seguir pidiendo el código y NO dar token: %+v", res)
	}
}

// --- D7: verificación ---

func TestF15_Verification_issueConsumeAndIdempotency(t *testing.T) {
	f := f15Fixtures(t)
	f.exec(t, `UPDATE users SET email_verified_at=NULL WHERE id=$1`, f15Ana)
	mk := func(created time.Time) (string, domain.EmailVerification) {
		tok, _ := domain.NewRecoveryToken()
		v, err := domain.NewEmailVerification(f15Ana, "f15-ana@test.com", domain.HashRecoveryToken(tok), created)
		if err != nil {
			t.Fatal(err)
		}
		return tok, v
	}
	now := time.Now().UTC()
	tok, v := mk(now.Add(-time.Hour))
	if ok, err := f.repo.IssueVerification(v, 0); err != nil || !ok {
		t.Fatalf("primer pedido sin límite: %v %v", ok, err)
	}
	if f.count(t, `SELECT count(*) FROM email_verifications WHERE token_hash=$1 AND email='f15-ana@test.com'`, domain.HashRecoveryToken(tok)) != 1 ||
		f.count(t, `SELECT count(*) FROM email_verifications WHERE token_hash=$1`, tok) != 0 {
		t.Fatal("solo el hash y el email van guardados")
	}
	// Reenvío dentro del minuto: frenado y sin escribir; a los 60 s pasa.
	_, v2 := mk(v.CreatedAt.Add(59 * time.Second))
	if ok, err := f.repo.IssueVerification(v2, 60*time.Second); err != nil || ok {
		t.Fatalf("a los 59 s: %v %v", ok, err)
	}
	if f.count(t, `SELECT count(*) FROM email_verifications WHERE user_id=$1`, f15Ana) != 1 {
		t.Fatal("un reenvío frenado escribió")
	}
	_, v3 := mk(v.CreatedAt.Add(60 * time.Second))
	if ok, err := f.repo.IssueVerification(v3, 60*time.Second); err != nil || !ok {
		t.Fatalf("a los 60 s: %v %v", ok, err)
	}

	// Canje: marca la cuenta y consume el pedido.
	if err := f.repo.ConsumeVerification(domain.HashRecoveryToken(tok)); err != nil {
		t.Fatalf("ConsumeVerification: %v", err)
	}
	var verified *time.Time
	f.pool.QueryRow(f.ctx, `SELECT email_verified_at FROM users WHERE id=$1`, f15Ana).Scan(&verified)
	if verified == nil || f.count(t, `SELECT count(*) FROM email_verifications WHERE token_hash=$1 AND used_at IS NOT NULL`, domain.HashRecoveryToken(tok)) != 1 {
		t.Fatal("no marcó la cuenta o no consumió el pedido")
	}
	firstVerified := *verified
	// Idempotente: el reuso con la cuenta verificada es nil y no cambia la fecha.
	if err := f.repo.ConsumeVerification(domain.HashRecoveryToken(tok)); err != nil {
		t.Fatalf("reuso idempotente: %v", err)
	}
	f.pool.QueryRow(f.ctx, `SELECT email_verified_at FROM users WHERE id=$1`, f15Ana).Scan(&verified)
	if !verified.Equal(firstVerified) {
		t.Error("el reuso cambió email_verified_at")
	}
	// Desconocido.
	if err := f.repo.ConsumeVerification(domain.HashRecoveryToken("nadie")); err != domain.ErrNotFound {
		t.Fatalf("desconocido: %v", err)
	}
}

func TestF15_Verification_expiredUsedUnverifiedAndEmailMismatch(t *testing.T) {
	f := f15Fixtures(t)
	f.exec(t, `UPDATE users SET email_verified_at=NULL WHERE id=ANY($1)`, []string{f15Ana, f15Bea})
	ins := func(id, user, email, tok, createdOff, expOff string) {
		f.exec(t, `INSERT INTO email_verifications(id,user_id,email,token_hash,created_at,expires_at)
		  VALUES($1,$2,$3,$4,now()+$5::interval,now()+$6::interval)`, id, user, email, domain.HashRecoveryToken(tok), createdOff, expOff)
	}
	ins("it-f15-v-exp", f15Ana, "f15-ana@test.com", "vencido", "-2 hours", "-1 second")
	if err := f.repo.ConsumeVerification(domain.HashRecoveryToken("vencido")); err != domain.ErrNotFound {
		t.Fatalf("vencido: %v", err)
	}
	// Usado pero la cuenta NO está verificada: inválido (no es el caso idempotente).
	ins("it-f15-v-used", f15Ana, "f15-ana@test.com", "usado", "-1 hour", "1 hour")
	f.exec(t, `UPDATE email_verifications SET used_at=now() WHERE id='it-f15-v-used'`)
	if err := f.repo.ConsumeVerification(domain.HashRecoveryToken("usado")); err != domain.ErrNotFound {
		t.Fatalf("usado sin verificar: %v", err)
	}
	// El email del pedido ya no es el de la cuenta: no verifica y no consume.
	ins("it-f15-v-mm", f15Bea, "otro@test.com", "otro", "-1 hour", "1 hour")
	if err := f.repo.ConsumeVerification(domain.HashRecoveryToken("otro")); err != domain.ErrNotFound {
		t.Fatalf("email distinto: %v", err)
	}
	if f.count(t, `SELECT count(*) FROM email_verifications WHERE id='it-f15-v-mm' AND used_at IS NULL`) != 1 ||
		f.count(t, `SELECT count(*) FROM users WHERE id=ANY($1) AND email_verified_at IS NOT NULL`, []string{f15Ana, f15Bea}) != 0 {
		t.Fatal("un canje fallido modificó datos")
	}
}

// --- D4: la compuerta ---

func TestF15_SessionGate(t *testing.T) {
	f := f15Fixtures(t)
	gate := SessionGate{Pool: f.pool}
	if at, err := gate.PasswordChangedAt(f15Ana); err != nil || at != nil {
		t.Fatalf("nunca cambió: %v %v", at, err)
	}
	want := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	f.exec(t, `UPDATE users SET password_changed_at=$2 WHERE id=$1`, f15Ana, want)
	if at, err := gate.PasswordChangedAt(f15Ana); err != nil || at == nil || !at.Equal(want) {
		t.Fatalf("cambió: %v %v", at, err)
	}
	if _, err := gate.PasswordChangedAt("u-f15-no-existe"); err != domain.ErrNotFound {
		t.Fatalf("inexistente: %v", err)
	}
	// Una base inalcanzable es un error (quien llama falla cerrado).
	closed := f15ClosedPool(t)
	if _, err := (SessionGate{Pool: closed}).PasswordChangedAt(f15Ana); err == nil || err == domain.ErrNotFound {
		t.Fatalf("con la base caída debe haber un error distinto de ErrNotFound: %v", err)
	}
}

func f15ClosedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	p := itPool(t)
	p.Close()
	return p
}
