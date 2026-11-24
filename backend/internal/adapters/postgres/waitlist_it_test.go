// Integración de la lista de espera (F7) contra Postgres real (SALON_IT=1): lo
// que necesita la base de verdad, que es el índice único parcial de las entradas
// activas, el advisory lock `waitlist-write:<salón>` con FOR UPDATE SKIP LOCKED,
// la retención blanda de 2 h y el tope de 3 intentos. Corre con:
//
//	SALON_IT=1 DATABASE_URL=<scratch> go test ./internal/adapters/postgres/ -run Waitlist -v
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// itWaitlistMailer registra lo enviado y puede fallar a pedido.
type itWaitlistMailer struct {
	mu   sync.Mutex
	sent []ports.Message
	err  error
}

func (m *itWaitlistMailer) Send(_ context.Context, msg ports.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

func (m *itWaitlistMailer) messages() []ports.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ports.Message(nil), m.sent...)
}

func (m *itWaitlistMailer) setErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// itWaitlistEnv es el caso de uso cableado con los adaptadores reales.
type itWaitlistEnv struct {
	pool     *pgxpool.Pool
	ctx      context.Context
	svc      app.WaitlistService
	repo     Waitlist
	bookings Bookings
	mails    *itWaitlistMailer
	salonID  string
	otherID  string
	slug     string
	service  domain.Service
	profID   string
}

// createEntry inserta una entrada por la primitiva de la transacción del alta
// (el repositorio ya no tiene Create suelto: el alta corre bajo el lock del salón).
// Devuelve el error del insert tal cual (ErrAlreadyExists si el índice único parcial
// lo rechaza).
func (e *itWaitlistEnv) createEntry(entry domain.WaitlistEntry) error {
	return e.repo.InWaitlistTx(func(tx ports.WaitlistTx) error { return tx.Create(entry) })
}

func newItWaitlistEnv(t *testing.T) *itWaitlistEnv {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	applyMigrations(t, pool)
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-wl%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-wl%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}

	salons := Salons{Pool: pool}
	users := Users{Pool: pool}
	mems := Memberships{Pool: pool}
	services := Services{Pool: pool}
	profs := ProfessionalServices{Pool: pool}
	hours := WeeklyHours{Pool: pool}
	timeOff := TimeOff{Pool: pool}
	resources := Resources{Pool: pool}
	bookings := Bookings{Pool: pool}
	waitlist := Waitlist{Pool: pool}
	mails := &itWaitlistMailer{}

	salon := domain.Salon{ID: "s-it-wl", Slug: "s-it-wl", Name: "Salón IT Lista"}
	if err := salons.Save(salon); err != nil {
		t.Fatalf("salón: %v", err)
	}
	other := domain.Salon{ID: "s-it-wl2", Slug: "s-it-wl2", Name: "Salón IT Lista 2"}
	if err := salons.Save(other); err != nil {
		t.Fatalf("salón 2: %v", err)
	}
	for _, id := range []string{salon.ID, other.ID} {
		if err := salons.SaveSettings(domain.SalonSettings{
			SalonID: id, Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "12:00", SlotStepMin: 30,
		}); err != nil {
			t.Fatalf("settings %s: %v", id, err)
		}
	}

	profID := "u-it-wl-prof"
	if err := users.Save(domain.User{ID: profID, Name: "Ana Lista", Email: "ana-it-wl@test.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuaria: %v", err)
	}
	if err := users.Save(domain.User{ID: "u-it-wl-owner", Name: "Dueña", Email: "duena-it-wl@test.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("dueña: %v", err)
	}
	if err := mems.Save(domain.Membership{
		UserID: profID, SalonID: salon.ID, Role: domain.RoleProfessional, Attends: true,
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	corte, err := domain.NewService(salon.ID, "Corte IT Lista", 45, 150000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	if err := services.Save(corte); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	if err := profs.SetServices(salon.ID, profID, []string{corte.ID}); err != nil {
		t.Fatalf("servicios de la profesional: %v", err)
	}

	availability := app.AvailabilityService{
		Salons: salons, Services: services, Settings: salons, Staff: mems, Bookings: bookings,
		Professionals: profs, Hours: hours, TimeOff: timeOff, Resources: resources,
	}
	svc := app.WaitlistService{
		Waitlist: waitlist, Tx: waitlist,
		Salons: salons, Services: services, Professionals: profs, Settings: salons,
		Availability: availability, Memberships: mems,
		Mailer: mails, AppURL: "http://localhost:8081",
	}
	return &itWaitlistEnv{
		pool: pool, ctx: ctx, svc: svc, repo: waitlist, bookings: bookings, mails: mails,
		salonID: salon.ID, otherID: other.ID, slug: salon.Slug, service: corte, profID: profID,
	}
}

// itWaitlistDay es un día futuro del salón (pasado mañana) y una función para
// ubicar minutos dentro de la ventana 09:00-12:00.
func itWaitlistDay(t *testing.T) (string, func(minutes int) time.Time) {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	d := time.Now().In(loc).AddDate(0, 0, 2)
	day := time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, loc)
	return day.Format("2006-01-02"), func(minutes int) time.Time {
		return day.Add(time.Duration(minutes) * time.Minute)
	}
}

// seedEntry deja una entrada como el alta del caso de uso (sin token: la
// capability nace con el aviso) y devuelve su id.
func (e *itWaitlistEnv) seedEntry(t *testing.T, professionalID, day, name, email string, createdAt time.Time) string {
	t.Helper()
	contact, err := domain.NewWaitlistContact(name, email, "")
	if err != nil {
		t.Fatalf("contacto: %v", err)
	}
	entry, err := domain.NewWaitlistEntry(e.salonID, e.service.ID, professionalID, day, contact, createdAt)
	if err != nil {
		t.Fatalf("entrada: %v", err)
	}
	// created_at explícito para que el FIFO sea determinista.
	if _, err := e.pool.Exec(e.ctx,
		`INSERT INTO waitlist_entries(id,salon_id,service_id,professional_id,day,name,email,created_at)
		 VALUES($1,$2,$3,NULLIF($4,''),$5::date,$6,$7,$8)`,
		entry.ID, e.salonID, e.service.ID, professionalID, day, entry.Name, entry.Email, createdAt.UTC(),
	); err != nil {
		t.Fatalf("guardar entrada: %v", err)
	}
	return entry.ID
}

// entryRow lee lo que quedó en la DB (intentos, reclamo y aviso).
func (e *itWaitlistEnv) entryRow(t *testing.T, id string) (attempts int, claimed, notified *time.Time, tokenHash string) {
	t.Helper()
	err := e.pool.QueryRow(e.ctx,
		`SELECT notify_attempts, notify_claimed_at, notified_at, coalesce(token_hash,'')
		   FROM waitlist_entries WHERE id=$1`, id).Scan(&attempts, &claimed, &notified, &tokenHash)
	if err != nil {
		t.Fatalf("leer entrada: %v", err)
	}
	return
}

// blockFrom llena la agenda de la profesional desde `minutes` hasta el cierre con
// UN turno (el EXCLUDE no admite solapes por profesional): deja libre solo el
// arranque de las 09:00.
func (e *itWaitlistEnv) blockFrom(t *testing.T, minutes int) {
	t.Helper()
	_, at := itWaitlistDay(t)
	start := at(minutes)
	end := at(3 * 60) // 12:00 local
	b, err := domain.NewBooking(e.salonID, e.profID, "Ocupa", "", start.UTC(), end.UTC())
	if err != nil {
		t.Fatalf("reserva: %v", err)
	}
	if err := e.bookings.Save(b); err != nil {
		t.Fatalf("guardar reserva: %v", err)
	}
}

// D4/D8 con Postgres real: alta, idempotencia por el índice parcial, topes,
// listado del personal con nombres, baja y aislamiento por salón.
func TestWaitlist_IT_flujo(t *testing.T) {
	env := newItWaitlistEnv(t)
	day, at := itWaitlistDay(t)

	// Con horarios libres el alta es 409: la lista no sirve para reservar.
	if _, err := env.svc.Join(env.slug, app.WaitlistJoinInput{
		ServiceID: env.service.ID, Date: day, Name: "Lía", Email: "lia@mail.com",
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("con horarios libres: %v", err)
	}
	// Ocupando el día entero (una reserva larga) no hay horarios: entra.
	env.blockFrom(t, 0)
	res, err := env.svc.Join(env.slug, app.WaitlistJoinInput{
		ServiceID: env.service.ID, ProfessionalID: env.profID, Date: day, Name: " Lía ", Email: "LIA@mail.com ",
	})
	if err != nil || !res.Created || res.Status != domain.WaitlistStatusWaiting {
		t.Fatalf("alta: %+v %v", res, err)
	}
	// Repetido idéntico: idempotente, sin otra fila (índice único parcial).
	again, err := env.svc.Join(env.slug, app.WaitlistJoinInput{
		ServiceID: env.service.ID, ProfessionalID: env.profID, Date: day, Name: "Lía", Email: "lia@mail.com",
	})
	if err != nil || again.Created {
		t.Fatalf("repetido: %+v %v", again, err)
	}
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*) FROM waitlist_entries WHERE salon_id=$1`, env.salonID).Scan(&count); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if count != 1 {
		t.Fatalf("tenía que quedar 1 entrada, hay %d", count)
	}
	// El token no existe hasta que hay un aviso.
	var tokenHash *string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT token_hash FROM waitlist_entries WHERE salon_id=$1`, env.salonID).Scan(&tokenHash); err != nil {
		t.Fatalf("token: %v", err)
	}
	if tokenHash != nil {
		t.Fatalf("una entrada sin avisar no tiene token: %q", *tokenHash)
	}

	// Topes: 3 activos por email del mismo email (otros días, sembrados directo).
	base := time.Now().UTC()
	for i := 0; i < domain.MaxWaitlistActivePerEmail; i++ {
		d := at((i + 3) * 24 * 60).Format("2006-01-02")
		env.seedEntry(t, "", d, "Lía", "lia@mail.com", base.Add(time.Duration(i)*time.Minute))
	}
	// Un cuarto intento (el mismo día bloqueado, sin profesional) choca con el
	// tope de email: el 409 es el del contrato y no escribe nada.
	if _, err := env.svc.Join(env.slug, app.WaitlistJoinInput{
		ServiceID: env.service.ID, Date: day, Name: "Lía", Email: "lia@mail.com",
	}); !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgWaitlistEmailLimit {
		t.Fatalf("tope por email: %v", err)
	}

	// El listado del personal trae los nombres y sale por día y alta.
	owner := domain.Membership{UserID: "u-it-wl-owner", SalonID: env.salonID, Role: domain.RoleOwner}
	mems := Memberships{Pool: env.pool}
	if err := mems.Save(owner); err != nil {
		t.Fatalf("dueña: %v", err)
	}
	list, err := env.svc.List(owner, env.salonID)
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) < 4 {
		t.Fatalf("esperaba al menos 4 entradas, hay %d", len(list))
	}
	if list[0].ServiceName != "Corte IT Lista" || list[0].ProfessionalName != "Ana Lista" {
		t.Fatalf("nombres del JOIN: %+v", list[0])
	}
	prev := ""
	for _, e := range list {
		if e.Day < prev {
			t.Fatalf("orden por día: %+v", list)
		}
		prev = e.Day
	}
	// Listar con un profesional es 403 (D8).
	prof := domain.Membership{UserID: env.profID, SalonID: env.salonID, Role: domain.RoleProfessional, Attends: true}
	if _, err := env.svc.List(prof, env.salonID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional lista: %v", err)
	}
	// Dar de baja: 204 y la segunda vez 404 (D8). La entrada de otro salón es 404.
	if err := env.svc.Remove(owner, env.salonID, list[0].ID); err != nil {
		t.Fatalf("quitar: %v", err)
	}
	if err := env.svc.Remove(owner, env.salonID, list[0].ID); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgWaitlistNotFound {
		t.Fatalf("quitar dos veces: %v", err)
	}
	if err := env.svc.Remove(owner, env.otherID, list[1].ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("salón ajeno: %v", err)
	}
}

// D6 con Postgres real: DOS pasadas simultáneas sobre 1 hueco y 2 entradas avisan
// a UNA sola (FIFO), y la siguiente espera a que venza la retención.
func TestWaitlist_IT_concurrencia_avisa_una(t *testing.T) {
	env := newItWaitlistEnv(t)
	day, _ := itWaitlistDay(t)
	// El único hueco de la profesional es el de las 09:00 (09:00-09:45): el
	// turno que ocupa el resto arranca a las 09:45.
	env.blockFrom(t, 45)

	base := time.Now().UTC()
	first := env.seedEntry(t, env.profID, day, "Lía", "lia@mail.com", base.Add(-2*time.Minute))
	second := env.seedEntry(t, env.profID, day, "Ana", "ana@mail.com", base.Add(-time.Minute))

	var wg sync.WaitGroup
	var mu sync.Mutex
	total, failed := 0, 0
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, f := env.svc.RunOnce(context.Background(), now)
			mu.Lock()
			total += n
			failed += f
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 1 || failed != 0 {
		t.Fatalf("dos pasadas simultáneas: avisos=%d fallidos=%d", total, failed)
	}
	msgs := env.mails.messages()
	if len(msgs) != 1 || msgs[0].To != "lia@mail.com" {
		t.Fatalf("FIFO: %+v", msgs)
	}
	// La primera quedó avisada (intentos 1) y la segunda intacta.
	if attempts, claimed, notified, hash := env.entryRow(t, first); attempts != 1 || claimed == nil || notified == nil || len(hash) != 64 {
		t.Fatalf("la primera: intentos=%d claimed=%v notified=%v hash=%q", attempts, claimed, notified, hash)
	}
	if attempts, claimed, notified, hash := env.entryRow(t, second); attempts != 0 || claimed != nil || notified != nil || hash != "" {
		t.Fatalf("la segunda no se toca: intentos=%d claimed=%v notified=%v hash=%q", attempts, claimed, notified, hash)
	}
	// Otro tick inmediato: la retención de 2 h sigue ocupando el hueco.
	if n, _ := env.svc.RunOnce(context.Background(), time.Now().UTC()); n != 0 {
		t.Fatalf("la retención tiene que frenar el segundo aviso, avisos=%d", n)
	}
	// Vencida la retención (reclamo de hace 3 h), la siguiente recibe su aviso.
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE waitlist_entries SET notify_claimed_at = now() - interval '3 hours' WHERE id=$1`, first); err != nil {
		t.Fatalf("envejecer el reclamo: %v", err)
	}
	if n, _ := env.svc.RunOnce(context.Background(), time.Now().UTC()); n != 1 {
		t.Fatalf("con la retención vencida tenía que avisar a la segunda, avisos=%d", n)
	}
	if msgs := env.mails.messages(); len(msgs) != 2 || msgs[1].To != "ana@mail.com" {
		t.Fatalf("segundo aviso: %+v", msgs)
	}
	// Y una entrada ya avisada no se vuelve a avisar NUNCA.
	if n, _ := env.svc.RunOnce(context.Background(), time.Now().UTC()); n != 0 {
		t.Fatalf("una entrada sola se avisa una vez, avisos=%d", n)
	}
}

// D6 con Postgres real: un envío fallido se reintenta pasada la gracia de 5
// minutos, hasta 3 intentos, y después no se reclama más.
func TestWaitlist_IT_reintento_tope_tres(t *testing.T) {
	env := newItWaitlistEnv(t)
	day, _ := itWaitlistDay(t)
	env.blockFrom(t, 45) // queda UN hueco: el de las 09:00 (09:00-09:45)
	entry := env.seedEntry(t, env.profID, day, "Lía", "lia@mail.com", time.Now().UTC())
	env.mails.setErr(errors.New("smtp caído"))

	if n, f := env.svc.RunOnce(context.Background(), time.Now().UTC()); n != 0 || f != 1 {
		t.Fatalf("primer intento: avisos=%d fallidos=%d", n, f)
	}
	if attempts, claimed, notified, _ := env.entryRow(t, entry); attempts != 1 || claimed == nil || notified != nil {
		t.Fatalf("tras el fallo: intentos=%d claimed=%v notified=%v", attempts, claimed, notified)
	}
	// Dentro de la gracia no vuelve a reclamarse.
	if n, f := env.svc.RunOnce(context.Background(), time.Now().UTC()); n != 0 || f != 0 {
		t.Fatalf("dentro de la gracia: avisos=%d fallidos=%d", n, f)
	}
	// Pasada la gracia, dos reintentos más hasta el tope.
	for want := 2; want <= domain.MaxWaitlistAttempts; want++ {
		if _, err := env.pool.Exec(env.ctx,
			`UPDATE waitlist_entries SET notify_claimed_at = now() - interval '6 minutes' WHERE id=$1`, entry); err != nil {
			t.Fatalf("envejecer: %v", err)
		}
		if _, f := env.svc.RunOnce(context.Background(), time.Now().UTC()); f != 1 {
			t.Fatalf("intento %d: fallidos=%d", want, f)
		}
		if attempts, _, _, _ := env.entryRow(t, entry); attempts != want {
			t.Fatalf("intentos: esperaba %d, hay %d", want, attempts)
		}
	}
	// Con 3 intentos gastados no se reclama nunca más (aunque el hueco siga).
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE waitlist_entries SET notify_claimed_at = now() - interval '24 hours' WHERE id=$1`, entry); err != nil {
		t.Fatalf("envejecer: %v", err)
	}
	if n, f := env.svc.RunOnce(context.Background(), time.Now().UTC()); n != 0 || f != 0 {
		t.Fatalf("agotado: avisos=%d fallidos=%d", n, f)
	}
	if len(env.mails.messages()) != 0 {
		t.Fatalf("no tenía que salir ningún email: %+v", env.mails.messages())
	}
}

// La migración 030 es idempotente y NO toca los datos previos: sobre una base
// con turnos y fichas, aplicar el archivo otra vez deja los md5 iguales.
func TestWaitlist_IT_migracion_sobre_datos_previos(t *testing.T) {
	env := newItWaitlistEnv(t)
	// Datos previos: una ficha y un turno del salón.
	clients := Clients{Pool: env.pool}
	c, err := domain.NewClient(env.salonID, "Clienta Previa", "555", "previa@mail.com", "")
	if err != nil {
		t.Fatalf("ficha: %v", err)
	}
	if err := clients.Save(c); err != nil {
		t.Fatalf("guardar ficha: %v", err)
	}
	_, at := itWaitlistDay(t)
	b, err := domain.NewBooking(env.salonID, env.profID, "Previa", "555", at(0).UTC(), at(45).UTC())
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	if err := env.bookings.Save(b); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}

	before := md5Of(t, env.pool, "bookings")
	clientsBefore := md5Of(t, env.pool, "clients")

	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "030_waitlist.sql"))
	if err != nil {
		t.Fatalf("leer migración: %v", err)
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := env.pool.Exec(env.ctx, string(sql)); err != nil {
			t.Fatalf("aplicar 030 (pasada %d): %v", pass, err)
		}
	}
	if after := md5Of(t, env.pool, "bookings"); after != before {
		t.Fatalf("bookings cambió: %s -> %s", before, after)
	}
	if after := md5Of(t, env.pool, "clients"); after != clientsBefore {
		t.Fatalf("clients cambió: %s -> %s", clientsBefore, after)
	}

	// Los CHECKs y el índice único parcial, a mano: la base es la que dice que no.
	contact, _ := domain.NewWaitlistContact("Lía", "lia@mail.com", "")
	entry, err := domain.NewWaitlistEntry(env.salonID, env.service.ID, "", "2027-01-01", contact, time.Now().UTC())
	if err != nil {
		t.Fatalf("entrada: %v", err)
	}
	if err := env.createEntry(entry); err != nil {
		t.Fatalf("alta: %v", err)
	}
	// Dos activas idénticas: el índice parcial rechaza la segunda.
	dup := entry
	dup.ID = domain.NewID()
	if err := env.createEntry(dup); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicado activo: %v", err)
	}
	// Nombre vacío, email demasiado corto y 3+ intentos: CHECK rechazados.
	for name, q := range map[string]string{
		"nombre vacío":       `UPDATE waitlist_entries SET name='' WHERE id='%s'`,
		"email corto":        `UPDATE waitlist_entries SET email='ab' WHERE id='%s'`,
		"intentos 4":         `UPDATE waitlist_entries SET notify_attempts=4 WHERE id='%s'`,
		"token no hex":       `UPDATE waitlist_entries SET token_hash='zz' WHERE id='%s'`,
		"notified sin claim": `UPDATE waitlist_entries SET notified_at=now() WHERE id='%s'`,
	} {
		if _, err := env.pool.Exec(env.ctx, fmt.Sprintf(q, entry.ID)); err == nil {
			t.Fatalf("%s: el CHECK tenía que rechazarlo", name)
		}
	}
	// Una BAJA libera el lugar: la misma entrada se puede volver a crear.
	left := time.Now().UTC()
	if err := env.repo.Leave("", left); err != nil {
		t.Fatalf("baja sin token: %v", err)
	}
	if err := env.repo.RemoveByID(env.salonID, entry.ID, left); err != nil {
		t.Fatalf("baja: %v", err)
	}
	if err := env.createEntry(dup); err != nil {
		t.Fatalf("después de la baja la misma entrada entra: %v", err)
	}
}

// md5Of calcula el md5 de TODAS las filas de una tabla (una sola sentencia):
// sirve para probar que una migración no reescribió nada.
func md5Of(t *testing.T, pool *pgxpool.Pool, table string) string {
	t.Helper()
	var out string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(md5(string_agg(t::text, '|' ORDER BY t::text)),'') FROM `+table+` t`).Scan(&out); err != nil {
		t.Fatalf("md5 de %s: %v", table, err)
	}
	return out
}

// La baja por enlace funciona con el token del AVISO (el hash que quedó en la
// DB), es idempotente y un token desconocido es ErrNotFound (D5).
func TestWaitlist_IT_leave_por_token_del_aviso(t *testing.T) {
	env := newItWaitlistEnv(t)
	day, _ := itWaitlistDay(t)
	env.blockFrom(t, 45) // queda UN hueco: el de las 09:00 (09:00-09:45)
	entry := env.seedEntry(t, env.profID, day, "Lía", "lia@mail.com", time.Now().UTC())

	if n, _ := env.svc.RunOnce(context.Background(), time.Now().UTC()); n != 1 {
		t.Fatalf("aviso: %d", n)
	}
	msgs := env.mails.messages()
	if len(msgs) != 1 {
		t.Fatalf("emails: %d", len(msgs))
	}
	const marker = "/lista-de-espera#"
	idx := strings.Index(msgs[0].Text, marker)
	if idx < 0 {
		t.Fatalf("el email no lleva el enlace de baja:\n%s", msgs[0].Text)
	}
	token := strings.TrimSpace(msgs[0].Text[idx+len(marker):])
	if i := strings.IndexAny(token, " \n"); i >= 0 {
		token = token[:i]
	}
	// El hash guardado es el del token enviado (nunca el token en claro).
	if _, _, _, hash := env.entryRow(t, entry); hash != domain.HashWaitlistToken(token) {
		t.Fatalf("el hash no coincide con el token enviado")
	}
	if err := env.svc.Leave(token); err != nil {
		t.Fatalf("baja: %v", err)
	}
	if err := env.svc.Leave(token); err != nil {
		t.Fatalf("baja repetida: %v", err)
	}
	if status := statusOfEntry(t, env, entry); status != domain.WaitlistStatusLeft {
		t.Fatalf("estado después de la baja: %q", status)
	}
	other, _ := domain.NewWaitlistToken()
	if err := env.svc.Leave(other); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgWaitlistNotFound {
		t.Fatalf("token desconocido: %v", err)
	}
}

func statusOfEntry(t *testing.T, env *itWaitlistEnv, id string) string {
	t.Helper()
	var leftAt, notifiedAt *time.Time
	if err := env.pool.QueryRow(env.ctx,
		`SELECT left_at, notified_at FROM waitlist_entries WHERE id=$1`, id).Scan(&leftAt, &notifiedAt); err != nil {
		t.Fatalf("estado: %v", err)
	}
	switch {
	case leftAt != nil:
		return domain.WaitlistStatusLeft
	case notifiedAt != nil:
		return domain.WaitlistStatusNotified
	default:
		return domain.WaitlistStatusWaiting
	}
}

// Revisión nativa R4-001 (CRITICAL), contra Postgres real: los topes de D4 solo
// cuentan los avisos de HOY en adelante en la zona del salón. Tres avisos sin
// baja de días pasados no bloquean al email, y los de un día futuro sí cuentan.
func TestWaitlist_IT_topes_ignoran_dias_pasados(t *testing.T) {
	env := newItWaitlistEnv(t)
	day, _ := itWaitlistDay(t)
	env.blockFrom(t, 0)
	now := time.Now().UTC()

	for i := 1; i <= domain.MaxWaitlistActivePerEmail; i++ {
		past := now.AddDate(0, 0, -i).Format("2006-01-02")
		env.seedEntry(t, "", past, "Lía", "lia@mail.com", now.Add(time.Duration(i)*time.Minute))
	}
	res, err := env.svc.Join(env.slug, app.WaitlistJoinInput{
		ServiceID: env.service.ID, ProfessionalID: env.profID, Date: day, Name: "Lía", Email: "lia@mail.com",
	})
	if err != nil || !res.Created {
		t.Fatalf("avisos de días pasados no cuentan para el tope: %+v %v", res, err)
	}
	// Un aviso de un día futuro SÍ cuenta: con 3 de hoy en adelante el 4° es 409.
	for i := 2; i <= domain.MaxWaitlistActivePerEmail; i++ {
		future := now.AddDate(0, 0, i+1).Format("2006-01-02")
		env.seedEntry(t, "", future, "Lía", "lia@mail.com", now.Add(time.Duration(10+i)*time.Minute))
	}
	_, err = env.svc.Join(env.slug, app.WaitlistJoinInput{
		ServiceID: env.service.ID, Date: day, Name: "Lía", Email: "lia@mail.com",
	})
	if err == nil || err.Error() != domain.MsgWaitlistEmailLimit {
		t.Fatalf("los avisos vigentes siguen contando para el tope: %v", err)
	}
}
