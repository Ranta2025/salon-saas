// Package simbank es el banco SIMULADO de F11 (D6): el adaptador de
// ports.PaymentGateway que habla con las tablas `simbank_charges` /
// `simbank_refunds` (migración 037) y firma el callback del comercio con HMAC.
//
// Deployable aparte, montado en el mismo binario: sus rutas son públicas y viven
// bajo /api/v1/simbank/. Todo lo que toca dinero está acá y en el puerto; el
// resto del sistema no sabe que el banco es simulado.
//
// Reglas de seguridad (D6):
//   - el NÚMERO de tarjeta nunca se guarda ni se loguea: solo `last4` y marca,
//     y la tarjeta viaja en una struct de campos no exportados (domain.BankCard);
//   - la firma `X-Simbank-Signature: t=<unix>,v1=<hex>` cubre "<t>.<cuerpo>",
//     se compara en tiempo constante y se acepta solo dentro de +-300 s
//     (domain.VerifyPaymentSignature);
//   - el evento del callback es DETERMINISTA por (cargo, intento): los tres
//     reintentos de entrega del mismo intento mandan el MISMO id y el índice
//     único del comercio lo aplica una sola vez.
package simbank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

const (
	// SignatureHeader es la cabecera firmada del banco (D6).
	SignatureHeader = "X-Simbank-Signature"
	// MaxCallbackBody es el tope del cuerpo del callback del comercio: 8 KB
	// (D7). Un cuerpo más grande se rechaza ANTES de parsear.
	MaxCallbackBody = 8 << 10
	// callbackAttempts y callbackBackoff: 3 entregas con espera corta (D6).
	callbackAttempts = 3
	callbackBackoff  = 100 * time.Millisecond
	// callbackTimeout es el tope de CADA entrega al comercio (D6).
	callbackTimeout = 5 * time.Second
	// payPath es la página del banco: /pagar#<charge_id> (D8).
	payPath = "/pagar"
)

// Bank es el banco simulado: el PaymentGateway y el almacenamiento de sus
// cargos. Con Secret vacío (o Pool nil) queda DESHABILITADO y todas las rutas
// del banco y el callback responden 404 (D12).
type Bank struct {
	Pool *pgxpool.Pool
	// Secret es PAYMENT_SIM_SECRET: firma el callback.
	Secret string
	// CallbackURL es la URL del callback del comercio (PAYMENT_CALLBACK_URL).
	CallbackURL string
	// Client es el cliente HTTP de la entrega del callback (nil = uno con el
	// tiempo de espera de D6). Los tests inyectan uno propio.
	Client *http.Client
	// Sleep es la espera entre reintentos (nil = time.Sleep).
	Sleep func(time.Duration)
	// Now es el reloj (nil = time.Now); los tests lo fijan.
	Now func() time.Time
}

func (b *Bank) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

// Enabled dice si el banco está configurado.
func (b *Bank) Enabled() bool {
	return b != nil && b.Pool != nil && strings.TrimSpace(b.Secret) != ""
}

func (b *Bank) client() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return &http.Client{Timeout: callbackTimeout}
}

// CreateCharge crea un cargo pendiente y devuelve su id (la credencial pública,
// D8) y la ruta de la página de pago.
func (b *Bank) CreateCharge(ctx context.Context, r ports.ChargeRequest) (ports.Charge, error) {
	if !b.Enabled() {
		return ports.Charge{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
	}
	if r.AmountCents < domain.MinDepositAmountCents || r.AmountCents > domain.MaxDepositAmountCents {
		return ports.Charge{}, domain.NewMessage(domain.ErrValidation, domain.MsgDepositInvalidAmount)
	}
	reference := strings.TrimSpace(r.Reference)
	if reference == "" || len(reference) > 100 {
		return ports.Charge{}, fmt.Errorf("%w: el cargo necesita la referencia del turno", domain.ErrValidation)
	}
	id, err := domain.NewDepositChargeID()
	if err != nil {
		return ports.Charge{}, err
	}
	description := strings.TrimSpace(r.Description)
	if len(description) > 200 {
		description = description[:200]
	}
	if _, err := b.Pool.Exec(ctx,
		`INSERT INTO simbank_charges(id,amount_cents,reference,description,status,attempts,expires_at,created_at)
		 VALUES($1,$2,$3,NULLIF($4,''),$5,0,$6,$7)`,
		id, r.AmountCents, reference, description, "pending", r.ExpiresAt.UTC(), b.now()); err != nil {
		return ports.Charge{}, err
	}
	return ports.Charge{ID: id, PayPath: payPath + "#" + id}, nil
}

// Refund devuelve el dinero del cargo y es IDEMPOTENTE por cargo (D5): la tabla
// simbank_refunds tiene UNIQUE(charge_id), así que reembolsar dos veces devuelve
// nil sin duplicar. Sin banco configurado devuelve ERROR (nunca nil): un
// reembolso que no se hizo no puede parecer exitoso.
func (b *Bank) Refund(ctx context.Context, chargeID string, amountCents int, reason string) error {
	if !b.Enabled() {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
	}
	if !domain.ValidDepositChargeID(chargeID) {
		return domain.NewMessage(domain.ErrValidation, domain.MsgDepositNotFound)
	}
	if amountCents < domain.MinDepositAmountCents || amountCents > domain.MaxDepositAmountCents {
		return domain.NewMessage(domain.ErrValidation, domain.MsgDepositInvalidAmount)
	}
	if len(reason) > 200 {
		reason = reason[:200]
	}
	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM simbank_charges WHERE id=$1 FOR UPDATE`, chargeID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
		}
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO simbank_refunds(id,charge_id,amount_cents,reason,created_at)
		 VALUES($1,$2,$3,$4,$5) ON CONFLICT (charge_id) DO NOTHING`,
		domain.NewID(), chargeID, amountCents, reason, b.now()); err != nil {
		return err
	}
	// El cargo reembolsado deja de ser cobrable; si ya estaba reembolsado, el
	// UPDATE no cambia nada (idempotencia).
	if _, err := tx.Exec(ctx,
		`UPDATE simbank_charges SET status='refunded', decided_at=COALESCE(decided_at,$2)
		 WHERE id=$1 AND status IN ('approved','pending')`, chargeID, b.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// callbackPayload es el cuerpo firmado que el banco le manda al comercio (D6).
// El comercio verifica la firma ANTES de parsearlo.
type callbackPayload struct {
	EventID     string `json:"event_id"`
	ChargeID    string `json:"charge_id"`
	Reference   string `json:"reference"`
	Kind        string `json:"kind"`
	AmountCents int    `json:"amount_cents"`
}

// ParseCallback verifica la firma y devuelve el evento (D5/D6). Cualquier motivo
// de rechazo es el MISMO error uniforme (400 «Firma invalida»): firma mala, sin
// cabecera, formato malo, t fuera de +-300 s o cuerpo alterado.
func (b *Bank) ParseCallback(header http.Header, body []byte, now time.Time) (ports.CallbackEvent, error) {
	if !b.Enabled() || len(body) > MaxCallbackBody {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
	}
	if err := domain.VerifyPaymentSignature(b.Secret, header.Get(SignatureHeader), body, now); err != nil {
		return ports.CallbackEvent{}, err
	}
	var p callbackPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
	}
	return ports.CallbackEvent{
		EventID:     p.EventID,
		ChargeID:    p.ChargeID,
		Reference:   p.Reference,
		Kind:        p.Kind,
		AmountCents: p.AmountCents,
	}, nil
}

// eventIDForCharge es el id DETERMINISTA del evento de un intento: el mismo
// (cargo, intento) siempre produce el mismo id, así los tres reintentos de
// entrega no aplican dos veces el mismo cobro.
func eventIDForCharge(chargeID string, attempt int) string {
	return "simbank-" + chargeID + "-" + strconv.Itoa(attempt)
}

// sendCallback entrega el evento al comercio con 3 intentos y espera corta
// (D6). El cuerpo es el MISMO en los tres: la firma se rearma con el instante de
// cada intento (la ventana de +-300 s del comercio).
func (b *Bank) sendCallback(ctx context.Context, p callbackPayload) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	sleep := b.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var lastErr error
	for attempt := 0; attempt < callbackAttempts; attempt++ {
		if attempt > 0 {
			sleep(callbackBackoff)
		}
		if err := b.postCallback(ctx, body); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("simbank: el comercio no recibió el callback")
	}
	return lastErr
}

// postCallback es UNA entrega firmada, con el tope de 5 s de D6.
func (b *Bank) postCallback(ctx context.Context, body []byte) error {
	url := strings.TrimSpace(b.CallbackURL)
	if url == "" {
		return errors.New("simbank: falta PAYMENT_CALLBACK_URL")
	}
	reqCtx, cancel := context.WithTimeout(ctx, callbackTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, domain.SignPayment(b.Secret, b.now(), body))
	resp, err := b.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// El cuerpo de la respuesta no se guarda (no trae nada que el banco use) y
	// se descarta acotado para poder reutilizar la conexión.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("simbank: el comercio respondio %d", resp.StatusCode)
	}
	return nil
}

var (
	_ ports.PaymentGateway = (*Bank)(nil)
)
