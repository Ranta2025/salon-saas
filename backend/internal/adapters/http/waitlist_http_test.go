package httpapi_test

import (
	"encoding/json"
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"
)

// Contrato F7 en HTTP: la forma exacta de las cuatro rutas, los códigos y los
// textos, los permisos rol por rol (D8) y el rate limit propio de la baja (D5).

func apiWaitlist(e *httpkit.WaitlistHTTPEnv) string {
	return "/api/v1/salons/" + e.SalonID + "/waitlist"
}

func apiJoin(e *httpkit.WaitlistHTTPEnv) string {
	return "/api/v1/public/salons/" + e.Slug + "/waitlist"
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
	env := httpkit.NewWaitlistHTTPEnv(t)
	day := env.ClosedDay(t)

	w := httpkit.Do(env.Router, "POST", apiJoin(env), wlBody(env.ServiceID, day, "Lía", "lia@mail.com", "phone", "555"), "")
	if w.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Status    string `json:"status"`
		Date      string `json:"date"`
		ServiceID string `json:"service_id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	if created.Status != domain.WaitlistStatusWaiting || created.Date != day || created.ServiceID != env.ServiceID {
		t.Fatalf("respuesta: %s", w.Body.String())
	}
	// Sin id ni token en la respuesta (D4).
	for _, leak := range []string{"token", `"id"`} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("la respuesta no puede llevar %q: %s", leak, w.Body.String())
		}
	}
	// Repetido idéntico: 200 con la misma forma y sin escribir otra entrada.
	w = httpkit.Do(env.Router, "POST", apiJoin(env), wlBody(env.ServiceID, day, "Lía", "lia@mail.com"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("repetido: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	if created.Status != domain.WaitlistStatusWaiting || created.Date != day {
		t.Fatalf("repetido: %s", w.Body.String())
	}
	if n := env.EntryCount(); n != 1 {
		t.Fatalf("el repetido no crea otra entrada: %d", n)
	}

	// Un día CON horarios libres: 409 con el texto exacto y sin escribir nada.
	env.OpenSalon(t)
	other := env.OpenDay(t)
	w = httpkit.Do(env.Router, "POST", apiJoin(env), wlBody(env.ServiceID, other, "Ana", "ana@mail.com"), "")
	if w.Code != http.StatusConflict || w.Body.String() != `{"error":"Todavía hay horarios libres ese día"}` {
		t.Fatalf("con horarios: %d %s", w.Code, w.Body.String())
	}
	if n := env.EntryCount(); n != 1 {
		t.Fatalf("el 409 no puede escribir nada: %d", n)
	}
}

// D4: las validaciones son 400 con el texto del contrato (y un NUL es 400, no un
// 500) y no escriben nada. El pedido no rellena campos obligatorios en silencio.
func TestHTTP_waitlist_join_validaciones(t *testing.T) {
	env := httpkit.NewWaitlistHTTPEnv(t)
	day := env.ClosedDay(t)

	cases := []struct {
		name string
		body string
		code int
		msg  string
	}{
		{"sin servicio", `{"date":"` + day + `","name":"Lía","email":"a@mail.com"}`, 400, domain.MsgWaitlistJoinMissing},
		{"sin fecha", `{"service_id":"` + env.ServiceID + `","name":"Lía","email":"a@mail.com"}`, 400, domain.MsgWaitlistJoinMissing},
		{"sin nombre", `{"service_id":"` + env.ServiceID + `","date":"` + day + `","email":"a@mail.com"}`, 400, domain.MsgWaitlistJoinMissing},
		{"sin email", `{"service_id":"` + env.ServiceID + `","date":"` + day + `","name":"Lía"}`, 400, domain.MsgWaitlistJoinMissing},
		{"nombre con NUL", wlBody(env.ServiceID, day, `Lía\u0000`, "a@mail.com"), 400, domain.MsgWaitlistInvalidChars},
		{"email con NUL", wlBody(env.ServiceID, day, "Lía", `a@mail.com\u0000`), 400, domain.MsgWaitlistInvalidChars},
		{"email inválido", wlBody(env.ServiceID, day, "Lía", "no-es"), 400, "Email inválido"},
		// Verificador independiente (LOW): CR/LF o un espacio ADENTRO del email o un
		// salto de línea en el nombre o el teléfono se guardaban (el mailer los
		// rechazaba al enviar y la fila gastaba sus 3 intentos).
		{"email con salto de línea", wlBody(env.ServiceID, day, "Lía", `a@mail.com\nBcc: otro@mail.com`), 400, "Email inválido"},
		{"email con CRLF", wlBody(env.ServiceID, day, "Lía", `a@mail.com\r\nBcc: otro@mail.com`), 400, "Email inválido"},
		{"email con espacio adentro", wlBody(env.ServiceID, day, "Lía", "a@mail.com b@mail.com"), 400, "Email inválido"},
		{"nombre con salto de línea", wlBody(env.ServiceID, day, `Lía\nBcc: x`, "a@mail.com"), 400, domain.MsgWaitlistInvalidChars},
		{"teléfono con salto de línea", wlBody(env.ServiceID, day, "Lía", "a@mail.com", "phone", `55\n1234`), 400, domain.MsgWaitlistInvalidChars},
		{"servicio inexistente", wlBody("no-existe", day, "Lía", "a@mail.com"), 400, domain.MsgWaitlistServiceInvalid},
		{"profesional ajena", wlBody(env.ServiceID, day, "Lía", "a@mail.com", "professional_id", "u-nadie"), 400, domain.MsgWaitlistProfessionalInvalid},
		{"fecha mal formada", wlBody(env.ServiceID, "24/12/2026", "Lía", "a@mail.com"), 400, domain.MsgWaitlistDate},
		{"fecha lejana", wlBody(env.ServiceID, "2099-01-01", "Lía", "a@mail.com"), 400, domain.MsgWaitlistDateRange},
		{"json roto", `{"service_id":`, 400, "body inválido"},
		{"tipo equivocado", `{"service_id":123}`, 400, "body inválido"},
	}
	for _, c := range cases {
		w := httpkit.Do(env.Router, "POST", apiJoin(env), c.body, "")
		if w.Code != c.code {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		var out struct {
			Error string `json:"error"`
		}
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out.Error != c.msg {
			t.Fatalf("%s: esperaba %q, dio %q", c.name, c.msg, out.Error)
		}
		if n := env.EntryCount(); n != 0 {
			t.Fatalf("%s: un 400 no puede escribir nada (%d entradas)", c.name, n)
		}
	}
	// Salón inexistente: 404.
	w := httpkit.Do(env.Router, "POST", "/api/v1/public/salons/no-existe/waitlist", wlBody(env.ServiceID, day, "Lía", "a@mail.com"), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("slug inexistente: %d %s", w.Code, w.Body.String())
	}
}

// D5: la baja por enlace es idempotente, responde {"status":"left"} y un token
// que no sirve es el MISMO 404 {"error":"Aviso no encontrado"}.
func TestHTTP_waitlist_leave(t *testing.T) {
	env := httpkit.NewWaitlistHTTPEnv(t)
	token := env.SeedEntry(t, env.SalonID, env.ServiceID, env.ClosedDay(t), "Lía", "lia@mail.com")

	w := httpkit.Do(env.Router, "POST", "/api/v1/public/waitlist/leave", `{"token":"`+token+`"}`, "")
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"left"}` {
		t.Fatalf("baja: %d %s", w.Code, w.Body.String())
	}
	// Repetir: 200 igual.
	w = httpkit.Do(env.Router, "POST", "/api/v1/public/waitlist/leave", `{"token":"`+token+`"}`, "")
	if w.Code != http.StatusOK || w.Body.String() != `{"status":"left"}` {
		t.Fatalf("baja repetida: %d %s", w.Code, w.Body.String())
	}
	// Token desconocido, mal formado o ausente: el mismo 404 con el mismo texto.
	other, _ := domain.NewWaitlistToken()
	for _, body := range []string{`{"token":"` + other + `"}`, `{"token":"corto"}`, `{}`, `{"token":""}`} {
		w := httpkit.Do(env.Router, "POST", "/api/v1/public/waitlist/leave", body, "")
		if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"Aviso no encontrado"}` {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	// Cuerpo inválido: 400 "body inválido" (un token que no es string incluido).
	for _, body := range []string{`{"token":`, `{"token":123}`} {
		w := httpkit.Do(env.Router, "POST", "/api/v1/public/waitlist/leave", body, "")
		if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"body inválido"}` {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
}

// D5/D4: la baja tiene SU PROPIO cubo de rate limit (no comparte el de la
// reserva pública ni el de los enlaces) y el alta comparte el de la reserva.
func TestHTTP_waitlist_rate_limits(t *testing.T) {
	// Baja: cubo propio.
	env := httpkit.NewWaitlistHTTPEnv(t)
	env.Router = env.RouterWith(t, func(d *httpapi.Deps) {
		d.PublicWaitlistLeaveLimit = httpapi.RateLimitFor(2, time.Minute, "POST", "/api/v1/public/waitlist/")
	})
	for i := 0; i < 2; i++ {
		if w := httpkit.Do(env.Router, "POST", "/api/v1/public/waitlist/leave", `{"token":"corto"}`, ""); w.Code == http.StatusTooManyRequests {
			t.Fatalf("pedido %d no debía estar limitado", i+1)
		}
	}
	w := httpkit.Do(env.Router, "POST", "/api/v1/public/waitlist/leave", `{"token":"corto"}`, "")
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "demasiados intentos") {
		t.Fatalf("tercer pedido de baja: %d %s", w.Code, w.Body.String())
	}

	// Alta: comparte PublicBookingLimit (D4).
	env2 := httpkit.NewWaitlistHTTPEnv(t)
	env2.Router = env2.RouterWith(t, func(d *httpapi.Deps) {
		d.PublicBookingLimit = httpapi.RateLimitFor(2, time.Minute, "POST", "/api/v1/public/salons/")
	})
	day := env2.ClosedDay(t)
	for i := 0; i < 2; i++ {
		if w := httpkit.Do(env2.Router, "POST", apiJoin(env2), wlBody(env2.ServiceID, day, "Lía", "a"+string(rune('a'+i))+"@mail.com"), ""); w.Code == http.StatusTooManyRequests {
			t.Fatalf("alta %d no debía estar limitada", i+1)
		}
	}
	if w := httpkit.Do(env2.Router, "POST", apiJoin(env2), wlBody(env2.ServiceID, day, "Ana", "z@mail.com"), ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("tercera alta: %d %s", w.Code, w.Body.String())
	}
}

// D8: GET/DELETE del personal. Owner, administración y recepción listan y dan de
// baja; profesional y cliente 403; sin token 401; otro salón 403; una entrada de
// otro salón es 404. La forma del JSON es la del contrato.
func TestHTTP_waitlist_staff(t *testing.T) {
	env := httpkit.NewWaitlistHTTPEnv(t)
	day := env.ClosedDay(t)
	entryID := env.SeedEntryWithID(t, env.SalonID, env.ServiceID, "", day, "Lía", "lia@mail.com", "555")
	env.MarkNotified(t, entryID)
	// Una entrada de ayer no se lista (día local >= hoy).
	yesterday := time.Now().In(env.Loc).AddDate(0, 0, -1).Format("2006-01-02")
	env.SeedEntryWithID(t, env.SalonID, env.ServiceID, "", yesterday, "Vieja", "vieja@mail.com", "")

	for _, tok := range []string{env.Owner, env.Admin, env.Reception} {
		w := httpkit.Do(env.Router, "GET", apiWaitlist(env), "", tok)
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
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if len(out.Entries) != 1 {
			t.Fatalf("esperaba 1 entrada, dio %d (%s)", len(out.Entries), w.Body.String())
		}
		e := out.Entries[0]
		if e.ID != entryID || e.ServiceID != env.ServiceID || e.ServiceName != "Corte" ||
			e.Date != day || e.Name != "Lía" || e.Email != "lia@mail.com" ||
			e.Status != domain.WaitlistStatusNotified || e.NotifiedAt == nil ||
			e.ProfessionalID != nil || e.ProfessionalName != nil ||
			e.Phone == nil || *e.Phone != "555" || e.CreatedAt == "" {
			t.Fatalf("forma de la entrada: %s", w.Body.String())
		}
	}
	// Profesional y cliente: 403 en las dos.
	for _, tok := range []string{env.Professional, env.Client} {
		if w := httpkit.Do(env.Router, "GET", apiWaitlist(env), "", tok); w.Code != http.StatusForbidden {
			t.Fatalf("profesional/cliente lista: %d", w.Code)
		}
		if w := httpkit.Do(env.Router, "DELETE", apiWaitlist(env)+"/"+entryID, "", tok); w.Code != http.StatusForbidden {
			t.Fatalf("profesional/cliente borra: %d", w.Code)
		}
	}
	// Sin token: 401.
	if w := httpkit.Do(env.Router, "GET", apiWaitlist(env), "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token lista: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "DELETE", apiWaitlist(env)+"/"+entryID, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token borra: %d", w.Code)
	}
	// Otro salón: quien no es miembro no entra (403 con el texto del borde).
	otherTok, _ := env.Register(t, "otro@mail.com")
	otherSalon := env.Salon(t, otherTok, "otro-salon")
	w := httpkit.Do(env.Router, "GET", apiWaitlist(env), "", otherTok)
	if w.Code != http.StatusForbidden || w.Body.String() != `{"error":"sin acceso al salón"}` {
		t.Fatalf("otro salón: %d %s", w.Code, w.Body.String())
	}
	// Una entrada de otro salón es 404 con el texto exacto.
	foreign := env.SeedEntryWithID(t, otherSalon, env.ServiceID, "", day, "Ajena", "ajena@mail.com", "")
	w = httpkit.Do(env.Router, "DELETE", apiWaitlist(env)+"/"+foreign, "", env.Owner)
	if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"Aviso no encontrado"}` {
		t.Fatalf("entrada de otro salón: %d %s", w.Code, w.Body.String())
	}
	// Quitar la propia: 204 y después 404 (ya dada de baja).
	if w := httpkit.Do(env.Router, "DELETE", apiWaitlist(env)+"/"+entryID, "", env.Owner); w.Code != http.StatusNoContent {
		t.Fatalf("quitar: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "DELETE", apiWaitlist(env)+"/"+entryID, "", env.Owner)
	if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"Aviso no encontrado"}` {
		t.Fatalf("quitar dos veces: %d %s", w.Code, w.Body.String())
	}
	// Recepción también puede quitar (D8).
	second := env.SeedEntryWithID(t, env.SalonID, env.ServiceID, "", day, "Ana", "ana@mail.com", "")
	if w := httpkit.Do(env.Router, "DELETE", apiWaitlist(env)+"/"+second, "", env.Reception); w.Code != http.StatusNoContent {
		t.Fatalf("recepción quita: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", apiWaitlist(env), "", env.Owner); w.Code != http.StatusOK {
		t.Fatalf("listar después: %d", w.Code)
	} else {
		var out map[string]json.RawMessage
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if string(out["entries"]) != "[]" {
			t.Fatalf("sin entradas la lista es [] y no null: %s", w.Body.String())
		}
	}
}
