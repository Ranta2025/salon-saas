// Integración con Postgres real de las reseñas (F13, D2..D7). Se saltea sin
// SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la base: la migración es
// re-ejecutable y sus CHECK/UNIQUE, el consumo atómico del enlace (una sola
// reseña aunque haya 30 POST a la vez), la sección pública con las visibles, y el
// barrido de pedidos (UNIQUE por turno + reclamo de `sent_at` + reintento).
package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/reviews"
	"salon-saas/internal/testutil/itdb"
)

type rvFix struct {
	pool    *pgxpool.Pool
	ctx     context.Context
	repo    Reviews
	now     time.Time
	svc     domain.Service
	profID  string
	salonID string
	// other es un salón ajeno: nada de lo suyo puede aparecer ni escribirse.
	other string
}

const (
	rvSalon  = "s-it-rv"
	rvOther  = "s-it-rv2"
	rvUserID = "u-it-rv-prof"
)

// rvFixtures deja un escenario limpio: dos salones (uno en Auckland), un
// profesional, un servicio y las migraciones aplicadas DOS veces.
func rvFixtures(t *testing.T) *rvFix {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-rv%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-rv%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('` + rvUserID + `','Prof Reseñas','rvprof@test.com','x')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('` + rvSalon + `','s-it-rv','Salón Reseñas IT','Pacific/Auckland')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('` + rvOther + `','s-it-rv2','Salón Reseñas IT 2','America/Mexico_City')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM salons WHERE id LIKE 's-it-rv%'`); err != nil {
			t.Errorf("borrar el salón con sus reseñas: %v", err)
		}
	})
	services := pgstore.Services{Pool: pool}
	svc, err := domain.NewService(rvSalon, "Corte", 45, 15000, 10)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	if err := services.Save(svc); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	return &rvFix{
		pool: pool, ctx: ctx, repo: Reviews{Pool: pool},
		now:     time.Now().UTC().Truncate(time.Microsecond),
		svc:     svc,
		profID:  rvUserID,
		salonID: rvSalon,
		other:   rvOther,
	}
}

// booking crea un turno en el estado pedido y devuelve su id.
func (f *rvFix) booking(t *testing.T, salonID, id, status string, startsAt time.Time, endsAt time.Time) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx,
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,starts_at,ends_at,status,service_id)
		 VALUES($1,$2,$3,'Lía Gómez',$4,$5,$6,$7)`,
		id, salonID, f.profID, startsAt.UTC(), endsAt.UTC(), status, f.svc.ID)
	if err != nil {
		t.Fatalf("turno %s: %v", id, err)
	}
}

// email guarda el correo del turno (es la fuente del pedido de reseña).
func (f *rvFix) email(t *testing.T, salonID, bookingID, email string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO booking_emails(booking_id,salon_id,email) VALUES($1,$2,$3)`, bookingID, salonID, email); err != nil {
		t.Fatalf("email del turno %s: %v", bookingID, err)
	}
}

// enabled activa las reseñas del salón con la demora pedida.
func (f *rvFix) enabled(t *testing.T, salonID string, delay int) {
	t.Helper()
	if err := f.repo.SaveSettings(ports.ReviewSettings{SalonID: salonID, Enabled: true, DelayHours: delay}); err != nil {
		t.Fatalf("configuración: %v", err)
	}
}

// request siembra un pedido de reseña enviado y devuelve (token en claro, hash).
func (f *rvFix) request(t *testing.T, salonID, bookingID string, expiresAt time.Time) (string, string) {
	t.Helper()
	tok, err := reviews.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	hash := reviews.HashToken(tok)
	// created_at es anterior a expires_at (el CHECK de la migración): un pedido
	// ya vencido se sembró hace 30 días.
	createdAt := f.now
	if floor := expiresAt.Add(-reviews.RequestTTL); floor.Before(createdAt) {
		createdAt = floor
	}
	_, err = f.pool.Exec(f.ctx,
		`INSERT INTO review_requests(id,salon_id,booking_id,token_hash,created_at,sent_at,expires_at)
		 VALUES($1,$2,$3,$4,$5,$5,$6)`,
		domain.NewID(), salonID, bookingID, hash, createdAt.UTC(), expiresAt.UTC())
	if err != nil {
		t.Fatalf("pedido: %v", err)
	}
	return tok, hash
}

func (f *rvFix) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	return itdb.CountRows(t, f.pool, sql, args...)
}

// --- D2: la migración ---------------------------------------------------------------

// La migración es re-ejecutable (itdb la aplica DOS veces en TestMain) y sus
// CHECK/UNIQUE son los del contrato.
func TestReviewsMigration_tablesAndGuards(t *testing.T) {
	f := rvFixtures(t)
	for _, table := range []string{"review_settings", "review_requests", "reviews"} {
		if n := f.count(t, `SELECT count(*) FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1`, table); n != 1 {
			t.Fatalf("falta la tabla %s", table)
		}
	}
	f.booking(t, f.salonID, "b-guards", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))

	// rating fuera de 1..5 y comentario de 501 CARACTERES (no bytes): la base los
	// rechaza aunque el caso de uso tuviera un bug.
	bad := []struct {
		name string
		sql  string
		args []any
	}{
		{"rating 0", `INSERT INTO reviews(id,salon_id,booking_id,rating) VALUES('x1',$1,'b-guards',0)`, nil},
		{"rating 6", `INSERT INTO reviews(id,salon_id,booking_id,rating) VALUES('x2',$1,'b-guards',6)`, nil},
		{"comentario 501 runas", `INSERT INTO reviews(id,salon_id,booking_id,rating,comment)
			VALUES('x3',$1,'b-guards',5,repeat('💅', 501))`, nil},
		{"demora 0", `INSERT INTO review_settings(salon_id,enabled,delay_hours) VALUES($1,true,0)`, nil},
		{"demora 73", `INSERT INTO review_settings(salon_id,enabled,delay_hours) VALUES($1,true,73)`, nil},
	}
	for _, c := range bad {
		if _, err := f.pool.Exec(f.ctx, c.sql, f.salonID); err == nil {
			t.Fatalf("%s: la base lo aceptó", c.name)
		}
	}
	// 500 runas sí entran (una reseña por turno: se usa un turno propio).
	f.booking(t, f.salonID, "b-500", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO reviews(id,salon_id,booking_id,rating,comment) VALUES('x4',$1,'b-500',5,repeat('💅', 500))`,
		f.salonID); err != nil {
		t.Fatalf("500 runas: %v", err)
	}
	// UNA reseña por turno y UN pedido por turno.
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO reviews(id,salon_id,booking_id,rating) VALUES('x5',$1,'b-500',4)`, f.salonID); err == nil {
		t.Fatal("dos reseñas para el mismo turno")
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO review_requests(id,salon_id,booking_id,token_hash,expires_at)
		 VALUES('rq-a',$1,'b-500',$2,now() + interval '1 day')`, f.salonID, reviews.HashToken("tok-a")); err != nil {
		t.Fatalf("pedido: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO review_requests(id,salon_id,booking_id,token_hash,expires_at)
		 VALUES('rq-b',$1,'b-500',$2,now() + interval '1 day')`, f.salonID, reviews.HashToken("tok-b")); err == nil {
		t.Fatal("dos pedidos para el mismo turno")
	}
}

// --- D4: el enlace y el canje ---------------------------------------------------------

func TestReviewsLink_uniformNotFound(t *testing.T) {
	f := rvFixtures(t)
	f.booking(t, f.salonID, "b-link", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))
	tok, hash := f.request(t, f.salonID, "b-link", f.now.Add(24*time.Hour))

	link, err := f.repo.Link(hash, f.now)
	if err != nil || link.SalonName != "Salón Reseñas IT" || link.ServiceName != "Corte" ||
		link.Timezone != "Pacific/Auckland" || !link.StartsAt.Equal(f.now.Add(-48*time.Hour).UTC()) {
		t.Fatalf("enlace: %+v %v", link, err)
	}
	_ = tok
	// Desconocido, vencido y usado: ErrNotFound (el caso de uso lo vuelve el MISMO
	// 404).
	if _, err := f.repo.Link(reviews.HashToken("otro"), f.now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("desconocido: %v", err)
	}
	f.booking(t, f.salonID, "b-link-old", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))
	old, oldHash := f.request(t, f.salonID, "b-link-old", f.now.Add(-time.Minute))
	_ = old
	if _, err := f.repo.Link(oldHash, f.now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("vencido: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE review_requests SET used_at=$2 WHERE token_hash=$1`, hash, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Link(hash, f.now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("usado: %v", err)
	}
}

// El canje guarda el nombre público (D5) y el servicio del turno, y NO acepta un
// token vencido.
func TestReviewsSubmit_snapshotAndExpiry(t *testing.T) {
	f := rvFixtures(t)
	f.booking(t, f.salonID, "b-ok", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))
	tok, hash := f.request(t, f.salonID, "b-ok", f.now.Add(24*time.Hour))
	review, err := f.repo.Submit(ports.ReviewSubmit{TokenHash: hash, Rating: 5, Comment: "Genial", Now: f.now})
	if err != nil {
		t.Fatalf("canje: %v", err)
	}
	if review.AuthorName != "Lía G." || review.ServiceName != "Corte" || review.Rating != 5 ||
		review.Comment != "Genial" || review.Hidden {
		t.Fatalf("snapshot: %+v", review)
	}
	_ = tok
	// Vencido: no crea nada.
	f.booking(t, f.salonID, "b-old", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))
	_, oldHash := f.request(t, f.salonID, "b-old", f.now.Add(-time.Hour))
	if _, err := f.repo.Submit(ports.ReviewSubmit{TokenHash: oldHash, Rating: 4, Now: f.now}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("vencido: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM reviews WHERE salon_id=$1`, f.salonID); n != 1 {
		t.Fatalf("reseñas: %d", n)
	}
}

// D4 + mutantes: 30 POST simultáneos del MISMO enlace dejan UNA reseña y UN solo
// ganador; los demás reciben ErrNotFound.
//
//	MUTANTE 1: quitar `used_at IS NULL` del UPDATE -> el segundo INSERT choca con
//	   UNIQUE(booking_id) y responde 409/500 (el test falla porque espera NotFound).
//	MUTANTE 2: quitar UNIQUE(booking_id) de reviews -> con el UPDATE intacto el
//	   test sigue verde: es la razón de ser del segundo guardia (defensa en
//	   profundidad, no un reemplazo).
func TestReviewsSubmit_singleUseUnderConcurrency(t *testing.T) {
	f := rvFixtures(t)
	f.repo = bigReviews(t)
	f.booking(t, f.salonID, "b-race", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))
	_, hash := f.request(t, f.salonID, "b-race", f.now.Add(24*time.Hour))

	const n = 30
	errs := make([]error, n)
	itdb.Race(n, func(i int) {
		_, errs[i] = f.repo.Submit(ports.ReviewSubmit{TokenHash: hash, Rating: 5, Comment: "ok", Now: f.now})
	})
	ok, notFound, other := 0, 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrNotFound):
			notFound++
		default:
			other++
			t.Errorf("error inesperado: %v", err)
		}
	}
	if ok != 1 || notFound != n-1 || other != 0 {
		t.Fatalf("ok=%d notFound=%d otros=%d", ok, notFound, other)
	}
	if got := f.count(t, `SELECT count(*) FROM reviews WHERE booking_id='b-race'`); got != 1 {
		t.Fatalf("reseñas del turno: %d", got)
	}
}

// bigReviews arma el adaptador sobre un pool de 40 conexiones: con el pool por
// defecto los pedidos se encolan y la carrera casi no existe.
func bigReviews(t *testing.T) Reviews {
	t.Helper()
	url := envOr("DATABASE_URL", "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 40
	cfg.MinConns = 40
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return Reviews{Pool: pool}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// --- D6/D7: ocultar y la sección pública ------------------------------------------------

func TestReviewsHiddenAndPublicSummary(t *testing.T) {
	f := rvFixtures(t)
	base := f.now.Add(-72 * time.Hour)
	ratings := []int{5, 4, 4, 3}
	for i, rating := range ratings {
		id := "b-pub-" + string(rune('a'+i))
		f.booking(t, f.salonID, id, "done", base, base.Add(time.Hour))
		tok, hash := f.request(t, f.salonID, id, f.now.Add(24*time.Hour))
		_ = tok
		if _, err := f.repo.Submit(ports.ReviewSubmit{TokenHash: hash, Rating: rating, Comment: "obra " + id,
			Now: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("canje %s: %v", id, err)
		}
	}
	sum, err := f.repo.Visible(f.salonID)
	if err != nil || sum.Count != 4 || sum.Average != 4.0 || len(sum.Items) != 4 {
		t.Fatalf("sección pública: %+v %v", sum, err)
	}
	// El día sale en la ZONA DEL SALÓN (Auckland, UTC+13 en noviembre) y el autor
	// es el nombre público, no el del turno.
	if len(sum.Items[0].Date) != 10 || sum.Items[0].Author != "Lía G." {
		t.Fatalf("renglón público: %+v", sum.Items[0])
	}
	// El panel lista del más nuevo al más viejo, con su cursor.
	page, err := f.repo.List(f.salonID, ports.ReviewQuery{Limit: 10})
	if err != nil || len(page) != 4 {
		t.Fatalf("lista: %+v %v", page, err)
	}
	if page[0].CreatedAt.Before(page[3].CreatedAt) {
		t.Fatalf("orden: %+v", page)
	}
	// Una oculta desaparece de la web y del promedio: se oculta la de 5 estrellas
	// y quedan 4+4+3 (11/3) sobre tres.
	var hiddenComment string
	for _, r := range page {
		if r.Rating == 5 {
			hiddenComment = r.Comment
			if err := f.repo.SetHidden(f.salonID, r.ID, true); err != nil {
				t.Fatalf("ocultar: %v", err)
			}
		}
	}
	sum, err = f.repo.Visible(f.salonID)
	if err != nil || sum.Count != 3 || len(sum.Items) != 3 {
		t.Fatalf("con una oculta: %+v %v", sum, err)
	}
	if want := 11.0 / 3.0; sum.Average < want-0.001 || sum.Average > want+0.001 {
		t.Fatalf("promedio sin la oculta: %v, esperaba %v", sum.Average, want)
	}
	for _, item := range sum.Items {
		if item.Comment == hiddenComment {
			t.Fatalf("la oculta sigue en la web: %+v", sum.Items)
		}
	}
	// Otro salón: la reseña no existe para él (404 del caso de uso).
	if err := f.repo.SetHidden(f.other, page[1].ID, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v", err)
	}
	// Y su sección pública está vacía.
	other, err := f.repo.Visible(f.other)
	if err != nil || other.Count != 0 || len(other.Items) != 0 {
		t.Fatalf("salón ajeno: %+v %v", other, err)
	}
}

// --- D3: el barrido ---------------------------------------------------------------------

func TestReviewsDue_onlyWhatTheContractSays(t *testing.T) {
	f := rvFixtures(t)
	f.enabled(t, f.salonID, 24)
	mk := func(id, status string, endsAgo time.Duration, email string) {
		endsAt := f.now.Add(-endsAgo)
		f.booking(t, f.salonID, id, status, endsAt.Add(-time.Hour), endsAt)
		if email != "" {
			f.email(t, f.salonID, id, email)
		}
	}
	mk("b-ready", "done", 48*time.Hour, "ok@mail.com")
	mk("b-soon", "done", 2*time.Hour, "soon@mail.com")   // todavía no pasó la demora
	mk("b-old", "done", 20*24*time.Hour, "old@mail.com") // más de 14 días
	mk("b-noemail", "done", 48*time.Hour, "")            // sin correo
	mk("b-booked", "booked", 48*time.Hour, "booked@mail.com")
	mk("b-noshow", "no_show", 48*time.Hour, "noshow@mail.com")

	due, err := f.repo.Due(f.now, 50)
	if err != nil {
		t.Fatalf("debidos: %v", err)
	}
	if len(due) != 1 || due[0].BookingID != "b-ready" || due[0].Email != "ok@mail.com" ||
		due[0].SalonName != "Salón Reseñas IT" || due[0].ServiceName != "Corte" {
		t.Fatalf("debidos: %+v", due)
	}
	// Con las reseñas apagadas no se pide nada (aunque la fila exista).
	if err := f.repo.SaveSettings(ports.ReviewSettings{SalonID: f.salonID, Enabled: false, DelayHours: 24}); err != nil {
		t.Fatal(err)
	}
	if due, err = f.repo.Due(f.now, 50); err != nil || len(due) != 0 {
		t.Fatalf("apagadas: %+v %v", due, err)
	}
	f.enabled(t, f.salonID, 24)
	// Un pedido ENVIADO saca el turno de la lista; uno sin enviar lo deja (reintento).
	f.request(t, f.salonID, "b-ready", f.now.Add(24*time.Hour))
	if due, err = f.repo.Due(f.now, 50); err != nil || len(due) != 0 {
		t.Fatalf("ya enviado: %+v %v", due, err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE review_requests SET sent_at=NULL WHERE booking_id='b-ready'`); err != nil {
		t.Fatal(err)
	}
	if due, err = f.repo.Due(f.now, 50); err != nil || len(due) != 1 {
		t.Fatalf("reintento: %+v %v", due, err)
	}
}

// Claim: dos barredores sobre el MISMO turno dejan UN pedido y UN ganador; el
// perdedor recibe claimed=false. Si el envío falla, Unclaim devuelve el reclamo y
// el siguiente Claim rota el token y vuelve a ganar.
func TestReviewsClaim_idempotentAcrossSweepers(t *testing.T) {
	f := rvFixtures(t)
	f.booking(t, f.salonID, "b-claim", "done", f.now.Add(-48*time.Hour), f.now.Add(-47*time.Hour))

	claims := make([]bool, 2)
	ids := make([]string, 2)
	errs := make([]error, 2)
	tok1, _ := reviews.NewToken()
	tok2, _ := reviews.NewToken()
	hashes := []string{reviews.HashToken(tok1), reviews.HashToken(tok2)}
	itdb.Race(2, func(i int) {
		ids[i], claims[i], errs[i] = f.repo.Claim(ports.ReviewClaim{
			ID: domain.NewID(), SalonID: f.salonID, BookingID: "b-claim",
			TokenHash: hashes[i], Now: f.now, ExpiresAt: f.now.Add(reviews.RequestTTL),
		})
	})
	won, lost := 0, 0
	for i := range claims {
		if errs[i] != nil {
			t.Fatalf("claim %d: %v", i, errs[i])
		}
		if claims[i] {
			won++
		} else {
			lost++
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("ganadores=%d perdedores=%d", won, lost)
	}
	if n := f.count(t, `SELECT count(*) FROM review_requests WHERE booking_id='b-claim'`); n != 1 {
		t.Fatalf("pedidos: %d", n)
	}
	// Un tercer barredor ya no gana (está enviado).
	if id, ok, err := f.repo.Claim(ports.ReviewClaim{ID: domain.NewID(), SalonID: f.salonID,
		BookingID: "b-claim", TokenHash: reviews.HashToken("tercero"), Now: f.now.Add(time.Minute),
		ExpiresAt: f.now.Add(reviews.RequestTTL)}); err != nil || ok || id != "" {
		t.Fatalf("tercero: %q %v %v", id, ok, err)
	}
	// El envío falló: se deshace el reclamo y el próximo Claim rota el token.
	winner := 0
	if !claims[0] {
		winner = 1
	}
	if err := f.repo.Unclaim(ids[winner], f.now); err != nil {
		t.Fatalf("unclaim: %v", err)
	}
	newTok, _ := reviews.NewToken()
	newHash := reviews.HashToken(newTok)
	id2, ok, err := f.repo.Claim(ports.ReviewClaim{ID: domain.NewID(), SalonID: f.salonID,
		BookingID: "b-claim", TokenHash: newHash, Now: f.now.Add(2 * time.Minute),
		ExpiresAt: f.now.Add(reviews.RequestTTL)})
	if err != nil || !ok {
		t.Fatalf("reintento: %q %v %v", id2, ok, err)
	}
	if id2 != ids[winner] {
		t.Fatalf("el reintento creó otro pedido: %q vs %q", id2, ids[winner])
	}
	// El token anterior ya no sirve y el nuevo sí.
	if _, err := f.repo.Link(hashes[winner], f.now.Add(3*time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el token viejo sobrevive: %v", err)
	}
	if _, err := f.repo.Link(newHash, f.now.Add(3*time.Minute)); err != nil {
		t.Fatalf("el token nuevo no sirve: %v", err)
	}
	// Unclaim solo deshace SU reclamo: con otro sent_at no toca nada.
	if err := f.repo.Unclaim(id2, f.now); err != nil {
		t.Fatalf("unclaim ajeno: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM review_requests WHERE booking_id='b-claim' AND sent_at IS NOT NULL`); n != 1 {
		t.Fatalf("unclaim ajeno deshizo el reclamo: %d", n)
	}
}

// La configuración se guarda y se lee; sin fila es ErrNotFound (el caso de uso
// responde los valores de fábrica).
func TestReviewSettings_roundTrip(t *testing.T) {
	f := rvFixtures(t)
	if _, err := f.repo.Settings(f.salonID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sin guardar: %v", err)
	}
	if err := f.repo.SaveSettings(ports.ReviewSettings{SalonID: f.salonID, Enabled: true, DelayHours: 6}); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	st, err := f.repo.Settings(f.salonID)
	if err != nil || !st.Enabled || st.DelayHours != 6 || !st.Saved {
		t.Fatalf("leer: %+v %v", st, err)
	}
	// Un salón inexistente no escribe (FK): es un 400, no un 500.
	if err := f.repo.SaveSettings(ports.ReviewSettings{SalonID: "s-no-existe", Enabled: true, DelayHours: 6}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("salón inexistente: %v", err)
	}
}
