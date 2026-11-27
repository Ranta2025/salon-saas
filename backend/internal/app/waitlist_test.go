package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/waitlist"
)

// Contrato F7 (lista de espera): el alta pública, la baja por enlace, el barrido
// que avisa sin repetir y la pantalla del personal. La concurrencia REAL (dos
// instancias con Postgres, el advisory lock y FOR UPDATE SKIP LOCKED) vive en
// los IT del adaptador; acá se fija el contrato del caso de uso con fakes.

const waitlistSlug = "navajas-oro"

type waitlistEnv struct {
	*publicEnv
	wl   *fakeWaitlist
	tx   *fakeWaitlistTx
	mems *fakeMemberships
	svc  waitlist.WaitlistService
}

// newWaitlistEnv arma la lista sobre la agenda pública (salón s1 en CDMX,
// servicio «Corte» de 45 minutos, prof1 y prof2 que lo hacen) con una ventana
// de 09:00 a 10:00: un solo hueco por persona, que es lo que D6 necesita para
// probar la retención con dos entradas.
func newWaitlistEnv(t *testing.T) *waitlistEnv {
	t.Helper()
	pub := newPublicEnv(t)
	pub.settings.BySalon["s1"] = domain.SalonSettings{
		SalonID: "s1", Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "10:00", SlotStepMin: 30,
	}
	pub.profs.ByService = map[string][]string{"s1|" + pub.corte.ID: {"prof1", "prof2"}}
	wl := &fakeWaitlist{}
	tx := &fakeWaitlistTx{repo: wl}
	mems := &fakeMemberships{}
	// El canal del aviso: un sender que registra lo enviado (nunca nil, salvo el
	// test que prueba justamente que sin email no se reclama nada).
	pub.mailer = &fakeMailer{}
	svc := waitlist.WaitlistService{
		Waitlist: wl, Tx: tx,
		Salons: pub.avail.Salons, Services: pub.services, Settings: pub.settings,
		Professionals: pub.profs, Availability: pub.avail, Memberships: mems,
		Mailer: pub.mailer, AppURL: linkAppURL,
	}
	return &waitlistEnv{publicEnv: pub, wl: wl, tx: tx, mems: mems, svc: svc}
}

// wlDay devuelve el día LOCAL del salón a `offset` días de hoy y su inicio.
func wlDay(t *testing.T, offset int) (string, time.Time) {
	t.Helper()
	loc := mxLocation(t)
	d := time.Now().In(loc).AddDate(0, 0, offset)
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	return start.Format("2006-01-02"), start
}

// blockDay ocupa el único hueco de la ventana 09:00-10:00 de las dos
// profesionales: la disponibilidad del día queda en [] (sin horarios).
func (e *waitlistEnv) blockDay(t *testing.T, day string) {
	t.Helper()
	loc := mxLocation(t)
	d, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		t.Fatalf("día: %v", err)
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, loc).UTC()
	end := start.Add(45 * time.Minute)
	for _, prof := range []string{"prof1", "prof2"} {
		b, err := domain.NewBooking("s1", prof, "Ocupa", "", start, end)
		if err != nil {
			t.Fatalf("reserva: %v", err)
		}
		if err := e.bookings.Save(b); err != nil {
			t.Fatalf("guardar reserva: %v", err)
		}
	}
}

// wlSeed inserta una entrada como la dejaría un alta anterior y devuelve la
// entrada y su token EN CLARO (el fake guarda solo el hash, como Postgres).
func (e *waitlistEnv) wlSeed(t *testing.T, serviceID, professionalID, day, name, email string, createdAt time.Time) (domain.WaitlistEntry, string) {
	t.Helper()
	contact, err := domain.NewWaitlistContact(name, email, "")
	if err != nil {
		t.Fatalf("contacto: %v", err)
	}
	token, err := domain.NewWaitlistToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	entry, err := domain.NewWaitlistEntry("s1", serviceID, professionalID, day, contact, createdAt)
	if err != nil {
		t.Fatalf("entrada: %v", err)
	}
	entry, err = entry.WithTokenHash(domain.HashWaitlistToken(token))
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	entry.CreatedAt = createdAt.UTC()
	if err := e.wl.Create(entry); err != nil {
		t.Fatalf("guardar entrada: %v", err)
	}
	return entry, token
}

// findEntry devuelve la entrada por id (para mirar intentos y fechas).
func (e *waitlistEnv) findEntry(t *testing.T, id string) domain.WaitlistEntry {
	t.Helper()
	e.wl.mu.Lock()
	defer e.wl.mu.Unlock()
	for _, got := range e.wl.entries {
		if got.ID == id {
			return got
		}
	}
	t.Fatalf("no existe la entrada %s", id)
	return domain.WaitlistEntry{}
}

// ageClam devuelve el reclamo `age` hacia atrás: simula que la retención venció
// o que la gracia del reintento ya pasó.
func (e *waitlistEnv) ageClam(t *testing.T, id string, age time.Duration) {
	t.Helper()
	e.wl.mu.Lock()
	defer e.wl.mu.Unlock()
	for i := range e.wl.entries {
		if e.wl.entries[i].ID == id {
			at := time.Now().UTC().Add(-age)
			e.wl.entries[i].NotifyClaimedAt = &at
			return
		}
	}
	t.Fatalf("no existe la entrada %s", id)
}

func (e *waitlistEnv) entryCount() int {
	e.wl.mu.Lock()
	defer e.wl.mu.Unlock()
	return len(e.wl.entries)
}

// D4: el alta pública crea la entrada con el token hasheado y devuelve la forma
// exacta del contrato (201 la primera vez).
func TestWaitlistJoin_crea_y_normaliza(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	env.blockDay(t, day)

	res, err := env.svc.Join(waitlistSlug, waitlist.WaitlistJoinInput{
		ServiceID: env.corte.ID, Date: day,
		Name: "  Lía  ", Email: "  LIA@Mail.com ", Phone: " 11 5555-1234 ",
	})
	if err != nil {
		t.Fatalf("unirse: %v", err)
	}
	if !res.Created || res.Status != domain.WaitlistStatusWaiting || res.Date != day || res.ServiceID != env.corte.ID {
		t.Fatalf("resultado: %+v", res)
	}
	if env.entryCount() != 1 {
		t.Fatalf("tenía que quedar 1 entrada, hay %d", env.entryCount())
	}
	entry := env.wl.entries[0]
	if entry.Name != "Lía" || entry.Email != "lia@mail.com" || entry.Phone != "11 5555-1234" ||
		entry.ProfessionalID != "" || entry.Status() != domain.WaitlistStatusWaiting {
		t.Fatalf("entrada: %+v", entry)
	}
	// La entrada nace SIN token: la capability de la baja se emite recién con el
	// aviso (D5), así un alta que nunca se avisa no deja un enlace vivo.
	if entry.TokenHash != "" {
		t.Fatalf("al unirse no hay token todavía: %q", entry.TokenHash)
	}
}

// D4: un pedido inválido es 400 con el texto exacto y NO escribe nada. Un NUL es
// 400, nunca un 500 de Postgres.
func TestWaitlistJoin_validaciones_no_escriben(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	env.blockDay(t, day)

	cases := []struct {
		name string
		in   waitlist.WaitlistJoinInput
		msg  string
	}{
		{"sin servicio", waitlist.WaitlistJoinInput{Date: day, Name: "Lía", Email: "a@mail.com"}, domain.MsgWaitlistServiceInvalid},
		{"servicio inexistente", waitlist.WaitlistJoinInput{ServiceID: "no-existe", Date: day, Name: "Lía", Email: "a@mail.com"}, domain.MsgWaitlistServiceInvalid},
		{"servicio inactivo", waitlist.WaitlistJoinInput{ServiceID: env.viejo.ID, Date: day, Name: "Lía", Email: "a@mail.com"}, domain.MsgWaitlistServiceInvalid},
		{"profesional ajena", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, ProfessionalID: "prof9", Date: day, Name: "Lía", Email: "a@mail.com"}, domain.MsgWaitlistProfessionalInvalid},
		{"fecha pasada", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: "2020-01-01", Name: "Lía", Email: "a@mail.com"}, domain.MsgWaitlistDateRange},
		{"fecha lejana", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: "2099-01-01", Name: "Lía", Email: "a@mail.com"}, domain.MsgWaitlistDateRange},
		{"fecha mal formada", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: "24/11/2026", Name: "Lía", Email: "a@mail.com"}, domain.MsgWaitlistDate},
		{"sin nombre", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Email: "a@mail.com"}, domain.MsgWaitlistNameRequired},
		{"nombre con NUL", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía\x00", Email: "a@mail.com"}, domain.MsgWaitlistInvalidChars},
		{"sin email", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía"}, domain.MsgWaitlistEmailRequired},
		{"email inválido", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "no-es"}, "Email inválido"},
		{"email con NUL", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com\x00"}, domain.MsgWaitlistInvalidChars},
		{"teléfono largo", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com", Phone: strings.Repeat("5", 41)}, domain.MsgWaitlistPhoneTooLong},
	}
	for _, c := range cases {
		_, err := env.svc.Join(waitlistSlug, c.in)
		if err == nil || !errors.Is(err, domain.ErrValidation) || err.Error() != c.msg {
			t.Fatalf("%s: esperaba %q, dio %v", c.name, c.msg, err)
		}
		if env.entryCount() != 0 {
			t.Fatalf("%s: un 400 no puede escribir nada (hay %d entradas)", c.name, env.entryCount())
		}
	}
	// Salón inexistente: 404.
	if _, err := env.svc.Join("no-existe", waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("slug inexistente: %v", err)
	}
}

// D4: un día con horarios libres es 409 («la lista no sirve para reservar») y no
// escribe nada; sin horarios, la unión entra.
func TestWaitlistJoin_409_con_horarios_libres(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)

	_, err := env.svc.Join(waitlistSlug, waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"})
	if err == nil || !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgWaitlistSlotsAvailable {
		t.Fatalf("con horarios libres: %v", err)
	}
	if env.entryCount() != 0 {
		t.Fatalf("el 409 no puede escribir nada")
	}
	// El mismo día, sin huecos, sí entra.
	env.blockDay(t, day)
	if _, err := env.svc.Join(waitlistSlug, waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"}); err != nil {
		t.Fatalf("sin horarios: %v", err)
	}
}

// D4: tope de 3 avisos activos por email y salón, y de 500 por salón.
func TestWaitlistJoin_topes(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	env.blockDay(t, day)
	now := time.Now()

	// Tres entradas activas del mismo email (días distintos, sembradas directo).
	for i := 0; i < domain.MaxWaitlistActivePerEmail; i++ {
		d, _ := wlDay(t, 3+i)
		env.wlSeed(t, env.corte.ID, "", d, "Lía", "a@mail.com", now.Add(time.Duration(i)*time.Minute))
	}
	_, err := env.svc.Join(waitlistSlug, waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"})
	if err == nil || !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgWaitlistEmailLimit {
		t.Fatalf("tope por email: %v", err)
	}
	if env.entryCount() != 3 {
		t.Fatalf("el 409 no puede escribir nada, hay %d", env.entryCount())
	}
	// Un email distinto sigue entrando.
	if _, err := env.svc.Join(waitlistSlug, waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Ana", Email: "b@mail.com"}); err != nil {
		t.Fatalf("otro email: %v", err)
	}

	// 500 activos en el salón: la lista está completa (otro salón no se afecta).
	env2 := newWaitlistEnv(t)
	env2.blockDay(t, day)
	for i := 0; i < domain.MaxWaitlistActivePerSalon; i++ {
		env2.wlSeed(t, env2.corte.ID, "", day, "N", "n"+strconv.Itoa(i)+"@mail.com", now)
	}
	_, err = env2.svc.Join(waitlistSlug, waitlist.WaitlistJoinInput{ServiceID: env2.corte.ID, Date: day, Name: "Lía", Email: "nueva@mail.com"})
	if err == nil || !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgWaitlistSalonFull {
		t.Fatalf("tope por salón: %v", err)
	}
	if env2.entryCount() != domain.MaxWaitlistActivePerSalon {
		t.Fatalf("el 409 no puede escribir nada")
	}
}

// D4: un repetido idéntico ACTIVO es idempotente (200, misma forma) y no crea
// otra entrada, ni siquiera cuando el email ya llegó a su tope.
func TestWaitlistJoin_idempotente(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	env.blockDay(t, day)
	in := waitlist.WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"}

	first, err := env.svc.Join(waitlistSlug, in)
	if err != nil || !first.Created {
		t.Fatalf("primera: %+v %v", first, err)
	}
	again, err := env.svc.Join(waitlistSlug, in)
	if err != nil || again.Created || again.Status != domain.WaitlistStatusWaiting || again.Date != day || again.ServiceID != env.corte.ID {
		t.Fatalf("repetido: %+v %v", again, err)
	}
	if env.entryCount() != 1 {
		t.Fatalf("el repetido no crea otra entrada: %d", env.entryCount())
	}
	// El email llega al tope con otras dos entradas: el repetido sigue siendo 200.
	now := time.Now()
	for i := 1; i < domain.MaxWaitlistActivePerEmail; i++ {
		d, _ := wlDay(t, 2+i)
		env.wlSeed(t, env.corte.ID, "", d, "Lía", "a@mail.com", now.Add(time.Duration(i)*time.Minute))
	}
	again, err = env.svc.Join(waitlistSlug, in)
	if err != nil || again.Created {
		t.Fatalf("repetido en el tope: %+v %v", again, err)
	}
}

// D5: la baja por token es idempotente y un token desconocido o mal formado es
// el 404 genérico (mismo texto, sin distinguir el motivo).
func TestWaitlistLeave(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	entry, token := env.wlSeed(t, env.corte.ID, "", day, "Lía", "a@mail.com", time.Now())

	if err := env.svc.Leave(token); err != nil {
		t.Fatalf("baja: %v", err)
	}
	if got := env.findEntry(t, entry.ID); got.Status() != domain.WaitlistStatusLeft || got.Active() {
		t.Fatalf("después de la baja: %+v", got)
	}
	// Repetir el mismo token: 200 igual (idempotente), sin error.
	if err := env.svc.Leave(token); err != nil {
		t.Fatalf("baja repetida: %v", err)
	}
	// Token desconocido y token mal formado: el MISMO 404.
	other, _ := domain.NewWaitlistToken()
	for _, bad := range []string{other, "", "corto", strings.Repeat("a", 44)} {
		err := env.svc.Leave(bad)
		if err == nil || !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgWaitlistNotFound {
			t.Fatalf("token %q: esperaba el 404 genérico, dio %v", bad, err)
		}
	}
	// El token no se recorta: con espacios alrededor no sirve.
	if err := env.svc.Leave(" " + token + " "); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("token con espacios: %v", err)
	}
}

// D6/D7: con UN hueco y DOS entradas se avisa exactamente a UNA (FIFO), una sola
// vez; el email lleva el asunto exacto, el salón, el servicio, el día local, el
// enlace de la página pública, el de baja y el aviso de «solo por esta lista».
func TestWaitlistRunOnce_avisa_una_fifo(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	now := time.Now().UTC()
	first, firstToken := env.wlSeed(t, env.corte.ID, "prof1", day, "Lía", "lia@mail.com", now.Add(-2*time.Minute))
	second, _ := env.wlSeed(t, env.corte.ID, "prof1", day, "Ana", "ana@mail.com", now.Add(-time.Minute))

	// Recién ahora el día tiene UN hueco: prof1 con la ventana de 09:00-10:00.
	notified, failed := env.svc.RunOnce(context.Background(), now)
	if notified != 1 || failed != 0 {
		t.Fatalf("avisos=%d fallidos=%d", notified, failed)
	}
	if env.mailer.count() != 1 {
		t.Fatalf("tenía que salir 1 email, salieron %d", env.mailer.count())
	}
	msg := env.mailer.messages()[0]
	if msg.To != "lia@mail.com" {
		t.Fatalf("FIFO: tenía que ir a lia@mail.com, fue a %q", msg.To)
	}
	if want := "Se liberó un turno en Navajas de Oro"; msg.Subject != want {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	label := func() string {
		d, _ := time.Parse("2006-01-02", day)
		return d.Format("02/01/2006")
	}()
	for _, want := range []string{
		"Navajas de Oro", "Corte", label,
		linkAppURL + "/salon/navajas-oro",
		linkAppURL + "/lista-de-espera#",
		"te escribimos solo por esta lista; ya no recibirás más avisos de esta entrada",
	} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("el email no menciona %q:\n%s", want, msg.Text)
		}
	}
	// El token del aviso es el que quedó guardado (hasheado) en la entrada y el
	// que la persona puede usar para darse de baja; el que existía antes del
	// aviso no sirve (no hay capability sin email).
	token := msg.Text[strings.Index(msg.Text, "/lista-de-espera#")+len("/lista-de-espera#"):]
	if i := strings.IndexAny(token, " \n"); i >= 0 {
		token = token[:i]
	}
	if got := env.findEntry(t, first.ID); got.TokenHash != domain.HashWaitlistToken(token) {
		t.Fatalf("el hash guardado no es el del token enviado: %+v", got)
	}
	if err := env.svc.Leave(firstToken); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el token previo al aviso no puede servir: %v", err)
	}
	if got := env.findEntry(t, first.ID); got.Status() != domain.WaitlistStatusNotified || got.NotifyAttempts != 1 {
		t.Fatalf("la primera entrada tenía que quedar avisada: %+v", got)
	}
	if got := env.findEntry(t, second.ID); got.Status() != domain.WaitlistStatusWaiting || got.NotifyClaimedAt != nil {
		t.Fatalf("la segunda NO se toca mientras la retención está viva: %+v", got)
	}

	// Segundo tick: la retención de 2 h del aviso enviado ocupa el único hueco.
	if notified, _ := env.svc.RunOnce(context.Background(), now); notified != 0 || env.mailer.count() != 1 {
		t.Fatalf("segundo tick: avisos=%d emails=%d", notified, env.mailer.count())
	}
	// Cuando la retención vence, la siguiente recibe su aviso (una sola vez).
	env.ageClam(t, first.ID, domain.WaitlistRetention+time.Hour)
	if notified, _ := env.svc.RunOnce(context.Background(), now); notified != 1 {
		t.Fatalf("con la retención vencida tenía que avisar a la segunda, avisos=%d", notified)
	}
	if msgs := env.mailer.messages(); len(msgs) != 2 || msgs[1].To != "ana@mail.com" {
		t.Fatalf("segundo email: %+v", msgs)
	}
	// Una entrada sola se avisa UNA vez (re-unirse crea otra).
	if notified, _ := env.svc.RunOnce(context.Background(), now); notified != 0 {
		t.Fatalf("nadie más a quién avisar, avisos=%d", notified)
	}
	// El enlace del aviso da de baja ESA entrada (D5) y no se vuelve a avisar.
	if err := env.svc.Leave(token); err != nil {
		t.Fatalf("el enlace del aviso tiene que dar de baja: %v", err)
	}
	if got := env.findEntry(t, first.ID); got.Status() != domain.WaitlistStatusLeft {
		t.Fatalf("después de la baja: %+v", got)
	}
	if notified, _ := env.svc.RunOnce(context.Background(), now); notified != 0 {
		t.Fatalf("una entrada dada de baja no se avisa: %d", notified)
	}
}

// D6: si el envío falla, la entrada se reintenta pasada la gracia de 5 minutos,
// con el tope de 3 intentos.
func TestWaitlistRunOnce_reintento_tras_fallo(t *testing.T) {
	env := newWaitlistEnv(t)
	env.mailer.err = errors.New("smtp caído")
	day, _ := wlDay(t, 2)
	now := time.Now().UTC()
	entry, _ := env.wlSeed(t, env.corte.ID, "prof1", day, "Lía", "lia@mail.com", now)

	if notified, failed := env.svc.RunOnce(context.Background(), now); notified != 0 || failed != 1 {
		t.Fatalf("primer intento: avisos=%d fallidos=%d", notified, failed)
	}
	if got := env.findEntry(t, entry.ID); got.NotifyAttempts != 1 || got.Status() != domain.WaitlistStatusWaiting {
		t.Fatalf("tras el fallo: %+v", got)
	}
	// Dentro de la gracia no se reintenta (el reclamo sigue vivo).
	claims := env.wl.claimCalls
	if notified, failed := env.svc.RunOnce(context.Background(), now); notified != 0 || failed != 0 {
		t.Fatalf("dentro de la gracia: avisos=%d fallidos=%d", notified, failed)
	}
	if env.wl.claimCalls != claims {
		t.Fatalf("dentro de la gracia no se reclama de nuevo")
	}
	// Pasada la gracia se reintenta: segundo intento.
	env.ageClam(t, entry.ID, domain.WaitlistRetryGrace+time.Minute)
	if _, failed := env.svc.RunOnce(context.Background(), now); failed != 1 {
		t.Fatalf("segundo intento: fallidos=%d", failed)
	}
	// Tercero y último.
	env.ageClam(t, entry.ID, domain.WaitlistRetryGrace+time.Minute)
	if _, failed := env.svc.RunOnce(context.Background(), now); failed != 1 {
		t.Fatalf("tercer intento: fallidos=%d", failed)
	}
	if got := env.findEntry(t, entry.ID); got.NotifyAttempts != domain.MaxWaitlistAttempts {
		t.Fatalf("intentos: %d", got.NotifyAttempts)
	}
	// Con los 3 intentos gastados no se vuelve a reclamar nunca.
	env.ageClam(t, entry.ID, 24*time.Hour)
	claims = env.wl.claimCalls
	if notified, failed := env.svc.RunOnce(context.Background(), now); notified != 0 || failed != 0 {
		t.Fatalf("agotado: avisos=%d fallidos=%d", notified, failed)
	}
	if env.wl.claimCalls != claims {
		t.Fatalf("una entrada con 3 intentos no se reclama más")
	}
}

// D7: sin email configurado (MailSender deshabilitado) la tarea NO reclama nada.
func TestWaitlistRunOnce_sin_mailer_no_reclama(t *testing.T) {
	env := newWaitlistEnv(t)
	env.svc.Mailer = nil
	day, _ := wlDay(t, 2)
	env.wlSeed(t, env.corte.ID, "prof1", day, "Lía", "lia@mail.com", time.Now().UTC())

	notified, failed := env.svc.RunOnce(context.Background(), time.Now().UTC())
	if notified != 0 || failed != 0 {
		t.Fatalf("sin email: avisos=%d fallidos=%d", notified, failed)
	}
	if env.wl.claimCalls != 0 {
		t.Fatalf("sin email no se reclama, se reclamó %d veces", env.wl.claimCalls)
	}
	// Sin repositorio tampoco hay nada que hacer.
	if n, f := (waitlist.WaitlistService{}).RunOnce(context.Background(), time.Now().UTC()); n != 0 || f != 0 {
		t.Fatalf("servicio vacío: %d %d", n, f)
	}
}

// D6: dos pasadas SIMULTÁNEAS sobre 1 hueco y 2 entradas avisan a UNA sola
// (el lock de escritura de la lista serializa el reclamo).
func TestWaitlistRunOnce_dos_pasadas_simultaneas_avisan_una(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	now := time.Now().UTC()
	env.wlSeed(t, env.corte.ID, "prof1", day, "Lía", "lia@mail.com", now.Add(-2*time.Minute))
	env.wlSeed(t, env.corte.ID, "prof1", day, "Ana", "ana@mail.com", now.Add(-time.Minute))

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, _ := env.svc.RunOnce(context.Background(), now)
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 1 || env.mailer.count() != 1 {
		t.Fatalf("dos pasadas simultáneas: avisos=%d emails=%d", total, env.mailer.count())
	}
	if len(env.wl.locks) == 0 || env.wl.locks[0] != "waitlist-write:s1" {
		t.Fatalf("locks pedidos: %v", env.wl.locks)
	}
}

// D8: la pantalla del personal lista solo las entradas vigentes del salón (día
// local >= hoy, sin baja) y las da de baja. Permisos rol por rol y aislamiento.
func TestWaitlistStaff_lista_y_baja(t *testing.T) {
	env := newWaitlistEnv(t)
	today, _ := wlDay(t, 0)
	tomorrow, _ := wlDay(t, 1)
	yesterday, _ := wlDay(t, -1)
	now := time.Now()
	long, _ := env.wlSeed(t, env.corte.ID, "prof1", tomorrow, "Lía", "lia@mail.com", now)
	old, _ := env.wlSeed(t, env.corte.ID, "", yesterday, "Vieja", "vieja@mail.com", now)
	left, _ := env.wlSeed(t, env.corte.ID, "", today, "Baja", "baja@mail.com", now)
	if err := env.wl.Leave(left.TokenHash, now); err != nil {
		t.Fatalf("baja: %v", err)
	}
	_ = old

	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleReception} {
		env.mems.Save(domain.Membership{UserID: "u-" + string(role), SalonID: "s1", Role: role})
	}
	for _, role := range []domain.Role{domain.RoleProfessional, domain.RoleClient} {
		env.mems.Save(domain.Membership{UserID: "u-" + string(role), SalonID: "s1", Role: role, Attends: role == domain.RoleProfessional})
	}

	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleReception} {
		actor := domain.Membership{UserID: "u-" + string(role), SalonID: "s1", Role: role}
		list, err := env.svc.List(actor, "s1")
		if err != nil {
			t.Fatalf("%s lista: %v", role, err)
		}
		if len(list) != 1 || list[0].ID != long.ID || list[0].ServiceName != "Corte" || list[0].ProfessionalName != "Ana" {
			t.Fatalf("%s: %+v", role, list)
		}
		if list[0].Status() != domain.WaitlistStatusWaiting || list[0].Day != tomorrow {
			t.Fatalf("%s estado/día: %+v", role, list[0])
		}
	}
	for _, role := range []domain.Role{domain.RoleProfessional, domain.RoleClient} {
		actor := domain.Membership{UserID: "u-" + string(role), SalonID: "s1", Role: role}
		if _, err := env.svc.List(actor, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no puede listar: %v", role, err)
		}
		if err := env.svc.Remove(actor, "s1", long.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no puede dar de baja: %v", role, err)
		}
	}
	// De otro salón: 403 (no es miembro).
	other := domain.Membership{UserID: "u-owner", SalonID: "s2", Role: domain.RoleOwner}
	if _, err := env.svc.List(other, "s3"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón: %v", err)
	}

	owner := domain.Membership{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner}
	// Pedir el salón del que no es miembro es 403 (es la autorización del pedido,
	// no la de la entrada).
	if err := env.svc.Remove(owner, "s2", long.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("salón ajeno: %v", err)
	}
	// Una entrada de OTRO salón es el 404 del contrato (aislamiento por salón).
	contact, _ := domain.NewWaitlistContact("Otra", "otra@mail.com", "")
	foreign, err := domain.NewWaitlistEntry("s2", env.corte.ID, "", tomorrow, contact, now)
	if err != nil {
		t.Fatalf("entrada ajena: %v", err)
	}
	env.wl.mu.Lock()
	env.wl.entries = append(env.wl.entries, foreign)
	env.wl.mu.Unlock()
	if err := env.svc.Remove(owner, "s1", foreign.ID); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgWaitlistNotFound {
		t.Fatalf("entrada de otro salón: %v", err)
	}
	if err := env.svc.Remove(owner, "s1", left.ID); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgWaitlistNotFound {
		t.Fatalf("ya dada de baja: %v", err)
	}
	// Dar de baja la entrada vigente: deja de listarse y repetirlo es 404.
	if err := env.svc.Remove(owner, "s1", long.ID); err != nil {
		t.Fatalf("quitar: %v", err)
	}
	if err := env.svc.Remove(owner, "s1", long.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("quitar dos veces: %v", err)
	}
	if list, _ := env.svc.List(owner, "s1"); len(list) != 0 {
		t.Fatalf("después de quitar no queda nada: %+v", list)
	}
}
