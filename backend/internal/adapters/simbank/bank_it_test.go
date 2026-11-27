// Integración del banco simulado (F11, D6) contra Postgres y una entrega HTTP
// real: cargo, tarjetas de prueba, tope de intentos, vencimiento, ya aprobado,
// reembolso idempotente, firma del callback (válida, alterada, vieja, futura,
// sin cabecera, formato malo, cuerpo de más de 8 KB) y el PAN ausente de la base.
package simbank

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

const itSecret = "simbank-it-secret-0123456789abcdef"

func itPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SALON_IT") == "" {
		t.Skip("SALON_IT no definido: test de integración salteado")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := pgstore.Migrate(context.Background(), pool, filepath.Join("..", "..", "..", "migrations")); err != nil {
		t.Fatalf("migrar: %v", err)
	}
	return pool
}

// callbackSink es el comercio: verifica la firma de cada entrega y guarda los
// eventos recibidos.
type callbackSink struct {
	mu     sync.Mutex
	secret string
	events []portsEvent
	fail   int
	srv    *httptest.Server
}

type portsEvent struct {
	EventID     string `json:"event_id"`
	ChargeID    string `json:"charge_id"`
	Reference   string `json:"reference"`
	Kind        string `json:"kind"`
	AmountCents int    `json:"amount_cents"`
}

func newCallbackSink(t *testing.T) *callbackSink {
	t.Helper()
	s := &callbackSink{secret: itSecret}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, MaxCallbackBody+1))
		if err := domain.VerifyPaymentSignature(s.secret, r.Header.Get(SignatureHeader), body, time.Now()); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.fail > 0 {
			s.fail--
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var ev portsEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.events = append(s.events, ev)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *callbackSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *callbackSink) last(t *testing.T) portsEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		t.Fatal("el comercio no recibió ningún callback")
	}
	return s.events[len(s.events)-1]
}

// newBank deja un banco con su pool, el sink de callback y un reloj fijo.
func newBank(t *testing.T) (*Bank, *callbackSink) {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	sink := newCallbackSink(t)
	bank := &Bank{Pool: pool, Secret: itSecret, CallbackURL: sink.srv.URL + "/api/v1/payments/callback",
		Sleep: func(time.Duration) {}}
	return bank, sink
}

func bankRouter(bank *Bank) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := Handler{Bank: bank}
	r := gin.New()
	r.POST("/api/v1/simbank/charges/lookup", h.Lookup)
	r.POST("/api/v1/simbank/charges/pay", h.Pay)
	return r
}

func doJSON(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func newCharge(t *testing.T, bank *Bank, expiresAt time.Time) string {
	t.Helper()
	c, err := bank.CreateCharge(context.Background(), ports.ChargeRequest{
		Reference: "b-it-" + domain.NewID(), AmountCents: 4500,
		Description: "Corte", ExpiresAt: expiresAt.UTC(),
	})
	if err != nil {
		t.Fatalf("cargo: %v", err)
	}
	return c.ID
}

// D6: CreateCharge guarda el cargo pendiente y devuelve el id credencial y la
// página del banco.
func TestBankIT_createCharge(t *testing.T) {
	bank, _ := newBank(t)
	expires := time.Now().UTC().Add(30 * time.Minute)
	ch, err := bank.CreateCharge(context.Background(), ports.ChargeRequest{
		Reference: "b-1", AmountCents: 4500, Description: "Corte", ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("cargo: %v", err)
	}
	if !domain.ValidDepositChargeID(ch.ID) || ch.PayPath != "/pagar#"+ch.ID {
		t.Fatalf("cargo: %+v", ch)
	}
	var status string
	var amount int
	if err := bank.Pool.QueryRow(context.Background(),
		`SELECT status, amount_cents FROM simbank_charges WHERE id=$1`, ch.ID).Scan(&status, &amount); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || amount != 4500 {
		t.Fatalf("fila: %s %d", status, amount)
	}
}

// D6: la firma se acepta solo válida y dentro de +-300 s; todo lo demás es el
// MISMO error uniforme.
func TestBankIT_parseCallbackSignature(t *testing.T) {
	// Sin base: la verificación de la firma es del adaptador y tiene que probarse
	// SIEMPRE (no solo con SALON_IT). ParseCallback no toca el pool; el pool vacío
	// solo satisface Enabled() (que exige banco configurado). Si esta prueba
	// necesitara una DB, una suite sin base no verificaría ninguna firma.
	bank := &Bank{Pool: &pgxpool.Pool{}, Secret: itSecret}
	body := []byte(`{"event_id":"ev-1","charge_id":"c","reference":"r","kind":"approved","amount_cents":100}`)
	now := time.Now().UTC()
	header := http.Header{}

	header.Set(SignatureHeader, domain.SignPayment(itSecret, now, body))
	if ev, err := bank.ParseCallback(header, body, now); err != nil || ev.EventID != "ev-1" || ev.Kind != "approved" {
		t.Fatalf("válida: %+v (%v)", ev, err)
	}
	// Cuerpo alterado, sin cabecera, formato malo, t viejo y t futuro.
	for name, h := range map[string]http.Header{
		"alterada": {SignatureHeader: {domain.SignPayment(itSecret, now, []byte("otro"))}},
		"sin":      {},
		"formato":  {SignatureHeader: {"t=1,v1=zz"}},
		"viejo":    {SignatureHeader: {domain.SignPayment(itSecret, now.Add(-301*time.Second), body)}},
		"futuro":   {SignatureHeader: {domain.SignPayment(itSecret, now.Add(301*time.Second), body)}},
	} {
		if _, err := bank.ParseCallback(h, body, now); err == nil || err.Error() != domain.MsgPaymentSignatureInvalid {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Cuerpo de más de 8 KB: rechazado antes de parsear.
	big := append([]byte(`{"event_id":"`), bytes.Repeat([]byte("a"), MaxCallbackBody)...)
	h := http.Header{}
	h.Set(SignatureHeader, domain.SignPayment(itSecret, now, big))
	if _, err := bank.ParseCallback(h, big, now); err == nil {
		t.Fatal("un cuerpo de más de 8 KB tiene que rechazarse")
	}
}

// D6: el reembolso es idempotente por cargo.
func TestBankIT_refundIdempotent(t *testing.T) {
	bank, _ := newBank(t)
	ch := newCharge(t, bank, time.Now().UTC().Add(time.Hour))
	for i := 0; i < 3; i++ {
		if err := bank.Refund(context.Background(), ch, 4500, "reserva cancelada"); err != nil {
			t.Fatalf("reembolso %d: %v", i, err)
		}
	}
	var n int
	if err := bank.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM simbank_refunds WHERE charge_id=$1`, ch).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reembolsos: %d (%v)", n, err)
	}
	var status string
	if err := bank.Pool.QueryRow(context.Background(), `SELECT status FROM simbank_charges WHERE id=$1`, ch).Scan(&status); err != nil || status != "refunded" {
		t.Fatalf("estado: %q (%v)", status, err)
	}
	// Un cargo desconocido con formato válido es 404, nunca nil silencioso.
	unknown, _ := domain.NewDepositChargeID()
	if err := bank.Refund(context.Background(), unknown, 4500, "x"); err == nil {
		t.Fatal("reembolsar un cargo inexistente no puede devolver nil")
	}
}

// D5/D12: sin banco configurado un reembolso NUNCA puede devolver nil (parecería
// que el dinero volvió y la seña quedaría cerrada como `refunded` para siempre).
func TestBankIT_refundWithoutBankFails(t *testing.T) {
	for name, bank := range map[string]*Bank{
		"nil":         nil,
		"sin secreto": {},
	} {
		if err := bank.Refund(context.Background(), "cargo-cualquiera", 4500, "reserva cancelada"); err == nil {
			t.Fatalf("%s: un reembolso imposible no puede devolver nil", name)
		} else if err.Error() != domain.MsgDepositGatewayUnavailable {
			t.Fatalf("%s: error del reembolso: %v", name, err)
		}
	}
}

// D6: lookup y pay por HTTP con la entrega REAL del callback firmado.
func TestBankIT_payApprovedAndDeclined(t *testing.T) {
	bank, sink := newBank(t)
	r := bankRouter(bank)
	expires := time.Now().UTC().Add(30 * time.Minute)
	ch := newCharge(t, bank, expires)

	// Lookup público: monto, estado y vencimiento.
	rec := doJSON(r, "/api/v1/simbank/charges/lookup", `{"charge_id":"`+ch+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup: %d %s", rec.Code, rec.Body.String())
	}
	var look map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &look)
	if look["amount_cents"].(float64) != 4500 || look["status"] != "pending" || look["simulated"] != true {
		t.Fatalf("lookup: %v", look)
	}

	// Tarjeta aprobada: decide, entrega el callback firmado y responde el
	// resultado a la página del banco.
	pay := `{"charge_id":"` + ch + `","card":{"number":"4242424242424242","exp":"12/30","cvc":"123","name":"Lía Pérez"}}`
	rec = doJSON(r, "/api/v1/simbank/charges/pay", pay)
	if rec.Code != http.StatusOK {
		t.Fatalf("pago aprobado: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result["status"] != "approved" || result["last4"] != "4242" || result["brand"] != "visa" {
		t.Fatalf("resultado: %v", result)
	}
	if sink.count() != 1 {
		t.Fatalf("callbacks: %d", sink.count())
	}
	ev := sink.last(t)
	if ev.Kind != domain.CallbackApproved || ev.ChargeID != ch || ev.AmountCents != 4500 || ev.EventID == "" {
		t.Fatalf("evento: %+v", ev)
	}
	// El PAN no está en la base: solo last4 y marca.
	var rowText string
	if err := bank.Pool.QueryRow(context.Background(),
		`SELECT row_to_json(c)::text FROM simbank_charges c WHERE id=$1`, ch).Scan(&rowText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rowText, "4242424242424242") || strings.Contains(rowText, "123") {
		t.Fatalf("la base guardó datos de la tarjeta: %s", rowText)
	}
	// Pagar otra vez un cargo aprobado es 409 con la frase del contrato.
	rec = doJSON(r, "/api/v1/simbank/charges/pay", pay)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), MsgChargeAlreadyPayed) {
		t.Fatalf("ya aprobado: %d %s", rec.Code, rec.Body.String())
	}

	// Tarjeta rechazada: 200 con el resultado y un callback `declined`; el cargo
	// sigue intentable hasta el tope.
	ch2 := newCharge(t, bank, expires)
	declined := `{"charge_id":"` + ch2 + `","card":{"number":"4000000000000002","exp":"12/30","cvc":"123","name":"Lía"}}`
	for i := 1; i <= 5; i++ {
		rec = doJSON(r, "/api/v1/simbank/charges/pay", declined)
		if rec.Code != http.StatusOK {
			t.Fatalf("intento %d: %d %s", i, rec.Code, rec.Body.String())
		}
		var res map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["status"] != "declined" || res["message"] != domain.MsgCardDeclined {
			t.Fatalf("intento %d: %v", i, res)
		}
	}
	// Sexto intento rechazado: 429 «Demasiados intentos».
	rec = doJSON(r, "/api/v1/simbank/charges/pay", declined)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), MsgTooManyAttempts) {
		t.Fatalf("tope de intentos: %d %s", rec.Code, rec.Body.String())
	}
	// Sin fondos tiene su propio texto.
	ch3 := newCharge(t, bank, expires)
	noFunds := `{"charge_id":"` + ch3 + `","card":{"number":"4000000000009995","exp":"12/30","cvc":"123","name":"Lía"}}`
	rec = doJSON(r, "/api/v1/simbank/charges/pay", noFunds)
	var res map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res["status"] != "insufficient" || res["message"] != domain.MsgCardInsufficient {
		t.Fatalf("sin fondos: %d %v", rec.Code, res)
	}
	// Otra tarjeta con Luhn válido: rechazada sin frase propia.
	ch4 := newCharge(t, bank, expires)
	other := `{"charge_id":"` + ch4 + `","card":{"number":"4111111111111111","exp":"12/30","cvc":"123","name":"Lía"}}`
	rec = doJSON(r, "/api/v1/simbank/charges/pay", other)
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res["status"] != "declined" {
		t.Fatalf("otra con Luhn: %d %v", rec.Code, res)
	}

	// Tarjeta inválida (Luhn malo): 400 SIN crear intento.
	ch5 := newCharge(t, bank, expires)
	bad := `{"charge_id":"` + ch5 + `","card":{"number":"4242424242424241","exp":"12/30","cvc":"123","name":"Lía"}}`
	rec = doJSON(r, "/api/v1/simbank/charges/pay", bad)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), domain.MsgCardInvalid) {
		t.Fatalf("tarjeta inválida: %d %s", rec.Code, rec.Body.String())
	}
	var attempts int
	if err := bank.Pool.QueryRow(context.Background(), `SELECT attempts FROM simbank_charges WHERE id=$1`, ch5).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("una tarjeta inválida no crea intento: %d (%v)", attempts, err)
	}

	// Cargo vencido: 409 «El pago vencio».
	expired := newCharge(t, bank, time.Now().UTC().Add(-time.Minute))
	rec = doJSON(r, "/api/v1/simbank/charges/pay", `{"charge_id":"`+expired+`","card":{"number":"4242424242424242","exp":"12/30","cvc":"123","name":"Lía"}}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), MsgChargeExpired) {
		t.Fatalf("vencido: %d %s", rec.Code, rec.Body.String())
	}
	// Cargo desconocido / id basura: 404.
	rec = doJSON(r, "/api/v1/simbank/charges/lookup", `{"charge_id":"no-es-un-cargo"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("id basura: %d", rec.Code)
	}
}

// D6: el evento del callback es DETERMINISTA por (cargo, intento): dos entregas
// del mismo intento mandan el mismo id (el comercio lo aplica una vez).
func TestBankIT_eventIDDeterministic(t *testing.T) {
	if eventIDForCharge("cargo", 1) != eventIDForCharge("cargo", 1) {
		t.Fatal("el id del evento tiene que ser determinista por (cargo, intento)")
	}
	if eventIDForCharge("cargo", 1) == eventIDForCharge("cargo", 2) {
		t.Fatal("otro intento es otro evento")
	}
	if got, want := eventIDForCharge("cargo", 7), "simbank-cargo-"+strconv.Itoa(7); got != want {
		t.Fatalf("formato: %q", got)
	}
}

// Los reintentos de entrega del MISMO intento no duplican el evento en el
// comercio: el sink falla una vez y el banco reintenta con el mismo id.
func TestBankIT_callbackRetriesSameEventID(t *testing.T) {
	bank, sink := newBank(t)
	sink.fail = 1
	ch := newCharge(t, bank, time.Now().UTC().Add(time.Hour))
	if err := bank.sendCallback(context.Background(), callbackPayload{
		EventID: eventIDForCharge(ch, 1), ChargeID: ch, Reference: "b", Kind: domain.CallbackApproved, AmountCents: 4500,
	}); err != nil {
		t.Fatalf("entrega: %v", err)
	}
	if sink.count() != 1 {
		t.Fatalf("el reintento entrega UNA vez al final: %d", sink.count())
	}
	if got := sink.last(t).EventID; got != eventIDForCharge(ch, 1) {
		t.Fatalf("id: %q", got)
	}
}
