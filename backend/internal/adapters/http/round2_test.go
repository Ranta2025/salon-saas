package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

// Contrato ronda 2 (T6). El frontend codifica contra estas formas exactas.

// salonSettings siembra la configuración de un salón de prueba (CDMX,
// 09:00-12:00, paso 30) para disponibilidad y reserva pública.
func salonSettings(env *posEnv, salonID string) {
	if env.settings.bySalon == nil {
		env.settings.bySalon = map[string]domain.SalonSettings{}
	}
	env.settings.bySalon[salonID] = domain.SalonSettings{
		SalonID: salonID, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "12:00", SlotStepMin: 30,
	}
}

// --- Settings ---

func TestHTTP_settings(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "ajustes")
	base := "/api/v1/salons/" + salonID + "/settings"

	// Defaults de fábrica (la migración los pone; el caso de uso los responde
	// aunque la fila falte).
	w := doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("get settings: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Timezone    string `json:"timezone"`
		OpenTime    string `json:"open_time"`
		CloseTime   string `json:"close_time"`
		SlotStepMin int    `json:"slot_step_min"`
	}
	mustJSON(t, w.Body.Bytes(), &got)
	if got.Timezone != domain.DefaultTimezone || got.OpenTime != "09:00" || got.CloseTime != "19:00" || got.SlotStepMin != 30 {
		t.Fatalf("defaults: %s", w.Body.String())
	}

	// PUT completo: 200 con el mismo cuerpo.
	w = doReq(env.r, "PUT", base, `{"timezone":"America/Bogota","open_time":"08:00","close_time":"17:30","slot_step_min":20}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("put settings: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &got)
	if got.Timezone != "America/Bogota" || got.OpenTime != "08:00" || got.CloseTime != "17:30" || got.SlotStepMin != 20 {
		t.Fatalf("put no aplicó: %s", w.Body.String())
	}
	w = doReq(env.r, "GET", base, "", ownerTok)
	mustJSON(t, w.Body.Bytes(), &got)
	if got.Timezone != "America/Bogota" {
		t.Fatalf("no persistió: %s", w.Body.String())
	}

	// Validación: zona, formato de hora y paso.
	for _, bad := range []string{
		`{"timezone":"Marte/Olympus","open_time":"08:00","close_time":"17:00","slot_step_min":30}`,
		`{"timezone":"America/Bogota","open_time":"8:00","close_time":"17:00","slot_step_min":30}`,
		`{"timezone":"America/Bogota","open_time":"18:00","close_time":"17:00","slot_step_min":30}`,
		`{"timezone":"America/Bogota","open_time":"08:00","close_time":"17:00","slot_step_min":0}`,
		`no-json`,
	} {
		if w := doReq(env.r, "PUT", base, bad, ownerTok); w.Code != http.StatusBadRequest {
			t.Fatalf("%q debe dar 400, dio %d %s", bad, w.Code, w.Body.String())
		}
	}
	// El profesional lee (necesita el horario para la agenda), no edita.
	profTok, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := doReq(env.r, "GET", base, "", profTok); w.Code != http.StatusOK {
		t.Fatalf("profesional leyendo: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PUT", base, `{"timezone":"America/Bogota","open_time":"08:00","close_time":"17:00","slot_step_min":30}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional editando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// Ajeno al salón: 403. Sin token: 401.
	strangerTok, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "GET", base, "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro leyendo: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}
}

// --- Disponibilidad pública ---

func TestHTTP_availability_public(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "agenda-publica")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	// SIN auth: la landing reserva sin login.
	w := doReq(env.r, "GET", "/api/v1/public/salons/agenda-publica/availability?date=2026-12-01&service_id="+svcID, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("disponibilidad: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Date  string `json:"date"`
		Slots []struct {
			ProfessionalID string `json:"professional_id"`
			StartsAt       string `json:"starts_at"`
			EndsAt         string `json:"ends_at"`
		} `json:"slots"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Date != "2026-12-01" {
		t.Fatalf("fecha: %q", out.Date)
	}
	// 09:00 local = 15:00Z, servicio de 45 min, cierre 12:00 local: 5 arranques
	// por cada persona que atiende (el owner también atiende), 10 en total.
	if len(out.Slots) != 10 {
		t.Fatalf("esperaba 10 huecos, dio %d: %s", len(out.Slots), w.Body.String())
	}
	if out.Slots[0].StartsAt != "2026-12-01T15:00:00Z" || out.Slots[0].EndsAt != "2026-12-01T15:45:00Z" {
		t.Fatalf("primer hueco: %+v", out.Slots[0])
	}
	// Filtro por profesional: solo los suyos.
	w = doReq(env.r, "GET", "/api/v1/public/salons/agenda-publica/availability?date=2026-12-01&service_id="+svcID+"&professional_id="+profID, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("filtro profesional: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if len(out.Slots) != 5 {
		t.Fatalf("el filtro debe traer 5 huecos, dio %d", len(out.Slots))
	}
	for _, s := range out.Slots {
		if s.ProfessionalID != profID {
			t.Fatalf("hueco de otro profesional: %+v", s)
		}
	}
	if w := doReq(env.r, "GET", "/api/v1/public/salons/agenda-publica/availability?date=2026-13-01&service_id="+svcID, "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("fecha inválida debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", "/api/v1/public/salons/agenda-publica/availability?date=2026-12-01", "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("sin service_id debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// Slug inexistente: 404.
	if w := doReq(env.r, "GET", "/api/v1/public/salons/no-existe/availability?date=2026-12-01&service_id="+svcID, "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("slug inexistente debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
	// Aislamiento: el servicio de OTRO salón no se ofrece acá (400, sin fugas).
	otherTok, _ := env.register(t, "otro@mail.com")
	otherSalon := env.salon(t, otherTok, "otro-salon")
	otherSvc := env.seedService(t, otherTok, otherSalon, "Corte ajeno", 100000)
	if w := doReq(env.r, "GET", "/api/v1/public/salons/agenda-publica/availability?date=2026-12-01&service_id="+otherSvc, "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("servicio de otro salón debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
}

// --- Reserva pública ---

func TestHTTP_public_booking(t *testing.T) {
	env := newPOSEnv()
	ownerTok, ownerID := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "reserva-web")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	base := "/api/v1/public/salons/reserva-web/bookings"

	body := `{"service_id":"` + svcID + `","professional_id":"` + profID + `","client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T15:00:00Z"}`
	w := doReq(env.r, "POST", base, body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva pública: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Booking struct {
			ID             string `json:"id"`
			ProfessionalID string `json:"professional_id"`
			StartsAt       string `json:"starts_at"`
			EndsAt         string `json:"ends_at"`
			Status         string `json:"status"`
		} `json:"booking"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.ID == "" || out.Booking.ProfessionalID != profID ||
		out.Booking.StartsAt != "2026-12-01T15:00:00Z" || out.Booking.EndsAt != "2026-12-01T15:45:00Z" ||
		out.Booking.Status != "booked" {
		t.Fatalf("reserva: %s", w.Body.String())
	}
	// El mismo hueco otra vez: 409.
	if w := doReq(env.r, "POST", base, body, ""); w.Code != http.StatusConflict {
		t.Fatalf("hueco tomado debe dar 409, dio %d %s", w.Code, w.Body.String())
	}
	// Faltan datos / servicio inexistente / fuera de horario: 400.
	for _, bad := range []string{
		`{"client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T16:00:00Z"}`,
		`{"service_id":"` + svcID + `","starts_at":"2026-12-01T16:00:00Z"}`,
		`{"service_id":"no-existe","client_name":"Lía","starts_at":"2026-12-01T16:00:00Z"}`,
		`{"service_id":"` + svcID + `","client_name":"Lía","starts_at":"2026-12-01T20:00:00Z"}`,
		`{"service_id":"` + svcID + `","client_name":"Lía","starts_at":"no-es-fecha"}`,
		`no-json`,
	} {
		if w := doReq(env.r, "POST", base, bad, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%q debe dar 400, dio %d %s", bad, w.Code, w.Body.String())
		}
	}
	// Sin profesional: elige uno libre en otro hueco (owner o profesional).
	w = doReq(env.r, "POST", base, `{"service_id":"`+svcID+`","client_name":"Ana","starts_at":"2026-12-01T16:00:00Z"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva sin profesional: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.ProfessionalID != profID && out.Booking.ProfessionalID != ownerID {
		t.Fatalf("debe elegir a alguien que atiende en el salón: %s", w.Body.String())
	}
	// Reintento idempotente con client_op_id: mismo booking, sin duplicar.
	op := `{"service_id":"` + svcID + `","client_name":"Eva","client_op_id":"op-web-1","starts_at":"2026-12-01T17:00:00Z"}`
	w1 := doReq(env.r, "POST", base, op, "")
	w2 := doReq(env.r, "POST", base, op, "")
	if w1.Code != http.StatusCreated || w2.Code != http.StatusCreated {
		t.Fatalf("replay: %d/%d (%s | %s)", w1.Code, w2.Code, w1.Body.String(), w2.Body.String())
	}
	var b1, b2 struct {
		Booking struct {
			ID string `json:"id"`
		} `json:"booking"`
	}
	mustJSON(t, w1.Body.Bytes(), &b1)
	mustJSON(t, w2.Body.Bytes(), &b2)
	if b1.Booking.ID == "" || b1.Booking.ID != b2.Booking.ID {
		t.Fatalf("el replay debe devolver el mismo turno: %q vs %q", b1.Booking.ID, b2.Booking.ID)
	}
}

// El POST público se limita por IP (como /auth) sin tocar los GET públicos.
func TestHTTP_public_booking_rate_limit(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "limite")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	d := env.deps()
	d.PublicBookingLimit = RateLimitFor(2, time.Minute, "POST", "/api/v1/public/salons/")
	r := gin.New()
	RegisterRoutes(r, d)

	post := func(ip string) int {
		req := httptest.NewRequest("POST", "/api/v1/public/salons/limite/bookings",
			strings.NewReader(`{"service_id":"`+svcID+`","client_name":"Lía","starts_at":"2026-12-01T15:00:00Z"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if c := post("203.0.113.9"); c != http.StatusCreated {
		t.Fatalf("primer intento: %d", c)
	}
	// Segundo intento (aunque el hueco ya esté tomado) entra al limiter.
	if c := post("203.0.113.9"); c == http.StatusTooManyRequests {
		t.Fatalf("el segundo intento todavía no debe limitarse: %d", c)
	}
	if c := post("203.0.113.9"); c != http.StatusTooManyRequests {
		t.Fatalf("el tercer intento en la ventana debe dar 429, dio %d", c)
	}
	// Otra IP no está limitada. El GET de disponibilidad tampoco.
	if c := post("198.51.100.7"); c == http.StatusTooManyRequests {
		t.Fatalf("otra IP no debe estar limitada: %d", c)
	}
	req := httptest.NewRequest("GET", "/api/v1/public/salons/limite/availability?date=2026-12-01&service_id="+svcID, nil)
	req.RemoteAddr = "203.0.113.9:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusTooManyRequests {
		t.Fatal("el GET público no se limita")
	}
}

// --- PATCH de turnos ---

func TestHTTP_booking_patch(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "turnos")
	base := "/api/v1/salons/" + salonID + "/bookings"
	seedProfessional(env, salonID, "prof1")
	seedProfessional(env, salonID, "prof2")

	create := `{"professional_id":"prof1","client_name":"Lía","starts_at":"2026-12-01T15:00:00Z","ends_at":"2026-12-01T16:00:00Z"}`
	w := doReq(env.r, "POST", base, create, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear turno: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	mustJSON(t, w.Body.Bytes(), &created)
	env.events.all = nil

	// Cancelar: 200 {booking} con estado cancelled y evento booking.updated.
	w = doReq(env.r, "PATCH", base+"/"+created.ID, `{"status":"cancelled"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("cancelar: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Booking struct {
			ID             string `json:"id"`
			ProfessionalID string `json:"professional_id"`
			ClientName     string `json:"client_name"`
			StartsAt       string `json:"starts_at"`
			EndsAt         string `json:"ends_at"`
			Status         string `json:"status"`
		} `json:"booking"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.ID != created.ID || out.Booking.Status != "cancelled" || out.Booking.ClientName != "Lía" {
		t.Fatalf("respuesta del patch: %s", w.Body.String())
	}
	if _, n := env.events.typed("booking.updated"); n != 1 {
		t.Fatalf("esperaba un booking.updated, hubo %d", n)
	}
	// El hueco liberado se puede volver a agendar (antes daba 409).
	if w := doReq(env.r, "POST", base, create, ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("el hueco cancelado debe quedar libre: %d %s", w.Code, w.Body.String())
	}

	// Reprogramar sobre un hueco ocupado: 409. Libre: 200.
	occupied := `{"professional_id":"prof1","client_name":"Ana","starts_at":"2026-12-02T15:00:00Z","ends_at":"2026-12-02T16:00:00Z"}`
	w = doReq(env.r, "POST", base, occupied, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("turno ocupado: %d %s", w.Code, w.Body.String())
	}
	var occ struct {
		ID string `json:"id"`
	}
	mustJSON(t, w.Body.Bytes(), &occ)
	// Un tercer turno libre para mover encima del ocupado.
	w = doReq(env.r, "POST", base, `{"professional_id":"prof1","client_name":"Eva","starts_at":"2026-12-03T15:00:00Z","ends_at":"2026-12-03T16:00:00Z"}`, ownerTok)
	var free struct {
		ID string `json:"id"`
	}
	mustJSON(t, w.Body.Bytes(), &free)
	w = doReq(env.r, "PATCH", base+"/"+free.ID, `{"starts_at":"2026-12-02T15:00:00Z","ends_at":"2026-12-02T16:00:00Z"}`, ownerTok)
	if w.Code != http.StatusConflict {
		t.Fatalf("reprogramar encima debe dar 409, dio %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "PATCH", base+"/"+free.ID, `{"starts_at":"2026-12-04T15:00:00Z","ends_at":"2026-12-04T16:00:00Z"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("reprogramar libre: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.StartsAt != "2026-12-04T15:00:00Z" {
		t.Fatalf("no movió el turno: %s", w.Body.String())
	}
	// Mover de profesional (prof2 libre): 200.
	if w := doReq(env.r, "PATCH", base+"/"+free.ID, `{"professional_id":"prof2"}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("cambiar profesional: %d %s", w.Code, w.Body.String())
	}

	// Permisos: el profesional solo toca sus turnos; ajeno 403; no-miembro 403.
	profTok, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := doReq(env.r, "PATCH", base+"/"+occ.ID, `{"status":"done"}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional ajeno debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	strangerTok, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "PATCH", base+"/"+occ.ID, `{"status":"done"}`, strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// Turno inexistente: 404. Estado inválido: 400. Sin token: 401.
	if w := doReq(env.r, "PATCH", base+"/no-existe", `{"status":"done"}`, ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("turno inexistente debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PATCH", base+"/"+occ.ID, `{"status":"cancelado"}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("estado inválido debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PATCH", base+"/"+occ.ID, `{"status":"done"}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

// --- Equipo ---

func TestHTTP_members(t *testing.T) {
	env := newPOSEnv()
	ownerTok, ownerID := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-http")
	base := "/api/v1/salons/" + salonID + "/members"
	adminTok, adminID := env.register(t, "admin@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: adminID, SalonID: salonID, Role: domain.RoleAdmin})
	profTok, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	_, nuevoID := env.register(t, "nuevo@mail.com")

	// Listado (owner): forma exacta {members:[{id,name,email,role}]}.
	w := doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar miembros: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Members []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"members"`
	}
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Members) != 3 {
		t.Fatalf("esperaba owner+admin+prof, dio %s", w.Body.String())
	}
	found := map[string]string{}
	for _, m := range list.Members {
		if m.ID == "" || m.Name == "" || m.Email == "" {
			t.Fatalf("miembro incompleto: %+v", m)
		}
		found[m.ID] = m.Role
	}
	if found[ownerID] != "owner" || found[profID] != "professional" {
		t.Fatalf("roles: %+v", found)
	}

	// Owner suma un admin: 201 con el miembro.
	w = doReq(env.r, "POST", base, `{"email":"nuevo@mail.com","role":"admin"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("owner agrega admin: %d %s", w.Code, w.Body.String())
	}
	var added struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	mustJSON(t, w.Body.Bytes(), &added)
	if added.ID != nuevoID || added.Role != "admin" || added.Email != "nuevo@mail.com" {
		t.Fatalf("miembro agregado: %s", w.Body.String())
	}
	// Duplicado: 409. Email sin cuenta: 400. Rol desconocido: 400.
	if w := doReq(env.r, "POST", base, `{"email":"nuevo@mail.com","role":"professional"}`, ownerTok); w.Code != http.StatusConflict {
		t.Fatalf("duplicado debe dar 409, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "POST", base, `{"email":"nadie@mail.com","role":"professional"}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("email sin cuenta debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "POST", base, `{"email":"profe@mail.com","role":"jefe"}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("rol desconocido debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// Admin no crea admins: 403. Profesional no administra: 403.
	if w := doReq(env.r, "POST", base, `{"email":"profe@mail.com","role":"admin"}`, adminTok); w.Code != http.StatusForbidden {
		t.Fatalf("admin creando admin debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional listando debe dar 403, dio %d", w.Code)
	}
	// Aislamiento de tenant: el owner de otro salón no ve ni toca este equipo.
	otherTok, _ := env.register(t, "another@mail.com")
	otherSalon := env.salon(t, otherTok, "otro-equipo")
	if w := doReq(env.r, "GET", base, "", otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("otro salón listando debe dar 403, dio %d", w.Code)
	}
	if w := doReq(env.r, "POST", base, `{"email":"profe@mail.com","role":"professional"}`, otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("otro salón agregando debe dar 403, dio %d", w.Code)
	}
	// La ruta del otro salón usa su propio id y no toca este.
	if w := doReq(env.r, "DELETE", "/api/v1/salons/"+otherSalon+"/members/"+profID, "", otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro de otro salón debe dar 404, dio %d %s", w.Code, w.Body.String())
	}

	// Quitar a un miembro: 204, y deja de estar.
	if w := doReq(env.r, "DELETE", base+"/"+nuevoID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("quitar miembro: %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "GET", base, "", ownerTok)
	mustJSON(t, w.Body.Bytes(), &list)
	for _, m := range list.Members {
		if m.ID == nuevoID {
			t.Fatal("el miembro quitado sigue en la lista")
		}
	}
	// Quitar al último owner: 400. Quitar a alguien que no está: 404.
	if w := doReq(env.r, "DELETE", base+"/"+ownerID, "", ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("último owner debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("quitar profesional: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("quitar dos veces debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

// --- Fotos firmadas ---

func TestHTTP_photo_signed_urls(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "fotos-firmadas")
	w := doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/clients", `{"name":"Lía"}`, ownerTok)
	var cl struct {
		ID string `json:"id"`
	}
	mustJSON(t, w.Body.Bytes(), &cl)
	photos := "/api/v1/salons/" + salonID + "/clients/" + cl.ID + "/photos"

	w = doMultipart(t, env.r, photos, ownerTok, "photo", "foto.png", "image/png", pngDePrueba(t, 20, 20))
	if w.Code != http.StatusCreated {
		t.Fatalf("subir foto: %d %s", w.Code, w.Body.String())
	}
	var up struct {
		Photo struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"photo"`
	}
	mustJSON(t, w.Body.Bytes(), &up)
	u, err := url.Parse(up.Photo.URL)
	if err != nil || u.Path != "/api/v1/photos/"+up.Photo.ID {
		t.Fatalf("url de la foto: %q (%v)", up.Photo.URL, err)
	}
	exp := u.Query().Get("exp")
	sig := u.Query().Get("sig")
	if exp == "" || sig == "" {
		t.Fatalf("la url debe venir firmada: %q", up.Photo.URL)
	}
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || expUnix <= photoURLNow().Unix() {
		t.Fatalf("vencimiento en el futuro: %q", exp)
	}
	_ = expUnix
	// La url firmada se sirve sin Authorization (es lo que hace <img src>).
	req := httptest.NewRequest("GET", up.Photo.URL, nil)
	rec := httptest.NewRecorder()
	env.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("servir foto firmada: %d %s", rec.Code, rec.Body.String())
	}
	// Sin firma, con firma alterada o vencida: 403.
	for _, bad := range []string{
		"/api/v1/photos/" + up.Photo.ID,
		"/api/v1/photos/" + up.Photo.ID + "?exp=" + exp + "&sig=deadbeef",
		"/api/v1/photos/" + up.Photo.ID + "?exp=1&sig=" + sig,
		"/api/v1/photos/otro-id?exp=" + exp + "&sig=" + sig,
	} {
		req = httptest.NewRequest("GET", bad, nil)
		rec = httptest.NewRecorder()
		env.r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%q debe dar 403, dio %d", bad, rec.Code)
		}
	}
	// Vencida de verdad: firmamos con un exp pasado.
	past := photoURLNow().Add(-time.Hour).Unix()
	expired := "/api/v1/photos/" + up.Photo.ID + "?exp=" + strconv.FormatInt(past, 10) + "&sig=" + env.signer.Sign(up.Photo.ID, past)
	req = httptest.NewRequest("GET", expired, nil)
	rec = httptest.NewRecorder()
	env.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("firma vencida debe dar 403, dio %d", rec.Code)
	}
	// La galería devuelve urls firmadas utilizables.
	w = doReq(env.r, "GET", photos, "", ownerTok)
	var gal struct {
		Photos []struct {
			URL string `json:"url"`
		} `json:"photos"`
	}
	mustJSON(t, w.Body.Bytes(), &gal)
	if len(gal.Photos) != 1 || gal.Photos[0].URL == "" {
		t.Fatalf("galería: %s", w.Body.String())
	}
	req = httptest.NewRequest("GET", gal.Photos[0].URL, nil)
	rec = httptest.NewRecorder()
	env.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("la url del listado debe servir: %d", rec.Code)
	}
}

// R3-002: el Content-Type se compara sin distinguir mayúsculas (los navegadores
// y algunas librerías mandan "IMAGE/PNG").
func TestHTTP_photo_content_type_case_insensitive(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "fotos-mayus")
	w := doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/clients", `{"name":"Lía"}`, ownerTok)
	var cl struct {
		ID string `json:"id"`
	}
	mustJSON(t, w.Body.Bytes(), &cl)
	path := "/api/v1/salons/" + salonID + "/clients/" + cl.ID + "/photos"

	// Multipart con el Content-Type de la parte en mayúsculas.
	if w := doMultipart(t, env.r, path, ownerTok, "photo", "foto.png", "IMAGE/PNG", pngDePrueba(t, 20, 20)); w.Code != http.StatusCreated {
		t.Fatalf("multipart IMAGE/PNG: %d %s", w.Code, w.Body.String())
	}
	// Body crudo con el header completo en mayúsculas.
	req := httptest.NewRequest("POST", path, strings.NewReader(string(pngDePrueba(t, 20, 20))))
	req.Header.Set("Content-Type", "IMAGE/PNG")
	req.Header.Set("Authorization", "Bearer "+ownerTok)
	w = httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("raw IMAGE/PNG: %d %s", w.Code, w.Body.String())
	}
}

// --- R4-003: el listado de avisos no cae si falla el contador ---

func TestHTTP_notifications_unread_failure(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "avisos-caidos")

	d := env.deps()
	d.Notifications = NotificationHandler{
		Notifications: app.NotificationService{Notifications: failUnread{inner: env.notifs}, Salons: env.mems, Events: env.events},
		Memberships:   env.mems,
	}
	r := gin.New()
	RegisterRoutes(r, d)

	// Un aviso para que el listado no esté vacío.
	_ = env.notifs.Save(domain.Notification{ID: "n1", SalonID: salonID, Title: "Hola", Body: "x", Kind: domain.KindBooking, CreatedAt: time.Now().UTC()})

	w := doReq(r, "GET", "/api/v1/salons/"+salonID+"/notifications", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar con contador caído debe dar 200, dio %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Notifications []map[string]any `json:"notifications"`
		Unread        int              `json:"unread"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v (%s)", err, w.Body.String())
	}
	if len(out.Notifications) != 1 {
		t.Fatalf("los avisos deben seguir saliendo: %s", w.Body.String())
	}
	if out.Unread != 0 {
		t.Fatalf("sin contador el badge va a 0 (no a un número inventado), dio %d", out.Unread)
	}
}
