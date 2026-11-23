package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

// Contrato F1 (HTTP): las cuatro rutas públicas del enlace firmado, con el token
// en el CUERPO, sin filtrar datos del cliente y con los mensajes exactos.

const linkHTTPAppURL = "http://localhost:8081"

type linkHTTPEnv struct {
	*posEnv
	r     *gin.Engine
	links *mlinks
	svc   app.BookingLinkService
}

// newLinkHTTPEnv arma el router con el caso de uso del enlace sobre los fakes del
// resto de la API: la reserva pública emite el token y las cuatro rutas lo usan.
func newLinkHTTPEnv(t *testing.T) *linkHTTPEnv { return buildLinkHTTPEnv(t, false) }

// buildLinkHTTPEnv arma el router con el enlace (y, si se pide, con los tres
// límites por IP de producción, cada uno con SU PROPIA instancia).
func buildLinkHTTPEnv(t *testing.T, withLimits bool) *linkHTTPEnv {
	t.Helper()
	env := newPOSEnv()
	links := &mlinks{bookings: env.bookings}
	d := env.deps()
	// La reserva pública emite el enlace de la confirmación (F1/D1).
	pub := d.Public.Bookings
	pub.Links = links
	pub.AppURL = linkHTTPAppURL
	d.Public.Bookings = pub
	svc := app.BookingLinkService{
		Links: links, Tx: links, Bookings: env.bookings, Salons: env.salons,
		Services: env.services, Staff: env.mems, Settings: env.settings,
		Professionals: env.profs, Hours: env.weekly, TimeOff: env.timeOff,
		Resources: env.resources, Availability: d.Public.Avail,
		Events: env.events, Notifier: d.Notifications.Notifications,
		Mailer: env.mailer, Emails: env.emails, AppURL: linkHTTPAppURL,
	}
	d.BookingLinks = BookingLinkHandler{Links: svc}
	if withLimits {
		// Como en cmd/api: mismo valor (20/min) y TRES instancias distintas.
		d.PublicBookingLimit = RateLimitFor(linkRateLimit, time.Minute, "POST", "/api/v1/public/salons/")
		d.PublicInvitationLimit = RateLimitFor(linkRateLimit, time.Minute, "POST", "/api/v1/public/invitations/")
		d.PublicBookingLinkLimit = RateLimitFor(linkRateLimit, time.Minute, "POST", "/api/v1/public/booking-links/")
	}
	env.r = gin.New()
	RegisterRoutes(env.r, d)
	return &linkHTTPEnv{posEnv: env, r: env.r, links: links, svc: svc}
}

// seedLinkSalon siembra salón, servicio y profesional con su horario.
func (e *linkHTTPEnv) seedLinkSalon(t *testing.T, slug string) (salonID, svcID, profID string) {
	t.Helper()
	ownerTok, _ := e.register(t, "owner-"+slug+"@mail.com")
	salonID = e.salon(t, ownerTok, slug)
	svcID = e.seedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(e.posEnv, salonID)
	_, profID = e.register(t, "profe-"+slug+"@mail.com")
	seedProfessional(e.posEnv, salonID, profID)
	// Qué servicio hace: la reserva pública exige que el profesional lo haga.
	e.profs.byService = map[string][]string{salonID + "|" + svcID: {profID}}
	return salonID, svcID, profID
}

// seedLinkBooking siembra el salón y reserva por la ruta pública con email;
// devuelve el token del enlace que salió en la confirmación.
func (e *linkHTTPEnv) seedLinkBooking(t *testing.T, slug string, offsetDays int) (salonID string, token string) {
	t.Helper()
	salonID, svcID, profID := e.seedLinkSalon(t, slug)

	starts := linkHTTPDay(t, offsetDays)
	body := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Lía","client_phone":"5551234","client_email":"lia@mail.com",` +
		`"starts_at":"` + starts.Format(time.RFC3339) + `"}`
	w := doReq(e.r, "POST", "/api/v1/public/salons/"+slug+"/bookings", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva pública: %d %s", w.Code, w.Body.String())
	}
	msg := e.mailer.waitCount(t, len(e.mailer.sent)+1)
	last := msg[len(msg)-1]
	idx := strings.Index(last.Text, "/turno#")
	if idx < 0 {
		t.Fatalf("la confirmación no lleva enlace:\n%s", last.Text)
	}
	rest := last.Text[idx+len("/turno#"):]
	if end := strings.IndexAny(rest, " \n\t\r<\"');,"); end >= 0 {
		rest = rest[:end]
	}
	if !domain.ValidBookingLinkToken(rest) {
		t.Fatalf("token del enlace: %q", rest)
	}
	return salonID, rest
}

// linkHTTPDay devuelve un arranque 09:00 local (CDMX) de un día futuro dentro de
// la ventana 09:00-12:00 del fixture.
func linkHTTPDay(t *testing.T, offsetDays int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	d := time.Now().In(loc).AddDate(0, 0, 2+offsetDays)
	return time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, loc)
}

// linkPost manda un POST JSON a una de las cuatro rutas del enlace.
func (e *linkHTTPEnv) linkPost(t *testing.T, route, body string) (int, map[string]any) {
	t.Helper()
	w := doReq(e.r, "POST", "/api/v1/public/booking-links/"+route, body, "")
	var out map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w.Code, out
}

// D6: con un token vigente la respuesta trae SOLO lo del turno: ni teléfono, ni
// email, ni ids internos del cliente.
func TestHTTP_booking_link_lookup(t *testing.T) {
	env := newLinkHTTPEnv(t)
	_, token := env.seedLinkBooking(t, "enlace-ok", 0)

	w := doReq(env.r, "POST", "/api/v1/public/booking-links/lookup", `{"token":"`+token+`"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Booking struct {
			SalonName        string `json:"salon_name"`
			ServiceName      string `json:"service_name"`
			ProfessionalName string `json:"professional_name"`
			StartsAt         string `json:"starts_at"`
			EndsAt           string `json:"ends_at"`
			Timezone         string `json:"timezone"`
			CanChange        bool   `json:"can_change"`
			MinNoticeHours   int    `json:"min_notice_hours"`
		} `json:"booking"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.SalonName != "Salón enlace-ok" || out.Booking.ServiceName != "Corte" ||
		out.Booking.ProfessionalName == "" || out.Booking.Timezone != "America/Mexico_City" ||
		!out.Booking.CanChange || out.Booking.MinNoticeHours != 2 {
		t.Fatalf("lookup incompleto: %s", w.Body.String())
	}
	if _, err := time.Parse(time.RFC3339, out.Booking.StartsAt); err != nil {
		t.Fatalf("starts_at no es RFC3339: %q", out.Booking.StartsAt)
	}
	if _, err := time.Parse(time.RFC3339, out.Booking.EndsAt); err != nil {
		t.Fatalf("ends_at no es RFC3339: %q", out.Booking.EndsAt)
	}
	// Nada de datos personales ni ids internos en el JSON.
	body := w.Body.String()
	for _, forbidden := range []string{"mail.com", "5551234", "client_name", "client_phone", "Lía", "profe-"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("la respuesta no puede llevar %q: %s", forbidden, body)
		}
	}
}

// D6: inexistente, vacío, mal formado y ya usado dan el MISMO 404 con el MISMO
// texto; y un token que no es una cadena, un JSON roto o un cuerpo sin token son
// 400 (nunca 500).
func TestHTTP_booking_link_errores(t *testing.T) {
	env := newLinkHTTPEnv(t)
	_, token := env.seedLinkBooking(t, "enlace-errores", 0)

	used, _ := domain.NewBookingLinkToken()
	for _, body := range []string{
		`{"token":"` + strings.Repeat("a", 43) + `"}`,
		`{"token":""}`,
		`{"token":"` + used + `"}`,
		`{"token":"   "}`,
	} {
		code, out := env.linkPost(t, "lookup", body)
		if code != http.StatusNotFound || out["error"] != domain.MsgBookingLinkInvalid {
			t.Fatalf("body %s: %d %v", body, code, out)
		}
	}
	// Un token de verdad, ya usado: el mismo 404.
	if code, out := env.linkPost(t, "cancel", `{"token":"`+token+`"}`); code != http.StatusOK {
		t.Fatalf("cancelar: %d %v", code, out)
	}
	if code, out := env.linkPost(t, "lookup", `{"token":"`+token+`"}`); code != http.StatusNotFound || out["error"] != domain.MsgBookingLinkInvalid {
		t.Fatalf("token usado: %d %v", code, out)
	}
	// Tipo equivocado, JSON roto y cuerpo sin token: 400 body inválido.
	for _, body := range []string{`{"token":123}`, `{"token":null}`, `{}`, `no-json`, `{"token":["a"]}`} {
		code, out := env.linkPost(t, "lookup", body)
		if code != http.StatusBadRequest || out["error"] != "body inválido" {
			t.Fatalf("body %s: %d %v", body, code, out)
		}
	}
}

// slots: misma forma de ítems que la disponibilidad pública, día local del salón
// y fecha inválida con su texto exacto.
func TestHTTP_booking_link_slots(t *testing.T) {
	env := newLinkHTTPEnv(t)
	_, token := env.seedLinkBooking(t, "enlace-slots", 0)
	date := linkHTTPDay(t, 0).Format("2006-01-02")

	code, out := env.linkPost(t, "slots", `{"token":"`+token+`","date":"`+date+`"}`)
	if code != http.StatusOK {
		t.Fatalf("slots: %d %v", code, out)
	}
	slots, _ := out["slots"].([]any)
	if len(slots) == 0 {
		t.Fatalf("esperaba huecos: %v", out)
	}
	first, _ := slots[0].(map[string]any)
	if first["professional_id"] == nil || first["starts_at"] == nil || first["ends_at"] == nil {
		t.Fatalf("forma del hueco: %v", first)
	}
	if out["date"] != date {
		t.Fatalf("fecha devuelta: %v", out["date"])
	}
	// Fecha inválida: 400 con el texto del contrato.
	code, out = env.linkPost(t, "slots", `{"token":"`+token+`","date":"01/12/2026"}`)
	if code != http.StatusBadRequest || out["error"] != domain.MsgBookingLinkDate {
		t.Fatalf("fecha inválida: %d %v", code, out)
	}
	// Sin fecha: body inválido, no un 404 mentiroso.
	if code, out := env.linkPost(t, "slots", `{"token":"`+token+`"}`); code != http.StatusBadRequest || out["error"] != "body inválido" {
		t.Fatalf("sin fecha: %d %v", code, out)
	}
}

// cancel: 200 {"status":"cancelled"} y el segundo intento con el mismo token es
// el 404 genérico.
func TestHTTP_booking_link_cancel(t *testing.T) {
	env := newLinkHTTPEnv(t)
	salonID, token := env.seedLinkBooking(t, "enlace-cancel", 0)

	code, out := env.linkPost(t, "cancel", `{"token":"`+token+`"}`)
	if code != http.StatusOK || out["status"] != "cancelled" {
		t.Fatalf("cancelar: %d %v", code, out)
	}
	stored, err := env.bookings.FindByID(salonID, env.bookings.all[len(env.bookings.all)-1].ID)
	if err != nil || stored.Status != domain.BookingCancelled {
		t.Fatalf("el turno quedó cancelado: %+v %v", stored, err)
	}
	if code, out := env.linkPost(t, "cancel", `{"token":"`+token+`"}`); code != http.StatusNotFound || out["error"] != domain.MsgBookingLinkInvalid {
		t.Fatalf("segundo cancelar: %d %v", code, out)
	}
}

// reprogramar: 200 con el turno nuevo y new_link_sent; el mismo instante es 400 y
// un horario ocupado 409, con los textos exactos.
func TestHTTP_booking_link_reschedule(t *testing.T) {
	env := newLinkHTTPEnv(t)
	day := linkHTTPDay(t, 0)
	_, token := env.seedLinkBooking(t, "enlace-mover", 0)

	// Mismo instante: 400 con el texto del contrato.
	code, out := env.linkPost(t, "reschedule", `{"token":"`+token+`","starts_at":"`+day.Format(time.RFC3339)+`"}`)
	if code != http.StatusBadRequest || out["error"] != domain.MsgBookingLinkSameSlot {
		t.Fatalf("mismo instante: %d %v", code, out)
	}
	// starts_at mal formado / faltante: body inválido.
	for _, body := range []string{
		`{"token":"` + token + `","starts_at":"mañana"}`,
		`{"token":"` + token + `"}`,
	} {
		if code, out := env.linkPost(t, "reschedule", body); code != http.StatusBadRequest || out["error"] != "body inválido" {
			t.Fatalf("body %s: %d %v", body, code, out)
		}
	}
	// Fuera de horario: 409 "Ese horario ya no está disponible: elegí otro".
	night := time.Date(day.Year(), day.Month(), day.Day(), 22, 0, 0, 0, day.Location())
	code, out = env.linkPost(t, "reschedule", `{"token":"`+token+`","starts_at":"`+night.Format(time.RFC3339)+`"}`)
	if code != http.StatusConflict || out["error"] != domain.MsgBookingLinkSlotTaken {
		t.Fatalf("fuera de horario: %d %v", code, out)
	}
	// Un hueco libre: 200 con la misma forma que lookup y new_link_sent=true.
	target := day.Add(90 * time.Minute)
	code, out = env.linkPost(t, "reschedule", `{"token":"`+token+`","starts_at":"`+target.Format(time.RFC3339)+`"}`)
	if code != http.StatusOK {
		t.Fatalf("reprogramar: %d %v", code, out)
	}
	if out["new_link_sent"] != true {
		t.Fatalf("new_link_sent: %v", out["new_link_sent"])
	}
	booking, _ := out["booking"].(map[string]any)
	if booking["starts_at"] != target.UTC().Format(time.RFC3339) || booking["salon_name"] == nil {
		t.Fatalf("booking de la respuesta: %v", booking)
	}
	// El token viejo murió y el nuevo (el del correo) sirve.
	if code, out := env.linkPost(t, "lookup", `{"token":"`+token+`"}`); code != http.StatusNotFound {
		t.Fatalf("token viejo: %d %v", code, out)
	}
	if len(env.links.rows()) != 2 {
		t.Fatalf("esperaba dos enlaces emitidos (confirmación + reprogramación): %+v", env.links.rows())
	}
}

// linkRateLimit es el valor del contrato (D7): el mismo de /auth, 20 por minuto.
const linkRateLimit = 20

// D7: el enlace tiene SU PROPIO cubo de velocidad. Una inundación de enlaces no
// consume el cubo de la reserva pública ni el de las invitaciones, y al revés.
func TestHTTP_booking_link_rate_limit_propio(t *testing.T) {
	// (a) Inundar el enlace no toca los otros dos cubos.
	env := buildLinkHTTPEnv(t, true)
	_, svcID, profID := env.seedLinkSalon(t, "limite-enlace")
	_, token := env.seedLinkBooking(t, "limite-enlace-2", 0)

	body := `{"token":"` + token + `"}`
	for i := 0; i < linkRateLimit; i++ {
		if code, _ := env.linkPost(t, "lookup", body); code == http.StatusTooManyRequests {
			t.Fatalf("el pedido %d no puede estar limitado todavía", i+1)
		}
	}
	code, out := env.linkPost(t, "lookup", body)
	if code != http.StatusTooManyRequests || out["error"] != "demasiados intentos, esperá un minuto y volvé a probar" {
		t.Fatalf("pedido %d: %d %v", linkRateLimit+1, code, out)
	}
	// La reserva pública sigue con su cupo intacto (y la invitación también).
	starts := linkHTTPDay(t, 1)
	book := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Ana","starts_at":"` + starts.Format(time.RFC3339) + `"}`
	w := doReq(env.r, "POST", "/api/v1/public/salons/limite-enlace/bookings", book, "")
	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("la inundación de enlaces no puede gastar el cubo de la reserva pública: %s", w.Body.String())
	}
	if w := doReq(env.r, "POST", "/api/v1/public/invitations/lookup", `{"token":"x"}`, ""); w.Code == http.StatusTooManyRequests {
		t.Fatalf("la inundación de enlaces no puede gastar el cubo de las invitaciones: %s", w.Body.String())
	}

	// (b) Inundar la reserva pública no toca el cubo del enlace.
	other := buildLinkHTTPEnv(t, true)
	_, oSvc, oProf := other.seedLinkSalon(t, "limite-reserva")
	_, token2 := other.seedLinkBooking(t, "limite-reserva-2", 0)
	// El seed ya gastó un intento: se completan los 20 y el siguiente da 429.
	for i := 1; i <= linkRateLimit; i++ {
		starts := linkHTTPDay(t, i)
		body := `{"service_id":"` + oSvc + `","professional_id":"` + oProf +
			`","client_name":"Ana","starts_at":"` + starts.Format(time.RFC3339) + `"}`
		w := doReq(other.r, "POST", "/api/v1/public/salons/limite-reserva/bookings", body, "")
		if i < linkRateLimit && w.Code == http.StatusTooManyRequests {
			t.Fatalf("la reserva %d no puede estar limitada todavía", i)
		}
		if i == linkRateLimit && w.Code != http.StatusTooManyRequests {
			t.Fatalf("la reserva %d tiene que dar 429: %d %s", i, w.Code, w.Body.String())
		}
	}
	if code, out := other.linkPost(t, "lookup", `{"token":"`+token2+`"}`); code == http.StatusTooManyRequests {
		t.Fatalf("la inundación de reservas no puede gastar el cubo del enlace: %d %v", code, out)
	}
}
