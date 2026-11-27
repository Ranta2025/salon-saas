package ports

import (
	"context"
	"net/http"
	"salon-saas/internal/domain"
	"time"
)

// ===== F11: sena online con banco simulado (inicio) =====
//
// Este bloque es el contrato que el adaptador Postgres (migración 037), el banco
// simulado (internal/simbank) y las rutas de la SLICE 2 implementan. Todo el
// dinero de F11 vive en tablas PROPIAS: ninguna consulta de acá toca ventas,
// caja, nómina, ganancia ni exportes (D1).

// DepositPolicyRepository: la política de señas del salón (migración 037, a lo
// sumo UNA fila por salón). Toda consulta va scopeada por salon_id.
type DepositPolicyRepository interface {
	// Find lee la política. ErrNotFound si el salón nunca la guardó (el caso de
	// uso lo traduce a Saved=false: enabled false y el resto null, D3).
	Find(salonID string) (domain.DepositPolicy, error)
	// Save es UN upsert que reemplaza enabled, percent, hold_minutes,
	// refund_hours y updated_at. No toca ninguna otra tabla y devuelve la fila
	// tal como quedó guardada (Saved=true).
	Save(p domain.DepositPolicy) (domain.DepositPolicy, error)
}

// DepositRepository: las señas de las reservas y su libro mayor (booking_deposits
// y deposit_events, migración 037).
//
// Invariantes del adaptador:
//   - TODA lectura y escritura va en UNA transacción con
//     `pg_advisory_xact_lock('deposit:' || <id de la sena>)` cuando toca una sena
//     concreta (Apply*, Settle*): así dos callbacks del mismo cargo no se
//     intercalan ni con el barrido.
//   - todo cambio de estado es UN solo `UPDATE ... WHERE status = <el de origen>`
//     (o `IN (...)` en el pago tardío) y, en la MISMA transacción, inserta su
//     evento en deposit_events. Si el UPDATE no afecta filas, NO se inserta el
//     evento y el resultado dice que no se aplicó: el reintento no tiene efecto.
//   - `deposit_events` es APPEND-ONLY (trigger de la migración 037, igual que
//     audit_events: solo se borra en cascada con la sena y el salón) y su índice
//     único parcial `bank_event_id` es la guarda de "el evento del banco se aplica
//     UNA sola vez".
//   - las lecturas que devuelven BookingDeposit completan SIEMPRE los campos de
//     solo lectura del turno (BookingStatus, StartsAt, ClientName) con un join:
//     el caso de uso decide con ellos sin volver a consultar.
//   - un id con NUL o UTF-8 inválido nunca llega a una query: el caso de uso lo
//     resuelve como 404 antes de llamar.
type DepositRepository interface {
	// Create inserta la sena y su evento `created` en UNA transacción. Es
	// idempotente por las dos claves del contrato: con el MISMO client_op_id (o
	// para el MISMO booking_id, que es UNIQUE) devuelve la sena original SIN
	// escribir nada y SIN error. Dos altas simultáneas: una gana y la otra
	// recibe ErrAlreadyExists (el caso de uso vuelve a leer y devuelve la
	// original; el cargo huérfano que alcanzó a crear el banco vence solo).
	Create(d domain.BookingDeposit) (domain.BookingDeposit, error)
	// FindByBooking resuelve la sena de un turno del salón. ErrNotFound si ese
	// turno no tiene sena (o es de otro salón). Es la lectura de la idempotencia
	// de D8: el reintento del navegador devuelve el MISMO payment.
	FindByBooking(salonID, bookingID string) (domain.BookingDeposit, error)
	// FindByCharge resuelve una sena por el id del cargo. NO va scopeada por
	// salón a propósito: el id del cargo es la credencial pública (D8) y el
	// callback del banco no sabe de qué salón es. ErrNotFound si no existe.
	FindByCharge(chargeID string) (domain.BookingDeposit, error)
	// ApplyApproved aplica un evento `approved` (D7) bajo el lock de la sena:
	//   - si el index único de bank_event_id ya tiene ese evento: Applied=false,
	//     sin efecto (el reenvío del banco es 200);
	//   - si la sena es pagable (domain.BookingDeposit.Payable): UN UPDATE
	//     condicional `status='pending'` -> `paid` con paid_at y el evento
	//     `paid` -> Applied=true;
	//   - si ya está resuelta (paid/refunded/forfeited): no escribe nada,
	//     Applied=false;
	//   - si llegó tarde (vencida o reserva cancelada): NO ESCRIBE NADA y
	//     devuelve LatePayment=true. Quien llama pide el reembolso al banco y
	//     recién después llama a SettleLateRefund: escribir antes dejaría una
	//     sena «devuelta» cuyo reembolso falló y no se reintenta nunca.
	ApplyApproved(r domain.DepositCallback) (domain.DepositApply, error)
	// ApplyDeclined registra un intento rechazado (`declined` o `insufficient`,
	// D7): evento `attempt_declined` con amount_cents y la MISMA guarda de
	// idempotencia por bank_event_id. La sena SIGUE `pending` (el cliente puede
	// reintentar hasta que venza). false = ese evento ya se había registrado.
	ApplyDeclined(r domain.DepositCallback) (bool, error)
	// ExpirePending vence hasta `limit` señas `pending` con expires_at <= now, en
	// UNA transacción: UN `UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP
	// LOCKED) ... RETURNING` a `expired` con su evento `expired`, y devuelve las
	// filas que acaba de vencer. Debe devolver ADEMÁS las señas que ya están
	// `expired` y cuyo turno sigue `booked` (un tick anterior no alcanzó a
	// cancelarlo): el caso de uso reintenta la cancelación y el turno no queda
	// retenido para siempre. Nunca devuelve señas con el turno ya cerrado.
	ExpirePending(now time.Time, limit int) ([]domain.BookingDeposit, error)
	// ListPaidCancelled trae hasta `limit` señas `paid` cuyo turno quedó
	// `cancelled`, las más viejas primero (D9(b)). `done` y `no_show` NO entran
	// nunca: esas no se reembolsan.
	ListPaidCancelled(limit int) ([]domain.BookingDeposit, error)
	// SettleLateRefund cierra una sena que estaba `pending` o `expired` (pago
	// tardío de D7): UN UPDATE condicional a `refunded` con settled_at y el
	// evento `late_payment_refunded` (bank_event_id con su guarda única).
	// false = ya estaba cerrada (otro callback o el barrido ganaron). El
	// reembolso al banco lo pidió quien llama ANTES, y es idempotente por cargo.
	SettleLateRefund(s domain.DepositSettlement) (bool, error)
	// SettlePaidCancelled cierra una sena `paid` cuyo turno se canceló (D9(b)):
	// UN UPDATE condicional `status='paid'` -> s.Status (`refunded` o
	// `forfeited`) con settled_at y el evento del mismo nombre. false = ya
	// estaba cerrada. El reembolso (cuando toca) lo pidió quien llama antes.
	SettlePaidCancelled(s domain.DepositSettlement) (bool, error)
	// List trae la pantalla «Señas» (D10) del salón: de la más nueva a la más
	// vieja por (created_at DESC, id DESC), a lo sumo q.Limit, opcionalmente
	// filtrada por estado y ESTRICTAMENTE anterior al cursor q.Before. Aislamiento
	// por salon_id obligatorio.
	List(salonID string, q domain.DepositQuery) ([]domain.BookingDeposit, error)
}

// ChargeRequest es lo que el comercio le pide al banco (D5).
type ChargeRequest struct {
	// Reference es el id del turno: el banco lo devuelve en el callback.
	Reference   string
	AmountCents int
	Description string
	ExpiresAt   time.Time
}

// Charge es lo que el banco devuelve al crear el cargo.
type Charge struct {
	// ID es la credencial pública (43 caracteres base64url, D8): el caso de uso
	// la guarda en booking_deposits.charge_id.
	ID string
	// PayPath es la ruta de la página de pago del banco (para armar pay_url).
	PayPath string
}

// CallbackEvent es un evento del banco YA verificado y parseado (D5).
type CallbackEvent struct {
	EventID     string
	ChargeID    string
	Reference   string
	Kind        string
	AmountCents int
}

// PaymentGateway es el banco (D5). La verificación de la firma vive COMPLETA en
// el adaptador: el caso de uso nunca ve bytes sin verificar.
type PaymentGateway interface {
	// Enabled dice si hay banco configurado (PAYMENT_SIM_SECRET). Con false las
	// rutas del banco y el callback no existen (404), no se puede activar la
	// política (409) y el trabajo periódico no corre (D12).
	Enabled() bool
	// CreateCharge crea el cargo. El id que devuelve tiene que ser el de D8
	// (domain.NewDepositChargeID). Un error se propaga: la reserva se cancela por
	// el camino existente y el cliente recibe el error de servidor de hoy (D8).
	CreateCharge(ctx context.Context, r ChargeRequest) (Charge, error)
	// Refund devuelve el dinero del cargo y es IDEMPOTENTE por cargo:
	// reembolsar dos veces devuelve nil sin duplicar (lo garantiza el banco
	// simulado con su tabla simbank_refunds). Lo usan el pago tardío (D7) y el
	// barrido (D9(b)); por eso el orden «reembolsar y después cerrar» es seguro.
	Refund(ctx context.Context, chargeID string, amountCents int, reason string) error
	// ParseCallback verifica la firma (`X-Simbank-Signature`, +-300 s, tiempo
	// constante) y devuelve el evento. Cualquier motivo de rechazo es el MISMO
	// error uniforme (domain.MsgPaymentSignatureInvalid, 400). Un cuerpo de más de
	// 8 KB se rechaza antes de parsear.
	ParseCallback(header http.Header, body []byte, now time.Time) (CallbackEvent, error)
}

// NoopGateway es el banco que NO está configurado (D5/D12). No crea cargos, no
// reembolsa nada y no valida callbacks: sin PAYMENT_SIM_SECRET no hay cobro.
type NoopGateway struct{}

// Enabled: sin banco configurado siempre false.
func (NoopGateway) Enabled() bool { return false }

// CreateCharge es imposible sin banco: el 409 del contrato (D3).
func (NoopGateway) CreateCharge(context.Context, ChargeRequest) (Charge, error) {
	return Charge{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
}

// Refund es imposible sin banco y NO puede devolver nil: un reembolso que no se
// hizo nunca puede parecer exitoso (quien llama cerraría la seña como «devuelta»
// sin haber devuelto el dinero). Se devuelve el mismo 409 de D3.
func (NoopGateway) Refund(context.Context, string, int, string) error {
	return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
}

// ParseCallback rechaza todo: sin banco no hay firma que valga.
func (NoopGateway) ParseCallback(http.Header, []byte, time.Time) (CallbackEvent, error) {
	return CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
}

var _ PaymentGateway = NoopGateway{}

// BookingCanceller cancela un turno por el camino EXISTENTE de la agenda (D9):
// `domain.Booking.SetStatus(cancelled)` + el UPDATE del repositorio de reservas
// bajo el lock de escrituras del salón, con sus eventos y avisos. El trabajo
// periódico no tiene sesión ni actor, así que este puerto es la única puerta.
//
// Contrato: cancelar un turno que YA está cerrado (cancelled/done/no_show) es
// nil, no error: el barrido no reintenta ni ensucia el log. Un turno que no
// existe también es nil (su sena se fue en cascada). Cualquier otro error se
// devuelve al trabajo periódico, que lo loguea y reintenta en el próximo tick.
type BookingCanceller interface {
	CancelBooking(salonID, bookingID string, now time.Time) error
}

// BookingNoticeRepository resuelve lo que necesita el correo de confirmación de
// una reserva con sena pagada (D7) sin tocar BookingEmailRepository (que no
// tiene lectura): el nombre del salón, el turno y el email de la clienta.
type BookingNoticeRepository interface {
	// Notice trae el aviso del turno. ErrNotFound si el turno no existe o es de
	// otro salón. ClientEmail vacío = reserva anónima (no hay a quién avisar).
	Notice(salonID, bookingID string) (domain.BookingNotice, error)
}

// ===== F11: sena online con banco simulado (fin) =====
