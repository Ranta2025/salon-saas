package simbank

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"salon-saas/internal/domain"
)

// Textos EXACTOS de D6 (sin tildes tal cual están escritos).
const (
	MsgChargeExpired      = "El pago vencio"
	MsgChargeAlreadyPayed = "Este pago ya se aprobo"
	MsgTooManyAttempts    = "Demasiados intentos"
	MsgChargeNotFound     = "Pago no encontrado"
	MsgChargeUnavailable  = "No se pudo avisar al comercio"
)

// maxAttempts es el tope de intentos de un cargo (D6): después de 5 intentos
// rechazados el banco responde 429.
const maxAttempts = 5

// Handler monta las DOS rutas públicas del banco simulado (D6):
//
//	POST /api/v1/simbank/charges/lookup  {charge_id}
//	POST /api/v1/simbank/charges/pay     {charge_id, card:{number,exp,cvc,name}}
//
// El estado del pago lo fija SOLO el servidor: la página del banco nunca decide
// nada por su cuenta.
type Handler struct {
	Bank *Bank
}

// charge es la fila del banco que las dos rutas necesitan.
type charge struct {
	id          string
	amountCents int
	reference   string
	description string
	status      string
	attempts    int
	expiresAt   time.Time
}

func (h Handler) now() time.Time {
	if h.Bank != nil && h.Bank.Now != nil {
		return h.Bank.Now().UTC()
	}
	return time.Now().UTC()
}

func badBody(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
}

// enabledOr404: sin banco configurado la ruta NO existe (D12).
func (h Handler) enabledOr404(c *gin.Context) bool {
	if h.Bank == nil || !h.Bank.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": MsgChargeNotFound})
		return false
	}
	return true
}

// readCharge lee un cargo por id. El id tiene la forma de D8: un valor basura no
// consulta la base (404 uniforme).
func (h Handler) readCharge(ctx context.Context, chargeID string) (charge, error) {
	var ch charge
	err := h.Bank.Pool.QueryRow(ctx,
		`SELECT id,amount_cents,reference,COALESCE(description,''),status,attempts,expires_at
		 FROM simbank_charges WHERE id=$1`, chargeID,
	).Scan(&ch.id, &ch.amountCents, &ch.reference, &ch.description, &ch.status, &ch.attempts, &ch.expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return charge{}, domain.NewMessage(domain.ErrNotFound, MsgChargeNotFound)
		}
		return charge{}, err
	}
	ch.expiresAt = ch.expiresAt.UTC()
	return ch, nil
}

// Lookup: POST /api/v1/simbank/charges/lookup {charge_id} -> 200
// {charge_id, amount_cents, description, status, expires_at, simulated:true}.
func (h Handler) Lookup(c *gin.Context) {
	if !h.enabledOr404(c) {
		return
	}
	var in struct {
		ChargeID string `json:"charge_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	chargeID := strings.TrimSpace(in.ChargeID)
	if !domain.ValidDepositChargeID(chargeID) {
		c.JSON(http.StatusNotFound, gin.H{"error": MsgChargeNotFound})
		return
	}
	ch, err := h.readCharge(c.Request.Context(), chargeID)
	if err != nil {
		respondBankErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"charge_id":    ch.id,
		"amount_cents": ch.amountCents,
		"description":  ch.description,
		"status":       ch.status,
		"expires_at":   ch.expiresAt.Format(time.RFC3339),
		"simulated":    true,
	})
}

// Pay: POST /api/v1/simbank/charges/pay {charge_id, card} (D6).
//
// Orden: cargo, vencimiento, ya aprobado, tope de intentos, tarjeta. Un dato de
// tarjeta inválido es 400 SIN crear intento; el intento se incrementa con UN
// UPDATE condicional (attempts < 5) para que dos pagos simultáneos no se salten
// el tope. El número NUNCA se guarda ni se loguea: solo last4 y marca.
func (h Handler) Pay(c *gin.Context) {
	if !h.enabledOr404(c) {
		return
	}
	var in struct {
		ChargeID string `json:"charge_id"`
		Card     struct {
			Number string `json:"number"`
			Exp    string `json:"exp"`
			CVC    string `json:"cvc"`
			Name   string `json:"name"`
		} `json:"card"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	chargeID := strings.TrimSpace(in.ChargeID)
	if !domain.ValidDepositChargeID(chargeID) {
		c.JSON(http.StatusNotFound, gin.H{"error": MsgChargeNotFound})
		return
	}
	now := h.now()
	// Estado del cargo ANTES de tocar la tarjeta: vencido/ya aprobado/tope.
	ch, err := h.readCharge(c.Request.Context(), chargeID)
	if err != nil {
		respondBankErr(c, err)
		return
	}
	switch {
	case ch.status == "approved" || ch.status == "refunded":
		c.JSON(http.StatusConflict, gin.H{"error": MsgChargeAlreadyPayed})
		return
	case ch.attempts >= maxAttempts:
		c.JSON(http.StatusTooManyRequests, gin.H{"error": MsgTooManyAttempts})
		return
	case !now.Before(ch.expiresAt):
		c.JSON(http.StatusConflict, gin.H{"error": MsgChargeExpired})
		return
	}
	// La tarjeta se valida ANTES de crear el intento (D6): un dato inválido no
	// cuenta como intento rechazado.
	card := domain.NewBankCard(in.Card.Number, in.Card.Exp, in.Card.CVC, in.Card.Name)
	decision, err := domain.DecideCard(card, now)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": domain.MsgCardInvalid})
		return
	}
	attempt, err := h.recordAttempt(c.Request.Context(), chargeID, decision, now)
	if err != nil {
		respondBankErr(c, err)
		return
	}
	// El callback va DESPUÉS del commit: el comercio lo aplica una sola vez y el
	// pago ya es un hecho del banco.
	payload := callbackPayload{
		EventID:     eventIDForCharge(chargeID, attempt),
		ChargeID:    chargeID,
		Reference:   ch.reference,
		Kind:        decision.Outcome,
		AmountCents: ch.amountCents,
	}
	// La entrega NO cuelga del contexto del pedido: si la persona cierra la
	// página, el aviso al comercio tiene que completarse igual (el pago ya es un
	// hecho del banco). Se acota con su propio tope.
	deliverCtx, cancel := context.WithTimeout(context.Background(), 3*callbackTimeout)
	defer cancel()
	if err := h.Bank.sendCallback(deliverCtx, payload); err != nil {
		// El cobro quedó decidido en el banco pero el comercio no se enteró. Se
		// responde el error para que la persona lo vea: el estado del pago lo
		// sigue fijando solo el callback (limitación conocida de la simulación).
		c.JSON(http.StatusInternalServerError, gin.H{"error": MsgChargeUnavailable})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"charge_id":    chargeID,
		"status":       decision.Outcome,
		"message":      decision.Message,
		"amount_cents": ch.amountCents,
		"last4":        decision.Last4,
		"brand":        decision.Brand,
		"simulated":    true,
	})
}

// recordAttempt incrementa intentos y deja el resultado del intento con UN
// UPDATE condicional bajo el row lock: dos pagos simultáneos del mismo cargo se
// serializan y nunca pasan de maxAttempts.
func (h Handler) recordAttempt(ctx context.Context, chargeID string, decision domain.CardDecision, now time.Time) (int, error) {
	tx, err := h.Bank.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var attempts int
	var expiresAt time.Time
	if err := tx.QueryRow(ctx,
		`SELECT status,attempts,expires_at FROM simbank_charges WHERE id=$1 FOR UPDATE`, chargeID,
	).Scan(&status, &attempts, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, domain.NewMessage(domain.ErrNotFound, MsgChargeNotFound)
		}
		return 0, err
	}
	switch {
	case status == "approved" || status == "refunded":
		return 0, domain.NewMessage(domain.ErrAlreadyExists, MsgChargeAlreadyPayed)
	case attempts >= maxAttempts:
		return 0, domain.NewMessage(domain.ErrAlreadyExists, MsgTooManyAttempts)
	case !now.Before(expiresAt.UTC()):
		return 0, domain.NewMessage(domain.ErrAlreadyExists, MsgChargeExpired)
	}
	var next int
	if err := tx.QueryRow(ctx,
		`UPDATE simbank_charges
		 SET attempts=attempts+1, status=$2, last4=$3, brand=$4, decided_at=$5
		 WHERE id=$1 AND attempts < $6 AND status IN ('pending','declined','insufficient')
		 RETURNING attempts`,
		chargeID, decision.Outcome, decision.Last4, decision.Brand, now, maxAttempts).Scan(&next); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, domain.NewMessage(domain.ErrAlreadyExists, MsgTooManyAttempts)
		}
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return next, nil
}

// respondBankErr traduce lo que devuelven las consultas del banco. Un 409 con la
// frase del contrato es lo que la página muestra verbatim.
func respondBankErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": MsgChargeNotFound})
	case errors.Is(err, domain.ErrAlreadyExists):
		text := err.Error()
		switch {
		case strings.Contains(text, MsgChargeAlreadyPayed):
			c.JSON(http.StatusConflict, gin.H{"error": MsgChargeAlreadyPayed})
		case strings.Contains(text, MsgChargeExpired):
			c.JSON(http.StatusConflict, gin.H{"error": MsgChargeExpired})
		case strings.Contains(text, MsgTooManyAttempts):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": MsgTooManyAttempts})
		default:
			c.JSON(http.StatusConflict, gin.H{"error": text})
		}
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		// Nunca detalles internos al cliente.
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error interno"})
	}
}
