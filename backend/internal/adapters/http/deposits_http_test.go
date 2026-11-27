package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// mDepositCreator es el DepositCreator de la reserva pública (F11, D8): crea una
// sena con la política de 30% o falla si el test lo pide.
type mDepositCreator struct {
	Deposits *httpkit.Deposits
	percent  int
	fail     error
	priceCap int
}

func (f *mDepositCreator) CreateForBooking(o domain.DepositOrder) (*domain.BookingDeposit, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	if existing, err := f.Deposits.FindByBooking(o.SalonID, o.BookingID); err == nil {
		return &existing, nil
	}
	amount := domain.DepositAmountCents(o.PriceCents, f.percent)
	if amount < domain.MinDepositAmountCents {
		return nil, nil
	}
	chargeID, err := domain.NewDepositChargeID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	d := f.Deposits.Seed(domain.BookingDeposit{
		SalonID: o.SalonID, BookingID: o.BookingID, AmountCents: amount, Percent: f.percent,
		Status: domain.DepositPending, ChargeID: chargeID, ClientOpID: o.ClientOpID,
		CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	})
	return &d, nil
}

var _ bookings.DepositCreator = (*mDepositCreator)(nil)

func depJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("json: %v (%s)", err, body)
	}
	return out
}

// --- Política (D3) ----------------------------------------------------------

func TestDepositPolicy_GET_neverSaved(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	path := "/api/v1/salons/" + w.Salon + "/deposit-policy"
	for _, tok := range []string{w.Owner, w.Admin, w.Recep, w.Prof} {
		r := httpkit.Do(w.Env.Router, "GET", path, "", tok)
		if r.Code != http.StatusOK {
			t.Fatalf("GET política (%s): %d %s", tok, r.Code, r.Body.String())
		}
		got := depJSON(t, r.Body.Bytes())
		if got["enabled"] != false || got["percent"] != nil || got["hold_minutes"] != nil || got["refund_hours"] != nil {
			t.Fatalf("política nunca guardada: %v", got)
		}
	}
	// Miembro dado de baja y otro salón: sin acceso.
	if r := httpkit.Do(w.Env.Router, "GET", path, "", w.Removed); r.Code != http.StatusForbidden {
		t.Fatalf("dado de baja: %d", r.Code)
	}
	if r := httpkit.Do(w.Env.Router, "GET", path, "", w.Outsider); r.Code != http.StatusForbidden {
		t.Fatalf("otro salón: %d", r.Code)
	}
}

func TestDepositPolicy_PUT_permisoAntesDelCuerpo(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	path := "/api/v1/salons/" + w.Salon + "/deposit-policy"
	// Profesional y recepción: 403 aunque el cuerpo sea basura.
	for _, tok := range []string{w.Prof, w.Recep} {
		r := httpkit.Do(w.Env.Router, "PUT", path, `{`, tok)
		if r.Code != http.StatusForbidden {
			t.Fatalf("PUT sin permiso con cuerpo roto: %d %s", r.Code, r.Body.String())
		}
	}
	if len(w.Policies.Rows) != 0 {
		t.Fatalf("un 403 no puede guardar nada: %v", w.Policies.Rows)
	}
}

func TestDepositPolicy_PUT_validaciones(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	path := "/api/v1/salons/" + w.Salon + "/deposit-policy"
	cases := []struct {
		body string
		want string
	}{
		{`{}`, domain.MsgDepositPolicyMissing},
		{`{"enabled":true,"percent":30,"hold_minutes":30}`, domain.MsgDepositPolicyMissing},
		{`{"enabled":true,"percent":9,"hold_minutes":30,"refund_hours":24}`, domain.MsgDepositPercentRange},
		{`{"enabled":true,"percent":101,"hold_minutes":30,"refund_hours":24}`, domain.MsgDepositPercentRange},
		{`{"enabled":true,"percent":30,"hold_minutes":4,"refund_hours":24}`, domain.MsgDepositHoldRange},
		{`{"enabled":true,"percent":30,"hold_minutes":121,"refund_hours":24}`, domain.MsgDepositHoldRange},
		{`{"enabled":true,"percent":30,"hold_minutes":30,"refund_hours":169}`, domain.MsgDepositRefundRange},
		{`{"enabled":true,"percent":30,"hold_minutes":30,"refund_hours":-1}`, domain.MsgDepositRefundRange},
	}
	for _, c := range cases {
		r := httpkit.Do(w.Env.Router, "PUT", path, c.body, w.Owner)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", c.body, r.Code, r.Body.String())
		}
		if got := depJSON(t, r.Body.Bytes())["error"]; got != c.want {
			t.Fatalf("%s: mensaje %v, quería %q", c.body, got, c.want)
		}
	}
	if len(w.Policies.Rows) != 0 {
		t.Fatalf("ningún rechazo puede guardar: %v", w.Policies.Rows)
	}
	// Guardar apagada es válido (las cuatro claves SIEMPRE viajan).
	r := httpkit.Do(w.Env.Router, "PUT", path, `{"enabled":false,"percent":30,"hold_minutes":30,"refund_hours":24}`, w.Owner)
	if r.Code != http.StatusOK {
		t.Fatalf("PUT apagada: %d %s", r.Code, r.Body.String())
	}
	got := depJSON(t, r.Body.Bytes())
	if got["enabled"] != false || got["percent"].(float64) != 30 || got["hold_minutes"].(float64) != 30 || got["refund_hours"].(float64) != 24 {
		t.Fatalf("política guardada: %v", got)
	}
	// GET la refleja.
	r = httpkit.Do(w.Env.Router, "GET", path, "", w.Prof)
	if got := depJSON(t, r.Body.Bytes()); got["percent"].(float64) != 30 {
		t.Fatalf("GET tras guardar: %v", got)
	}
}

func TestDepositPolicy_PUT_sinBancoEs409(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.Gateway.On = false
	path := "/api/v1/salons/" + w.Salon + "/deposit-policy"
	r := httpkit.Do(w.Env.Router, "PUT", path, `{"enabled":true,"percent":30,"hold_minutes":30,"refund_hours":24}`, w.Owner)
	if r.Code != http.StatusConflict {
		t.Fatalf("activar sin banco: %d %s", r.Code, r.Body.String())
	}
	if got := depJSON(t, r.Body.Bytes())["error"]; got != domain.MsgDepositGatewayUnavailable {
		t.Fatalf("mensaje: %v", got)
	}
	if len(w.Policies.Rows) != 0 {
		t.Fatalf("el 409 no guarda nada")
	}
	// Apagarla sí se puede sin banco (no se cobra nada).
	r = httpkit.Do(w.Env.Router, "PUT", path, `{"enabled":false,"percent":30,"hold_minutes":30,"refund_hours":24}`, w.Admin)
	if r.Code != http.StatusOK {
		t.Fatalf("apagar sin banco: %d %s", r.Code, r.Body.String())
	}
}

// --- Pantalla «Señas» (D10) -------------------------------------------------

func TestDeposits_list_permisoAntesDeParametros(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.SeedCharge(t, domain.BookingBooked)
	path := "/api/v1/salons/" + w.Salon + "/deposits?limit=0&status=nope&before=%zz"
	for _, tok := range []string{w.Prof, w.Removed, w.Outsider} {
		r := httpkit.Do(w.Env.Router, "GET", path, "", tok)
		if r.Code != http.StatusForbidden {
			t.Fatalf("sin permiso ve la lista: %d %s", r.Code, r.Body.String())
		}
	}
	// Un id con NUL nunca llega a la base: es un recurso que no existe (403 antes
	// de cualquier parámetro).
	r := httpkit.Do(w.Env.Router, "GET", "/api/v1/salons/%00/deposits", "", w.Owner)
	if r.Code != http.StatusForbidden && r.Code != http.StatusNotFound && r.Code != http.StatusBadRequest {
		t.Fatalf("id con NUL: %d %s", r.Code, r.Body.String())
	}
}

func TestDeposits_list_formaYParametros(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	d := w.SeedCharge(t, domain.BookingBooked)
	base := "/api/v1/salons/" + w.Salon + "/deposits"
	for _, tok := range []string{w.Owner, w.Admin, w.Recep} {
		r := httpkit.Do(w.Env.Router, "GET", base, "", tok)
		if r.Code != http.StatusOK {
			t.Fatalf("listar (%s): %d %s", tok, r.Code, r.Body.String())
		}
		body := depJSON(t, r.Body.Bytes())
		rows, ok := body["deposits"].([]any)
		if !ok || len(rows) != 1 {
			t.Fatalf("filas: %v", body["deposits"])
		}
		row := rows[0].(map[string]any)
		for _, key := range []string{"id", "booking_id", "client_name", "starts_at", "amount_cents", "status", "paid_at", "settled_at"} {
			if _, present := row[key]; !present {
				t.Fatalf("falta la clave %q en la fila: %v", key, row)
			}
		}
		if row["id"] != d.ID || row["status"] != domain.DepositPending || row["amount_cents"].(float64) != 4500 {
			t.Fatalf("fila: %v", row)
		}
		if body["next_cursor"] != nil {
			t.Fatalf("una página corta no lleva cursor: %v", body["next_cursor"])
		}
	}
	// Parámetros inválidos: 400 con el texto del dominio.
	for _, c := range []struct{ query, want string }{
		{"?limit=0", domain.MsgDepositLimitRange},
		{"?limit=101", domain.MsgDepositLimitRange},
		{"?status=nope", domain.MsgDepositStatusInvalid},
		{"?before=bm9wZQ", domain.MsgDepositCursorInvalid},
	} {
		r := httpkit.Do(w.Env.Router, "GET", base+c.query, "", w.Owner)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", c.query, r.Code, r.Body.String())
		}
		if got := depJSON(t, r.Body.Bytes())["error"]; got != c.want {
			t.Fatalf("%s: mensaje %v, quería %q", c.query, got, c.want)
		}
	}
	// Filtro por estado y aislamiento por salón.
	w.Policies.Seed(domain.DepositPolicy{SalonID: w.Salon, Enabled: true, Percent: 30, HoldMinutes: 30, RefundHours: 24})
	w.Deposits.Seed(domain.BookingDeposit{ID: "dep-ajena", SalonID: "otro-salon", BookingID: "b-ajena",
		AmountCents: 100, Percent: 30, Status: domain.DepositPaid, ChargeID: "c-ajena",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)})
	r := httpkit.Do(w.Env.Router, "GET", base+"?status=paid", "", w.Owner)
	body := depJSON(t, r.Body.Bytes())
	if rows := body["deposits"].([]any); len(rows) != 0 {
		t.Fatalf("filtro por estado: %v", rows)
	}
	r = httpkit.Do(w.Env.Router, "GET", base, "", w.Owner)
	if rows := depJSON(t, r.Body.Bytes())["deposits"].([]any); len(rows) != 1 {
		t.Fatalf("aislamiento: %v", rows)
	}
}

func TestDeposits_list_cursorOpaco(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	// Tres señas con distinto created_at: la página de 2 deja un cursor y la
	// siguiente trae la tercera sin repetir.
	now := time.Now().UTC()
	for i, at := range []time.Time{now.Add(-3 * time.Minute), now.Add(-2 * time.Minute), now.Add(-time.Minute)} {
		w.Deposits.Seed(domain.BookingDeposit{
			ID: "dep-" + httpkit.Itoa(i), SalonID: w.Salon, BookingID: "b-" + httpkit.Itoa(i),
			AmountCents: 100, Percent: 30, Status: domain.DepositPending,
			ChargeID: "charge-" + httpkit.Itoa(i), CreatedAt: at, ExpiresAt: at.Add(time.Hour),
		})
	}
	base := "/api/v1/salons/" + w.Salon + "/deposits"
	r := httpkit.Do(w.Env.Router, "GET", base+"?limit=2", "", w.Owner)
	body := depJSON(t, r.Body.Bytes())
	rows := body["deposits"].([]any)
	if len(rows) != 2 || body["next_cursor"] == nil {
		t.Fatalf("primera página: %v (cursor %v)", rows, body["next_cursor"])
	}
	cursor := body["next_cursor"].(string)
	r = httpkit.Do(w.Env.Router, "GET", base+"?limit=2&before="+cursor, "", w.Owner)
	body = depJSON(t, r.Body.Bytes())
	rows = body["deposits"].([]any)
	if len(rows) != 1 {
		t.Fatalf("segunda página: %v", rows)
	}
	if rows[0].(map[string]any)["id"] != "dep-0" {
		t.Fatalf("la segunda página trae la más vieja: %v", rows[0])
	}
	// Un cursor manipulado no adivina posiciones: 400.
	r = httpkit.Do(w.Env.Router, "GET", base+"?before=no-es-un-cursor", "", w.Owner)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("cursor inválido: %d", r.Code)
	}
}

// --- Callback del banco (D7) ------------------------------------------------

func TestPaymentsCallback_firmaInvalida(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.SeedCharge(t, domain.BookingBooked)
	body, sig := w.CallbackRaw("ev-1", w.ChargeID, domain.CallbackApproved, 4500)
	path := "/api/v1/payments/callback"

	if r := w.PostSigned(path, body, ""); r.Code != http.StatusBadRequest {
		t.Fatalf("sin cabecera: %d %s", r.Code, r.Body.String())
	}
	// Cuerpo alterado: la firma es de otro cuerpo.
	tampered := strings.Replace(body, `"amount_cents":4500`, `"amount_cents":1`, 1)
	r := w.PostSigned(path, tampered, sig)
	if r.Code != http.StatusBadRequest || depJSON(t, r.Body.Bytes())["error"] != domain.MsgPaymentSignatureInvalid {
		t.Fatalf("cuerpo alterado: %d %s", r.Code, r.Body.String())
	}
	// Formato de cabecera malo.
	r = w.PostSigned(path, body, "t=nope,v1=zz")
	if r.Code != http.StatusBadRequest {
		t.Fatalf("formato malo: %d %s", r.Code, r.Body.String())
	}
	// t viejo y t futuro: fuera de la ventana de +-300 s.
	old := domain.SignPayment(w.Secret, time.Now().UTC().Add(-301*time.Second), []byte(body))
	if r = w.PostSigned(path, body, old); r.Code != http.StatusBadRequest {
		t.Fatalf("t viejo: %d %s", r.Code, r.Body.String())
	}
	future := domain.SignPayment(w.Secret, time.Now().UTC().Add(301*time.Second), []byte(body))
	if r = w.PostSigned(path, body, future); r.Code != http.StatusBadRequest {
		t.Fatalf("t futuro: %d %s", r.Code, r.Body.String())
	}
	// Cuerpo de más de 8 KB: rechazado ANTES de parsear.
	big := `{"event_id":"` + strings.Repeat("a", 9000) + `"}`
	if r = w.PostSigned(path, big, sig); r.Code != http.StatusBadRequest {
		t.Fatalf("cuerpo de 8 KB: %d %s", r.Code, r.Body.String())
	}
	// Nada de lo rechazado tuvo efecto.
	if kinds := w.Deposits.EventKinds(w.DepositID); len(kinds) != 1 || kinds[0] != domain.DepositEventCreated {
		t.Fatalf("un callback inválido no toca el libro mayor: %v", kinds)
	}
}

func TestPaymentsCallback_aprobadoUnaSolaVez(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.SeedCharge(t, domain.BookingBooked)
	body, sig := w.CallbackRaw("ev-aprobado", w.ChargeID, domain.CallbackApproved, 4500)
	path := "/api/v1/payments/callback"

	r := w.PostSigned(path, body, sig)
	if r.Code != http.StatusOK || depJSON(t, r.Body.Bytes())["ok"] != true {
		t.Fatalf("aprobado: %d %s", r.Code, r.Body.String())
	}
	d, _ := w.Deposits.FindByCharge(w.ChargeID)
	if d.Status != domain.DepositPaid || d.PaidAt == nil {
		t.Fatalf("estado tras el pago: %+v", d)
	}
	if kinds := w.Deposits.EventKinds(w.DepositID); len(kinds) != 2 || kinds[1] != domain.DepositEventPaid {
		t.Fatalf("libro mayor: %v", kinds)
	}
	// Reenvío del banco: 200 {"ok":true} SIN efecto (ni segundo evento).
	r = w.PostSigned(path, body, sig)
	if r.Code != http.StatusOK || depJSON(t, r.Body.Bytes())["ok"] != true {
		t.Fatalf("reenvío: %d %s", r.Code, r.Body.String())
	}
	if kinds := w.Deposits.EventKinds(w.DepositID); len(kinds) != 2 {
		t.Fatalf("el reenvío duplicó eventos: %v", kinds)
	}
	// Un segundo evento del MISMO cargo no vuelve a transicionar.
	body2, sig2 := w.CallbackRaw("ev-otro", w.ChargeID, domain.CallbackApproved, 4500)
	if r = w.PostSigned(path, body2, sig2); r.Code != http.StatusOK {
		t.Fatalf("segundo evento: %d %s", r.Code, r.Body.String())
	}
	if kinds := w.Deposits.EventKinds(w.DepositID); len(kinds) != 2 {
		t.Fatalf("dos eventos, una transición: %v", kinds)
	}
}

func TestPaymentsCallback_rechazadoSiguePendiente(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.SeedCharge(t, domain.BookingBooked)
	body, sig := w.CallbackRaw("ev-rechazo", w.ChargeID, domain.CallbackDeclined, 4500)
	r := w.PostSigned("/api/v1/payments/callback", body, sig)
	if r.Code != http.StatusOK {
		t.Fatalf("rechazado: %d %s", r.Code, r.Body.String())
	}
	d, _ := w.Deposits.FindByCharge(w.ChargeID)
	if d.Status != domain.DepositPending {
		t.Fatalf("un intento rechazado NO mueve la seña: %+v", d)
	}
	kinds := w.Deposits.EventKinds(w.DepositID)
	if len(kinds) != 2 || kinds[1] != domain.DepositEventAttemptDeclined {
		t.Fatalf("libro mayor: %v", kinds)
	}
}

func TestPaymentsCallback_montoInvalidoYDesconocido(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.SeedCharge(t, domain.BookingBooked)
	path := "/api/v1/payments/callback"
	body, sig := w.CallbackRaw("ev-monto", w.ChargeID, domain.CallbackApproved, 999)
	r := w.PostSigned(path, body, sig)
	if r.Code != http.StatusBadRequest || depJSON(t, r.Body.Bytes())["error"] != domain.MsgDepositInvalidAmount {
		t.Fatalf("monto distinto: %d %s", r.Code, r.Body.String())
	}
	if d, _ := w.Deposits.FindByCharge(w.ChargeID); d.Status != domain.DepositPending {
		t.Fatalf("un monto inválido no tiene efecto: %+v", d)
	}
	unknown, _ := domain.NewDepositChargeID()
	body, sig = w.CallbackRaw("ev-desconocido", unknown, domain.CallbackApproved, 4500)
	r = w.PostSigned(path, body, sig)
	if r.Code != http.StatusNotFound || depJSON(t, r.Body.Bytes())["error"] != domain.MsgDepositNotFound {
		t.Fatalf("cargo desconocido: %d %s", r.Code, r.Body.String())
	}
	// Id con formato inválido: mismo 404 (no consulta la base).
	body, sig = w.CallbackRaw("ev-basura", "no-es-un-cargo", domain.CallbackApproved, 4500)
	r = w.PostSigned(path, body, sig)
	if r.Code != http.StatusNotFound {
		t.Fatalf("id basura: %d %s", r.Code, r.Body.String())
	}
}

func TestPaymentsCallback_pagoTardioReembolsaUnaVez(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	// La sena ya venció (y el barrido la cerró): el turno sigue booked.
	w.SeedCharge(t, domain.BookingCancelled)
	w.Deposits.Mu.Lock()
	d := w.Deposits.Rows[w.DepositID]
	d.Status = domain.DepositExpired
	w.Deposits.Rows[w.DepositID] = d
	w.Deposits.Mu.Unlock()

	body, sig := w.CallbackRaw("ev-tarde", w.ChargeID, domain.CallbackApproved, 4500)
	path := "/api/v1/payments/callback"
	r := w.PostSigned(path, body, sig)
	if r.Code != http.StatusOK {
		t.Fatalf("pago tardío: %d %s", r.Code, r.Body.String())
	}
	if got := w.Gateway.RefundCount(w.ChargeID); got != 1 {
		t.Fatalf("reembolsos: %d", got)
	}
	got, _ := w.Deposits.FindByCharge(w.ChargeID)
	if got.Status != domain.DepositRefunded || got.SettledAt == nil {
		t.Fatalf("sena tras el pago tardío: %+v", got)
	}
	kinds := w.Deposits.EventKinds(w.DepositID)
	if len(kinds) != 2 || kinds[1] != domain.DepositEventLatePaymentRefunded {
		t.Fatalf("libro mayor: %v", kinds)
	}
	// El turno no revive y se cancela por el camino existente.
	if b := w.Bookings.Rows[got.BookingID]; b.Status != domain.BookingCancelled {
		t.Fatalf("el turno no puede quedar retenido: %+v", b)
	}
	// Reintento del banco: ni segundo reembolso ni segundo evento.
	if r = w.PostSigned(path, body, sig); r.Code != http.StatusOK {
		t.Fatalf("reenvío del pago tardío: %d %s", r.Code, r.Body.String())
	}
	if got := w.Gateway.RefundCount(w.ChargeID); got != 1 {
		t.Fatalf("el reenvío reembolsó de nuevo: %d", got)
	}
	if kinds := w.Deposits.EventKinds(w.DepositID); len(kinds) != 2 {
		t.Fatalf("el reenvío duplicó eventos: %v", kinds)
	}
}

func TestPaymentsCallback_sinBancoNoExiste(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.Gateway.On = false
	w.Env.Deps.PaymentsCallback = nil
	w.Env.Router = gin.New()
	httpapi.RegisterRoutes(w.Env.Router, w.Env.Deps)
	body, sig := w.CallbackRaw("ev-1", w.ChargeID, domain.CallbackApproved, 4500)
	if r := w.PostSigned("/api/v1/payments/callback", body, sig); r.Code != http.StatusNotFound {
		t.Fatalf("sin banco el callback no existe (D12): %d", r.Code)
	}
}

// --- Consulta pública del pago (D8) ----------------------------------------

func TestPublicDepositLookup(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	w.SeedCharge(t, domain.BookingBooked)
	path := "/api/v1/public/deposits/lookup"
	r := httpkit.Do(w.Env.Router, "POST", path, `{"charge_id":"`+w.ChargeID+`"}`, "")
	if r.Code != http.StatusOK {
		t.Fatalf("lookup: %d %s", r.Code, r.Body.String())
	}
	got := depJSON(t, r.Body.Bytes())
	if got["status"] != domain.DepositPending || got["amount_cents"].(float64) != 4500 {
		t.Fatalf("lookup: %v", got)
	}
	for _, key := range []string{"expires_at", "starts_at"} {
		if _, ok := got[key].(string); !ok {
			t.Fatalf("%s: %v", key, got[key])
		}
	}
	// Formato inválido, NUL y cargo desconocido: el MISMO 404 con el texto del
	// contrato, y el primero ni consulta la base.
	unknown, _ := domain.NewDepositChargeID()
	for _, charge := range []string{"", "no-es-un-cargo", "a\u0000b", unknown} {
		body, _ := json.Marshal(map[string]string{"charge_id": charge})
		r := httpkit.Do(w.Env.Router, "POST", path, string(body), "")
		if r.Code != http.StatusNotFound || depJSON(t, r.Body.Bytes())["error"] != domain.MsgDepositNotFound {
			t.Fatalf("cargo %q: %d %s", charge, r.Code, r.Body.String())
		}
	}
	if r := httpkit.Do(w.Env.Router, "POST", path, `{`, ""); r.Code != http.StatusBadRequest {
		t.Fatalf("cuerpo roto: %d", r.Code)
	}
}

// --- Payload público (D11) --------------------------------------------------

func TestPublicSalon_depositPayload(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	svcID := w.Env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	if err := w.Env.Professionals.SetServices(w.Salon, w.ProfID, []string{svcID}); err != nil {
		t.Fatal(err)
	}
	// Sin seña: el payload es el de hoy (sin la clave `deposit` ni
	// `deposit_cents`): la landing no puede inventar un cobro y el test de forma
	// del payload publico (public_test.go) fija el conjunto exacto de claves.
	// El frontend lee la clave ausente como `null` (publicSalon.ts).
	r := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/"+w.Slug, "", "")
	body := depJSON(t, r.Body.Bytes())
	if dep, present := body["deposit"]; present {
		t.Fatalf("sin seña no viaja `deposit`: %v", dep)
	}
	svc := body["services"].([]any)[0].(map[string]any)
	if _, present := svc["deposit_cents"]; present {
		t.Fatalf("sin seña no viaja `deposit_cents`: %v", svc)
	}
	// Con seña activa: `deposit` con la política y `deposit_cents` por servicio.
	w.Policies.Seed(domain.DepositPolicy{SalonID: w.Salon, Enabled: true, Percent: 30, HoldMinutes: 30, RefundHours: 24})
	r = httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/"+w.Slug, "", "")
	body = depJSON(t, r.Body.Bytes())
	deposit, ok := body["deposit"].(map[string]any)
	if !ok {
		t.Fatalf("deposit: %v", body["deposit"])
	}
	if deposit["percent"].(float64) != 30 || deposit["hold_minutes"].(float64) != 30 ||
		deposit["refund_hours"].(float64) != 24 || deposit["simulated"] != true {
		t.Fatalf("deposit: %v", deposit)
	}
	svc = body["services"].([]any)[0].(map[string]any)
	if svc["deposit_cents"].(float64) != float64(domain.DepositAmountCents(15000, 30)) {
		t.Fatalf("deposit_cents: %v", svc["deposit_cents"])
	}
}

// --- Reserva pública con seña (D8) -----------------------------------------

func TestPublicBooking_payment(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	svcID := w.Env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	if err := w.Env.Professionals.SetServices(w.Salon, w.OwnerID, []string{svcID}); err != nil {
		t.Fatal(err)
	}
	salonSettings(w.Env, w.Salon)
	// La reserva pública con el DepositCreator cableado.
	creator := &mDepositCreator{Deposits: w.Deposits, percent: 30}
	pub := w.Env.Deps.Public
	pb := pub.Bookings
	pb.Deposits = creator
	pb.AppURL = "http://localhost:8081"
	pub.Bookings = pb
	w.Env.Deps.Public = pub
	w.Env.Router = gin.New()
	httpapi.RegisterRoutes(w.Env.Router, w.Env.Deps)

	starts := time.Date(2027, 3, 2, 15, 0, 0, 0, time.UTC) // 09:00 en CDMX
	body := `{"service_id":"` + svcID + `","client_name":"Lía","client_phone":"555","client_email":"lia@mail.com","starts_at":"` +
		starts.Format(time.RFC3339) + `","client_op_id":"op-sena-1"}`
	r := httpkit.Do(w.Env.Router, "POST", "/api/v1/public/salons/"+w.Slug+"/bookings", body, "")
	if r.Code != http.StatusCreated {
		t.Fatalf("reservar: %d %s", r.Code, r.Body.String())
	}
	got := depJSON(t, r.Body.Bytes())
	payment, ok := got["payment"].(map[string]any)
	if !ok {
		t.Fatalf("payment: %v", got)
	}
	if payment["amount_cents"].(float64) != 4500 || payment["simulated"] != true ||
		!strings.Contains(payment["pay_url"].(string), "/pagar#") {
		t.Fatalf("payment: %v", payment)
	}
	if _, err := time.Parse(time.RFC3339, payment["expires_at"].(string)); err != nil {
		t.Fatalf("expires_at: %v", payment["expires_at"])
	}
	// Con seña la confirmación NO sale todavía (sale al pagarse, D7).
	if n := w.Env.Mailer.Count(); n != 0 {
		t.Fatalf("no puede salir la confirmación sin pagar: %d", n)
	}
	// El reintento con el mismo client_op_id devuelve el MISMO payment.
	r = httpkit.Do(w.Env.Router, "POST", "/api/v1/public/salons/"+w.Slug+"/bookings", body, "")
	if r.Code != http.StatusCreated {
		t.Fatalf("reintento: %d %s", r.Code, r.Body.String())
	}
	again := depJSON(t, r.Body.Bytes())["payment"].(map[string]any)
	if again["deposit_id"] != payment["deposit_id"] || again["pay_url"] != payment["pay_url"] {
		t.Fatalf("el reintento cambió el pago: %v vs %v", again, payment)
	}
}

func TestPublicBooking_sinSenaRespuestaYCorreoDeHoy(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	// Precio 0: no hay seña y la respuesta y el correo son los de hoy (D8).
	svcID := w.Env.SeedService(t, w.Owner, w.Salon, "Gratis", 0)
	if err := w.Env.Professionals.SetServices(w.Salon, w.OwnerID, []string{svcID}); err != nil {
		t.Fatal(err)
	}
	salonSettings(w.Env, w.Salon)
	creator := &mDepositCreator{Deposits: w.Deposits, percent: 30}
	pub := w.Env.Deps.Public
	pb := pub.Bookings
	pb.Deposits = creator
	pub.Bookings = pb
	w.Env.Deps.Public = pub
	w.Env.Router = gin.New()
	httpapi.RegisterRoutes(w.Env.Router, w.Env.Deps)

	starts := time.Date(2027, 3, 2, 15, 0, 0, 0, time.UTC)
	body := `{"service_id":"` + svcID + `","client_name":"Lía","client_phone":"555","client_email":"lia@mail.com","starts_at":"` +
		starts.Format(time.RFC3339) + `"}`
	r := httpkit.Do(w.Env.Router, "POST", "/api/v1/public/salons/"+w.Slug+"/bookings", body, "")
	if r.Code != http.StatusCreated {
		t.Fatalf("reservar: %d %s", r.Code, r.Body.String())
	}
	got := depJSON(t, r.Body.Bytes())
	if _, present := got["payment"]; present {
		t.Fatalf("sin precio no hay payment (la respuesta es la de hoy): %v", got)
	}
	// El correo de siempre sigue saliendo.
	w.Env.Mailer.WaitCount(t, 1)
}

func TestPublicBooking_fallaElCargoCancelaLaReserva(t *testing.T) {
	w := httpkit.NewDepositEnv(t)
	svcID := w.Env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	if err := w.Env.Professionals.SetServices(w.Salon, w.OwnerID, []string{svcID}); err != nil {
		t.Fatal(err)
	}
	salonSettings(w.Env, w.Salon)
	creator := &mDepositCreator{Deposits: w.Deposits, percent: 30, fail: errors.New("banco caído")}
	pub := w.Env.Deps.Public
	pb := pub.Bookings
	pb.Deposits = creator
	pub.Bookings = pb
	w.Env.Deps.Public = pub
	w.Env.Router = gin.New()
	httpapi.RegisterRoutes(w.Env.Router, w.Env.Deps)

	starts := time.Date(2027, 3, 2, 15, 0, 0, 0, time.UTC)
	body := `{"service_id":"` + svcID + `","client_name":"Lía","client_phone":"555","starts_at":"` +
		starts.Format(time.RFC3339) + `"}`
	r := httpkit.Do(w.Env.Router, "POST", "/api/v1/public/salons/"+w.Slug+"/bookings", body, "")
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("el error del cargo es el 500 de siempre: %d %s", r.Code, r.Body.String())
	}
	// El turno se canceló por el camino existente: no queda el cupo retenido.
	booked := 0
	for _, b := range w.Env.Bookings.All {
		if b.SalonID == w.Salon {
			if b.Status != domain.BookingCancelled {
				t.Fatalf("el turno sin cargo debe quedar cancelado: %+v", b)
			}
			booked++
		}
	}
	if booked != 1 {
		t.Fatalf("turnos: %d", booked)
	}
}
