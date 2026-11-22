package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"salon-saas/internal/domain"
)

// tokenOf vuelve a loguear a alguien ya registrado (el helper register no
// devuelve el token al segundo pedido).
func tokenOf(t *testing.T, env *posEnv, email string) string {
	t.Helper()
	w := doReq(env.r, "POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"secreta123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", email, w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatalf("login %s sin token: %s", email, w.Body.String())
	}
	return out.Token
}

// memberBody es el miembro tal como lo devuelve la API en F2c: la marca de F2a
// más el oficio y los servicios que hace.
type memberBody struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	Attends    bool     `json:"attends"`
	Trade      string   `json:"trade"`
	ServiceIDs []string `json:"service_ids"`
}

// membersOf pide el listado del equipo y lo indexa por id (es la fuente del
// conteo "N profesionales lo hacen" de la pantalla de servicios).
func membersOf(t *testing.T, env *posEnv, salonID, token string) map[string]memberBody {
	t.Helper()
	w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/members", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("listar equipo: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Members []memberBody `json:"members"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	byID := map[string]memberBody{}
	for _, m := range out.Members {
		byID[m.ID] = m
	}
	return byID
}

// S-F2c-3: el PATCH del equipo guarda el oficio; la pantalla de equipo lo ve
// junto a los servicios de cada quien. Recepción no tiene oficio y el body
// vacío dice qué falta.
func TestHTTP_member_trade(t *testing.T) {
	env := newPOSEnv()
	ownerTok, ownerID := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-oficio")
	base := "/api/v1/salons/" + salonID + "/members"
	_, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	_, recepID := env.register(t, "recep@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})

	// Oficio de la profesional: 200 con el miembro completo.
	w := doReq(env.r, "PATCH", base+"/"+profID, `{"trade":"  Barbero  "}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH trade: %d %s", w.Code, w.Body.String())
	}
	var patched memberBody
	mustJSON(t, w.Body.Bytes(), &patched)
	if patched.ID != profID || patched.Trade != "Barbero" || patched.Role != "professional" ||
		patched.Name == "" || patched.Email == "" || !patched.Attends {
		t.Fatalf("miembro devuelto: %s", w.Body.String())
	}
	if patched.ServiceIDs == nil {
		t.Fatal("service_ids tiene que ser [] y nunca null")
	}
	if got := membersOf(t, env, salonID, ownerTok)[profID]; got.Trade != "Barbero" {
		t.Fatalf("el oficio no quedó guardado: %+v", got)
	}

	// El dueño puede tener oficio (atiende) y la administración también cuando
	// se enciende en el mismo pedido.
	w = doReq(env.r, "PATCH", base+"/"+ownerID, `{"attends":true,"trade":"Estilista"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("dueño con oficio: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &patched)
	if patched.Trade != "Estilista" || !patched.Attends {
		t.Fatalf("dueño-barbero: %s", w.Body.String())
	}

	casos := []struct {
		nombre string
		uid    string
		body   string
		tok    string
		status int
		errMsg string
	}{
		// Recepción no atiende: no tiene oficio.
		{"oficio en recepción", recepID, `{"trade":"Barbero"}`, ownerTok, http.StatusBadRequest, "Ese rol no tiene oficio"},
		// Nada que cambiar: el mensaje lo dice (F2c reemplaza el "body inválido").
		{"sin campos", profID, `{}`, ownerTok, http.StatusBadRequest, "Indicá qué cambiar"},
		// El oficio tiene tope y no lleva saltos de línea.
		{"oficio largo", profID, `{"trade":"` + strings.Repeat("a", 41) + `"}`, ownerTok, http.StatusBadRequest,
			"El oficio no puede superar los 40 caracteres"},
		{"oficio con salto", profID, `{"trade":"Barbero\nEstilista"}`, ownerTok, http.StatusBadRequest,
			"El oficio no puede tener saltos de línea"},
		// Cuerpo que no es JSON.
		{"body roto", profID, `{"trade":123}`, ownerTok, http.StatusBadRequest, "body inválido"},
	}
	for _, c := range casos {
		w := doReq(env.r, "PATCH", base+"/"+c.uid, c.body, c.tok)
		if w.Code != c.status {
			t.Fatalf("%s: %d %s", c.nombre, w.Code, w.Body.String())
		}
		var body struct {
			Error string `json:"error"`
		}
		mustJSON(t, w.Body.Bytes(), &body)
		if body.Error != c.errMsg {
			t.Fatalf("%s: mensaje %q, esperaba %q", c.nombre, body.Error, c.errMsg)
		}
	}
	// Nada de eso tocó el oficio ya guardado.
	if got := membersOf(t, env, salonID, ownerTok)[profID]; got.Trade != "Barbero" {
		t.Fatalf("un rechazo no puede cambiar el oficio: %+v", got)
	}

	// Permisos y aislamiento.
	profTok := tokenOf(t, env, "profe@mail.com")
	if w := doReq(env.r, "PATCH", base+"/"+recepID, `{"trade":"Barbero"}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional editando el equipo: %d %s", w.Code, w.Body.String())
	}
	otherTok, _ := env.register(t, "otro@mail.com")
	otherSalon := env.salon(t, otherTok, "equipo-oficio-otro")
	if w := doReq(env.r, "PATCH", "/api/v1/salons/"+otherSalon+"/members/"+profID, `{"trade":"Barbero"}`, otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro de otro salón: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PATCH", base+"/"+profID, `{"trade":"Barbero"}`, otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("dueño de otro salón: %d %s", w.Code, w.Body.String())
	}
}

// S-F2c-3: PUT /salons/:id/members/:uid/services reemplaza la lista entera.
func TestHTTP_member_services(t *testing.T) {
	env := newPOSEnv()
	ownerTok, ownerID := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-servicios")
	base := "/api/v1/salons/" + salonID + "/members"
	corte := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	keratina := env.seedService(t, ownerTok, salonID, "Keratina", 250000)
	_, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	_, recepID := env.register(t, "recep@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})

	// Asignar dos: 200 con la lista (sin repetidos y ordenada).
	w := doReq(env.r, "PUT", base+"/"+profID+"/services",
		`{"service_ids":["`+keratina+`","`+corte+`","`+corte+`"]}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT servicios: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		ServiceIDs []string `json:"service_ids"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if len(out.ServiceIDs) != 2 {
		t.Fatalf("lista devuelta: %s", w.Body.String())
	}
	got := membersOf(t, env, salonID, ownerTok)[profID]
	if len(got.ServiceIDs) != 2 {
		t.Fatalf("el listado del equipo lleva los servicios: %+v", got)
	}

	// La lista vacía es válida: esa persona no hace ninguno.
	w = doReq(env.r, "PUT", base+"/"+profID+"/services", `{"service_ids":[]}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("lista vacía: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.ServiceIDs == nil || len(out.ServiceIDs) != 0 {
		t.Fatalf("la lista vacía tiene que ser []: %s", w.Body.String())
	}
	if got := membersOf(t, env, salonID, ownerTok)[profID]; len(got.ServiceIDs) != 0 {
		t.Fatalf("la lista vacía no se guardó: %+v", got)
	}

	// Un servicio de otro salón (o inexistente) es 400 con el mensaje exacto.
	otherTok, _ := env.register(t, "otro@mail.com")
	otherSalon := env.salon(t, otherTok, "equipo-servicios-otro")
	ajeno := env.seedService(t, otherTok, otherSalon, "Corte ajeno", 100000)
	for _, bad := range []string{ajeno, "svc-no-existe"} {
		if w := doReq(env.r, "PUT", base+"/"+profID+"/services", `{"service_ids":["`+bad+`"]}`, ownerTok); w.Code != http.StatusBadRequest {
			t.Fatalf("servicio inválido %s: %d %s", bad, w.Code, w.Body.String())
		} else if !strings.Contains(w.Body.String(), `"Servicio inválido"`) {
			t.Fatalf("mensaje del servicio inválido: %s", w.Body.String())
		}
	}

	// Recepción no hace servicios: 400 con el texto del contrato.
	if w := doReq(env.r, "PUT", base+"/"+recepID+"/services", `{"service_ids":["`+corte+`"]}`, ownerTok); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), `"Ese rol no hace servicios"`) {
		t.Fatalf("servicios para recepción: %d %s", w.Code, w.Body.String())
	}
	// Miembro de otro salón: 404. Sin permiso: 403. Sin token: 401.
	if w := doReq(env.r, "PUT", "/api/v1/salons/"+otherSalon+"/members/"+profID+"/services",
		`{"service_ids":[]}`, otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro de otro salón: %d %s", w.Code, w.Body.String())
	}
	profTok := tokenOf(t, env, "profe@mail.com")
	if w := doReq(env.r, "PUT", base+"/"+profID+"/services", `{"service_ids":["`+corte+`"]}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional autoasignándose: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PUT", base+"/"+profID+"/services", `{"service_ids":[]}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d %s", w.Code, w.Body.String())
	}
	// El dueño también es reservable: se puede asignar a sí mismo.
	if w := doReq(env.r, "PUT", base+"/"+ownerID+"/services", `{"service_ids":["`+corte+`"]}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("dueño-barbero: %d %s", w.Code, w.Body.String())
	}
}

// S-F2c-3: staff agrega el oficio pero NO los ids de servicios (lo ve cualquier
// miembro del salón; qué hace cada quien es de la pantalla de equipo).
func TestHTTP_staff_trade_sin_servicios(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "agenda-oficio")
	corte := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	base := "/api/v1/salons/" + salonID + "/members"
	_, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	if w := doReq(env.r, "PATCH", base+"/"+profID, `{"trade":"Barbero"}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("oficio: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PUT", base+"/"+profID+"/services", `{"service_ids":["`+corte+`"]}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("servicios: %d %s", w.Code, w.Body.String())
	}

	w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/staff", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("staff: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Staff []map[string]any `json:"staff"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	var found bool
	for _, m := range out.Staff {
		if m["id"] == profID {
			found = true
			if m["trade"] != "Barbero" {
				t.Fatalf("el oficio viaja en staff: %+v", m)
			}
			if _, leaks := m["service_ids"]; leaks {
				t.Fatalf("staff no expone qué servicios hace cada quien: %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("la profesional tiene que estar en staff: %s", w.Body.String())
	}
}

// S-F2c-4: la disponibilidad y la reserva pública solo ofrecen y asignan a
// quien HACE el servicio; si nadie lo hace, 409 (nunca 500).
func TestHTTP_public_availability_y_reserva_por_servicio(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "agenda-servicios")
	salonSettings(env, salonID)
	corte := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	keratina := env.seedService(t, ownerTok, salonID, "Keratina", 250000)
	_, profCorte := env.register(t, "corte@mail.com")
	seedProfessional(env, salonID, profCorte)
	_, profKeratina := env.register(t, "keratina@mail.com")
	seedProfessional(env, salonID, profKeratina)
	env.profs.assign(salonID, corte, profCorte)
	env.profs.assign(salonID, keratina, profKeratina)

	type slotsBody struct {
		Slots []struct {
			ProfessionalID string `json:"professional_id"`
		} `json:"slots"`
	}
	w := doReq(env.r, "GET", "/api/v1/public/salons/agenda-servicios/availability?date=2026-12-01&service_id="+keratina, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("disponibilidad de keratina: %d %s", w.Code, w.Body.String())
	}
	var slots slotsBody
	mustJSON(t, w.Body.Bytes(), &slots)
	if len(slots.Slots) == 0 {
		t.Fatalf("la keratina tiene una profesional: %s", w.Body.String())
	}
	for _, s := range slots.Slots {
		if s.ProfessionalID != profKeratina {
			t.Fatalf("la de corte no hace keratina: %+v", slots.Slots)
		}
	}

	// Reserva de keratina a las 09:00 (15:00Z): 201, la asigna a la que la hace
	// y guarda el servicio.
	body := `{"service_id":"` + keratina + `","client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T15:00:00Z"}`
	w = doReq(env.r, "POST", "/api/v1/public/salons/agenda-servicios/bookings", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva de keratina: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Booking struct {
			ID             string `json:"id"`
			ProfessionalID string `json:"professional_id"`
		} `json:"booking"`
	}
	mustJSON(t, w.Body.Bytes(), &created)
	if created.Booking.ProfessionalID != profKeratina {
		t.Fatalf("autoasignación por servicio: %s", w.Body.String())
	}

	// El mismo hueco para corte lo toma la otra profesional: no se pisan.
	w = doReq(env.r, "POST", "/api/v1/public/salons/agenda-servicios/bookings",
		`{"service_id":"`+corte+`","client_name":"Ana","client_phone":"556","starts_at":"2026-12-01T15:00:00Z"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("corte a la misma hora: %d %s", w.Code, w.Body.String())
	}

	// Un servicio que nadie hace: disponibilidad vacía y reserva 409.
	manicura := env.seedService(t, ownerTok, salonID, "Manicura", 100000)
	env.profs.assign(salonID, manicura)
	w = doReq(env.r, "GET", "/api/v1/public/salons/agenda-servicios/availability?date=2026-12-01&service_id="+manicura, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("servicio sin profesionales: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &slots)
	if slots.Slots == nil || len(slots.Slots) != 0 {
		t.Fatalf("esperaba {\"slots\":[]}: %s", w.Body.String())
	}
	w = doReq(env.r, "POST", "/api/v1/public/salons/agenda-servicios/bookings",
		`{"service_id":"`+manicura+`","client_name":"Eva","client_phone":"557","starts_at":"2026-12-01T15:00:00Z"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("reserva de un servicio que nadie hace: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), "error interno") {
		t.Fatalf("409 con mensaje, nunca 500: %s", w.Body.String())
	}
}

// S-F2c-3: los turnos de staff llevan service_id (null cuando se agenda a mano)
// y el alta de staff sigue sin exigir servicio.
func TestHTTP_staff_booking_service_id(t *testing.T) {
	env := newPOSEnv()
	ownerTok, ownerID := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "turnos-servicio")
	_, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)

	// Alta de staff sin servicio: service_id null (comportamiento de siempre,
	// con el campo nuevo).
	w := doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/bookings",
		`{"professional_id":"`+profID+`","client_name":"Lía","starts_at":"2026-12-01T15:00:00Z","ends_at":"2026-12-01T16:00:00Z"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta de staff: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID        string  `json:"id"`
		ServiceID *string `json:"service_id"`
	}
	mustJSON(t, w.Body.Bytes(), &created)
	if created.ServiceID != nil {
		t.Fatalf("un turno sin servicio tiene que llevar service_id null: %s", w.Body.String())
	}

	// El listado del calendario trae el campo (null) y no rompe a nadie. Se pide
	// la ventana del turno: por defecto el listado es el día de hoy.
	w = doReq(env.r, "GET", "/api/v1/salons/"+salonID+
		"/bookings?from=2026-12-01T00:00:00Z&to=2026-12-02T00:00:00Z", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar turnos: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Bookings []map[string]any `json:"bookings"`
	}
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Bookings) != 1 {
		t.Fatalf("turnos: %s", w.Body.String())
	}
	if v, ok := list.Bookings[0]["service_id"]; !ok || v != nil {
		t.Fatalf("service_id null en el listado: %+v", list.Bookings[0])
	}
	_ = ownerID
}

// S-F2c-5: el alta de un servicio acepta professional_ids opcional; sin ellos
// el servicio nace sin nadie que lo haga.
func TestHTTP_service_professional_ids(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "servicios-asignados")
	base := "/api/v1/salons/" + salonID + "/services"
	_, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	_, recepID := env.register(t, "recep@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})

	// Sin professional_ids: se crea igual y nadie lo hace.
	w := doReq(env.r, "POST", base, `{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear sin profesionales: %d %s", w.Code, w.Body.String())
	}
	if got := membersOf(t, env, salonID, ownerTok)[profID]; len(got.ServiceIDs) != 0 {
		t.Fatalf("sin ids nadie queda asignado: %+v", got)
	}

	// Con la profesional: el servicio queda asignado a ella.
	w = doReq(env.r, "POST", base,
		`{"name":"Keratina","duration_min":60,"price_cents":250000,"commission_pct":15,"professional_ids":["`+profID+`"]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear con profesional: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	mustJSON(t, w.Body.Bytes(), &created)
	got := membersOf(t, env, salonID, ownerTok)[profID]
	if len(got.ServiceIDs) != 1 || got.ServiceIDs[0] != created.ID {
		t.Fatalf("asignación al crear: %+v", got)
	}

	// Recepción no es reservable: 400 y el servicio no se crea.
	w = doReq(env.r, "POST", base,
		`{"name":"Color","duration_min":60,"price_cents":250000,"professional_ids":["`+recepID+`"]}`, ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"Profesional inválido"`) {
		t.Fatalf("profesional no reservable: %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "GET", base+"?include_inactive=true", "", ownerTok)
	if strings.Contains(w.Body.String(), `"Color"`) {
		t.Fatalf("el alta rechazada no puede quedar: %s", w.Body.String())
	}

	// Más de 50 ids: 400.
	many := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		many = append(many, `"`+profID+`"`)
	}
	w = doReq(env.r, "POST", base,
		`{"name":"Planchado","duration_min":45,"price_cents":200000,"professional_ids":[`+strings.Join(many, ",")+`]}`, ownerTok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("más de 50 profesionales: %d %s", w.Code, w.Body.String())
	}
}
