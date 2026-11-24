package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// Contrato F7 en HTTP: la forma exacta de las cuatro rutas, los códigos y los
// textos, los permisos rol por rol (D8) y el rate limit propio de la baja (D5).

func (e *waitlistHTTPEnv) apiWaitlist() string {
	return "/api/v1/salons/" + e.salonID + "/waitlist"
}

func (e *waitlistHTTPEnv) apiJoin() string {
	return "/api/v1/public/salons/" + e.slug + "/waitlist"
}

// wlBody arma el cuerpo del alta pública.
func wlBody(serviceID, date, name, email string, extra ...string) string {
	b := `{"service_id":"` + serviceID + `","date":"` + date + `","name":"` + name + `","email":"` + email + `"`
	for i := 0; i+1 < len(extra); i += 2 {
		b += `,"` + extra[i] + `":"` + extra[i+1] + `"`
	}
	return b + `}`
}

// D4: el alta pública crea (201) y es idempotente (200), con la MISMA forma y
// sin devolver NUNCA el token ni el id. Un día con horarios libres es 409 y no
// escribe nada.
func TestHTTP_waitlist_join_publico(t *testing.T) {
	env := newWaitlistHTTPEnv(t)
	day := env.closedDay(t)

	w := doReq(env.r, "POST", env.apiJoin(), wlBody(env.serviceID, day, "Lía", "lia@mail.com", "phone", "555"), "")
	if w.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Status    string `json:"status"`
		Date      string `json:"date"`
		ServiceID string `json:"service_id"`
	}
	mustJSON(t, w.Body.Bytes(), &created)
	if created.Status != domain.WaitlistStatusWaiting || created.Date != day || created.ServiceID != env.serviceID {
		t.Fatalf("respuesta: %s", w.Body.String())
	}
	// Sin id ni token en la respuesta (D4).
	for _, leak := range []string{"token", `"id"`} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("la respuesta no puede llevar %q: %s", leak, w.Body.String())
		}
	}
	// Repetido idéntico: 200 con la misma forma y sin escribir otra entrada.
	w = doReq(env.r, "POST", env.apiJoin(), wlBody(env.serviceID, day, "Lía", "lia@mail.com"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("repetido: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &created)
	if created.Status != domain.WaitlistStatusWaiting || created.Date != day {
		t.Fatalf("repetido: %s", w.Body.String())
	}
	if n := env.entryCount(); n != 1 {
		t.Fatalf("el repetido no crea otra entrada: %d", n)
	}

	// Un día CON horarios libres: 409 con el texto exacto y sin escribir nada.
	env.openSalon(t)
	other := env.openDay(t)
	w = doReq(env.r, "POST", env.apiJoin(), wlBody(env.serviceID, other, "Ana", "ana@mail.com"), "")
	if w.Code != http.StatusConflict || w.Body.String() != `{"error":"Todavía hay horarios libres ese día"}` {
		t.Fatalf("con horarios: %d %s", w.Code, w.Body.String())
	}
	if n := env.entryCount(); n != 1 {
		t.Fatalf("el 409 no puede escribir nada: %d", n)
	}
}

// D4: las validaciones son 400 con el texto del contrato (y un NUL es 400, no un
// 500) y no escriben nada. El pedido no rellena campos obligatorios en silencio.
func TestHTTP_waitlist_join_validaciones(t *testing.T) {
	env := newWaitlistHTTPEnv(t)
	day := env.closedDay(t)

	cases := []struct {
		name string
		body string
		code int
		msg  string
	}{
		{"sin servicio", `{"date":"` + day + `","name":"Lía","email":"a@mail.com"}`, 400, domain.MsgWaitlistJoinMissing},
		{"sin fecha", `{"service_id":"` + env.serviceID + `","name":"Lía","email":"a@mail.com"}`, 400, domain.MsgWaitlistJoinMissing},
		{"sin nombre", `{"service_id":"` + env.serviceID + `","date":"` + day + `","email":"a@mail.com"}`, 400, domain.MsgWaitlistJoinMissing},
		{"sin email", `{"service_id":"` + env.serviceID + `","date":"` + day + `","name":"Lía"}`, 400, domain.MsgWaitlistJoinMissing},
		{"nombre con NUL", wlBody(env.serviceID, day, `Lía\u0000`, "a@mail.com"), 400, domain.MsgWaitlistInvalidChars},
		{"email con NUL", wlBody(env.serviceID, day, "Lía", `a@mail.com\u0000`), 400, domain.MsgWaitlistInvalidChars},
		{"email inválido", wlBody(env.serviceID, day, "Lía", "no-es"), 400, "Email inválido"},
		{"servicio inexistente", wlBody("no-existe", day, "Lía", "a@mail.com"), 400, domain.MsgWaitlistServiceInvalid},
		{"profesional ajena", wlBody(env.serviceID, day, "Lía", "a@mail.com", "professional_id", "u-nadie"), 400, domain.MsgWaitlistProfessionalInvalid},
		{"fecha mal formada", wlBody(env.serviceID, "24/12/2026", "Lía", "a@mail.com"), 400, domain.MsgWaitlistDate},
		{"fecha lejana", wlBody(env.serviceID, "2099-01-01", "Lía", "a@mail.com"), 400, domain.MsgWaitlistDateRange},
		{"json roto", `{"service_id":`, 400, "body inválido"},
		{"tipo equivocado", `{"service_id":123}`, 400, "body inválido"},
	}
	for _, c := range cases {
		w := doReq(env.r, "POST", env.apiJoin(), c.body, "")
		if w.Code != c.code {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		var out struct {
			Error string `json:"error"`
		}
		mustJSON(t, w.Body.Bytes(), &out)
		if out.Error != c.msg {
			t.Fatalf("%s: esperaba %q, dio %q", c.name, c.msg, out.Error)
		}
		if n := env.entryCount(); n != 0 {
			t.Fatalf("%s: un 400 no puede escribir nada (%d entradas)", c.name, n)
		}
	}
	// Salón inexistente: 404.
	w := doReq(env.r, "POST", "/api/v1/public/salons/no-existe/waitlist", wlBody(env.serviceID, day, "Lía", "a@mail.com"), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("slug inexistente: %d %s", w.Code, w.Body.String())
	}
}

// D5: la baja por enlace es idempotente, responde {"status":"left"} y un token
// que no sirve es el MISMO 404 {"error":"Aviso no encontrado"}.
func TestHTTP_waitlist_leave(t *testing.T) {
	env := newWaitlistHTTPEnv(t)
	token := env.seedEntry(t, env.salonID, env.serviceID, env.closedDay(t), "Lía", "lia@mail.com")

	w := doReq(env.r, "POST", "/api/v1/public/waitlist/leave", `{"token":"`+token+`"}`, "")
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"left"}` {
		t.Fatalf("baja: %d %s", w.Code, w.Body.String())
	}
	// Repetir: 200 igual.
	w = doReq(env.r, "POST", "/api/v1/public/waitlist/leave", `{"token":"`+token+`"}`, "")
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"left"}` {
		t.Fatalf("baja repetida: %d %s", w.Code, w.Body.String())
	}
	// Token desconocido, mal formado o ausente: el mismo 404 con el mismo texto.
	other, _ := domain.NewWaitlistToken()
	for _, body := range []string{`{"token":"` + other + `"}`, `{"token":"corto"}`, `{}`, `{"token":""}`} {
		w := doReq(env.r, "POST", "/api/v1/public/waitlist/leave", body, "")
		if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"Aviso no encontrado"}` {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	// Cuerpo inválido: 400 "body inválido" (un token que no es string incluido).
	for _, body := range []string{`{"token":`, `{"token":123}`} {
		w := doReq(env.r, "POST", "/api/v1/public/waitlist/leave", body, "")
		if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"body inválido"}` {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
}

// D5/D4: la baja tiene SU PROPIO cubo de rate limit (no comparte el de la
// reserva pública ni el de los enlaces) y el alta comparte el de la reserva.
func TestHTTP_waitlist_rate_limits(t *testing.T) {
	// Baja: cubo propio.
	env := newWaitlistHTTPEnv(t)
	env.r = env.routerWith(t, func(d *Deps) {
		d.PublicWaitlistLeaveLimit = RateLimitFor(2, time.Minute, "POST", "/api/v1/public/waitlist/")
	})
	for i := 0; i < 2; i++ {
		if w := doReq(env.r, "POST", "/api/v1/public/waitlist/leave", `{"token":"corto"}`, ""); w.Code == http.StatusTooManyRequests {
			t.Fatalf("pedido %d no debía estar limitado", i+1)
		}
	}
	w := doReq(env.r, "POST", "/api/v1/public/waitlist/leave", `{"token":"corto"}`, "")
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "demasiados intentos") {
		t.Fatalf("tercer pedido de baja: %d %s", w.Code, w.Body.String())
	}

	// Alta: comparte PublicBookingLimit (D4).
	env2 := newWaitlistHTTPEnv(t)
	env2.r = env2.routerWith(t, func(d *Deps) {
		d.PublicBookingLimit = RateLimitFor(2, time.Minute, "POST", "/api/v1/public/salons/")
	})
	day := env2.closedDay(t)
	for i := 0; i < 2; i++ {
		if w := doReq(env2.r, "POST", env2.apiJoin(), wlBody(env2.serviceID, day, "Lía", "a"+string(rune('a'+i))+"@mail.com"), ""); w.Code == http.StatusTooManyRequests {
			t.Fatalf("alta %d no debía estar limitada", i+1)
		}
	}
	if w := doReq(env2.r, "POST", env2.apiJoin(), wlBody(env2.serviceID, day, "Ana", "z@mail.com"), ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("tercera alta: %d %s", w.Code, w.Body.String())
	}
}

// D8: GET/DELETE del personal. Owner, administración y recepción listan y dan de
// baja; profesional y cliente 403; sin token 401; otro salón 403; una entrada de
// otro salón es 404. La forma del JSON es la del contrato.
func TestHTTP_waitlist_staff(t *testing.T) {
	env := newWaitlistHTTPEnv(t)
	day := env.closedDay(t)
	entryID := env.seedEntryWithID(t, env.salonID, env.serviceID, "", day, "Lía", "lia@mail.com", "555")
	env.markNotified(t, entryID)
	// Una entrada de ayer no se lista (día local >= hoy).
	yesterday := time.Now().In(env.loc).AddDate(0, 0, -1).Format("2006-01-02")
	env.seedEntryWithID(t, env.salonID, env.serviceID, "", yesterday, "Vieja", "vieja@mail.com", "")

	for _, tok := range []string{env.owner, env.admin, env.reception} {
		w := doReq(env.r, "GET", env.apiWaitlist(), "", tok)
		if w.Code != http.StatusOK {
			t.Fatalf("listar: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Entries []struct {
				ID               string  `json:"id"`
				ServiceID        string  `json:"service_id"`
				ServiceName      string  `json:"service_name"`
				ProfessionalID   *string `json:"professional_id"`
				ProfessionalName *string `json:"professional_name"`
				Date             string  `json:"date"`
				Name             string  `json:"name"`
				Email            string  `json:"email"`
				Phone            *string `json:"phone"`
				Status           string  `json:"status"`
				CreatedAt        string  `json:"created_at"`
				NotifiedAt       *string `json:"notified_at"`
			} `json:"entries"`
		}
		mustJSON(t, w.Body.Bytes(), &out)
		if len(out.Entries) != 1 {
			t.Fatalf("esperaba 1 entrada, dio %d (%s)", len(out.Entries), w.Body.String())
		}
		e := out.Entries[0]
		if e.ID != entryID || e.ServiceID != env.serviceID || e.ServiceName != "Corte" ||
			e.Date != day || e.Name != "Lía" || e.Email != "lia@mail.com" ||
			e.Status != domain.WaitlistStatusNotified || e.NotifiedAt == nil ||
			e.ProfessionalID != nil || e.ProfessionalName != nil ||
			e.Phone == nil || *e.Phone != "555" || e.CreatedAt == "" {
			t.Fatalf("forma de la entrada: %s", w.Body.String())
		}
	}
	// Profesional y cliente: 403 en las dos.
	for _, tok := range []string{env.professional, env.client} {
		if w := doReq(env.r, "GET", env.apiWaitlist(), "", tok); w.Code != http.StatusForbidden {
			t.Fatalf("profesional/cliente lista: %d", w.Code)
		}
		if w := doReq(env.r, "DELETE", env.apiWaitlist()+"/"+entryID, "", tok); w.Code != http.StatusForbidden {
			t.Fatalf("profesional/cliente borra: %d", w.Code)
		}
	}
	// Sin token: 401.
	if w := doReq(env.r, "GET", env.apiWaitlist(), "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token lista: %d", w.Code)
	}
	if w := doReq(env.r, "DELETE", env.apiWaitlist()+"/"+entryID, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token borra: %d", w.Code)
	}
	// Otro salón: quien no es miembro no entra (403 con el texto del borde).
	otherTok, _ := env.register(t, "otro@mail.com")
	otherSalon := env.salon(t, otherTok, "otro-salon")
	w := doReq(env.r, "GET", env.apiWaitlist(), "", otherTok)
	if w.Code != http.StatusForbidden || w.Body.String() != `{"error":"sin acceso al salón"}` {
		t.Fatalf("otro salón: %d %s", w.Code, w.Body.String())
	}
	// Una entrada de otro salón es 404 con el texto exacto.
	foreign := env.seedEntryWithID(t, otherSalon, env.serviceID, "", day, "Ajena", "ajena@mail.com", "")
	w = doReq(env.r, "DELETE", env.apiWaitlist()+"/"+foreign, "", env.owner)
	if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"Aviso no encontrado"}` {
		t.Fatalf("entrada de otro salón: %d %s", w.Code, w.Body.String())
	}
	// Quitar la propia: 204 y después 404 (ya dada de baja).
	if w := doReq(env.r, "DELETE", env.apiWaitlist()+"/"+entryID, "", env.owner); w.Code != http.StatusNoContent {
		t.Fatalf("quitar: %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "DELETE", env.apiWaitlist()+"/"+entryID, "", env.owner)
	if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"Aviso no encontrado"}` {
		t.Fatalf("quitar dos veces: %d %s", w.Code, w.Body.String())
	}
	// Recepción también puede quitar (D8).
	second := env.seedEntryWithID(t, env.salonID, env.serviceID, "", day, "Ana", "ana@mail.com", "")
	if w := doReq(env.r, "DELETE", env.apiWaitlist()+"/"+second, "", env.reception); w.Code != http.StatusNoContent {
		t.Fatalf("recepción quita: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", env.apiWaitlist(), "", env.owner); w.Code != http.StatusOK {
		t.Fatalf("listar después: %d", w.Code)
	} else {
		var out map[string]json.RawMessage
		mustJSON(t, w.Body.Bytes(), &out)
		if string(out["entries"]) != "[]" {
			t.Fatalf("sin entradas la lista es [] y no null: %s", w.Body.String())
		}
	}
}
