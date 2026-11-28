package httpapi_test

import (
	"fmt"
	"net/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"
)

var _ ports.BookingTxRunner = (*httpkit.BookingTx)(nil)

// Formas del contrato F2d3 (el frontend codifica contra esto).
type candidateBodyJSON struct {
	ProfessionalID string `json:"professional_id"`
	Name           string `json:"name"`
	Available      bool   `json:"available"`
	Reason         string `json:"reason"`
}

type absenceBookingBodyJSON struct {
	ID          string              `json:"id"`
	ClientName  string              `json:"client_name"`
	StartsAt    string              `json:"starts_at"`
	EndsAt      string              `json:"ends_at"`
	ServiceID   *string             `json:"service_id"`
	ServiceName string              `json:"service_name"`
	Candidates  []candidateBodyJSON `json:"candidates"`
}

type absenceBodyJSON struct {
	ID       string                   `json:"id"`
	UserID   string                   `json:"user_id"`
	UserName string                   `json:"user_name"`
	StartsAt string                   `json:"starts_at"`
	EndsAt   string                   `json:"ends_at"`
	Reason   string                   `json:"reason"`
	Bookings []absenceBookingBodyJSON `json:"bookings"`
}

type absencesListJSON struct {
	Absences []absenceBodyJSON `json:"absences"`
}

type reassignedBookingJSON struct {
	ID         string `json:"id"`
	ClientName string `json:"client_name"`
	StartsAt   string `json:"starts_at"`
}

type reassignBodyJSON struct {
	Reassigned int                     `json:"reassigned"`
	Bookings   []reassignedBookingJSON `json:"bookings"`
}

type conflictBody struct {
	Error    string                  `json:"error"`
	Bookings []reassignedBookingJSON `json:"bookings"`
}

// coverageSalon deja un salón con dueño, dos profesionales, recepción y un
// servicio. Devuelve el id del salón, el id del servicio y los ids de la gente.
type coverageEnv struct {
	Env      *httpkit.Env
	SalonID  string
	service  string
	ownerTok string
	OwnerID  string
	anaTok   string
	anaID    string
	beaTok   string
	beaID    string
	recTok   string
	recID    string
}

func newCoverageEnv(t *testing.T, slug string) *coverageEnv {
	t.Helper()
	env := httpkit.NewPOSEnv()
	c := &coverageEnv{Env: env}
	c.ownerTok, c.OwnerID = env.Register(t, "owner@"+slug+".com")
	c.SalonID = env.Salon(t, c.ownerTok, slug)
	c.service = env.SeedService(t, c.ownerTok, c.SalonID, "Corte", 150000)
	httpkit.SalonSettings(env, c.SalonID)
	// Horario ancho para que el turno del test caiga dentro de la ventana del
	// salón sin depender de la hora del reloj: 00:00 a 23:59, todos los días.
	env.Settings.BySalon[c.SalonID] = domain.SalonSettings{
		SalonID: c.SalonID, Timezone: "America/Mexico_City",
		OpenTime: "00:00", CloseTime: "23:59", SlotStepMin: 30,
	}

	_, c.anaID = env.Register(t, "ana@"+slug+".com")
	_, c.beaID = env.Register(t, "bea@"+slug+".com")
	_, c.recID = env.Register(t, "rec@"+slug+".com")
	addMember(t, env, c.SalonID, c.ownerTok, "ana@"+slug+".com", "professional")
	addMember(t, env, c.SalonID, c.ownerTok, "bea@"+slug+".com", "professional")
	addMember(t, env, c.SalonID, c.ownerTok, "rec@"+slug+".com", "reception")
	// Los tokens de las profesionales, para los casos de permisos.
	c.anaTok = loginToken(t, env, "ana@"+slug+".com")
	c.beaTok = loginToken(t, env, "bea@"+slug+".com")
	c.recTok = loginToken(t, env, "rec@"+slug+".com")
	return c
}

// loginToken devuelve el token de una cuenta ya registrada.
func loginToken(t *testing.T, env *httpkit.Env, email string) string {
	t.Helper()
	w := httpkit.Do(env.Router, "POST", "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"secreta123"}`, email), "")
	var out struct {
		Token string `json:"token"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatalf("login de %s: %d %s", email, w.Code, w.Body.String())
	}
	return out.Token
}

// seedCoveredBooking deja un turno reservado a nombre de una profesional.
func (c *coverageEnv) seedCoveredBooking(t *testing.T, id, userID string, start time.Time, serviceID string) domain.Booking {
	t.Helper()
	b, err := domain.NewBooking(c.SalonID, userID, "Lía", "555", start, start.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno %s: %v", id, err)
	}
	b.ID = id
	b.ServiceID = serviceID
	if err := c.Env.Bookings.Save(b); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}
	return b
}

// seedAbsence crea la ausencia por el endpoint real (F2d2) y devuelve su id.
func (c *coverageEnv) seedAbsence(t *testing.T, userID string, from, to time.Time, reason string) string {
	t.Helper()
	w := httpkit.Do(c.Env.Router, "POST", "/api/v1/salons/"+c.SalonID+"/members/"+userID+"/time-off",
		fmt.Sprintf(`{"starts_at":%q,"ends_at":%q,"reason":%q}`, rfc3339(from), rfc3339(to), reason),
		c.ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta de ausencia: %d %s", w.Code, w.Body.String())
	}
	var out timeOffCreateJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	return out.TimeOff.ID
}

func candidateOf(t *testing.T, b absenceBookingBodyJSON, professionalID string) candidateBodyJSON {
	t.Helper()
	for _, c := range b.Candidates {
		if c.ProfessionalID == professionalID {
			return c
		}
	}
	t.Fatalf("la persona %s no está en la lista de candidatas: %+v", professionalID, b.Candidates)
	return candidateBodyJSON{}
}

// S-F2d3-2/4: listar (con candidatas y motivos), reasignar todo y ver el aviso
// interno, el correo y el turno movido con la MISMA hora.
func TestHTTP_absences_flow(t *testing.T) {
	c := newCoverageEnv(t, "cobertura")
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	booking := c.seedCoveredBooking(t, "b-cover-1", c.anaID, start, c.service)
	if err := c.Env.BookingEmails.Save(c.SalonID, booking.ID, "lia@mail.com"); err != nil {
		t.Fatalf("email del turno: %v", err)
	}
	absenceID := c.seedAbsence(t, c.anaID, start.Add(-time.Hour), start.Add(2*time.Hour), "Vacaciones")

	listURL := "/api/v1/salons/" + c.SalonID + "/absences?from=" +
		rfc3339(start.Add(-time.Hour)) + "&to=" + rfc3339(start.Add(2*time.Hour))
	w := httpkit.Do(c.Env.Router, "GET", listURL, "", c.ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar ausencias: %d %s", w.Code, w.Body.String())
	}
	var list absencesListJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Absences) != 1 {
		t.Fatalf("esperaba una ausencia: %+v", list.Absences)
	}
	got := list.Absences[0]
	if got.ID != absenceID || got.UserID != c.anaID || got.Reason != "Vacaciones" {
		t.Fatalf("ausencia: %+v", got)
	}
	if len(got.Bookings) != 1 || got.Bookings[0].ID != booking.ID {
		t.Fatalf("turnos de la ausencia: %+v", got.Bookings)
	}
	if got.Bookings[0].ServiceID == nil || *got.Bookings[0].ServiceID != c.service ||
		got.Bookings[0].ServiceName != "Corte" {
		t.Fatalf("servicio del turno: %+v", got.Bookings[0])
	}
	// La ausente no puede cubrir su propio turno; su compañera sí.
	if cand := candidateOf(t, got.Bookings[0], c.anaID); cand.Available || cand.Reason != "Esa persona no está de turno ese día" {
		t.Fatalf("candidata ausente: %+v", cand)
	}
	if cand := candidateOf(t, got.Bookings[0], c.beaID); !cand.Available || cand.Reason != "" {
		t.Fatalf("candidata libre: %+v", cand)
	}
	// Recepción no es una candidata: no atiende clientes y no está en la lista
	// (el POST que la nombra lo dice con el mensaje del contrato).
	for _, cand := range got.Bookings[0].Candidates {
		if cand.ProfessionalID == c.recID {
			t.Fatalf("recepción no debería ser candidata: %+v", cand)
		}
	}

	// Reasignar todo a la compañera.
	w = httpkit.Do(c.Env.Router, "POST", "/api/v1/salons/"+c.SalonID+"/absences/"+absenceID+"/reassign",
		fmt.Sprintf(`{"professional_id":%q}`, c.beaID), c.ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("reasignar: %d %s", w.Code, w.Body.String())
	}
	var res reassignBodyJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &res)
	if res.Reassigned != 1 || len(res.Bookings) != 1 || res.Bookings[0].ID != booking.ID {
		t.Fatalf("respuesta de la reasignación: %+v", res)
	}
	// La clienta conserva el horario y el turno cambió de profesional.
	if res.Bookings[0].StartsAt != rfc3339(start) {
		t.Fatalf("el horario se movió: %+v", res.Bookings[0])
	}
	stored, err := c.Env.Bookings.FindByID(c.SalonID, booking.ID)
	if err != nil || stored.ProfessionalID != c.beaID {
		t.Fatalf("turno guardado: %+v %v", stored, err)
	}
	// Aviso interno y correo a la clienta (T12).
	if len(c.Env.Notifications.All) == 0 {
		t.Fatal("falta el aviso interno de la reasignación")
	}
	last := c.Env.Notifications.All[len(c.Env.Notifications.All)-1]
	if last.Title != "Turno reasignado" || !strings.Contains(last.Body, c.beaID) {
		t.Fatalf("aviso interno: %+v", last)
	}
	sent := c.Env.Mailer.WaitCount(t, 1)
	if sent[0].To != "lia@mail.com" || !strings.Contains(sent[0].Text, "se mantiene") {
		t.Fatalf("correo de la reasignación: %+v", sent[0])
	}
}

// S-F2d3-3: los mensajes EXACTOS del destino y el 409 del doble turno con el
// detalle de los turnos que no se movieron.
func TestHTTP_absences_reassign_errors(t *testing.T) {
	c := newCoverageEnv(t, "cobertura-errores")
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	booking := c.seedCoveredBooking(t, "b-err-1", c.anaID, start, c.service)
	absenceID := c.seedAbsence(t, c.anaID, start.Add(-time.Hour), start.Add(2*time.Hour), "")
	reassignURL := "/api/v1/salons/" + c.SalonID + "/absences/" + absenceID + "/reassign"

	// Recepción no atiende clientes.
	w := httpkit.Do(c.Env.Router, "POST", reassignURL, fmt.Sprintf(`{"professional_id":%q}`, c.recID), c.ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Esa persona no atiende clientes") {
		t.Fatalf("recepción: %d %s", w.Code, w.Body.String())
	}
	// Un candidato de otro salón: para ESTE salón no atiende.
	_, otroProID := c.Env.Register(t, "otro-pro@mail.com")
	otroSalon := c.Env.Salon(t, c.ownerTok, "cobertura-otro")
	addMember(t, c.Env, otroSalon, c.ownerTok, "otro-pro@mail.com", "professional")
	w = httpkit.Do(c.Env.Router, "POST", reassignURL, fmt.Sprintf(`{"professional_id":%q}`, otroProID), c.ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Esa persona no atiende clientes") {
		t.Fatalf("candidato de otro salón: %d %s", w.Code, w.Body.String())
	}
	// De descanso ese día (F2d1): la compañera solo trabaja otro día de la semana.
	salonLoc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	weekday := domain.IsoWeekday(start, salonLoc)
	other := weekday%7 + 1
	if c.Env.Weekly.MemberSeg == nil {
		c.Env.Weekly.MemberSeg = map[string][]domain.HoursSegment{}
	}
	c.Env.Weekly.MemberSeg[c.SalonID+"|"+c.beaID] = []domain.HoursSegment{
		{Weekday: other, Start: "00:00", End: "23:59"},
	}
	w = httpkit.Do(c.Env.Router, "POST", reassignURL, fmt.Sprintf(`{"professional_id":%q}`, c.beaID), c.ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Esa persona no está de turno ese día") {
		t.Fatalf("de descanso: %d %s", w.Code, w.Body.String())
	}
	// Sin horario propio vuelve a poder cubrir.
	delete(c.Env.Weekly.MemberSeg, c.SalonID+"|"+c.beaID)

	// El turno de otra profesional no es de esa ausencia.
	otroTurno := c.seedCoveredBooking(t, "b-err-otro", c.beaID, start.Add(time.Hour), c.service)
	w = httpkit.Do(c.Env.Router, "POST", reassignURL,
		fmt.Sprintf(`{"professional_id":%q,"booking_ids":[%q]}`, c.beaID, otroTurno.ID), c.ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Ese turno no es de esa ausencia") {
		t.Fatalf("turno de otra persona: %d %s", w.Code, w.Body.String())
	}

	// Doble turno: la compañera ya tiene ese horario -> 409 con el detalle y nada
	// movido.
	c.seedCoveredBooking(t, "b-err-busy", c.beaID, start, c.service)
	w = httpkit.Do(c.Env.Router, "POST", reassignURL, fmt.Sprintf(`{"professional_id":%q}`, c.beaID), c.ownerTok)
	if w.Code != http.StatusConflict {
		t.Fatalf("doble turno: %d %s", w.Code, w.Body.String())
	}
	var conflict conflictBody
	httpkit.MustJSON(t, w.Body.Bytes(), &conflict)
	if !strings.Contains(conflict.Error, "Ya tiene un turno a esa hora") || len(conflict.Bookings) != 1 || conflict.Bookings[0].ID != booking.ID {
		t.Fatalf("conflicto: %+v", conflict)
	}
	if stored, _ := c.Env.Bookings.FindByID(c.SalonID, booking.ID); stored.ProfessionalID != c.anaID {
		t.Fatalf("el turno se movió igual: %+v", stored)
	}

	// Permisos: una profesional no administra la cobertura, recepción tampoco.
	for _, tok := range []string{c.anaTok, c.beaTok, c.recTok} {
		if w := httpkit.Do(c.Env.Router, "POST", reassignURL, fmt.Sprintf(`{"professional_id":%q}`, c.beaID), tok); w.Code != http.StatusForbidden {
			t.Fatalf("un miembro sin permiso reasigna: %d %s", w.Code, w.Body.String())
		}
	}
	// Sin token: 401.
	if w := httpkit.Do(c.Env.Router, "POST", reassignURL, `{"professional_id":"x"}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}
	// Una ausencia de otro salón no existe acá (404).
	otra := c.seedAbsenceForOtherSalon(t, otroSalon)
	w = httpkit.Do(c.Env.Router, "POST", "/api/v1/salons/"+c.SalonID+"/absences/"+otra+"/reassign",
		fmt.Sprintf(`{"professional_id":%q}`, c.beaID), c.ownerTok)
	if w.Code != http.StatusNotFound {
		t.Fatalf("ausencia de otro salón: %d %s", w.Code, w.Body.String())
	}
	// El listado también es solo de dueño/administración.
	listURL := "/api/v1/salons/" + c.SalonID + "/absences?from=" +
		rfc3339(start.Add(-time.Hour)) + "&to=" + rfc3339(start.Add(2*time.Hour))
	if w := httpkit.Do(c.Env.Router, "GET", listURL, "", c.anaTok); w.Code != http.StatusForbidden {
		t.Fatalf("listado sin permiso: %d %s", w.Code, w.Body.String())
	}
	// Rango inválido o demasiado ancho: 400 con los textos de F2d2.
	if w := httpkit.Do(c.Env.Router, "GET", "/api/v1/salons/"+c.SalonID+"/absences?from="+rfc3339(start), "", c.ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("sin to: %d %s", w.Code, w.Body.String())
	}
	longURL := "/api/v1/salons/" + c.SalonID + "/absences?from=" + rfc3339(start) + "&to=" + rfc3339(start.Add(93*24*time.Hour))
	if w := httpkit.Do(c.Env.Router, "GET", longURL, "", c.ownerTok); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "El rango no puede superar los 92 días") {
		t.Fatalf("rango largo: %d %s", w.Code, w.Body.String())
	}
}

// seedAbsenceForOtherSalon crea una ausencia en OTRO salón y devuelve su id.
func (c *coverageEnv) seedAbsenceForOtherSalon(t *testing.T, otroSalonID string) string {
	t.Helper()
	off := domain.TimeOff{
		ID: "off-otro-" + otroSalonID, SalonID: otroSalonID, UserID: c.OwnerID,
		StartsAt: time.Now().UTC().Add(72 * time.Hour), EndsAt: time.Now().UTC().Add(96 * time.Hour),
	}
	// La dueña del salón propio no pertenece al otro: se guarda directo en el
	// fake para probar el scope de la ruta.
	if err := c.Env.TimeOff.Save(off); err != nil {
		t.Fatalf("ausencia de otro salón: %v", err)
	}
	return off.ID
}

// S-F2d3-2: un turno suelto se reasigna o se cancela con motivo.
func TestHTTP_bookings_reassign_and_cancel(t *testing.T) {
	c := newCoverageEnv(t, "cobertura-suelto")
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	booking := c.seedCoveredBooking(t, "b-suelto-1", c.anaID, start, "")
	if err := c.Env.BookingEmails.Save(c.SalonID, booking.ID, "lia@mail.com"); err != nil {
		t.Fatalf("email: %v", err)
	}
	url := "/api/v1/salons/" + c.SalonID + "/bookings/" + booking.ID + "/reassign"

	// Reasignar: el turno no tiene servicio, así que cualquier reservable sirve.
	w := httpkit.Do(c.Env.Router, "POST", url, fmt.Sprintf(`{"professional_id":%q}`, c.recID), c.ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Esa persona no atiende clientes") {
		t.Fatalf("recepción: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(c.Env.Router, "POST", url, fmt.Sprintf(`{"professional_id":%q}`, c.beaID), c.ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("reasignar suelto: %d %s", w.Code, w.Body.String())
	}
	var res reassignBodyJSON
	httpkit.MustJSON(t, w.Body.Bytes(), &res)
	if res.Reassigned != 1 || res.Bookings[0].ID != booking.ID {
		t.Fatalf("respuesta: %+v", res)
	}
	if stored, _ := c.Env.Bookings.FindByID(c.SalonID, booking.ID); stored.ProfessionalID != c.beaID ||
		!stored.StartsAt.Equal(start) {
		t.Fatalf("turno: %+v", stored)
	}

	// Cancelar: 200, estado cancelado y el motivo en el aviso interno.
	second := c.seedCoveredBooking(t, "b-suelto-2", c.anaID, start.Add(2*time.Hour), "")
	if err := c.Env.BookingEmails.Save(c.SalonID, second.ID, "lia@mail.com"); err != nil {
		t.Fatalf("email: %v", err)
	}
	w = httpkit.Do(c.Env.Router, "POST", "/api/v1/salons/"+c.SalonID+"/bookings/"+second.ID+"/reassign",
		`{"cancel":true,"reason":"  se enfermó  "}`, c.ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("cancelar: %d %s", w.Code, w.Body.String())
	}
	var cancelled struct {
		Cancelled bool           `json:"cancelled"`
		Booking   map[string]any `json:"booking"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &cancelled)
	if !cancelled.Cancelled || cancelled.Booking["status"] != "cancelled" {
		t.Fatalf("cancelación: %+v", cancelled)
	}
	last := c.Env.Notifications.All[len(c.Env.Notifications.All)-1]
	if last.Title != "Turno cancelado" || !strings.Contains(last.Body, "se enfermó") {
		t.Fatalf("el motivo tiene que quedar registrado: %+v", last)
	}
	// Reasignar y cancelar mandan un correo CADA UNO desde su goroutine: el orden
	// de llegada no es el de los pedidos, así que el de la cancelación se busca
	// por destinataria y asunto (con una espera corta) y no por posición.
	cancelMail := waitCoverMail(t, c.Env.Mailer, "lia@mail.com", "Turno cancelado")
	if !strings.Contains(cancelMail.Text, "cancelado") {
		t.Fatalf("correo de la cancelación: %+v", cancelMail)
	}

	// Un turno ya cancelado no se vuelve a cancelar (409) ni se reasigna.
	w = httpkit.Do(c.Env.Router, "POST", "/api/v1/salons/"+c.SalonID+"/bookings/"+second.ID+"/reassign",
		`{"cancel":true}`, c.ownerTok)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "El turno ya no está reservado") {
		t.Fatalf("doble cancelación: %d %s", w.Code, w.Body.String())
	}
	// Sin profesional ni cancelación: no hay a quién reasignar.
	w = httpkit.Do(c.Env.Router, "POST", url, `{}`, c.ownerTok)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "No hay a quién reasignar") {
		t.Fatalf("pedido vacío: %d %s", w.Code, w.Body.String())
	}
	// Un turno de otro salón no existe (404).
	if w := httpkit.Do(c.Env.Router, "POST", "/api/v1/salons/cobertura-suelto-otro/bookings/"+booking.ID+"/reassign",
		fmt.Sprintf(`{"professional_id":%q}`, c.beaID), c.ownerTok); w.Code != http.StatusForbidden {
		t.Fatalf("salón ajeno: %d %s", w.Code, w.Body.String())
	}
	// Permisos: la profesional no.
	if w := httpkit.Do(c.Env.Router, "POST", url, fmt.Sprintf(`{"professional_id":%q}`, c.beaID), c.anaTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional reasigna: %d %s", w.Code, w.Body.String())
	}
}

// waitCoverMail espera hasta 2 s (pasos de 10 ms) a que el Mailer tenga el
// correo del cambio de turno para `to` con `subject` en el asunto, y lo
// devuelve. Es la forma estable de leerlos: la cobertura manda un correo por
// turno en segundo plano (F2d3), así que la lista no llega en orden.
func waitCoverMail(t *testing.T, m *httpkit.Mailer, to, subject string) ports.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.Mu.Lock()
		sent := append([]ports.Message(nil), m.Sent...)
		m.Mu.Unlock()
		for _, msg := range sent {
			if msg.To == to && strings.Contains(msg.Subject, subject) {
				return msg
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("esperaba en 2 s un correo %q para %s; llegaron %d: %+v", subject, to, len(sent), sent)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// staffBookingBody es la forma del turno en los endpoints del personal: el
// calendario necesita saber quién tenía el turno (S-F2d3-6).
type staffBookingBody struct {
	ID             string  `json:"id"`
	ProfessionalID string  `json:"professional_id"`
	Status         string  `json:"status"`
	ServiceID      *string `json:"service_id"`
	CoveredFrom    *string `json:"covered_from"`
}

// S-F2d3-6: `covered_from` viaja en el JSON de turnos del personal, como texto o
// null, y el PATCH clásico no lo borra. El alta de staff lo devuelve en null.
func TestHTTP_staff_bookings_covered_from(t *testing.T) {
	c := newCoverageEnv(t, "cobertura-campo")
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)
	// El turno de Bea lo tenía Ana: lo cubre otra.
	covered := c.seedCoveredBooking(t, "b-cover-ana", c.beaID, start, c.service)
	covered.CoveredFrom = c.anaID
	if err := c.Env.Bookings.Update(covered); err != nil {
		t.Fatalf("guardar covered_from: %v", err)
	}
	// Otro turno sin cobertura: el campo viaja en null.
	c.seedCoveredBooking(t, "b-sin-cover", c.beaID, start.Add(2*time.Hour), c.service)

	listURL := "/api/v1/salons/" + c.SalonID + "/bookings?from=" + rfc3339(start) +
		"&to=" + rfc3339(start.Add(4*time.Hour))
	w := httpkit.Do(c.Env.Router, "GET", listURL, "", c.ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar turnos: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Bookings []staffBookingBody `json:"bookings"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	got := map[string]staffBookingBody{}
	for _, b := range list.Bookings {
		got[b.ID] = b
	}
	if b, ok := got[covered.ID]; !ok || b.CoveredFrom == nil || *b.CoveredFrom != c.anaID {
		t.Fatalf("covered_from del turno cubierto: %+v (body %s)", got[covered.ID], w.Body.String())
	}
	if b, ok := got["b-sin-cover"]; !ok || b.CoveredFrom != nil {
		t.Fatalf("un turno sin cobertura lleva null: %+v", got["b-sin-cover"])
	}

	// El PATCH de estado devuelve el turno del personal y conserva el campo.
	w = httpkit.Do(c.Env.Router, "PATCH", "/api/v1/salons/"+c.SalonID+"/bookings/"+covered.ID,
		`{"status":"done"}`, c.ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("cambiar estado: %d %s", w.Code, w.Body.String())
	}
	var patched struct {
		Booking staffBookingBody `json:"booking"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &patched)
	if patched.Booking.CoveredFrom == nil || *patched.Booking.CoveredFrom != c.anaID {
		t.Fatalf("el PATCH no toca covered_from: %s", w.Body.String())
	}

	// El alta de staff: nace sin cobertura, y lo dice con null.
	w = httpkit.Do(c.Env.Router, "POST", "/api/v1/salons/"+c.SalonID+"/bookings",
		fmt.Sprintf(`{"professional_id":%q,"client_name":"Lía","starts_at":%q,"ends_at":%q}`,
			c.beaID, rfc3339(start.Add(5*time.Hour)), rfc3339(start.Add(5*time.Hour+45*time.Minute))),
		c.ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta de staff: %d %s", w.Code, w.Body.String())
	}
	var created staffBookingBody
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	if created.CoveredFrom != nil {
		t.Fatalf("un turno nuevo no tiene cobertura: %s", w.Body.String())
	}
	if created.ID == "" || created.Status != "booked" {
		t.Fatalf("el alta sigue devolviendo el turno: %s", w.Body.String())
	}
}

// S-F2d3-6: `covered_from` NO sale en la reserva pública ni en el payload del
// websocket (el calendario relee la agenda del personal).
func TestHTTP_public_and_events_have_no_covered_from(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "sin-campo-publico")
	httpkit.SalonSettings(env, salonID)
	svc := env.SeedService(t, ownerTok, salonID, "Corte", 150000)
	_, profID := env.Register(t, "profe@mail.com")
	httpkit.SeedProfessional(env, salonID, profID)
	_, prof2ID := env.Register(t, "profe2@mail.com")
	httpkit.SeedProfessional(env, salonID, prof2ID)
	env.Professionals.Assign(salonID, svc, profID, prof2ID)

	w := httpkit.Do(env.Router, "POST", "/api/v1/public/salons/sin-campo-publico/bookings",
		`{"service_id":"`+svc+`","client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T15:00:00Z"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva pública: %d %s", w.Code, w.Body.String())
	}
	var public struct {
		Booking map[string]any `json:"booking"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &public)
	if _, ok := public.Booking["covered_from"]; ok {
		t.Fatalf("la reserva pública no expone covered_from: %s", w.Body.String())
	}
	if public.Booking["id"] == nil {
		t.Fatalf("la reserva pública sigue devolviendo el turno: %s", w.Body.String())
	}

	// Reasignar ese turno: el evento va al websocket y tampoco lleva el campo.
	bookingID, _ := public.Booking["id"].(string)
	if w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/bookings/"+bookingID+"/reassign",
		fmt.Sprintf(`{"professional_id":%q}`, profID), ownerTok); w.Code != http.StatusOK {
		t.Fatalf("reasignar: %d %s", w.Code, w.Body.String())
	}
	var updated bool
	for _, ev := range env.Events.All {
		if ev.Type != "booking.updated" {
			continue
		}
		data, ok := ev.Data.(map[string]any)
		if !ok {
			t.Fatalf("el payload del evento no es un mapa: %T", ev.Data)
		}
		updated = true
		if _, ok := data["covered_from"]; ok {
			t.Fatalf("el payload del websocket no expone covered_from: %+v", data)
		}
		if data["id"] != bookingID {
			t.Fatalf("el payload sigue siendo el del turno: %+v", data)
		}
	}
	if !updated {
		t.Fatal("falta el booking.updated de la reasignación")
	}
}
