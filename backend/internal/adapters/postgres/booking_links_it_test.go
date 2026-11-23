// Integración del enlace firmado de un turno (F1) contra Postgres real
// (SALON_IT=1): lo que necesita la base de verdad, que es la concurrencia sobre
// el advisory lock `booking-write:<salón>` y las dos EXCLUDE de turnos.
package pgstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// itLinkMailer guarda lo enviado y dice si salió (las confirmaciones y los
// recordatorios viajan en goroutines).
type itLinkMailer struct {
	mu   sync.Mutex
	sent []ports.Message
}

func (m *itLinkMailer) Send(_ context.Context, msg ports.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *itLinkMailer) messages() []ports.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ports.Message(nil), m.sent...)
}

// itLinkEnv es el caso de uso del enlace cableado con los adaptadores reales,
// como cmd/api.
type itLinkEnv struct {
	pool     *pgxpool.Pool
	ctx      context.Context
	svc      app.BookingLinkService
	book     app.PublicBookingService
	links    BookingLinks
	bookings Bookings
	mails    *itLinkMailer
	salonID  string
	slug     string
	service  domain.Service
	profID   string
}

// newItLinkEnv deja un salón en CDMX (09:00-12:00 cada 30 min) con un servicio de
// 45 minutos, una profesional que lo hace y el enlace cableado.
func newItLinkEnv(t *testing.T) *itLinkEnv {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	applyMigrations(t, pool)
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-link%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-link%'`,
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
	emails := BookingEmails{Pool: pool}
	bookings := Bookings{Pool: pool}
	links := BookingLinks{Pool: pool}
	mails := &itLinkMailer{}

	salon := domain.Salon{ID: "s-it-link", Slug: "s-it-link", Name: "Salón IT Link"}
	if err := salons.Save(salon); err != nil {
		t.Fatalf("salón: %v", err)
	}
	other := domain.Salon{ID: "s-it-link2", Slug: "s-it-link2", Name: "Salón IT Link 2"}
	if err := salons.Save(other); err != nil {
		t.Fatalf("salón 2: %v", err)
	}
	settings, err := domain.NewSalonSettings(salon.ID, "America/Mexico_City", "09:00", "12:00", 30)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if err := salons.SaveSettings(settings); err != nil {
		t.Fatalf("guardar settings: %v", err)
	}
	if err := salons.SaveSettings(domain.SalonSettings{
		SalonID: other.ID, Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "12:00", SlotStepMin: 30,
	}); err != nil {
		t.Fatalf("settings del salón 2: %v", err)
	}

	profID := "u-it-link-prof"
	if err := users.Save(domain.User{ID: profID, Name: "Ana", Email: "ana-it-link@test.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuaria: %v", err)
	}
	if err := mems.Save(domain.Membership{
		UserID: profID, SalonID: salon.ID, Role: domain.RoleProfessional, Attends: true,
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	corte, err := domain.NewService(salon.ID, "Corte IT Link", 45, 150000, 20)
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
	svc := app.BookingLinkService{
		Links: links, Tx: links, Bookings: bookings, Salons: salons, Services: services,
		Staff: mems, Settings: salons, Professionals: profs, Hours: hours, TimeOff: timeOff,
		Resources: resources, Availability: availability,
		Mailer: mails, Emails: emails, AppURL: "http://localhost:8081",
	}
	publicBook := app.PublicBookingService{
		Salons: salons, Services: services, Settings: salons, Staff: mems, Bookings: bookings,
		Professionals: profs, Hours: hours, TimeOff: timeOff, Resources: resources,
		Mailer: mails, Emails: emails, Links: links, AppURL: "http://localhost:8081",
	}
	return &itLinkEnv{
		pool: pool, ctx: ctx, svc: svc, book: publicBook, links: links, bookings: bookings,
		mails: mails, salonID: salon.ID, slug: salon.Slug, service: corte, profID: profID,
	}
}

// itLinkDay devuelve un día futuro del salón (pasado mañana) y una función para
// ubicar minutos dentro de la ventana 09:00-12:00 del fixture.
func itLinkDay(t *testing.T) (string, func(minutes int) time.Time) {
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

// bookWithLink reserva por la ruta pública con email y devuelve el turno y el
// token que salió en la confirmación.
func (e *itLinkEnv) bookWithLink(t *testing.T, starts time.Time) (domain.Booking, string) {
	t.Helper()
	in := app.PublicBookingInput{
		ServiceID: e.service.ID, ProfessionalID: e.profID, ClientName: "Lía",
		ClientPhone: "555", ClientEmail: "lia-it-link@mail.com", StartsAt: starts,
	}
	before := len(e.mails.messages())
	b, err := e.book.Book(e.slug, in)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	// La confirmación viaja en goroutine: se espera el mensaje NUEVO y se
	// comprueba que su enlace es el de ESTE turno (no el de una reserva anterior).
	deadline := time.Now().Add(5 * time.Second)
	for {
		msgs := e.mails.messages()
		for i := before; i < len(msgs); i++ {
			token, ok := itTokenFromText(msgs[i].Text)
			if !ok {
				continue
			}
			if link, err := e.links.FindByTokenHash(domain.HashBookingLinkToken(token)); err == nil && link.BookingID == b.ID {
				return b, token
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("la confirmación no llevó un enlace del turno")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// mintLink emite un enlace para un turno sin pasar por el correo.
func (e *itLinkEnv) mintLink(t *testing.T, b domain.Booking) string {
	t.Helper()
	token, err := domain.NewBookingLinkToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	link, err := domain.NewBookingLink(b.SalonID, b.ID, domain.HashBookingLinkToken(token), time.Now().UTC())
	if err != nil {
		t.Fatalf("enlace: %v", err)
	}
	if err := e.links.Save(link); err != nil {
		t.Fatalf("guardar enlace: %v", err)
	}
	return token
}

func itTokenFromText(text string) (string, bool) {
	idx := strings.Index(text, "/turno#")
	if idx < 0 {
		return "", false
	}
	rest := text[idx+len("/turno#"):]
	if end := strings.IndexAny(rest, " \n\t\r<\"');,"); end >= 0 {
		rest = rest[:end]
	}
	if !domain.ValidBookingLinkToken(rest) {
		return "", false
	}
	return rest, true
}

// El flujo completo contra Postgres: la confirmación emite un enlace que el
// lookup acepta, se guarda SOLO el hash y cancelar libera el hueco para una
// reserva pública nueva de inmediato.
func TestBookingLinks_IT_flujo_y_hash(t *testing.T) {
	env := newItLinkEnv(t)
	_, at := itLinkDay(t)
	b, token := env.bookWithLink(t, at(0))

	view, err := env.svc.Lookup(token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if view.SalonName != "Salón IT Link" || view.ServiceName != "Corte IT Link" || view.ProfessionalName != "Ana" {
		t.Fatalf("lookup: %+v", view)
	}
	if !view.CanChange || view.Timezone != "America/Mexico_City" {
		t.Fatalf("lookup: %+v", view)
	}

	// Lo ÚNICO guardado del token es su SHA-256: ni el token ni su prefijo.
	var plain int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT COUNT(*) FROM booking_links
		  WHERE token_hash = $1 OR token_hash <> $2 OR id = $1 OR booking_id = $1`,
		token, domain.HashBookingLinkToken(token)).Scan(&plain); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if plain != 0 {
		t.Fatalf("hay %d filas que no guardan solo el SHA-256", plain)
	}
	var hash string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT token_hash FROM booking_links WHERE booking_id=$1`, b.ID).Scan(&hash); err != nil {
		t.Fatalf("fila del enlace: %v", err)
	}
	if hash != domain.HashBookingLinkToken(token) || strings.Contains(hash, token) {
		t.Fatalf("el hash guardado no es el SHA-256 del token: %q", hash)
	}

	// Cancelar libera el hueco para una reserva pública nueva ENSEGUIDA.
	if err := env.svc.Cancel(token); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	in := app.PublicBookingInput{
		ServiceID: env.service.ID, ProfessionalID: env.profID, ClientName: "Nueva", StartsAt: at(0),
	}
	if _, err := env.book.Book(env.slug, in); err != nil {
		t.Fatalf("el hueco liberado tiene que aceptar la reserva nueva: %v", err)
	}
}

// D4: 16 pedidos SIMULTÁNEOS con el mismo token: exactamente un 200 y el resto el
// 404 genérico, tanto al cancelar como al reprogramar.
func TestBookingLinks_IT_16_concurrentes_mismo_token(t *testing.T) {
	for _, op := range []string{"cancel", "reschedule"} {
		t.Run(op, func(t *testing.T) {
			env := newItLinkEnv(t)
			_, at := itLinkDay(t)
			b, token := env.bookWithLink(t, at(0))
			target := at(90)

			var wg sync.WaitGroup
			errs := make([]error, 16)
			for i := range errs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if op == "cancel" {
						errs[i] = env.svc.Cancel(token)
						return
					}
					_, _, errs[i] = env.svc.Reschedule(token, target)
				}(i)
			}
			wg.Wait()

			ok, invalid, other := 0, 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					ok++
				case errors.Is(err, domain.ErrNotFound) && err.Error() == domain.MsgBookingLinkInvalid:
					invalid++
				default:
					other++
					t.Logf("error inesperado: %v", err)
				}
			}
			if ok != 1 || invalid != 15 || other != 0 {
				t.Fatalf("%s: ok=%d inválidos=%d otros=%d", op, ok, invalid, other)
			}
			// Una sola fila de enlace, usada.
			var used int
			if err := env.pool.QueryRow(env.ctx,
				`SELECT COUNT(*) FROM booking_links WHERE booking_id=$1 AND used_at IS NOT NULL`, b.ID).Scan(&used); err != nil {
				t.Fatalf("contar usados: %v", err)
			}
			if used != 1 {
				t.Fatalf("enlaces usados = %d, esperaba 1", used)
			}
		})
	}
}

// Un horario ocupado es 409 y el token NO se consume: el siguiente intento con un
// hueco libre funciona.
func TestBookingLinks_IT_horario_ocupado_no_consume_el_token(t *testing.T) {
	env := newItLinkEnv(t)
	_, at := itLinkDay(t)
	b, token := env.bookWithLink(t, at(0))
	// Otra reserva pública en el hueco de las 10:00 (mismo profesional).
	in := app.PublicBookingInput{
		ServiceID: env.service.ID, ProfessionalID: env.profID, ClientName: "Otra", StartsAt: at(60),
	}
	if _, err := env.book.Book(env.slug, in); err != nil {
		t.Fatalf("reservar el hueco de las 10:00: %v", err)
	}

	_, _, err := env.svc.Reschedule(token, at(60))
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgBookingLinkSlotTaken {
		t.Fatalf("horario ocupado: %v", err)
	}
	if stored, err := env.bookings.FindByID(env.salonID, b.ID); err != nil || !stored.StartsAt.Equal(at(0)) {
		t.Fatalf("el turno no se movió: %+v %v", stored, err)
	}

	// El token sigue vivo: se reprograma a un hueco libre y el token nuevo del
	// correo funciona y el viejo da 404.
	view, sent, err := env.svc.Reschedule(token, at(120))
	if err != nil {
		t.Fatalf("reprogramar a un hueco libre: %v", err)
	}
	if !sent || !view.StartsAt.Equal(at(120)) {
		t.Fatalf("respuesta de la reprogramación: %+v sent=%v", view, sent)
	}
	if _, err := env.svc.Lookup(token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el token viejo tiene que morir: %v", err)
	}
	var newHash string
	if err := env.pool.QueryRow(env.ctx,
		`SELECT token_hash FROM booking_links WHERE booking_id=$1 AND used_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`, b.ID).Scan(&newHash); err != nil {
		t.Fatalf("token nuevo: %v", err)
	}
	// El correo del cambio lleva el enlace nuevo: se saca de ahí y se valida.
	var tokenNew string
	deadline := time.Now().Add(3 * time.Second)
	for tokenNew == "" {
		for _, m := range env.mails.messages() {
			if candidate, ok := itTokenFromText(m.Text); ok && domain.HashBookingLinkToken(candidate) == newHash {
				tokenNew = candidate
			}
		}
		if tokenNew != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("el correo de la reprogramación no llevó el token nuevo")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := env.svc.Lookup(tokenNew); err != nil {
		t.Fatalf("el token nuevo tiene que servir: %v", err)
	}
}

// Dos reprogramaciones de turnos DISTINTOS al MISMO hueco libre, a la vez: un solo
// ganador y nunca dos turnos solapados.
func TestBookingLinks_IT_dos_reprogramaciones_al_mismo_hueco(t *testing.T) {
	env := newItLinkEnv(t)
	_, at := itLinkDay(t)
	// Turnos del mismo profesional en 09:00 y 10:00 (45 min cada uno, no se pisan).
	first, tokenA := env.bookWithLink(t, at(0))
	second, tokenB := env.bookWithLink(t, at(60))
	target := at(120) // 11:00, libre para los dos

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, _, results[0] = env.svc.Reschedule(tokenA, target) }()
	go func() { defer wg.Done(); _, _, results[1] = env.svc.Reschedule(tokenB, target) }()
	wg.Wait()

	ok, taken := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrAlreadyExists):
			taken++
		default:
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if ok != 1 || taken != 1 {
		t.Fatalf("ganadores=%d rechazados=%d (esperaba 1 y 1)", ok, taken)
	}
	// Nunca solape: a lo sumo UN turno activo en el hueco.
	overlapA, err := env.bookings.FindOverlapping(env.salonID, env.profID, target, target.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("solape: %v", err)
	}
	if len(overlapA) != 1 {
		t.Fatalf("esperaba exactamente un turno en el hueco ganador, hubo %d: %+v", len(overlapA), overlapA)
	}
	// El perdedor conserva su horario original y su token sigue vivo.
	moved := overlapA[0].ID
	loserID, loserToken := first.ID, tokenA
	if moved == first.ID {
		loserID, loserToken = second.ID, tokenB
	}
	kept, err := env.bookings.FindByID(env.salonID, loserID)
	if err != nil || kept.StartsAt.Equal(target) {
		t.Fatalf("el perdedor no se movió: %+v %v", kept, err)
	}
	if _, err := env.svc.Lookup(loserToken); err != nil {
		t.Fatalf("el 409 no consume el token del perdedor: %v", err)
	}
}

// El turno de OTRO salón nunca se toca, ni con una fila de enlace que apunte mal.
func TestBookingLinks_IT_otro_salon(t *testing.T) {
	env := newItLinkEnv(t)
	_, at := itLinkDay(t)
	// La FK de bookings exige que el profesional sea del salón: se crea una
	// profesional del segundo salón (nadie más la usa).
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-link-otro','Otra','otra-it-link@test.com','x')
		 ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatalf("usuaria del salón 2: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO memberships(user_id,salon_id,role,attends) VALUES('u-it-link-otro','s-it-link2','professional',true)
		 ON CONFLICT (user_id, salon_id) DO NOTHING`); err != nil {
		t.Fatalf("membership del salón 2: %v", err)
	}
	ajeno := domain.Booking{
		ID: domain.NewID(), SalonID: "s-it-link2", ProfessionalID: "u-it-link-otro",
		ClientName: "De otro salón", StartsAt: at(0).UTC(), EndsAt: at(0).Add(45 * time.Minute).UTC(),
		Status: domain.BookingBooked, CreatedAt: time.Now().UTC(),
	}
	if err := env.bookings.Save(ajeno); err != nil {
		t.Fatalf("turno ajeno: %v", err)
	}
	token, _ := domain.NewBookingLinkToken()
	link, err := domain.NewBookingLink(env.salonID, ajeno.ID, domain.HashBookingLinkToken(token), time.Now().UTC())
	if err != nil {
		t.Fatalf("enlace: %v", err)
	}
	if err := env.links.Save(link); err != nil {
		t.Fatalf("guardar enlace: %v", err)
	}
	if err := env.svc.Cancel(token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("un turno de otro salón no existe para el enlace: %v", err)
	}
	if stored, err := env.bookings.FindByID("s-it-link2", ajeno.ID); err != nil || stored.Status != domain.BookingBooked {
		t.Fatalf("el turno ajeno no se toca: %+v %v", stored, err)
	}
}

// El recordatorio emite SU token (nuevo) y el lookup lo acepta; el enlace de la
// confirmación sigue vivo hasta que se usa (D2).
func TestBookingLinks_IT_recordatorio(t *testing.T) {
	env := newItLinkEnv(t)
	// Un turno que empieza dentro de 24 h y cuya reserva se hizo con 24 h de
	// anticipación (el reclamo del recordatorio exige las dos cosas): se arma la
	// fila directo para no depender del horario del salón.
	starts := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Minute)
	b := domain.Booking{
		ID: domain.NewID(), SalonID: env.salonID, ProfessionalID: env.profID,
		ClientName: "Lía", StartsAt: starts, EndsAt: starts.Add(45 * time.Minute),
		Status: domain.BookingBooked, ServiceID: env.service.ID, CreatedAt: time.Now().UTC(),
	}
	if err := env.bookings.Save(b); err != nil {
		t.Fatalf("turno: %v", err)
	}
	if err := (BookingEmails{Pool: env.pool}).Save(env.salonID, b.ID, "lia-it-link@mail.com"); err != nil {
		t.Fatalf("email del turno: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE bookings SET created_at = now() - interval '2 days' WHERE id=$1`, b.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	tokenConfirm := env.mintLink(t, b)

	reminders := app.ReminderService{
		Emails: BookingEmails{Pool: env.pool}, Mailer: env.mails, Settings: Salons{Pool: env.pool},
		Links: env.links, AppURL: "http://localhost:8081",
	}
	sent, failed := reminders.RunOnce(env.ctx, time.Now().UTC())
	if failed != 0 || sent != 1 {
		t.Fatalf("recordatorios: enviados=%d fallidos=%d", sent, failed)
	}
	var tokenReminder string
	for _, m := range env.mails.messages() {
		if !strings.Contains(m.Subject, "Recordatorio") {
			continue
		}
		if candidate, ok := itTokenFromText(m.Text); ok && candidate != tokenConfirm {
			tokenReminder = candidate
		}
	}
	if tokenReminder == "" {
		t.Fatal("el recordatorio no llevó un enlace nuevo")
	}
	if _, err := env.svc.Lookup(tokenReminder); err != nil {
		t.Fatalf("el enlace del recordatorio tiene que servir: %v", err)
	}
	if _, err := env.svc.Lookup(tokenConfirm); err != nil {
		t.Fatalf("el enlace de la confirmación sigue vivo: %v", err)
	}
	var count int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT COUNT(*) FROM booking_links WHERE booking_id=$1`, b.ID).Scan(&count); err != nil {
		t.Fatalf("contar enlaces: %v", err)
	}
	if count != 2 {
		t.Fatalf("esperaba dos enlaces (confirmación + recordatorio), hubo %d", count)
	}
}

// La migración 027 es idempotente y la tabla existe con su CHECK y su índice
// único (la reaplican TestMain y applyMigrations, que corre todo dos veces).
func TestMigration027_IT_idempotente(t *testing.T) {
	pool := itPool(t)
	t.Cleanup(pool.Close)
	applyMigrations(t, pool)
	ctx := context.Background()
	var def string
	if err := pool.QueryRow(ctx,
		`SELECT column_default FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name='booking_links' AND column_name='created_at'`).Scan(&def); err != nil {
		t.Fatalf("booking_links no existe: %v", err)
	}
	if !strings.Contains(def, "now()") {
		t.Fatalf("created_at default: %q", def)
	}
	var hasCheck, hasUnique, hasIndex bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='booking_links_token_hash_len')`).Scan(&hasCheck); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='booking_links_token_hash_key')`).Scan(&hasUnique); err != nil {
		t.Fatalf("unique: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='booking_links_booking_id_idx')`).Scan(&hasIndex); err != nil {
		t.Fatalf("índice: %v", err)
	}
	if !hasCheck || !hasUnique || !hasIndex {
		t.Fatalf("faltan objetos de la 027: check=%v unique=%v index=%v", hasCheck, hasUnique, hasIndex)
	}
	// El CHECK rechaza un hash que no mida 64.
	_, err := pool.Exec(ctx,
		`INSERT INTO booking_links(id,salon_id,booking_id,token_hash) VALUES('bad','s-it-link','no-existe','corto')`)
	if err == nil {
		t.Fatal("el CHECK del hash tiene que rechazar un valor corto")
	}
}
