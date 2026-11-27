// DepositService: la sena online de una reserva (F11 del contrato de la fase 6).
//
// La sena vive en tablas PROPIAS (booking_deposits + deposit_events) y el estado
// del pago lo fija SOLO el servidor: el navegador nunca dice «pagué» (D7). Los
// caminos son:
//
//   - CreateForBooking: la reserva pública pide su sena (monto de D4, cargo en el
//     banco, fila pendiente). Idempotente por el turno y por client_op_id: el
//     reintento del navegador devuelve el MISMO payment y UN solo cargo (D8).
//   - ApplyCallback: el banco avisa (firma verificada por el adaptador). Un
//     evento se aplica UNA vez: aprobado y vigente -> paid + correo de
//     confirmación; rechazado -> attempt_declined (sigue pendiente); aprobado
//     tarde -> reembolso al banco y late_payment_refunded, sin reactivar la
//     reserva (D7).
//   - Lookup: la consulta pública por id del cargo (la credencial es el id, D8).
//   - List: la pantalla «Señas» del panel, solo owner/admin/recepción (D10).
//   - Sweep: el trabajo periódico (D9): vence las pendientes y cancela su reserva
//     por el camino existente; después cierra las pagadas cuya reserva se
//     canceló, reembolsando o perdiendo la sena según la ventana.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// depositGatewayTimeout es el techo de una llamada al banco dentro de un pedido.
const depositGatewayTimeout = 15 * time.Second

// defaultDepositSweepLimit es el tamaño de página del barrido si el trabajo no
// pide otro.
const defaultDepositSweepLimit = 50

// Textos que van al `detail` del libro mayor (máx 200 caracteres, migración 037)
// y al motivo del reembolso del banco.
const (
	depositReasonCancelled   = "reserva cancelada"
	depositReasonLatePayment = "pago tardio"
	depositDetailExpired     = "vencida sin pago"
	depositDetailRefund      = "reserva cancelada: se devuelve la sena"
	depositDetailForfeit     = "reserva cancelada: la sena se pierde"
	depositDetailLateRefund  = "pago aprobado despues del vencimiento"
)

// DepositSweepResult es lo que hizo un tick del trabajo periódico (D9).
type DepositSweepResult struct {
	// Expired son las señas que este tick venció.
	Expired int
	// Cancelled son las reservas que se cancelaron por el vencimiento.
	Cancelled int
	// Refunded y Forfeited son las señas pagadas cuya reserva estaba cancelada.
	Refunded  int
	Forfeited int
	// Failed son los pasos que fallaron y se reintentan en el próximo tick.
	Failed int
}

type DepositService struct {
	Policies ports.DepositPolicyRepository
	Deposits ports.DepositRepository
	// Gateway es el banco (D5). nil = no hay banco: no se crean cargos.
	Gateway ports.PaymentGateway
	// Bookings es el camino EXISTENTE de cancelación de la agenda (D9(a)).
	Bookings ports.BookingCanceller
	// Notices resuelve el correo de la confirmación de una sena pagada (D7).
	Notices ports.BookingNoticeRepository
	// Mailer nil = este entorno no manda correos.
	Mailer ports.MailSender
	// Links emite el enlace de un solo uso de la confirmación (F1). nil = el
	// correo sale con la línea de contacto.
	Links ports.BookingLinkRepository
	// Settings da la zona horaria del salón para el texto del correo.
	Settings ports.SalonSettingsRepository
	// Memberships valida al actor de la pantalla «Señas» (D10).
	Memberships ports.MembershipRepository
	// AppURL es la base del enlace (APP_URL).
	AppURL string
	// Run es el runner asíncrono del correo; nil = GoAsync.
	Run AsyncRunner
	// Now es el reloj (nil = time.Now); los tests lo fijan.
	Now func() time.Time
}

func (s DepositService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// CreateForBooking crea (o devuelve) la sena de una reserva (D8).
//
// Devuelve nil SIN error cuando no hay sena que cobrar: el salón nunca guardó la
// política, la tiene apagada o el precio es 0. En esos casos la reserva pública
// responde y manda el correo exactamente como hoy (D8).
func (s DepositService) CreateForBooking(order domain.DepositOrder) (*domain.BookingDeposit, error) {
	salonID := strings.TrimSpace(order.SalonID)
	bookingID := strings.TrimSpace(order.BookingID)
	if !depositCleanID(salonID) || !depositCleanID(bookingID) {
		return nil, fmt.Errorf("%w: la sena necesita el salon y el turno", domain.ErrValidation)
	}
	// 1) El reintento del navegador (mismo turno, misma client_op_id) devuelve la
	// MISMA sena: el cargo ya se creó y no se crea otro (D8, «un solo cargo»).
	existing, err := s.Deposits.FindByBooking(salonID, bookingID)
	switch {
	case err == nil:
		return &existing, nil
	case !errors.Is(err, domain.ErrNotFound):
		return nil, err
	}
	// 2) La política. Nunca guardada o apagada: comportamiento de hoy, sin sena.
	policy, err := s.Policies.Find(salonID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !policy.Enabled {
		return nil, nil
	}
	// 3) El monto de D4. Precio 0 (o un porcentaje que da 0) = no hay sena.
	amount := domain.DepositAmountCents(order.PriceCents, policy.Percent)
	if amount < domain.MinDepositAmountCents {
		return nil, nil
	}
	// 4) Sin banco no se puede cobrar (D12): la política activa con el banco
	// apagado es una configuración rota, no un turno sin sena.
	if s.Gateway == nil || !s.Gateway.Enabled() {
		return nil, errDepositGatewayUnavailable()
	}
	now := s.now()
	expiresAt := now.Add(time.Duration(policy.HoldMinutes) * time.Minute)
	// 5) El cargo en el banco. Si falla, el turno se cancela por el camino
	// existente (lo hace Book) y el cliente recibe el error de servidor de hoy.
	ctx, cancel := context.WithTimeout(context.Background(), depositGatewayTimeout)
	defer cancel()
	charge, err := s.Gateway.CreateCharge(ctx, ports.ChargeRequest{
		Reference:   bookingID,
		AmountCents: amount,
		Description: strings.TrimSpace(order.Description),
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return nil, err
	}
	deposit, err := domain.NewBookingDeposit(order, policy, charge.ID, now)
	if err != nil {
		return nil, err
	}
	// 6) La fila y su evento `created`, atómicos. La carrera del alta la resuelve
	// el UNIQUE de booking_id: el perdedor devuelve la sena que sí entró (su cargo
	// huérfano vence solo en el banco).
	saved, err := s.Deposits.Create(deposit)
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			if winner, rerr := s.Deposits.FindByBooking(salonID, bookingID); rerr == nil {
				return &winner, nil
			}
		}
		return nil, err
	}
	return &saved, nil
}

// ApplyCallback aplica UN evento del banco ya firmado (D7).
//
//	aprobado y vigente -> paid + correo de confirmación (UNA vez);
//	rechazado          -> attempt_declined, la sena sigue pendiente;
//	aprobado tarde     -> reembolso al banco y late_payment_refunded, sin
//	                      reactivar la reserva.
//
// El estado del pago lo fija SOLO este camino: el navegador nunca dice «pagué».
func (s DepositService) ApplyCallback(header http.Header, body []byte) error {
	// Sin banco el callback no existe (D12): 404 antes de parsear nada.
	if s.Gateway == nil || !s.Gateway.Enabled() {
		return domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
	}
	now := s.now()
	event, err := s.Gateway.ParseCallback(header, body, now)
	if err != nil {
		// Firma inválida o cuerpo ilegible: el adaptador devuelve el error
		// uniforme (400 «Firma invalida»).
		return err
	}
	if strings.TrimSpace(event.EventID) == "" || !domain.ValidDepositCallbackKind(event.Kind) {
		return domain.NewMessage(domain.ErrValidation, domain.MsgDepositInvalidAmount)
	}
	chargeID := strings.TrimSpace(event.ChargeID)
	// La forma del id se chequea ANTES de la base: un valor basura nunca consulta.
	if !domain.ValidDepositChargeID(chargeID) {
		return domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
	}
	deposit, err := s.Deposits.FindByCharge(chargeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
		}
		return err
	}
	// El monto del evento tiene que ser el de la sena: si no, 400 sin efecto (D7).
	if event.AmountCents != deposit.AmountCents {
		return domain.NewMessage(domain.ErrValidation, domain.MsgDepositInvalidAmount)
	}
	callback := domain.DepositCallback{
		EventID:     strings.TrimSpace(event.EventID),
		ChargeID:    chargeID,
		Reference:   event.Reference,
		Kind:        event.Kind,
		AmountCents: event.AmountCents,
		Now:         now,
	}
	switch event.Kind {
	case domain.CallbackApproved:
		return s.applyApproved(callback, deposit)
	default:
		// declined o insufficient: se registra el intento y la sena sigue
		// pendiente hasta que venza (el cliente puede reintentar con otra tarjeta).
		_, err := s.Deposits.ApplyDeclined(callback)
		return err
	}
}

// applyApproved aplica un evento aprobado ya verificado y monto-controlado.
func (s DepositService) applyApproved(callback domain.DepositCallback, deposit domain.BookingDeposit) error {
	applied, err := s.Deposits.ApplyApproved(callback)
	if err != nil {
		return err
	}
	if applied.LatePayment {
		// Pago tardío (D7): PRIMERO se devuelve el dinero (el banco es idempotente
		// por cargo) y RECIÉN DESPUÉS se cierra la sena. Si el reembolso falla, no
		// se escribe nada y el reintento del banco vuelve a intentarlo.
		ctx, cancel := context.WithTimeout(context.Background(), depositGatewayTimeout)
		defer cancel()
		if err := s.Gateway.Refund(ctx, deposit.ChargeID, deposit.AmountCents, depositReasonLatePayment); err != nil {
			return err
		}
		// The booking must not keep holding the slot: cancel it BEFORE closing the
		// deposit and fail the callback if that cannot be done, so the bank retries
		// (refund is idempotent per charge, cancelling a closed booking is nil). A
		// closed deposit would never be looked at again (F11 review R4-002).
		if err := s.cancelBookingOrFail(deposit, callback.Now); err != nil {
			return err
		}
		_, err := s.Deposits.SettleLateRefund(domain.DepositSettlement{
			DepositID:   deposit.ID,
			Status:      domain.DepositRefunded,
			BankEventID: callback.EventID,
			Detail:      depositDetailLateRefund,
			Now:         callback.Now,
		})
		if err != nil {
			return err
		}
		return nil
	}
	if !applied.Applied {
		// Evento repetido o sena ya resuelta: 200 sin efecto y SIN segundo correo.
		return nil
	}
	// Recién ahora la reserva está pagada: sale el correo de confirmación que
	// Book deja de mandar cuando hay sena (D7).
	s.sendConfirmation(applied.Deposit, callback.Now)
	return nil
}

// cancelBookingOrFail cancels the booking through the existing path and returns
// the error (nil when there is no canceller wired).
func (s DepositService) cancelBookingOrFail(deposit domain.BookingDeposit, now time.Time) error {
	if s.Bookings == nil {
		return nil
	}
	return s.Bookings.CancelBooking(deposit.SalonID, deposit.BookingID, now)
}

// sendConfirmation manda la confirmación de la reserva pagada por el MISMO camino
// de siempre (plantilla + enlace de un solo uso + runner asíncrono). Best-effort:
// la sena ya está pagada y ningún error de acá puede volver al banco.
func (s DepositService) sendConfirmation(deposit domain.BookingDeposit, now time.Time) {
	if s.Mailer == nil || s.Notices == nil {
		return
	}
	notice, err := s.Notices.Notice(deposit.SalonID, deposit.BookingID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			log.Printf("sena: leer el aviso del turno %s del salon %s: %v", deposit.BookingID, deposit.SalonID, err)
		}
		return
	}
	email := strings.TrimSpace(notice.ClientEmail)
	if email == "" {
		return // reserva anónima: no hay a quién avisar
	}
	settings, err := kernel.LoadSalonSettings(s.Settings, deposit.SalonID)
	if err != nil {
		log.Printf("sena: leer la configuracion del salon %s: %v", deposit.SalonID, err)
		return
	}
	link := ""
	if s.Links != nil {
		if _, token, lerr := mintBookingLink(s.Links, deposit.SalonID, deposit.BookingID, now); lerr != nil {
			// Sin el token en el log: el id del turno y el salón alcanzan.
			log.Printf("sena: emitir el enlace del turno %s del salon %s: %v", deposit.BookingID, deposit.SalonID, lerr)
		} else {
			link = bookingLinkURL(s.AppURL, token)
		}
	}
	msg := bookingMailWithLink(bookingMailConfirmation, notice.SalonName, notice.ClientName, notice.StartsAt, settings, link)
	msg.To = email
	runAsync(s.Run, func(ctx context.Context) {
		if err := s.Mailer.Send(ctx, msg); err != nil {
			// Sin la dirección en el log: el turno y el salón alcanzan.
			log.Printf("sena: enviar la confirmacion del turno %s del salon %s: %v", deposit.BookingID, deposit.SalonID, err)
		}
	})
}

// Lookup es la consulta pública por id del cargo (D8).
//
// El id del cargo ES la credencial: 43 caracteres base64url. Un formato inválido
// y un cargo desconocido dan el MISMO 404 «Pago no encontrado» y el primero ni
// siquiera consulta la base.
func (s DepositService) Lookup(chargeID string) (domain.DepositLookup, error) {
	chargeID = strings.TrimSpace(chargeID)
	if !domain.ValidDepositChargeID(chargeID) {
		return domain.DepositLookup{}, domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
	}
	deposit, err := s.Deposits.FindByCharge(chargeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.DepositLookup{}, domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
		}
		return domain.DepositLookup{}, err
	}
	return domain.DepositLookup{
		Status:      deposit.Status,
		AmountCents: deposit.AmountCents,
		ExpiresAt:   deposit.ExpiresAt,
		StartsAt:    deposit.StartsAt,
	}, nil
}

// RequireViewer es el permiso de la pantalla «Señas» (D10): owner/admin/recepción.
// Profesional, clienta, miembro dado de baja y otro salón: 403.
func (s DepositService) RequireViewer(actor domain.Membership, salonID string) error {
	m, err := s.member(actor, salonID)
	if err != nil {
		return err
	}
	if !kernel.PuedeVender(m) {
		return domain.ErrForbidden
	}
	return nil
}

// List trae la pantalla «Señas» (D10). El permiso va ANTES de leer parámetros:
// quien no puede ver la pantalla recibe 403 aunque el estado, el límite o el
// cursor estén mal.
func (s DepositService) List(actor domain.Membership, salonID, status, limit, before string) ([]domain.BookingDeposit, error) {
	if err := s.RequireViewer(actor, salonID); err != nil {
		return nil, err
	}
	q, err := domain.ParseDepositQuery(status, limit, before)
	if err != nil {
		return nil, err
	}
	return s.Deposits.List(salonID, q)
}

// Sweep es el trabajo periódico de D9(a) y D9(b).
//
// (a) Vence las pendientes que pasaron su espera y cancela su reserva por el
// camino existente (SetStatus(cancelled) bajo el lock de la agenda). Si la
// cancelación falla, el próximo tick la reintenta: la sena ya está `expired` y
// el adaptador la vuelve a devolver mientras el turno siga `booked`.
//
// (b) Cierra las señas `paid` cuya reserva se canceló: reembolso si faltan al
// menos `refund_hours` horas para el turno, se pierde si faltan menos.
// `done` y `no_show` no entran nunca (el adaptador filtra esas reservas).
//
// Nunca devuelve error por una fila: los fallos se cuentan en Failed y se
// reintentan en el próximo tick. Un error de las lecturas sí se devuelve.
func (s DepositService) Sweep(limit int) (DepositSweepResult, error) {
	var out DepositSweepResult
	if limit <= 0 {
		limit = defaultDepositSweepLimit
	}
	now := s.now()
	// (a) Vencer y cancelar.
	expired, err := s.Deposits.ExpirePending(now, limit)
	if err != nil {
		return out, err
	}
	out.Expired = len(expired)
	for _, d := range expired {
		if s.Bookings == nil {
			continue
		}
		if err := s.Bookings.CancelBooking(d.SalonID, d.BookingID, now); err != nil {
			log.Printf("sena: cancelar el turno %s del salon %s al vencer la sena: %v", d.BookingID, d.SalonID, err)
			out.Failed++
			continue
		}
		out.Cancelled++
	}
	// (b) Cerrar las pagadas de reservas canceladas.
	paid, err := s.Deposits.ListPaidCancelled(limit)
	if err != nil {
		return out, err
	}
	for _, d := range paid {
		refundHours, err := s.refundHours(d.SalonID)
		if err != nil {
			log.Printf("sena: leer la politica del salon %s: %v", d.SalonID, err)
			out.Failed++
			continue
		}
		status := domain.DepositCancelDecision(d.StartsAt, now, refundHours)
		if status == domain.DepositRefunded {
			// Without a working bank the money cannot go back: never close the
			// deposit as refunded (it would never be retried). It stays `paid` and
			// counts as a failure until the bank is back (F11 review R4-001).
			if s.Gateway == nil || !s.Gateway.Enabled() {
				log.Printf("sena: sin banco para reembolsar el turno %s del salon %s", d.BookingID, d.SalonID)
				out.Failed++
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), depositGatewayTimeout)
			err := s.Gateway.Refund(ctx, d.ChargeID, d.AmountCents, depositReasonCancelled)
			cancel()
			if err != nil {
				log.Printf("sena: reembolsar el cargo del turno %s del salon %s: %v", d.BookingID, d.SalonID, err)
				out.Failed++
				continue
			}
		}
		detail := depositDetailRefund
		if status == domain.DepositForfeited {
			detail = depositDetailForfeit
		}
		applied, err := s.Deposits.SettlePaidCancelled(domain.DepositSettlement{
			DepositID: d.ID, Status: status, Detail: detail, Now: now,
		})
		if err != nil {
			log.Printf("sena: cerrar la sena del turno %s del salon %s: %v", d.BookingID, d.SalonID, err)
			out.Failed++
			continue
		}
		if !applied {
			continue
		}
		if status == domain.DepositRefunded {
			out.Refunded++
		} else {
			out.Forfeited++
		}
	}
	return out, nil
}

// refundHours es la ventana de la política VIGENTE del salón (el porcentaje es la
// única foto, D2). Sin fila (borrada a mano) se usa 0: ante la duda se devuelve
// el dinero de la clienta, nunca se lo queda el salón en silencio.
func (s DepositService) refundHours(salonID string) (int, error) {
	policy, err := s.Policies.Find(salonID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return policy.RefundHours, nil
}

// member valida que el actor sea miembro ACTIVO del salón (no confía en el rol
// que traiga el token).
func (s DepositService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// depositCleanID dice si un id es usable: un NUL o un UTF-8 inválido llegaría a
// Postgres como un 500 (no cabe en un TEXT); es un recurso que no existe.
func depositCleanID(id string) bool {
	return id != "" && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}
