package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// mweekly: horario semanal del salón y de cada persona en memoria, con la misma
// semántica que pgstore (reemplazo total, scope por salón).
type mweekly struct {
	salonDays map[string][]domain.WeekdayHours
	memberSeg map[string][]domain.HoursSegment // "salonID|userID"
}

func (f *mweekly) SalonHours(salonID string) ([]domain.WeekdayHours, error) {
	return append([]domain.WeekdayHours{}, f.salonDays[salonID]...), nil
}

func (f *mweekly) SaveSalonHours(salonID string, days []domain.WeekdayHours) error {
	if f.salonDays == nil {
		f.salonDays = map[string][]domain.WeekdayHours{}
	}
	f.salonDays[salonID] = append([]domain.WeekdayHours{}, days...)
	return nil
}

func (f *mweekly) MemberHours(salonID, userID string) ([]domain.HoursSegment, error) {
	return append([]domain.HoursSegment{}, f.memberSeg[salonID+"|"+userID]...), nil
}

func (f *mweekly) SaveMemberHours(salonID, userID string, segments []domain.HoursSegment) error {
	if f.memberSeg == nil {
		f.memberSeg = map[string][]domain.HoursSegment{}
	}
	f.memberSeg[salonID+"|"+userID] = append([]domain.HoursSegment{}, segments...)
	return nil
}

func (f *mweekly) AllMemberHours(salonID string) (map[string][]domain.HoursSegment, error) {
	prefix := salonID + "|"
	out := map[string][]domain.HoursSegment{}
	for key, segs := range f.memberSeg {
		if strings.HasPrefix(key, prefix) {
			out[strings.TrimPrefix(key, prefix)] = append([]domain.HoursSegment{}, segs...)
		}
	}
	return out, nil
}

var _ ports.WeeklyHoursRepository = (*mweekly)(nil)

// Formas del contrato F2d1 (el frontend codifica contra esto).
type dayJSON struct {
	Weekday int    `json:"weekday"`
	Open    string `json:"open"`
	Close   string `json:"close"`
}

type salonHoursJSON struct {
	Days   []dayJSON `json:"days"`
	Custom bool      `json:"custom"`
}

type segmentJSON struct {
	Weekday int    `json:"weekday"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type memberHoursJSON struct {
	Segments []segmentJSON `json:"segments"`
	Custom   bool          `json:"custom"`
}

// errorOf devuelve el cuerpo {"error": "..."} de una respuesta.
func errorOf(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	mustJSON(t, body, &out)
	return out.Error
}

// S-F2d1-4: GET/PUT /salons/:id/hours. Cualquier miembro lee, owner/admin
// escribe, el reemplazo es total y los errores son los textos exactos del
// contrato. /settings no cambia.
func TestHTTP_salon_hours(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "horario")
	base := "/api/v1/salons/" + salonID + "/hours"

	// Sin filas propias: la ventana única de /settings expandida a los 7 días.
	w := doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("get horas: %d %s", w.Code, w.Body.String())
	}
	var got salonHoursJSON
	mustJSON(t, w.Body.Bytes(), &got)
	if got.Custom || len(got.Days) != 7 {
		t.Fatalf("sin filas: %+v", got)
	}
	if got.Days[0].Weekday != 1 || got.Days[0].Open != "09:00" || got.Days[0].Close != "19:00" ||
		got.Days[6].Weekday != 7 || got.Days[6].Open != "09:00" {
		t.Fatalf("ventana única expandida: %+v", got.Days)
	}

	// PUT: reemplazo total del horario.
	w = doReq(env.r, "PUT", base, `{"days":[{"weekday":1,"open":"09:00","close":"18:00"},{"weekday":2,"open":"10:00","close":"14:00"}]}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("put horas: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &got)
	if !got.Custom || len(got.Days) != 2 || got.Days[0].Open != "09:00" || got.Days[1].Close != "14:00" {
		t.Fatalf("horario guardado: %+v", got)
	}
	w = doReq(env.r, "GET", base, "", ownerTok)
	mustJSON(t, w.Body.Bytes(), &got)
	if !got.Custom || len(got.Days) != 2 {
		t.Fatalf("el horario no persistió: %s", w.Body.String())
	}

	// Errores exactos en español; ninguno cambia el horario.
	for _, bad := range []struct {
		body string
		msg  string
	}{
		{`{"days":[{"weekday":1,"open":"19:00","close":"09:00"}]}`, "Horario inválido: el inicio debe ser anterior al fin"},
		{`{"days":[{"weekday":0,"open":"09:00","close":"19:00"}]}`, "Día inválido"},
		{`{"days":[{"weekday":1,"open":"9:00","close":"19:00"}]}`, "Horario inválido: usá horas HH:MM"},
		{`{"days":[{"weekday":1,"open":"09:00","close":"13:00"},{"weekday":1,"open":"14:00","close":"19:00"}]}`, "El día está repetido"},
	} {
		w := doReq(env.r, "PUT", base, bad.body, ownerTok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad.body, w.Code, w.Body.String())
		}
		if msg := errorOf(t, w.Body.Bytes()); msg != bad.msg {
			t.Fatalf("mensaje %q (esperaba %q)", msg, bad.msg)
		}
	}
	w = doReq(env.r, "GET", base, "", ownerTok)
	mustJSON(t, w.Body.Bytes(), &got)
	if len(got.Days) != 2 {
		t.Fatalf("un cuerpo inválido no puede tocar el horario: %+v", got)
	}

	// /settings NO cambia: sigue siendo la ventana única del salón (PRESERVE).
	w = doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/settings", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	var settings struct {
		OpenTime  string `json:"open_time"`
		CloseTime string `json:"close_time"`
	}
	mustJSON(t, w.Body.Bytes(), &settings)
	if settings.OpenTime != "09:00" || settings.CloseTime != "19:00" {
		t.Fatalf("settings no tiene que cambiar con el horario semanal: %+v", settings)
	}

	// Cualquier miembro lee; solo owner/admin escribe.
	profTok, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	if w := doReq(env.r, "GET", base, "", profTok); w.Code != http.StatusOK {
		t.Fatalf("la profesional lee: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PUT", base, `{"days":[{"weekday":3,"open":"09:00","close":"19:00"}]}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("la profesional edita: %d %s", w.Code, w.Body.String())
	}
	strangerTok, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "GET", base, "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("ajeno leyendo: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}

	// Lista vacía = volver al horario único.
	w = doReq(env.r, "PUT", base, `{"days":[]}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("volver al horario único: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &got)
	if got.Custom || len(got.Days) != 7 {
		t.Fatalf("horario único: %+v", got)
	}
}

// S-F2d1-4: GET/PUT /salons/:id/members/:uid/hours. Cada una ve su horario, otra
// profesional no; solo owner/admin edita; un miembro de otro salón es 404.
func TestHTTP_professional_hours(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-horario")
	profTok, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	otraTok, otraID := env.register(t, "otra@mail.com")
	seedProfessional(env, salonID, otraID)
	_, recepID := env.register(t, "recep@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})

	base := "/api/v1/salons/" + salonID + "/members/"
	body := `{"segments":[{"weekday":2,"start":"09:00","end":"13:00"},{"weekday":2,"start":"14:00","end":"18:00"}]}`

	// El dueño configura el horario de la profesional.
	w := doReq(env.r, "PUT", base+profID+"/hours", body, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("put horario de la profesional: %d %s", w.Code, w.Body.String())
	}
	var got memberHoursJSON
	mustJSON(t, w.Body.Bytes(), &got)
	if !got.Custom || len(got.Segments) != 2 || got.Segments[0].Start != "09:00" || got.Segments[1].End != "18:00" {
		t.Fatalf("tramos guardados: %+v", got)
	}

	// La propia persona lo lee; la compañera no; el dueño sí.
	if w := doReq(env.r, "GET", base+profID+"/hours", "", profTok); w.Code != http.StatusOK {
		t.Fatalf("la profesional lee su horario: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base+profID+"/hours", "", otraTok); w.Code != http.StatusForbidden {
		t.Fatalf("otra profesional: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base+profID+"/hours", "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("el dueño lee: %d %s", w.Code, w.Body.String())
	}
	// Editar su propio horario tampoco: eso es de gestión.
	if w := doReq(env.r, "PUT", base+profID+"/hours", body, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("la profesional edita su horario: %d %s", w.Code, w.Body.String())
	}
	// Recepción no tiene horario propio.
	w = doReq(env.r, "PUT", base+recepID+"/hours", body, ownerTok)
	if w.Code != http.StatusBadRequest || errorOf(t, w.Body.Bytes()) != "Ese rol no tiene horario" {
		t.Fatalf("recepción: %d %s", w.Code, w.Body.String())
	}
	// Un miembro de otro salón: 404 (no distingue "no existe" de "es de otro").
	if w := doReq(env.r, "PUT", base+"u-ajeno/hours", body, ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro inexistente: %d %s", w.Code, w.Body.String())
	}
	// Los errores del horario, exactos.
	for _, bad := range []struct {
		body string
		msg  string
	}{
		{`{"segments":[{"weekday":2,"start":"14:00","end":"13:00"}]}`, "Horario inválido: el inicio debe ser anterior al fin"},
		{`{"segments":[{"weekday":2,"start":"09:00","end":"13:00"},{"weekday":2,"start":"12:00","end":"15:00"}]}`, "Los tramos de un mismo día no pueden solaparse"},
		{`{"segments":[{"weekday":2,"start":"08:00","end":"09:00"},{"weekday":2,"start":"09:00","end":"10:00"},{"weekday":2,"start":"10:00","end":"11:00"},{"weekday":2,"start":"11:00","end":"12:00"},{"weekday":2,"start":"12:00","end":"13:00"}]}`, "Máximo 4 tramos por día"},
		{`{"segments":[{"weekday":8,"start":"09:00","end":"13:00"}]}`, "Día inválido"},
	} {
		w := doReq(env.r, "PUT", base+profID+"/hours", bad.body, ownerTok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad.body, w.Code, w.Body.String())
		}
		if msg := errorOf(t, w.Body.Bytes()); msg != bad.msg {
			t.Fatalf("mensaje %q (esperaba %q)", msg, bad.msg)
		}
	}
	// Después de los errores, los tramos siguen siendo los buenos.
	w = doReq(env.r, "GET", base+profID+"/hours", "", ownerTok)
	mustJSON(t, w.Body.Bytes(), &got)
	if len(got.Segments) != 2 {
		t.Fatalf("un cuerpo inválido no toca los tramos: %+v", got)
	}

	// Lista vacía = "igual que el salón".
	w = doReq(env.r, "PUT", base+profID+"/hours", `{"segments":[]}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("volver al horario del salón: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &got)
	if got.Custom || len(got.Segments) != 0 {
		t.Fatalf("sin tramos propios: %+v", got)
	}
}

// S-F2d1-4: GET /public/salons/:slug agrega `hours` ya expandido, tanto para un
// salón con horario propio como para uno que nunca lo configuró.
func TestHTTP_public_salon_hours(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "vidriera-horario")

	var pub struct {
		Salon struct {
			Slug string `json:"slug"`
		} `json:"salon"`
		Hours []dayJSON `json:"hours"`
	}
	w := doReq(env.r, "GET", "/api/v1/public/salons/vidriera-horario", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("vidriera: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &pub)
	if len(pub.Hours) != 7 || pub.Hours[0].Open != "09:00" || pub.Hours[6].Weekday != 7 {
		t.Fatalf("sin horario propio: %+v", pub.Hours)
	}

	// Con horario propio: solo los días abiertos.
	if w := doReq(env.r, "PUT", "/api/v1/salons/"+salonID+"/hours",
		`{"days":[{"weekday":1,"open":"10:00","close":"14:00"},{"weekday":5,"open":"09:00","close":"18:00"}]}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("guardar horario: %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "GET", "/api/v1/public/salons/vidriera-horario", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("vidriera: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &pub)
	if len(pub.Hours) != 2 || pub.Hours[0].Weekday != 1 || pub.Hours[1].Close != "18:00" {
		t.Fatalf("días abiertos: %+v", pub.Hours)
	}
}

// S-F2d1-5 por HTTP: el día cerrado no ofrece huecos y la reserva es 409; los
// tramos de la profesional no ofrecen el descanso ni dejan reservar un turno que
// lo cruce; la asignación automática saltea a quien no trabaja.
func TestHTTP_availability_booking_weekly_hours(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "horario-publico")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	if env.settings.bySalon == nil {
		env.settings.bySalon = map[string]domain.SalonSettings{}
	}
	env.settings.bySalon[salonID] = domain.SalonSettings{
		SalonID: salonID, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}
	_, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	_, otraID := env.register(t, "otra@mail.com")
	seedProfessional(env, salonID, otraID)

	// Salón abierto de lunes a sábado: el domingo está cerrado.
	if w := doReq(env.r, "PUT", "/api/v1/salons/"+salonID+"/hours", `{"days":[
		{"weekday":1,"open":"09:00","close":"19:00"},{"weekday":2,"open":"09:00","close":"19:00"},
		{"weekday":3,"open":"09:00","close":"19:00"},{"weekday":4,"open":"09:00","close":"19:00"},
		{"weekday":5,"open":"09:00","close":"19:00"},{"weekday":6,"open":"09:00","close":"19:00"}]}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("horario del salón: %d %s", w.Code, w.Body.String())
	}

	// Domingo 6/12/2026: sin huecos y la reserva es 409.
	w := doReq(env.r, "GET", "/api/v1/public/salons/horario-publico/availability?date=2026-12-06&service_id="+svcID, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("disponibilidad del domingo: %d %s", w.Code, w.Body.String())
	}
	var avail struct {
		Slots []struct {
			ProfessionalID string `json:"professional_id"`
			StartsAt       string `json:"starts_at"`
		} `json:"slots"`
	}
	mustJSON(t, w.Body.Bytes(), &avail)
	if len(avail.Slots) != 0 {
		t.Fatalf("el domingo está cerrado: %s", w.Body.String())
	}
	w = doReq(env.r, "POST", "/api/v1/public/salons/horario-publico/bookings",
		`{"service_id":"`+svcID+`","client_name":"Lía","starts_at":"2026-12-06T16:00:00Z"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("reserva en día cerrado: %d %s", w.Code, w.Body.String())
	}

	// La profesional solo trabaja el martes 09:00-13:00 y 14:00-18:00.
	if w := doReq(env.r, "PUT", "/api/v1/salons/"+salonID+"/members/"+profID+"/hours",
		`{"segments":[{"weekday":2,"start":"09:00","end":"13:00"},{"weekday":2,"start":"14:00","end":"18:00"}]}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("tramos de la profesional: %d %s", w.Code, w.Body.String())
	}

	// Martes 1/12/2026: ningún hueco de la profesional cae en el descanso.
	w = doReq(env.r, "GET", "/api/v1/public/salons/horario-publico/availability?date=2026-12-01&service_id="+svcID+"&professional_id="+profID, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("disponibilidad del martes: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &avail)
	if len(avail.Slots) == 0 {
		t.Fatal("el martes la profesional trabaja y tiene huecos")
	}
	for _, s := range avail.Slots {
		// 13:00-14:00 local = 19:00Z-20:00Z (CDMX es UTC-6).
		if s.StartsAt >= "2026-12-01T19:00:00Z" && s.StartsAt < "2026-12-01T20:00:00Z" {
			t.Fatalf("el descanso no ofrece turnos: %+v", avail.Slots)
		}
	}
	// Un turno de 45 min a las 12:30 (18:30Z) cruza el descanso: 409.
	w = doReq(env.r, "POST", "/api/v1/public/salons/horario-publico/bookings",
		`{"service_id":"`+svcID+`","professional_id":"`+profID+`","client_name":"Lía","starts_at":"2026-12-01T18:30:00Z"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("turno que cruza el descanso: %d %s", w.Code, w.Body.String())
	}

	// Lunes 7/12/2026: la profesional no trabaja, la otra sí (no tiene horario
	// propio) y la asignación automática la elige a ella.
	w = doReq(env.r, "POST", "/api/v1/public/salons/horario-publico/bookings",
		`{"service_id":"`+svcID+`","client_name":"Otra","starts_at":"2026-12-07T16:00:00Z"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva del lunes: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Booking struct {
			ProfessionalID string `json:"professional_id"`
		} `json:"booking"`
	}
	mustJSON(t, w.Body.Bytes(), &created)
	// Quien no trabaja el lunes no puede recibir el turno automático. (El dueño
	// del salón también atiende y sigue el horario del salón: cualquiera de los
	// dos con ventana vale; lo que NO vale es la profesional sin horario hoy.)
	if created.Booking.ProfessionalID == profID {
		t.Fatalf("la asignación automática tiene que saltear a quien no trabaja el lunes: %s", created.Booking.ProfessionalID)
	}
	if created.Booking.ProfessionalID == "" {
		t.Fatal("la reserva tiene que quedar asignada")
	}
}
