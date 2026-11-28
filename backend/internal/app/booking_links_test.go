package app

import (
	"errors"
	"salon-saas/internal/testutil"
	"salon-saas/internal/testutil/agendakit"
	"strings"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Contrato F1: el enlace firmado de un solo uso para cancelar o reprogramar el
// turno desde el email. Los tests de concurrencia real (16 pedidos con el mismo
// token, dos reprogramaciones al mismo hueco) viven en los IT de Postgres: acá
// se fija el contrato del caso de uso con fakes.

type linkEnv struct {
	*agendakit.PublicEnv
	links *fakeBookingLinks
	svc   bkg.BookingLinkService
}

// newLinkEnv arma el caso de uso del enlace sobre los fakes de la agenda pública
// (salón s1 en CDMX, 09:00-12:00 cada 30 minutos, servicio "Corte" de 45 min,
// profesionales prof1 y prof2).
func newLinkEnv(t *testing.T) *linkEnv {
	t.Helper()
	pub := agendakit.NewPublicEnv(t)
	links := &fakeBookingLinks{bookings: pub.Bookings}
	svc := bkg.BookingLinkService{
		Links: links, Tx: links, Bookings: pub.Bookings, Salons: pub.Avail.Salons,
		Services: pub.Services, Settings: pub.Settings, Staff: pub.Staff,
		Professionals: pub.Profs, Hours: pub.Hours, TimeOff: pub.TimeOff,
		Resources: pub.Resources, Availability: pub.Avail,
		Events: pub.Events, Notifier: pub.Notifier,
		Mailer: pub.Mailer, Emails: pub.Emails, AppURL: testutil.LinkAppURL,
	}
	pub.Book.Links = links
	pub.Book.AppURL = testutil.LinkAppURL
	return &linkEnv{PublicEnv: pub, links: links, svc: svc}
}

// linkDay devuelve un día LOCAL del salón con huecos garantizados en el futuro
// (pasado mañana a las 09:00) y una función para ubicar minutos dentro de la
// ventana 09:00-12:00 del fixture.
func linkDay() (string, func(minutes int) time.Time) {
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		panic(err)
	}
	d := time.Now().In(loc).AddDate(0, 0, 2)
	day := time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, loc)
	return day.Format("2006-01-02"), func(minutes int) time.Time {
		return day.Add(time.Duration(minutes) * time.Minute)
	}
}

// withMailer deja el sender y la cola de emails listos y limpia lo que publicó
// la reserva, así el test cuenta solo lo del enlace.
func (e *linkEnv) withMailer() *testutil.FakeMailer {
	mailer := &testutil.FakeMailer{}
	emails := &testutil.FakeBookingEmails{}
	e.Mailer = mailer
	e.Emails = emails
	e.Book.Mailer = mailer
	e.Book.Emails = emails
	e.Book.Links = e.links
	e.Book.AppURL = testutil.LinkAppURL
	e.svc.Mailer = mailer
	e.svc.Emails = emails
	e.svc.Links = e.links
	e.Events.All = nil
	e.Notifier.Calls = 0
	e.Notifier.Bodies = nil
	return mailer
}

// bookWithEmail reserva por la ruta pública con email y devuelve el turno y el
// token del enlace que salió en la confirmación.
func (e *linkEnv) bookWithEmail(t *testing.T, starts time.Time, professional string) (domain.Booking, string) {
	t.Helper()
	in := agendakit.PublicInput()
	in.ServiceID = e.Corte.ID
	in.ClientEmail = "Lia@Mail.com"
	in.StartsAt = starts
	in.ProfessionalID = professional
	b, err := e.Book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	msg := e.Mailer.WaitCount(t, 1)[0]
	return b, tokenFromMail(t, msg)
}

// tokenFromMail saca el token del enlace del correo: el enlace va en el
// fragmento (`/turno#<token>`), nunca en el query.
func tokenFromMail(t *testing.T, msg ports.Message) string {
	t.Helper()
	const marker = "/turno#"
	idx := strings.Index(msg.Text, marker)
	if idx < 0 {
		t.Fatalf("el correo no lleva el enlace del turno:\n%s", msg.Text)
	}
	rest := msg.Text[idx+len(marker):]
	if end := strings.IndexAny(rest, " \n\t\r<\"');,"); end >= 0 {
		rest = rest[:end]
	}
	if !domain.ValidBookingLinkToken(rest) {
		t.Fatalf("el token del enlace no tiene la forma del contrato: %q", rest)
	}
	return rest
}

// linkFixture siembra un turno futuro con enlace ya emitido (sin pasar por el
// correo) y devuelve el turno y el token en claro.
func (e *linkEnv) linkFixture(t *testing.T, starts time.Time, professional string) (domain.Booking, string) {
	t.Helper()
	// Se arma la fila sin la validación de "no agendar en el pasado": el test del
	// turno vencido necesita un turno que ya pasó.
	b := domain.Booking{
		ID: domain.NewID(), SalonID: "s1", ProfessionalID: professional, ClientName: "Lía",
		ClientPhone: "555", StartsAt: starts.UTC(), EndsAt: starts.Add(45 * time.Minute).UTC(),
		Status: domain.BookingBooked, ServiceID: e.Corte.ID, CreatedAt: time.Now().UTC(),
	}
	if err := e.Bookings.Save(b); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}
	token, err := domain.NewBookingLinkToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	link, err := domain.NewBookingLink("s1", b.ID, domain.HashBookingLinkToken(token), time.Now().UTC())
	if err != nil {
		t.Fatalf("enlace: %v", err)
	}
	if err := e.links.Save(link); err != nil {
		t.Fatalf("guardar enlace: %v", err)
	}
	return b, token
}

// D1: solo existe un enlace para un turno CON email, y de ese token se guarda
// únicamente el SHA-256 (el token en claro no aparece en ninguna columna).
func TestBookingLink_solo_el_hash_y_solo_con_email(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()

	_, token := env.bookWithEmail(t, at(0), "prof1")
	rows := env.links.rows()
	if len(rows) != 1 {
		t.Fatalf("esperaba un enlace emitido, hubo %d: %+v", len(rows), rows)
	}
	if rows[0].TokenHash != domain.HashBookingLinkToken(token) {
		t.Fatalf("la fila no guarda el SHA-256 del token: %+v", rows[0])
	}
	if strings.Contains(rows[0].ID+rows[0].SalonID+rows[0].BookingID+rows[0].TokenHash, token) {
		t.Fatalf("el token en claro no puede aparecer en la fila: %+v", rows[0])
	}
	if rows[0].CreatedAt.IsZero() || rows[0].Used() {
		t.Fatalf("el enlace nace sin usar y con fecha: %+v", rows[0])
	}

	// Un turno SIN email no emite token (D1) y la confirmación no sale.
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID
	in.StartsAt = at(60)
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("reservar sin email: %v", err)
	}
	if n := len(env.links.rows()); n != 1 {
		t.Fatalf("un turno sin email no emite enlace: %d filas", n)
	}
}

// lookup devuelve lo que la pantalla muestra y NADA más: ni teléfono, ni email,
// ni ids internos (D6).
func TestBookingLink_lookup(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	b, token := env.bookWithEmail(t, at(30), "prof1")

	got, err := env.svc.Lookup(token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.SalonName != "Navajas de Oro" || got.ServiceName != "Corte" || got.ProfessionalName != "Ana" {
		t.Fatalf("lookup: %+v", got)
	}
	if !got.StartsAt.Equal(b.StartsAt) || !got.EndsAt.Equal(b.EndsAt) {
		t.Fatalf("horario del lookup: %+v (turno %+v)", got, b)
	}
	if got.Timezone != "America/Mexico_City" {
		t.Fatalf("zona del salón: %q", got.Timezone)
	}
	if !got.CanChange {
		t.Fatal("con días de anticipación el turno se puede cambiar")
	}
}

// D6: inexistente, usado, mal formado o de un turno que ya no está vigente dan
// SIEMPRE el mismo 404 con el mismo texto.
func TestBookingLink_lookup_token_que_no_sirve(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	cancelada := at(0)
	bCancelled, tokenCancelled := env.bookWithEmail(t, cancelada, "prof1")
	done, err := bCancelled.SetStatus(domain.BookingDone)
	if err != nil {
		t.Fatalf("cerrar turno: %v", err)
	}
	if err := env.Bookings.Update(done); err != nil {
		t.Fatalf("guardar turno cerrado: %v", err)
	}

	pasado, tokenPast := env.linkFixture(t, time.Now().UTC().Add(-3*time.Hour), "prof2")

	good, _ := domain.NewBookingLinkToken()
	bad := []string{"", "   ", "no-es-un-token", strings.Repeat("a", 42), strings.Repeat("+", 43), good}
	for _, token := range bad {
		_, err := env.svc.Lookup(token)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("token %q debe dar 404, dio %v", token, err)
		}
		if err.Error() != domain.MsgBookingLinkInvalid {
			t.Fatalf("token %q: mensaje %q", token, err.Error())
		}
	}
	// Un turno cerrado mata su enlace aunque el token no se haya usado.
	if _, err := env.svc.Lookup(tokenCancelled); err == nil {
		t.Fatal("un turno cerrado no tiene enlace vigente")
	}
	if _, err := env.svc.Lookup(tokenPast); err == nil || err.Error() != domain.MsgBookingLinkInvalid {
		t.Fatalf("un turno pasado no tiene enlace vigente: %v", err)
	}
	if pasado.ID == "" {
		t.Fatal("fixture inválido")
	}
	// Y un enlace ya USADO tampoco sirve.
	_, tokenUsed := env.linkFixture(t, at(120), "prof2")
	if err := env.svc.Cancel(tokenUsed); err != nil {
		t.Fatalf("cancelar para gastar el token: %v", err)
	}
	if _, err := env.svc.Lookup(tokenUsed); err == nil || err.Error() != domain.MsgBookingLinkInvalid {
		t.Fatalf("un token usado da el 404 genérico: %v", err)
	}
}

// D3: con menos de 2 horas el turno se muestra pero no se puede cambiar.
func TestBookingLink_dentro_de_dos_horas(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	b, token := env.linkFixture(t, time.Now().UTC().Add(90*time.Minute), "prof1")

	got, err := env.svc.Lookup(token)
	if err != nil {
		t.Fatalf("lookup dentro de 2 h: %v", err)
	}
	if got.CanChange {
		t.Fatal("faltando menos de 2 horas no se puede cambiar")
	}
	if !got.StartsAt.Equal(b.StartsAt) {
		t.Fatalf("el turno se muestra igual: %+v", got)
	}
	if _, err := env.svc.Slots(token, time.Now().UTC().Format("2006-01-02")); !isMinNotice(err) {
		t.Fatalf("slots dentro de 2 h debe dar 409: %v", err)
	}
	if err := env.svc.Cancel(token); !isMinNotice(err) {
		t.Fatalf("cancelar dentro de 2 h debe dar 409: %v", err)
	}
	if _, _, err := env.svc.Reschedule(token, time.Now().UTC().Add(72*time.Hour)); !isMinNotice(err) {
		t.Fatalf("reprogramar dentro de 2 h debe dar 409: %v", err)
	}
	// Nada cambió: el turno sigue agendado y el token sigue vivo (el 409 no lo
	// consume, D2).
	if b2, err := env.Bookings.FindByID("s1", b.ID); err != nil || b2.Status != domain.BookingBooked {
		t.Fatalf("el turno no se toca: %+v %v", b2, err)
	}
	if rows := env.links.rows(); rows[0].Used() {
		t.Fatal("un 409 NO consume el token")
	}
}

func isMinNotice(err error) bool {
	return err != nil && errors.Is(err, domain.ErrAlreadyExists) && err.Error() == domain.MsgBookingLinkMinNotice
}

// Slots: la MISMA forma que la disponibilidad pública, para el servicio y el
// profesional del turno, con el propio turno excluido de los conflictos (D5).
func TestBookingLink_slots(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	date, at := linkDay()
	_, token := env.bookWithEmail(t, at(30), "prof1")

	day, err := env.svc.Slots(token, date)
	if err != nil {
		t.Fatalf("slots: %v", err)
	}
	if day.Date != date {
		t.Fatalf("fecha: %q", day.Date)
	}
	// El turno ocupa 09:30-10:15: con el propio turno EXCLUIDO de los conflictos
	// (D5) los cinco huecos de la ventana vuelven a ofrecerse, incluido el suyo
	// (elegirlo da el 400 de "un horario distinto"). Solo de prof1, que es el
	// profesional del turno.
	starts := make([]string, 0, len(day.Slots))
	for _, s := range day.Slots {
		if s.ProfessionalID != "prof1" {
			t.Fatalf("los huecos son del profesional del turno: %+v", day.Slots)
		}
		starts = append(starts, s.StartsAt.Format(time.RFC3339))
	}
	want := []string{
		at(0).UTC().Format(time.RFC3339), at(30).UTC().Format(time.RFC3339),
		at(60).UTC().Format(time.RFC3339), at(90).UTC().Format(time.RFC3339),
		at(120).UTC().Format(time.RFC3339),
	}
	if strings.Join(starts, ",") != strings.Join(want, ",") {
		t.Fatalf("huecos ofrecidos: %v (esperaba %v)", starts, want)
	}
	// Sin la exclusión el propio turno se come tres huecos: es la diferencia con
	// la disponibilidad pública de siempre.
	plain, err := env.Avail.Day("navajas-oro", date, env.Corte.ID, "prof1")
	if err != nil {
		t.Fatalf("disponibilidad pública: %v", err)
	}
	if len(plain.Slots) != 2 {
		t.Fatalf("la disponibilidad sin excluir el turno deja 2 huecos, dio %d", len(plain.Slots))
	}

	// Fecha inválida: 400 con el texto del contrato.
	if _, err := env.svc.Slots(token, "01/12/2026"); !errors.Is(err, domain.ErrValidation) || err.Error() != domain.MsgBookingLinkDate {
		t.Fatalf("fecha inválida: %v", err)
	}
}

// D8: el recordatorio lleva su PROPIO enlace (token nuevo por envío) y lookup lo
// acepta. La confirmación, el mismo contrato.
func TestBookingLink_confirmacion_y_recordatorio_llevan_enlace(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	_, token := env.bookWithEmail(t, at(0), "prof1")
	if _, err := env.svc.Lookup(token); err != nil {
		t.Fatalf("el enlace de la confirmación no sirve: %v", err)
	}

	// Recordatorio: el servicio emite un token nuevo para ese turno.
	b, err := domain.NewBooking("s1", "prof1", "Lía", "555", at(120), at(120).Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno del recordatorio: %v", err)
	}
	b.ServiceID = env.Corte.ID
	if err := env.Bookings.Save(b); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}
	emails := &testutil.FakeBookingEmails{Due: []ports.DueReminder{{
		BookingID: b.ID, SalonID: "s1", SalonName: "Navajas de Oro", ClientName: "Lía",
		Email: "lia@mail.com", StartsAt: b.StartsAt,
	}}}
	mailer := &testutil.FakeMailer{}
	reminders := bkg.ReminderService{
		Emails: emails, Mailer: mailer, Settings: env.Settings,
		Links: env.links, AppURL: testutil.LinkAppURL,
	}
	sent, failed := reminders.RunOnce(t.Context(), time.Now().UTC())
	if sent != 1 || failed != 0 {
		t.Fatalf("RunOnce: enviados=%d fallidos=%d", sent, failed)
	}
	msg := mailer.WaitCount(t, 1)[0]
	tokenReminder := tokenFromMail(t, msg)
	if tokenReminder == token {
		t.Fatal("el recordatorio emite un token NUEVO (D1), no reusa el de la confirmación")
	}
	if _, err := env.svc.Lookup(tokenReminder); err != nil {
		t.Fatalf("el enlace del recordatorio no sirve: %v", err)
	}
	// Los dos tokens del mismo turno siguen vivos hasta que se usen (D2).
	if _, err := env.svc.Lookup(token); err != nil {
		t.Fatalf("el token viejo sigue vivo: %v", err)
	}
}

// D8: cancelar publica el MISMO evento de tiempo real y el MISMO aviso interno
// que bkg.BookingService.Update, y manda el correo de cancelación.
func TestBookingLink_cancel(t *testing.T) {
	env := newLinkEnv(t)
	mailer := env.withMailer()
	_, at := linkDay()
	b, token := env.bookWithEmail(t, at(30), "prof1")
	env.Events.All = nil
	notices := &fakeLinkNotifier{}
	env.svc.Notifier = notices

	if err := env.svc.Cancel(token); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	stored, err := env.Bookings.FindByID("s1", b.ID)
	if err != nil || stored.Status != domain.BookingCancelled {
		t.Fatalf("el turno tiene que quedar cancelado: %+v %v", stored, err)
	}
	ev, n := env.Events.Typed("booking.updated")
	if n != 1 {
		t.Fatalf("esperaba un booking.updated, hubo %d", n)
	}
	data, ok := ev.Data.(map[string]any)
	if !ok {
		t.Fatalf("payload: %+v", ev.Data)
	}
	want := map[string]string{
		"id": b.ID, "professional_id": "prof1", "client_name": "Lía",
		"starts_at": b.StartsAt.Format(time.RFC3339), "ends_at": b.EndsAt.Format(time.RFC3339),
		"status": "cancelled",
	}
	for k, v := range want {
		if data[k] != v {
			t.Fatalf("evento %q = %v, esperaba %q", k, data[k], v)
		}
	}
	if ev.SalonID != "s1" {
		t.Fatalf("salón del evento: %q", ev.SalonID)
	}
	if len(notices.titles) != 1 || notices.titles[0] != bkg.BookingChangeTitle(domain.BookingCancelled) {
		t.Fatalf("el aviso tiene que ser el mismo del PATCH: %+v", notices.titles)
	}
	if !strings.HasPrefix(notices.bodies[0], "Lía · ") || notices.kinds[0] != domain.KindBooking {
		t.Fatalf("cuerpo del aviso: %q (%q)", notices.bodies[0], notices.kinds[0])
	}
	// El enlace quedó usado: el segundo intento es el 404 genérico.
	if err := env.svc.Cancel(token); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgBookingLinkInvalid {
		t.Fatalf("segundo cancelar: %v", err)
	}
	// El correo de cancelación salió (sin enlace: el turno ya no existe).
	msgs := mailer.WaitCount(t, 2)
	last := msgs[len(msgs)-1]
	if last.To != "lia@mail.com" || !strings.Contains(last.Subject, "cancelado") {
		t.Fatalf("correo de cancelación: %+v", last)
	}
}

// Un SMTP caído se loguea y NUNCA cambia la respuesta (D8).
func TestBookingLink_smtp_caido_no_cambia_la_respuesta(t *testing.T) {
	env := newLinkEnv(t)
	mailer := env.withMailer()
	_, at := linkDay()
	b, token := env.bookWithEmail(t, at(0), "prof1")
	// El SMTP se cae recién ahora: la confirmación ya salió y lo que importa es
	// que el correo del cambio falle sin tocar la respuesta.
	mailer.Err = errors.New("smtp caído")

	view, sent, err := env.svc.Reschedule(token, at(90))
	if err != nil {
		t.Fatalf("el SMTP caído no puede tumbar la reprogramación: %v", err)
	}
	if !view.StartsAt.Equal(at(90)) {
		t.Fatalf("horario de la respuesta: %+v", view)
	}
	// El email quedó encolado (segundo plano): new_link_sent = true aunque el
	// envío falle después.
	if !sent {
		t.Fatal("new_link_sent es true si el email quedó encolado")
	}
	if stored, _ := env.Bookings.FindByID("s1", b.ID); !stored.StartsAt.Equal(at(90)) {
		t.Fatalf("el turno se movió igual: %+v", stored)
	}
}

// D2/D5: reprogramar mueve el turno con las reglas de una reserva nueva, emite un
// token nuevo por email y el token viejo muere.
func TestBookingLink_reschedule(t *testing.T) {
	env := newLinkEnv(t)
	mailer := env.withMailer()
	date, at := linkDay()
	b, token := env.bookWithEmail(t, at(0), "prof1")
	env.Events.All = nil
	notices := &fakeLinkNotifier{}
	env.svc.Notifier = notices

	view, newLinkSent, err := env.svc.Reschedule(token, at(90))
	if err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	if !newLinkSent {
		t.Fatal("new_link_sent tiene que ser true: hay email y el correo quedó encolado")
	}
	if !view.StartsAt.Equal(at(90)) {
		t.Fatalf("respuesta del lookup: %+v", view)
	}
	stored, err := env.Bookings.FindByID("s1", b.ID)
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	if !stored.StartsAt.Equal(at(90)) || !stored.EndsAt.Equal(at(90).Add(45*time.Minute)) {
		t.Fatalf("el turno se movió con la duración del servicio: %+v", stored)
	}
	if stored.ProfessionalID != "prof1" || stored.ServiceID != env.Corte.ID {
		t.Fatalf("mismo profesional y servicio: %+v", stored)
	}
	// Mismo evento y aviso que bkg.BookingService.Update.
	if _, n := env.Events.Typed("booking.updated"); n != 1 {
		t.Fatalf("esperaba un booking.updated, hubo %d", n)
	}
	if len(notices.titles) != 1 || notices.titles[0] != "Turno reprogramado" {
		t.Fatalf("aviso interno de la reprogramación: %+v", notices.titles)
	}
	// Dos enlaces emitidos (confirmación + reprogramación) y el correo nuevo
	// trae el token nuevo, que sirve; el viejo ya no.
	msgs := mailer.WaitCount(t, 2)
	newest := tokenFromMail(t, msgs[len(msgs)-1])
	if newest == token {
		t.Fatal("el token nuevo no puede ser el viejo")
	}
	if _, err := env.svc.Lookup(newest); err != nil {
		t.Fatalf("el token nuevo tiene que servir: %v", err)
	}
	if _, err := env.svc.Lookup(token); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgBookingLinkInvalid {
		t.Fatalf("el token viejo tiene que morir: %v", err)
	}
	// El token nuevo sigue sirviendo para la fecha nueva.
	day, err := env.svc.Slots(newest, date)
	if err != nil {
		t.Fatalf("slots con el token nuevo: %v", err)
	}
	if len(day.Slots) == 0 {
		t.Fatal("esperaba huecos con el token nuevo")
	}
}

// Un horario ocupado (o fuera de horario) es 409 y NO consume el token.
func TestBookingLink_reschedule_a_horario_ocupado(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	_, token := env.bookWithEmail(t, at(0), "prof1")
	// Otro turno del mismo profesional a las 10:00.
	ocupado, err := domain.NewBooking("s1", "prof1", "Otra", "", at(60), at(60).Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno ocupado: %v", err)
	}
	ocupado.ServiceID = env.Corte.ID
	if err := env.Bookings.Save(ocupado); err != nil {
		t.Fatalf("guardar turno ocupado: %v", err)
	}

	for _, starts := range []time.Time{at(60), at(300)} { // ocupado y fuera de horario
		_, _, err := env.svc.Reschedule(token, starts)
		if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgBookingLinkSlotTaken {
			t.Fatalf("horario %v: esperaba el 409 del contrato, dio %v", starts, err)
		}
	}
	// El token sigue vivo: la persona puede elegir otro horario.
	if _, err := env.svc.Lookup(token); err != nil {
		t.Fatalf("el 409 no consume el token: %v", err)
	}
	if _, _, err := env.svc.Reschedule(token, at(120)); err != nil {
		t.Fatalf("reprogramar a un hueco libre después del 409: %v", err)
	}
}

// Reprogramar al mismo instante es 400 (no 409) y no consume el token.
func TestBookingLink_reschedule_al_mismo_instante(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	b, token := env.bookWithEmail(t, at(0), "prof1")

	_, _, err := env.svc.Reschedule(token, b.StartsAt)
	if !errors.Is(err, domain.ErrValidation) || err.Error() != domain.MsgBookingLinkSameSlot {
		t.Fatalf("mismo instante: %v", err)
	}
	if _, err := env.svc.Lookup(token); err != nil {
		t.Fatalf("el 400 no consume el token: %v", err)
	}
}

// La EXCLUDE de la DB rechaza el solape en la carrera: el caso de uso responde el
// 409 del contrato y la transacción deshace el reclamo del token.
func TestBookingLink_reschedule_solape_rechazado_por_la_db(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	b, token := env.bookWithEmail(t, at(0), "prof1")
	// El turno de otro profesional no se pisa: el solape lo provoca la unidad.
	env.links.rejectUpdate = map[string]bool{b.ID: true}

	_, _, err := env.svc.Reschedule(token, at(90))
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgBookingLinkSlotTaken {
		t.Fatalf("solape: %v", err)
	}
	if rows := env.links.rows(); rows[0].Used() {
		t.Fatal("la transacción deshecha no deja el token usado")
	}
	if env.links.rollbacks == 0 {
		t.Fatal("esperaba un rollback")
	}
}

// El turno de OTRO salón nunca se toca: el enlace resuelve el turno con el
// scope del salón de la fila.
func TestBookingLink_otro_salon_nunca_se_toca(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	// Un turno de s2 y una fila de enlace que dice s1.
	ajeno, err := domain.NewBooking("s2", "prof9", "De otro salón", "", at(0), at(0).Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno ajeno: %v", err)
	}
	if err := env.Bookings.Save(ajeno); err != nil {
		t.Fatalf("guardar turno ajeno: %v", err)
	}
	token, _ := domain.NewBookingLinkToken()
	link, err := domain.NewBookingLink("s1", ajeno.ID, domain.HashBookingLinkToken(token), time.Now().UTC())
	if err != nil {
		t.Fatalf("enlace: %v", err)
	}
	if err := env.links.Save(link); err != nil {
		t.Fatalf("guardar enlace: %v", err)
	}
	if err := env.svc.Cancel(token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("un turno de otro salón no existe para el enlace: %v", err)
	}
	if stored, _ := env.Bookings.FindByID("s2", ajeno.ID); stored.Status != domain.BookingBooked {
		t.Fatalf("el turno ajeno no se toca: %+v", stored)
	}
}

// D4: la transacción abre con UN solo lock, el de las escrituras del salón, y no
// pide nada más (ni una segunda conexión ni otro advisory lock).
func TestBookingLink_un_solo_lock(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	_, token := env.bookWithEmail(t, at(0), "prof1")
	env.links.locks = nil

	if err := env.svc.Cancel(token); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	if locks := env.links.lockLog(); len(locks) != 1 || locks[0] != "booking-write:s1" {
		t.Fatalf("locks pedidos: %v", locks)
	}
}

// Cancelar libera el hueco para una reserva pública NUEVA de inmediato.
func TestBookingLink_cancelar_libera_el_hueco(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	_, token := env.bookWithEmail(t, at(0), "prof1")

	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID
	in.StartsAt = at(0)
	in.ProfessionalID = "prof1"
	if _, err := env.Book.Book("navajas-oro", in); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("con el turno vivo el hueco está tomado: %v", err)
	}
	if err := env.svc.Cancel(token); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	in.ClientName = "Nueva"
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("el hueco liberado tiene que aceptar la reserva nueva: %v", err)
	}
}

// D5 (prueba diferencial): Day no cambia; DayExcluding con id vacío da
// EXACTAMENTE lo mismo y con el propio turno lo agrega.
func TestAvailability_Day_y_DayExcluding(t *testing.T) {
	env := newLinkEnv(t)
	date, at := linkDay()
	own, err := domain.NewBooking("s1", "prof1", "Lía", "", at(30), at(30).Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	own.ServiceID = env.Corte.ID
	if err := env.Bookings.Save(own); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}

	day, err := env.Avail.Day("navajas-oro", date, env.Corte.ID, "")
	if err != nil {
		t.Fatalf("Day: %v", err)
	}
	same, err := env.Avail.DayExcluding("navajas-oro", date, env.Corte.ID, "", "")
	if err != nil {
		t.Fatalf("DayExcluding vacío: %v", err)
	}
	if len(day.Slots) != len(same.Slots) {
		t.Fatalf("Day y DayExcluding(\"\") difieren: %d vs %d", len(day.Slots), len(same.Slots))
	}
	for i := range day.Slots {
		if day.Slots[i] != same.Slots[i] {
			t.Fatalf("hueco %d distinto: %+v vs %+v", i, day.Slots[i], same.Slots[i])
		}
	}

	withOwn, err := env.Avail.DayExcluding("navajas-oro", date, env.Corte.ID, "", own.ID)
	if err != nil {
		t.Fatalf("DayExcluding con el turno: %v", err)
	}
	if len(withOwn.Slots) != len(day.Slots)+3 {
		t.Fatalf("excluyendo el propio turno vuelven los tres huecos de prof1 que se pisaban: %d vs %d", len(withOwn.Slots), len(day.Slots))
	}
}

// D5: el turno puede cambiar de UNIDAD de equipamiento al reprogramarse, pero
// nunca queda sin una: si su unidad está tomada en el horario nuevo y hay otra
// libre del tipo, se mueve a esa.
func TestBookingLink_reschedule_cambia_de_unidad(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	rt, units := equipa(t, env.PublicEnv, "Puesto", 2)
	svc, err := domain.NewService("s1", "Con equipo", 45, 100000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	svc.ResourceTypeID = rt.ID
	if err := env.Services.Save(svc); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	// Turno propio a las 09:00 en la unidad 1, con su enlace.
	own := domain.Booking{
		ID: domain.NewID(), SalonID: "s1", ProfessionalID: "prof1", ClientName: "Lía",
		StartsAt: at(0).UTC(), EndsAt: at(0).Add(45 * time.Minute).UTC(),
		Status: domain.BookingBooked, ServiceID: svc.ID, ResourceUnitID: units[0].ID,
		CreatedAt: time.Now().UTC(),
	}
	if err := env.Bookings.Save(own); err != nil {
		t.Fatalf("turno: %v", err)
	}
	token, _ := domain.NewBookingLinkToken()
	link, err := domain.NewBookingLink("s1", own.ID, domain.HashBookingLinkToken(token), time.Now().UTC())
	if err != nil {
		t.Fatalf("enlace: %v", err)
	}
	if err := env.links.Save(link); err != nil {
		t.Fatalf("guardar enlace: %v", err)
	}
	// Otra persona (prof2) ocupa la unidad 1 a las 10:00: la unidad 2 queda libre.
	other := domain.Booking{
		ID: domain.NewID(), SalonID: "s1", ProfessionalID: "prof2", ClientName: "Otra",
		StartsAt: at(60).UTC(), EndsAt: at(60).Add(45 * time.Minute).UTC(),
		Status: domain.BookingBooked, ServiceID: svc.ID, ResourceUnitID: units[0].ID,
		CreatedAt: time.Now().UTC(),
	}
	if err := env.Bookings.Save(other); err != nil {
		t.Fatalf("turno de la otra unidad: %v", err)
	}

	if _, _, err := env.svc.Reschedule(token, at(60)); err != nil {
		t.Fatalf("reprogramar cambiando de unidad: %v", err)
	}
	stored, err := env.Bookings.FindByID("s1", own.ID)
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	if stored.ResourceUnitID != units[1].ID {
		t.Fatalf("tenía que tomar la unidad libre: %q (esperaba %q)", stored.ResourceUnitID, units[1].ID)
	}
}

// R3-001 (revisión nativa): reprogramar no puede mover un turno futuro a un
// instante que ya pasó. Con el reloj del servicio en las 10:00 de un día, las
// 09:30 de ese día están DENTRO del horario del salón y libres: solo la regla
// "tiene que ser futuro" las frena.
func TestBookingLink_reschedule_rechaza_el_pasado(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	// El turno está tres días después: faltan más de 2 horas y se puede cambiar.
	b, token := env.bookWithEmail(t, at(0).AddDate(0, 0, 3), "prof1")
	env.svc.Now = func() time.Time { return at(60) } // "ahora" = 10:00 del día 1

	for _, starts := range []time.Time{at(30), at(60)} { // pasado y justo ahora
		_, _, err := env.svc.Reschedule(token, starts)
		if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgBookingLinkSlotTaken {
			t.Fatalf("horario %v: esperaba el 409 del contrato, dio %v", starts, err)
		}
	}
	// Nada cambió y el token sigue vivo.
	stored, err := env.Bookings.FindByID("s1", b.ID)
	if err != nil || !stored.StartsAt.Equal(b.StartsAt) {
		t.Fatalf("el turno no se movió: %+v %v", stored, err)
	}
	if _, err := env.svc.Lookup(token); err != nil {
		t.Fatalf("el 409 no consume el token: %v", err)
	}
	// Un horario futuro y libre sigue funcionando.
	if _, _, err := env.svc.Reschedule(token, at(120)); err != nil {
		t.Fatalf("reprogramar al futuro: %v", err)
	}
}

// Verificador independiente, D5: una reserva nueva NUNCA cae en una unidad de
// equipo desactivada. Reprogramar conservaba la unidad propia sin mirar si seguía
// activa y dejaba el turno sobre un puesto fuera de servicio.
func TestBookingLink_reschedule_no_conserva_una_unidad_desactivada(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	rt, units := equipa(t, env.PublicEnv, "Puesto", 1)
	svc, err := domain.NewService("s1", "Con equipo", 45, 100000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	svc.ResourceTypeID = rt.ID
	if err := env.Services.Save(svc); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	own := domain.Booking{
		ID: domain.NewID(), SalonID: "s1", ProfessionalID: "prof1", ClientName: "Lía",
		StartsAt: at(0).UTC(), EndsAt: at(0).Add(45 * time.Minute).UTC(),
		Status: domain.BookingBooked, ServiceID: svc.ID, ResourceUnitID: units[0].ID,
		CreatedAt: time.Now().UTC(),
	}
	if err := env.Bookings.Save(own); err != nil {
		t.Fatalf("turno: %v", err)
	}
	token, _ := domain.NewBookingLinkToken()
	link, err := domain.NewBookingLink("s1", own.ID, domain.HashBookingLinkToken(token), time.Now().UTC())
	if err != nil {
		t.Fatalf("enlace: %v", err)
	}
	if err := env.links.Save(link); err != nil {
		t.Fatalf("guardar enlace: %v", err)
	}
	off := units[0]
	off.Active = false
	if err := env.Resources.SaveUnit(off); err != nil {
		t.Fatalf("desactivar la unidad: %v", err)
	}

	_, _, err = env.svc.Reschedule(token, at(60))
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgBookingLinkSlotTaken {
		t.Fatalf("esperaba el 409 del contrato, dio %v", err)
	}
	stored, err := env.Bookings.FindByID("s1", own.ID)
	if err != nil || !stored.StartsAt.Equal(own.StartsAt) || stored.ResourceUnitID != units[0].ID {
		t.Fatalf("el turno no se movió: %+v %v", stored, err)
	}
	if _, err := env.svc.Lookup(token); err != nil {
		t.Fatalf("el 409 no consume el token: %v", err)
	}
}

// Verificador independiente, D5: la duración del turno nuevo es la del servicio
// HOY (como una reserva nueva y como los horarios de `slots`), no la que tenía el
// turno cuando se reservó.
func TestBookingLink_reschedule_usa_la_duracion_actual_del_servicio(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	b, token := env.bookWithEmail(t, at(0), "prof1")
	svc := env.Corte
	svc.DurationMin = 90 // el salón lo alargó después de la reserva
	if err := env.Services.Save(svc); err != nil {
		t.Fatalf("alargar el servicio: %v", err)
	}

	if _, _, err := env.svc.Reschedule(token, at(30)); err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	stored, err := env.Bookings.FindByID("s1", b.ID)
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	if want := at(30).Add(90 * time.Minute); !stored.EndsAt.Equal(want) {
		t.Fatalf("termina %v, tenía que seguir la duración del servicio (%v)", stored.EndsAt, want)
	}
}

// Verificador independiente, D6: un token con espacios, tabs o saltos de línea
// alrededor NO es el token: el 404 genérico, igual que cualquier forma mala.
func TestBookingLink_token_con_espacios_alrededor_es_invalido(t *testing.T) {
	env := newLinkEnv(t)
	env.withMailer()
	_, at := linkDay()
	_, token := env.bookWithEmail(t, at(0), "prof1")

	for _, bad := range []string{" " + token, token + " ", "\t" + token, token + "\n"} {
		if _, err := env.svc.Lookup(bad); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgBookingLinkInvalid {
			t.Fatalf("lookup %q: %v", bad, err)
		}
		if err := env.svc.Cancel(bad); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgBookingLinkInvalid {
			t.Fatalf("cancel %q: %v", bad, err)
		}
	}
	// El token exacto sigue sirviendo (los intentos malos no lo consumieron).
	if _, err := env.svc.Lookup(token); err != nil {
		t.Fatalf("el token exacto sirve: %v", err)
	}
}
