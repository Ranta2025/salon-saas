package httpapi_test

import (
	"fmt"
	"net/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	httpkit "salon-saas/internal/testutil/httpkit"
	"testing"
	"time"
)

var _ ports.TimeOffRepository = (*httpkit.TimeOff)(nil)

// Formas del contrato F2d2 (el frontend codifica contra esto).
type timeOffBodyJSON struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
	Reason   string `json:"reason"`
}

type timeOffListJSON struct {
	TimeOff []timeOffBodyJSON `json:"time_off"`
}

type conflictBodyJSON struct {
	ID         string `json:"id"`
	ClientName string `json:"client_name"`
	StartsAt   string `json:"starts_at"`
	EndsAt     string `json:"ends_at"`
}

type timeOffCreateJSON struct {
	TimeOff   timeOffBodyJSON    `json:"time_off"`
	Conflicts []conflictBodyJSON `json:"conflicts"`
}

// addMember suma a una persona ya registrada al salón con su rol.
func addMember(t *testing.T, env *httpkit.Env, salonID, token, email, role string) {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"role":%q}`, email, role)
	if w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members", body, token); w.Code != http.StatusCreated {
		t.Fatalf("agregar %s: %d %s", email, w.Code, w.Body.String())
	}
}

// rfc3339 formatea un instante en el formato del contrato.
func rfc3339(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// S-F2d2-4/5: alta, lista, privacidad del motivo, conflictos y borrado.
func TestHTTP_time_off(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "ausencias")
	base := "/api/v1/salons/" + salonID + "/time-off"

	proTok, proID := env.Register(t, "pro@mail.com")
	_, pro2ID := env.Register(t, "pro2@mail.com")
	recTok, _ := env.Register(t, "rec@mail.com")
	addMember(t, env, salonID, ownerTok, "pro@mail.com", "professional")
	addMember(t, env, salonID, ownerTok, "pro2@mail.com", "professional")
	addMember(t, env, salonID, ownerTok, "rec@mail.com", "reception")

	// Un turno `booked` de la profesional y otro cancelado en el mismo período:
	// solo el vigente es conflicto.
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	if w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/bookings",
		fmt.Sprintf(`{"professional_id":%q,"client_name":"Lía","starts_at":%q,"ends_at":%q}`,
			proID, rfc3339(start), rfc3339(start.Add(45*time.Minute))), ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("turno: %d %s", w.Code, w.Body.String())
	}
	canceled := start.Add(2 * time.Hour)
	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/bookings",
		fmt.Sprintf(`{"professional_id":%q,"client_name":"Ana","starts_at":%q,"ends_at":%q}`,
			proID, rfc3339(canceled), rfc3339(canceled.Add(45*time.Minute))), ownerTok)
	var created struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	if w := httpkit.Do(env.Router, "PATCH", "/api/v1/salons/"+salonID+"/bookings/"+created.ID, `{"status":"cancelled"}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("cancelar: %d %s", w.Code, w.Body.String())
	}

	// POST: 201 con la ausencia y los conflictos (solo el turno vigente).
	from, to := start, start.Add(3*time.Hour)
	w = httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members/"+proID+"/time-off",
		fmt.Sprintf(`{"starts_at":%q,"ends_at":%q,"reason":"  Vacaciones  "}`, rfc3339(from), rfc3339(to)), ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", w.Code, w.Body.String())
	}
	var create timeOffCreateJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &create)
	if create.TimeOff.ID == "" || create.TimeOff.UserID != proID || create.TimeOff.Reason != "Vacaciones" {
		t.Fatalf("ausencia: %+v", create.TimeOff)
	}
	if create.TimeOff.StartsAt != rfc3339(from) || create.TimeOff.EndsAt != rfc3339(to) {
		t.Fatalf("período: %+v", create.TimeOff)
	}
	if len(create.Conflicts) != 1 {
		t.Fatalf("un solo conflicto (el cancelado no cuenta): %+v", create.Conflicts)
	}
	if c := create.Conflicts[0]; c.ClientName != "Lía" || c.ID == "" ||
		c.StartsAt != rfc3339(start) || c.EndsAt != rfc3339(start.Add(45*time.Minute)) {
		t.Fatalf("conflicto: %+v", c)
	}

	// GET: dueño ve el motivo; la profesional el suyo; recepción ninguno. Las
	// ausencias se ven siempre (la agenda las necesita).
	query := "?from=" + rfc3339(from.Add(-time.Hour)) + "&to=" + rfc3339(to.Add(time.Hour))
	cases := []struct {
		name     string
		token    string
		wantLen  int
		wantReas string
	}{
		{"dueño", ownerTok, 1, "Vacaciones"},
		{"la propia profesional", proTok, 1, "Vacaciones"},
		{"recepción", recTok, 1, ""},
	}
	for _, c := range cases {
		w := httpkit.Do(env.Router, "GET", base+query, "", c.token)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		var list timeOffListJSON
		httpkit.MustJSON(t, w.Body.Bytes(), &list)
		if len(list.TimeOff) != c.wantLen {
			t.Fatalf("%s: %d ausencias: %+v", c.name, len(list.TimeOff), list.TimeOff)
		}
		if got := list.TimeOff[0].Reason; got != c.wantReas {
			t.Fatalf("%s: motivo %q (esperaba %q)", c.name, got, c.wantReas)
		}
	}

	// Una compañera ve la ausencia pero NO el motivo ajeno (el suyo sí).
	if w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members/"+pro2ID+"/time-off",
		fmt.Sprintf(`{"starts_at":%q,"ends_at":%q,"reason":"Médico"}`, rfc3339(from), rfc3339(to)), ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("alta de la compañera: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "GET", base+query, "", proTok)
	var mine timeOffListJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &mine)
	reasons := map[string]string{}
	for _, off := range mine.TimeOff {
		reasons[off.UserID] = off.Reason
	}
	if reasons[proID] != "Vacaciones" || reasons[pro2ID] != "" {
		t.Fatalf("motivos vistos por una compañera: %+v", reasons)
	}

	// DELETE: 204 y la ausencia se va.
	w = httpkit.Do(env.Router, "DELETE", base+"/"+create.TimeOff.ID, "", ownerTok)
	if w.Code != http.StatusNoContent {
		t.Fatalf("borrar: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "GET", base+query, "", ownerTok)
	var after timeOffListJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &after)
	for _, off := range after.TimeOff {
		if off.ID == create.TimeOff.ID {
			t.Fatalf("la ausencia borrada sigue en la lista: %+v", off)
		}
	}
}

// S-F2d2-5: permisos, tenant isolation y los textos EXACTOS de los errores.
func TestHTTP_time_off_errores(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "ausencias-errores")
	base := "/api/v1/salons/" + salonID + "/time-off"

	proTok, proID := env.Register(t, "pro@mail.com")
	_, recID := env.Register(t, "rec@mail.com")
	addMember(t, env, salonID, ownerTok, "pro@mail.com", "professional")
	addMember(t, env, salonID, ownerTok, "rec@mail.com", "reception")

	// Salón y profesional de OTRO tenant (S-F2d2: la ausencia ajena no existe).
	otroTok, _ := env.Register(t, "otro@mail.com")
	otroSalon := env.Salon(t, otroTok, "ausencias-otro")
	_, otroProID := env.Register(t, "otrop@mail.com")
	addMember(t, env, otroSalon, otroTok, "otrop@mail.com", "professional")

	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	body := func(from, to time.Time, reason string) string {
		return fmt.Sprintf(`{"starts_at":%q,"ends_at":%q,"reason":%q}`, rfc3339(from), rfc3339(to), reason)
	}
	post := func(token, salon, uid, payload string) *http.Response {
		return httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salon+"/members/"+uid+"/time-off", payload, token).Result()
	}

	// Sin token: 401.
	if w := httpkit.Do(env.Router, "GET", base+"?from="+rfc3339(start)+"&to="+rfc3339(start.Add(24*time.Hour)), "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}

	// La profesional no carga ausencias (403): las carga el salón.
	if resp := post(proTok, salonID, proID, body(start, start.Add(24*time.Hour), "")); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("profesional carga: %d", resp.StatusCode)
	}
	// Un ajeno al salón tampoco.
	if resp := post(otroTok, salonID, proID, body(start, start.Add(24*time.Hour), "")); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ajeno carga: %d", resp.StatusCode)
	}

	// Errores de validación con su texto exacto (400).
	cases := []struct {
		name    string
		userID  string
		payload string
		want    string
	}{
		{"fin antes del inicio", proID, body(start, start.Add(-time.Hour), ""), domain.MsgTimeOffOrder},
		{"más de 90 días", proID, body(start, start.Add(91*24*time.Hour), ""), domain.MsgTimeOffTooLong},
		{"ya terminó", proID, body(time.Now().UTC().Add(-48*time.Hour), time.Now().UTC().Add(-24*time.Hour), ""), domain.MsgTimeOffPast},
		{"rol sin ausencias", recID, body(start, start.Add(24*time.Hour), ""), domain.MsgTimeOffRole},
	}
	for _, c := range cases {
		w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members/"+c.userID+"/time-off", c.payload, ownerTok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		if got := errorOf(t, w.Body.Bytes()); got != c.want {
			t.Fatalf("%s: %q (esperaba %q)", c.name, got, c.want)
		}
	}
	// Un miembro de otro salón es 404 (no se confirma ni se niega ajenos).
	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members/"+otroProID+"/time-off",
		body(start, start.Add(24*time.Hour), ""), ownerTok)
	if w.Code != http.StatusNotFound {
		t.Fatalf("miembro de otro salón: %d %s", w.Code, w.Body.String())
	}

	// Alta válida, después el solapamiento de la MISMA persona: 400 exacto.
	w = httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members/"+proID+"/time-off",
		body(start, start.Add(3*time.Hour), "Vacaciones"), ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta válida: %d %s", w.Code, w.Body.String())
	}
	var create timeOffCreateJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &create)
	w = httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/members/"+proID+"/time-off",
		body(start.Add(time.Hour), start.Add(2*time.Hour), ""), ownerTok)
	if w.Code != http.StatusBadRequest || errorOf(t, w.Body.Bytes()) != domain.MsgTimeOffOverlap {
		t.Fatalf("solapamiento: %d %s", w.Code, w.Body.String())
	}

	// El rango de la lista: tope de 92 días y fechas ordenadas.
	w = httpkit.Do(env.Router, "GET", base+"?from="+rfc3339(start)+"&to="+rfc3339(start.Add(93*24*time.Hour)), "", ownerTok)
	if w.Code != http.StatusBadRequest || errorOf(t, w.Body.Bytes()) != domain.MsgTimeOffRangeTooLong {
		t.Fatalf("rango de 93 días: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "GET", base+"?from="+rfc3339(start)+"&to="+rfc3339(start), "", ownerTok)
	if w.Code != http.StatusBadRequest || errorOf(t, w.Body.Bytes()) != domain.MsgTimeOffRangeOrder {
		t.Fatalf("rango vacío: %d %s", w.Code, w.Body.String())
	}

	// DELETE: la profesional no puede (403); el id de otro salón es 404; el
	// propio 204.
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+create.TimeOff.ID, "", proTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional borra: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+create.TimeOff.ID, "", otroTok); w.Code != http.StatusForbidden {
		t.Fatalf("ajeno borra: %d %s", w.Code, w.Body.String())
	}
	// La misma id pedida desde el salón de la otra persona: 404 (otro tenant).
	w = httpkit.Do(env.Router, "DELETE", "/api/v1/salons/"+otroSalon+"/time-off/"+create.TimeOff.ID, "", otroTok)
	if w.Code != http.StatusNotFound {
		t.Fatalf("id de otro salón: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+create.TimeOff.ID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("borrar la propia: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+create.TimeOff.ID, "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("borrar dos veces: %d %s", w.Code, w.Body.String())
	}
}
