// Sena online con banco simulado (F11 del contrato de la fase 6): la política del
// salón (D2/D3), el monto (D4), el estado de una sena (D2/D7/D9), el libro mayor
// de eventos y el filtro de la pantalla «Señas» (D10).
//
// El dominio no sabe de HTTP, de SQL ni del banco: valida, calcula y decide con
// enteros. El único dinero de F11 vive en estas tablas: NADA de acá toca ventas,
// caja, nómina ni ganancia (D1).
package domain

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// Rangos de la política (D2/D3), los mismos CHECK de la migración 037.
	MinDepositPercent     = 10
	MaxDepositPercent     = 100
	MinDepositHoldMinutes = 5
	MaxDepositHoldMinutes = 120
	MinDepositRefundHours = 0
	MaxDepositRefundHours = 168
	// MinDepositAmountCents y MaxDepositAmountCents acotan el monto de la sena
	// (el techo es el mismo de 010_limits.sql).
	MinDepositAmountCents = 1
	MaxDepositAmountCents = 2000000000
	// MaxDepositDetail es el CHECK de deposit_events.detail.
	MaxDepositDetail = 200
	// DefaultDepositLimit y MaxDepositLimit son el tamaño de página de la
	// pantalla «Señas» (D10, el mismo que la auditoría).
	DefaultDepositLimit = 50
	MaxDepositLimit     = 100
	// PaymentChargeIDLen es el largo del id de un cargo del banco simulado: 32
	// bytes en base64url sin relleno (D8: el id del cargo es la credencial).
	PaymentChargeIDLen = 43
	// maxDepositIDLen acota un id antes de meterlo en una consulta.
	maxDepositIDLen = 100
	// maxDepositCursorLen acota el cursor antes de decodificarlo.
	maxDepositCursorLen = 300
)

// Textos EXACTOS del contrato F11 (D3, D7, D8), sin tildes tal cual están
// escritos.
const (
	MsgDepositPolicyMissing      = "Faltan datos de la sena: activada, porcentaje, espera y devolucion"
	MsgDepositPercentRange       = "El porcentaje debe ser entre 10 y 100"
	MsgDepositHoldRange          = "La espera debe ser entre 5 y 120 minutos"
	MsgDepositRefundRange        = "La devolucion debe ser entre 0 y 168 horas"
	MsgDepositGatewayUnavailable = "El cobro de senas no esta disponible en este servidor"
	MsgDepositInvalidAmount      = "Monto invalido"
	MsgDepositNotFound           = "Pago no encontrado"

	// No están literales en D10 (solo dice «cursor opaco como la auditoría»):
	// siguen el estilo de F10.
	MsgDepositLimitRange    = "El limite debe ser entre 1 y 100"
	MsgDepositCursorInvalid = "Cursor invalido"
	MsgDepositStatusInvalid = "Estado invalido"
)

// Estados de una sena (D2). El estado lo fija SOLO el servidor.
const (
	DepositPending   = "pending"
	DepositPaid      = "paid"
	DepositExpired   = "expired"
	DepositRefunded  = "refunded"
	DepositForfeited = "forfeited"
)

// Tipos de evento del libro mayor (D2, CHECK de deposit_events.kind).
const (
	DepositEventCreated             = "created"
	DepositEventAttemptDeclined     = "attempt_declined"
	DepositEventPaid                = "paid"
	DepositEventExpired             = "expired"
	DepositEventRefunded            = "refunded"
	DepositEventForfeited           = "forfeited"
	DepositEventLatePaymentRefunded = "late_payment_refunded"
)

// Tipos de evento del callback del banco (D5/D7).
const (
	CallbackApproved     = "approved"
	CallbackDeclined     = "declined"
	CallbackInsufficient = "insufficient"
)

// DepositPolicy es la política de señas del salón (a lo sumo una, D2).
type DepositPolicy struct {
	SalonID string
	// Saved es solo de lectura: false = el salón nunca la guardó (D3: enabled
	// false y el resto null). Lo completa el caso de uso.
	Saved       bool
	Enabled     bool
	Percent     int
	HoldMinutes int
	RefundHours int
	UpdatedAt   time.Time
}

// ValidateDepositPercent valida el porcentaje de la sena: 10..100.
func ValidateDepositPercent(percent int) error {
	if percent < MinDepositPercent || percent > MaxDepositPercent {
		return NewMessage(ErrValidation, MsgDepositPercentRange)
	}
	return nil
}

// ValidateDepositHoldMinutes valida la espera del pago: 5..120 minutos.
func ValidateDepositHoldMinutes(minutes int) error {
	if minutes < MinDepositHoldMinutes || minutes > MaxDepositHoldMinutes {
		return NewMessage(ErrValidation, MsgDepositHoldRange)
	}
	return nil
}

// ValidateDepositRefundHours valida la ventana de devolución: 0..168 horas.
func ValidateDepositRefundHours(hours int) error {
	if hours < MinDepositRefundHours || hours > MaxDepositRefundHours {
		return NewMessage(ErrValidation, MsgDepositRefundRange)
	}
	return nil
}

// NewDepositPolicy valida y arma la política del salón. El orden de las reglas es
// el del contrato (porcentaje, espera, devolución) y cada una tiene su texto.
func NewDepositPolicy(salonID string, enabled bool, percent, holdMinutes, refundHours int, now time.Time) (DepositPolicy, error) {
	if !cleanDepositID(salonID) {
		return DepositPolicy{}, fmt.Errorf("%w: la politica de la sena necesita el salon", ErrValidation)
	}
	if err := ValidateDepositPercent(percent); err != nil {
		return DepositPolicy{}, err
	}
	if err := ValidateDepositHoldMinutes(holdMinutes); err != nil {
		return DepositPolicy{}, err
	}
	if err := ValidateDepositRefundHours(refundHours); err != nil {
		return DepositPolicy{}, err
	}
	// A microsegundos, la precisión que guarda Postgres.
	now = now.UTC().Truncate(time.Microsecond)
	return DepositPolicy{
		SalonID:     salonID,
		Saved:       true,
		Enabled:     enabled,
		Percent:     percent,
		HoldMinutes: holdMinutes,
		RefundHours: refundHours,
		UpdatedAt:   now,
	}, nil
}

// DepositAmountCents es la fórmula de D4: (price_cents * percent + 50) / 100 en
// enteros (mitad hacia arriba, sin floats), mínimo 1 centavo y techo
// MaxDepositAmountCents. Precio 0 (o negativo) = no hay sena.
//
// La división entera trunca hacia abajo, y como el +50 es la mitad exacta del
// divisor, el resultado es SIEMPRE el redondeo mitad-hacia-arriba de la cuenta
// real. Nunca se pasa por float: 0.5 centavos de error en un precio grande son
// centavos de verdad.
func DepositAmountCents(priceCents, percent int) int {
	if priceCents <= 0 || percent <= 0 {
		return 0
	}
	amount := (priceCents*percent + 50) / 100
	if amount < MinDepositAmountCents {
		amount = MinDepositAmountCents
	}
	if amount > MaxDepositAmountCents {
		amount = MaxDepositAmountCents
	}
	return amount
}

// ValidDepositStatus dice si el texto es uno de los cinco estados de D2.
func ValidDepositStatus(status string) bool {
	switch status {
	case DepositPending, DepositPaid, DepositExpired, DepositRefunded, DepositForfeited:
		return true
	default:
		return false
	}
}

// DepositSettled dice si la sena ya no puede cambiar de estado: refunded y
// forfeited son finales (settled_at escrito).
func DepositSettled(status string) bool {
	return status == DepositRefunded || status == DepositForfeited
}

// DepositTransitionAllowed dice si la sena puede pasar de `from` a `to`. Es la
// ÚNICA tabla de transiciones (D2/D7/D9):
//
//	pending -> paid       callback aprobado y vigente
//	pending -> expired    el trabajo periódico la venció sin pagar
//	pending -> refunded   pago tardío: el banco aprobó después del vencimiento
//	expired -> refunded   pago tardío: el callback llega después del barrido
//	paid    -> refunded   la reserva se canceló y quedaba tiempo (D9(b))
//	paid    -> forfeited  la reserva se canceló y ya no quedaba tiempo (D9(b))
//
// refunded y forfeited son finales: una sena devuelta no se vuelve a cobrar.
func DepositTransitionAllowed(from, to string) bool {
	switch from {
	case DepositPending:
		return to == DepositPaid || to == DepositExpired || to == DepositRefunded
	case DepositExpired:
		return to == DepositRefunded
	case DepositPaid:
		return to == DepositRefunded || to == DepositForfeited
	default:
		return false
	}
}

// DepositCancelDecision responde D9(b): con la reserva cancelada y la sena
// pagada, ¿se devuelve la sena o se la queda el salón? Se devuelve si faltan AL
// MENOS refundHours horas para el turno; si faltan menos (o el turno ya empezó)
// se pierde. El borde es el de la frase: a las 24 h exactas de un turno, con
// ventana de 24 h, todavía se devuelve.
func DepositCancelDecision(startsAt, now time.Time, refundHours int) string {
	if refundHours < 0 {
		refundHours = 0
	}
	if startsAt.Sub(now.UTC()) >= time.Duration(refundHours)*time.Hour {
		return DepositRefunded
	}
	return DepositForfeited
}

// ValidDepositEventKind dice si el texto es un tipo de evento del libro mayor.
func ValidDepositEventKind(kind string) bool {
	switch kind {
	case DepositEventCreated, DepositEventAttemptDeclined, DepositEventPaid,
		DepositEventExpired, DepositEventRefunded, DepositEventForfeited,
		DepositEventLatePaymentRefunded:
		return true
	default:
		return false
	}
}

// ValidDepositCallbackKind dice si el texto es un `kind` del callback del banco.
func ValidDepositCallbackKind(kind string) bool {
	switch kind {
	case CallbackApproved, CallbackDeclined, CallbackInsufficient:
		return true
	default:
		return false
	}
}

// BookingDeposit es una sena de UN turno (D2).
type BookingDeposit struct {
	ID        string
	SalonID   string
	BookingID string
	// AmountCents es el monto de la sena y Percent la FOTO del porcentaje con
	// el que se calculó (cambiar la política después no la mueve).
	AmountCents int
	Percent     int
	Status      string
	// ChargeID es el cargo del banco: el id es la credencial pública (D8).
	ChargeID   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	PaidAt     *time.Time
	SettledAt  *time.Time
	ClientOpID string
	// Solo lectura: los completa el adaptador con un join al turno. El estado
	// del turno vacío se trata como vivo (solo `cancelled` bloquea un pago).
	BookingStatus BookingStatus
	StartsAt      time.Time
	ClientName    string
}

// Payable dice si un callback aprobado se puede aplicar AHORA (D7): sigue
// pendiente, no venció y la reserva no está cancelada.
//
// Un BookingStatus vacío (adaptador que no hizo el join) cuenta como reserva
// viva: cobrar una sena válida es mejor que reembolsarla por un dato que falta.
func (d BookingDeposit) Payable(now time.Time) bool {
	return d.Status == DepositPending &&
		now.UTC().Before(d.ExpiresAt) &&
		d.BookingStatus != BookingCancelled
}

// LatePayment dice si un callback aprobado llegó tarde (D7): la sena dejó de ser
// pagable y el salón NO se queda con el dinero. Una sena ya resuelta (paid,
// refunded, forfeited) no es un pago tardío: no hay nada que hacer.
func (d BookingDeposit) LatePayment(now time.Time) bool {
	switch d.Status {
	case DepositPending, DepositExpired:
		return !d.Payable(now)
	default:
		return false
	}
}

// DepositEvent es una fila del libro mayor (solo escritura, migración 037).
type DepositEvent struct {
	ID          string
	DepositID   string
	Kind        string
	AmountCents int
	Detail      string
	// BankEventID vacío = NULL (un evento nuestro, no del banco).
	BankEventID string
	CreatedAt   time.Time
}

// DepositCallback es un evento del banco YA verificado por el adaptador (D5):
// la firma no se chequea acá.
type DepositCallback struct {
	EventID     string
	ChargeID    string
	Reference   string
	Kind        string
	AmountCents int
	Now         time.Time
}

// DepositApply es el resultado de aplicar un callback aprobado (D7).
type DepositApply struct {
	// Applied: este evento hizo la transición (false = evento repetido o sena
	// ya resuelta: el reenvío no tiene efecto).
	Applied bool
	// LatePayment: la sena ya no era pagable. El adaptador NO escribe nada en
	// ese caso: quien llama reembolsa con el banco y recién después llama a
	// SettleLateRefund (si se escribiera antes, un reembolso fallido quedaría
	// como «devuelto» para siempre).
	LatePayment bool
	Deposit     BookingDeposit
}

// DepositSettlement es el cierre de una sena por una decisión NUESTRA (no un
// callback nuevo): el barrido de D9(b) o el reembolso de un pago tardío.
type DepositSettlement struct {
	DepositID string
	// Status es el estado final: refunded o forfeited.
	Status string
	// BankEventID es el evento del banco que disparó el cierre; vacío en el
	// camino del barrido (no hay evento del banco).
	BankEventID string
	Detail      string
	Now         time.Time
}

// DepositOrder es lo que la reserva pública pide (D8): el precio que Book ya
// calculó, el concepto que muestra el banco y la clave de idempotencia.
type DepositOrder struct {
	SalonID     string
	BookingID   string
	PriceCents  int
	ClientOpID  string
	Description string
}

// NewBookingDeposit valida y arma la sena PENDIENTE de un turno: el monto de D4
// con la foto del porcentaje y el vencimiento ahora + espera. NO persiste nada.
//
// El id del cargo lo emite el banco (adaptador) y tiene que tener la forma de
// D8: es la credencial pública que el navegador manda en el fragmento de la URL.
func NewBookingDeposit(order DepositOrder, policy DepositPolicy, chargeID string, now time.Time) (BookingDeposit, error) {
	if !cleanDepositID(order.SalonID) || !cleanDepositID(order.BookingID) {
		return BookingDeposit{}, fmt.Errorf("%w: la sena necesita el salon y el turno", ErrValidation)
	}
	// La política llega de la base: se revalida lo que va a la fila.
	if err := ValidateDepositPercent(policy.Percent); err != nil {
		return BookingDeposit{}, err
	}
	if err := ValidateDepositHoldMinutes(policy.HoldMinutes); err != nil {
		return BookingDeposit{}, err
	}
	amount := DepositAmountCents(order.PriceCents, policy.Percent)
	if amount < MinDepositAmountCents {
		return BookingDeposit{}, fmt.Errorf("%w: la sena tiene que ser de al menos %d centavo", ErrValidation, MinDepositAmountCents)
	}
	chargeID = strings.TrimSpace(chargeID)
	if !ValidDepositChargeID(chargeID) {
		return BookingDeposit{}, fmt.Errorf("%w: el cargo del banco no es valido", ErrValidation)
	}
	opID, err := ValidatePackageOpID(order.ClientOpID)
	if err != nil {
		return BookingDeposit{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	return BookingDeposit{
		ID:          NewID(),
		SalonID:     order.SalonID,
		BookingID:   order.BookingID,
		AmountCents: amount,
		Percent:     policy.Percent,
		Status:      DepositPending,
		ChargeID:    chargeID,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Duration(policy.HoldMinutes) * time.Minute),
		ClientOpID:  opID,
	}, nil
}

// BookingNotice es lo que necesita el correo de confirmación de una reserva con
// sena pagada (D7): el salón, el turno y el email de la clienta. El email vacío
// es una reserva anónima: no hay a quién avisar.
type BookingNotice struct {
	SalonID     string
	BookingID   string
	SalonName   string
	ClientName  string
	ClientEmail string
	StartsAt    time.Time
}

// DepositLookup es la respuesta pública de D8 (el id del cargo es la
// credencial): estado, monto, vencimiento y horario del turno.
type DepositLookup struct {
	Status      string
	AmountCents int
	ExpiresAt   time.Time
	StartsAt    time.Time
}

// NewDepositChargeID genera el id de un cargo del banco simulado: 32 bytes de
// crypto/rand en base64url sin relleno (43 caracteres, D8).
func NewDepositChargeID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("sena: generar el id del cargo: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// ValidDepositChargeID dice si el valor PUEDE ser un id de cargo: exactamente 43
// caracteres del alfabeto base64url. Se chequea antes de consultar la base, así
// un valor basura (vacío, largo, con NUL, con relleno) nunca llega a una query.
func ValidDepositChargeID(id string) bool {
	if len(id) != PaymentChargeIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// DepositCursor es la posición de una página de la pantalla «Señas»: el último
// deposit ya devuelto. El orden es (created_at DESC, id DESC), así que la página
// siguiente son las filas ESTRICTAMENTE anteriores a (CreatedAt, ID).
type DepositCursor struct {
	CreatedAt time.Time
	ID        string
}

// DepositQuery es una consulta ya validada de GET /salons/:id/deposits.
type DepositQuery struct {
	Limit int
	// Status vacío = todos; si no, uno de los cinco estados.
	Status string
	Before *DepositCursor
}

// EncodeDepositCursor arma el cursor opaco: base64url sin relleno de
// «<created_at RFC3339Nano UTC>|<id>».
func EncodeDepositCursor(c DepositCursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeDepositCursor valida un cursor: solo acepta la forma que produce
// EncodeDepositCursor (base64url sin relleno, fecha canónica, id limpio). Todo lo
// demás es 400 «Cursor invalido».
func DecodeDepositCursor(s string) (DepositCursor, error) {
	invalid := NewMessage(ErrValidation, MsgDepositCursorInvalid)
	if s == "" || len(s) > maxDepositCursorLen {
		return DepositCursor{}, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return DepositCursor{}, invalid
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || !cleanDepositID(id) {
		return DepositCursor{}, invalid
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return DepositCursor{}, invalid
	}
	c := DepositCursor{CreatedAt: at.UTC(), ID: id}
	if EncodeDepositCursor(c) != s {
		return DepositCursor{}, invalid
	}
	return c, nil
}

// ParseDepositQuery valida los tres parámetros de la pantalla (los textos tal
// cual llegan; vacío = no se pasó). El orden de las reglas es: límite, estado,
// cursor.
func ParseDepositQuery(status, limit, before string) (DepositQuery, error) {
	q := DepositQuery{Limit: DefaultDepositLimit}
	if limit != "" {
		n, ok := depositDecimals(limit, 4)
		if !ok || n < 1 || n > MaxDepositLimit {
			return DepositQuery{}, NewMessage(ErrValidation, MsgDepositLimitRange)
		}
		q.Limit = int(n)
	}
	// Solo se recortan espacios de los bordes: un salto de línea es un 400.
	status = strings.Trim(status, " ")
	if status != "" {
		if !ValidDepositStatus(status) {
			return DepositQuery{}, NewMessage(ErrValidation, MsgDepositStatusInvalid)
		}
		q.Status = status
	}
	if before != "" {
		c, err := DecodeDepositCursor(before)
		if err != nil {
			return DepositQuery{}, err
		}
		q.Before = &c
	}
	return q, nil
}

// cleanDepositID dice si un id es usable: un NUL o un UTF-8 inválido llegaría a
// Postgres como un 500 (no cabe en un TEXT); es un recurso que no existe.
func cleanDepositID(id string) bool {
	if id == "" || !utf8.ValidString(id) || utf8.RuneCountInString(id) > maxDepositIDLen {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// depositDecimals parsea un entero sin signo de solo dígitos ASCII y acota el
// largo para que no desborde.
func depositDecimals(s string, maxLen int) (int64, bool) {
	if s == "" || len(s) > maxLen {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}
