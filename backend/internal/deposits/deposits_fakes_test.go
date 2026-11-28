package deposits_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de F11 con la MISMA semántica que el adaptador Postgres (lock por sena
// simulado con un mutex, UPDATE condicional, libro mayor append-only y el índice
// único del evento del banco) y que el banco simulado. Un caso de uso que
// dependiera de detalles de SQL no compilaría contra estos fakes.

// --- Política ---------------------------------------------------------------

type fakeDepositPolicies struct {
	mu    sync.Mutex
	rows  map[string]domain.DepositPolicy
	saves int
	err   error
}

func (f *fakeDepositPolicies) Find(salonID string) (domain.DepositPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.DepositPolicy{}, f.err
	}
	p, ok := f.rows[salonID]
	if !ok {
		return domain.DepositPolicy{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakeDepositPolicies) Save(p domain.DepositPolicy) (domain.DepositPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.DepositPolicy{}, f.err
	}
	if f.rows == nil {
		f.rows = map[string]domain.DepositPolicy{}
	}
	f.saves++
	p.Saved = true
	f.rows[p.SalonID] = p
	return p, nil
}

func (f *fakeDepositPolicies) savedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

func (f *fakeDepositPolicies) seed(p domain.DepositPolicy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]domain.DepositPolicy{}
	}
	p.Saved = true
	f.rows[p.SalonID] = p
}

var _ ports.DepositPolicyRepository = (*fakeDepositPolicies)(nil)

// --- Turnos (cancelación por el camino existente) ---------------------------

type fakeDepositBookings struct {
	mu      sync.Mutex
	rows    map[string]domain.Booking
	cancels []string
	fail    error
}

func newFakeDepositBookings() *fakeDepositBookings {
	return &fakeDepositBookings{rows: map[string]domain.Booking{}}
}

func (f *fakeDepositBookings) add(salonID, id string, status domain.BookingStatus, startsAt time.Time) domain.Booking {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := domain.Booking{
		ID: id, SalonID: salonID, ClientName: "Ana Pérez",
		StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour), Status: status,
	}
	f.rows[id] = b
	return b
}

// CancelBooking: cancelar un turno ya cerrado es nil; uno inexistente también.
func (f *fakeDepositBookings) CancelBooking(salonID, bookingID string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	b, ok := f.rows[bookingID]
	if !ok || b.SalonID != salonID {
		return nil
	}
	if b.Status != domain.BookingBooked {
		return nil
	}
	cancelled, err := b.SetStatus(domain.BookingCancelled)
	if err != nil {
		return err
	}
	f.rows[bookingID] = cancelled
	f.cancels = append(f.cancels, bookingID)
	return nil
}

func (f *fakeDepositBookings) status(bookingID string) domain.BookingStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[bookingID].Status
}

func (f *fakeDepositBookings) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancels)
}

// setStatus es lo que hace el panel (cancelar a mano, marcar atendida...).
func (f *fakeDepositBookings) setStatus(bookingID string, status domain.BookingStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.rows[bookingID]
	b.Status = status
	f.rows[bookingID] = b
}

// booking devuelve el turno para completar los campos de solo lectura de la sena.
func (f *fakeDepositBookings) booking(bookingID string) (domain.Booking, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.rows[bookingID]
	return b, ok
}

var _ ports.BookingCanceller = (*fakeDepositBookings)(nil)

// --- Señoras y libro mayor --------------------------------------------------

type fakeDeposits struct {
	mu       sync.Mutex
	rows     []*domain.BookingDeposit
	events   []domain.DepositEvent
	bookings *fakeDepositBookings
	// createErr fuerza un error del adaptador; raceOnce simula la carrera del
	// alta (el INSERT choca con el UNIQUE del turno).
	createErr error
	raceOnce  bool
	// hideOnce esconde la fila en la PRIMERA lectura por turno (la carrera del
	// alta: el caso de uso no ve nada y el INSERT choca).
	hideOnce bool
	lists    int
	// finds cuenta las consultas por cargo (para probar que un id con forma
	// inválida NUNCA llega a la base).
	finds int
}

func (f *fakeDeposits) withBooking(d domain.BookingDeposit) domain.BookingDeposit {
	if f.bookings == nil {
		return d
	}
	if b, ok := f.bookings.booking(d.BookingID); ok {
		d.BookingStatus = b.Status
		d.StartsAt = b.StartsAt
		d.ClientName = b.ClientName
	}
	return d
}

func (f *fakeDeposits) findLocked(pred func(domain.BookingDeposit) bool) (*domain.BookingDeposit, bool) {
	for _, d := range f.rows {
		if pred(*d) {
			return d, true
		}
	}
	return nil, false
}

func (f *fakeDeposits) eventLocked(bankEventID string) (domain.DepositEvent, bool) {
	if bankEventID == "" {
		return domain.DepositEvent{}, false
	}
	for _, e := range f.events {
		if e.BankEventID == bankEventID {
			return e, true
		}
	}
	return domain.DepositEvent{}, false
}

func (f *fakeDeposits) addEventLocked(depositID, kind string, amount int, detail, bankEventID string, now time.Time) {
	f.events = append(f.events, domain.DepositEvent{
		ID: domain.NewID(), DepositID: depositID, Kind: kind,
		AmountCents: amount, Detail: detail, BankEventID: bankEventID, CreatedAt: now,
	})
}

func (f *fakeDeposits) eventsOf(depositID string) []domain.DepositEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.DepositEvent
	for _, e := range f.events {
		if e.DepositID == depositID {
			out = append(out, e)
		}
	}
	return out
}

func (f *fakeDeposits) countEvents(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// Create: idempotente por client_op_id y por turno (UNIQUE), como el adaptador.
func (f *fakeDeposits) Create(d domain.BookingDeposit) (domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return domain.BookingDeposit{}, f.createErr
	}
	if f.raceOnce {
		f.raceOnce = false
		return domain.BookingDeposit{}, domain.ErrAlreadyExists
	}
	if existing, ok := f.findLocked(func(x domain.BookingDeposit) bool {
		if x.SalonID != d.SalonID {
			return false
		}
		if x.BookingID == d.BookingID {
			return true
		}
		return d.ClientOpID != "" && x.ClientOpID == d.ClientOpID
	}); ok {
		return f.withBooking(*existing), nil
	}
	stored := d
	stored.BookingStatus = ""
	stored.StartsAt = time.Time{}
	// Si el turno no existe en el fake de reservas, la sena guardada se queda sin
	// join: se completa al leer, como el adaptador.
	f.rows = append(f.rows, &stored)
	f.addEventLocked(stored.ID, domain.DepositEventCreated, stored.AmountCents, "", "", stored.CreatedAt)
	return f.withBooking(stored), nil
}

func (f *fakeDeposits) FindByBooking(salonID, bookingID string) (domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hideOnce {
		f.hideOnce = false
		return domain.BookingDeposit{}, domain.ErrNotFound
	}
	d, ok := f.findLocked(func(x domain.BookingDeposit) bool {
		return x.SalonID == salonID && x.BookingID == bookingID
	})
	if !ok {
		return domain.BookingDeposit{}, domain.ErrNotFound
	}
	return f.withBooking(*d), nil
}

func (f *fakeDeposits) FindByCharge(chargeID string) (domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finds++
	d, ok := f.findLocked(func(x domain.BookingDeposit) bool { return x.ChargeID == chargeID })
	if !ok {
		return domain.BookingDeposit{}, domain.ErrNotFound
	}
	return f.withBooking(*d), nil
}

func (f *fakeDeposits) findCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finds
}

func (f *fakeDeposits) ApplyApproved(r domain.DepositCallback) (domain.DepositApply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(func(x domain.BookingDeposit) bool { return x.ChargeID == r.ChargeID })
	if !ok {
		return domain.DepositApply{}, domain.ErrNotFound
	}
	d.BookingStatus = f.withBooking(*d).BookingStatus
	if _, seen := f.eventLocked(r.EventID); seen {
		return domain.DepositApply{Applied: false, Deposit: *d}, nil
	}
	if (*d).Payable(r.Now) {
		paidAt := r.Now
		d.Status = domain.DepositPaid
		d.PaidAt = &paidAt
		f.addEventLocked(d.ID, domain.DepositEventPaid, d.AmountCents, "", r.EventID, r.Now)
		return domain.DepositApply{Applied: true, Deposit: *d}, nil
	}
	if (*d).LatePayment(r.Now) {
		return domain.DepositApply{LatePayment: true, Deposit: *d}, nil
	}
	return domain.DepositApply{Applied: false, Deposit: *d}, nil
}

func (f *fakeDeposits) ApplyDeclined(r domain.DepositCallback) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(func(x domain.BookingDeposit) bool { return x.ChargeID == r.ChargeID })
	if !ok {
		return false, domain.ErrNotFound
	}
	if _, seen := f.eventLocked(r.EventID); seen {
		return false, nil
	}
	f.addEventLocked(d.ID, domain.DepositEventAttemptDeclined, r.AmountCents, r.Kind, r.EventID, r.Now)
	return true, nil
}

func (f *fakeDeposits) ExpirePending(now time.Time, limit int) ([]domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BookingDeposit
	for _, d := range f.rows {
		if len(out) >= limit {
			break
		}
		if d.Status == domain.DepositPending && !d.ExpiresAt.After(now) {
			d.Status = domain.DepositExpired
			f.addEventLocked(d.ID, domain.DepositEventExpired, d.AmountCents, "", "", now)
			out = append(out, f.withBooking(*d))
			continue
		}
		// Ya vencida y el turno sigue retenido: el tick anterior no lo canceló.
		if d.Status == domain.DepositExpired {
			if b, ok := f.bookings.booking(d.BookingID); ok && b.Status == domain.BookingBooked {
				out = append(out, f.withBooking(*d))
			}
		}
	}
	return out, nil
}

func (f *fakeDeposits) ListPaidCancelled(limit int) ([]domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BookingDeposit
	for _, d := range f.rows {
		if d.Status != domain.DepositPaid {
			continue
		}
		joined := f.withBooking(*d)
		if joined.BookingStatus != domain.BookingCancelled {
			continue
		}
		out = append(out, joined)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeDeposits) SettleLateRefund(s domain.DepositSettlement) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(func(x domain.BookingDeposit) bool { return x.ID == s.DepositID })
	if !ok {
		return false, domain.ErrNotFound
	}
	if d.Status != domain.DepositPending && d.Status != domain.DepositExpired {
		return false, nil
	}
	if _, seen := f.eventLocked(s.BankEventID); seen {
		return false, nil
	}
	settledAt := s.Now
	d.Status = domain.DepositRefunded
	d.SettledAt = &settledAt
	f.addEventLocked(d.ID, domain.DepositEventLatePaymentRefunded, d.AmountCents, s.Detail, s.BankEventID, s.Now)
	return true, nil
}

func (f *fakeDeposits) SettlePaidCancelled(s domain.DepositSettlement) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(func(x domain.BookingDeposit) bool { return x.ID == s.DepositID })
	if !ok {
		return false, domain.ErrNotFound
	}
	if d.Status != domain.DepositPaid {
		return false, nil
	}
	if s.Status != domain.DepositRefunded && s.Status != domain.DepositForfeited {
		return false, domain.ErrValidation
	}
	settledAt := s.Now
	d.Status = s.Status
	d.SettledAt = &settledAt
	f.addEventLocked(d.ID, s.Status, d.AmountCents, s.Detail, s.BankEventID, s.Now)
	return true, nil
}

func (f *fakeDeposits) List(salonID string, q domain.DepositQuery) ([]domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	var out []domain.BookingDeposit
	for _, d := range f.rows {
		if d.SalonID != salonID {
			continue
		}
		if q.Status != "" && d.Status != q.Status {
			continue
		}
		if q.Before != nil {
			if !d.CreatedAt.Before(q.Before.CreatedAt) &&
				!(d.CreatedAt.Equal(q.Before.CreatedAt) && d.ID < q.Before.ID) {
				continue
			}
		}
		out = append(out, f.withBooking(*d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (f *fakeDeposits) listCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

var _ ports.DepositRepository = (*fakeDeposits)(nil)

// --- Banco simulado ---------------------------------------------------------

type fakeDepositRefund struct {
	ChargeID    string
	AmountCents int
	Reason      string
}

type fakeDepositGateway struct {
	mu      sync.Mutex
	enabled bool
	secret  string
	// createErr y parseErr fuerzan fallas del adaptador.
	createErr error
	parseErr  error
	refundErr error
	charges   []ports.ChargeRequest
	chargeIDs []string
	// refundAttempts cuenta TODAS las llamadas a Refund; refunds son las que
	// efectivamente devolvieron el dinero (idempotente por cargo).
	refundAttempts int
	refunds        []fakeDepositRefund
	refunded       map[string]bool
}

func newFakeDepositGateway(secret string) *fakeDepositGateway {
	return &fakeDepositGateway{enabled: true, secret: secret, refunded: map[string]bool{}}
}

func (g *fakeDepositGateway) Enabled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enabled
}

func (g *fakeDepositGateway) CreateCharge(ctx context.Context, r ports.ChargeRequest) (ports.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.createErr != nil {
		return ports.Charge{}, g.createErr
	}
	if !g.enabled {
		return ports.Charge{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
	}
	id, err := domain.NewDepositChargeID()
	if err != nil {
		return ports.Charge{}, err
	}
	g.charges = append(g.charges, r)
	g.chargeIDs = append(g.chargeIDs, id)
	return ports.Charge{ID: id, PayPath: "/pagar#" + id}, nil
}

func (g *fakeDepositGateway) Refund(ctx context.Context, chargeID string, amountCents int, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refundAttempts++
	if g.refundErr != nil {
		return g.refundErr
	}
	if g.refunded[chargeID] {
		return nil // idempotente por cargo
	}
	g.refunded[chargeID] = true
	g.refunds = append(g.refunds, fakeDepositRefund{ChargeID: chargeID, AmountCents: amountCents, Reason: reason})
	return nil
}

// callbackBody es el cuerpo que manda el banco al comercio (D6/D7).
type callbackBody struct {
	EventID     string `json:"event_id"`
	ChargeID    string `json:"charge_id"`
	Reference   string `json:"reference"`
	Kind        string `json:"kind"`
	AmountCents int    `json:"amount_cents"`
}

func (g *fakeDepositGateway) ParseCallback(header http.Header, body []byte, now time.Time) (ports.CallbackEvent, error) {
	g.mu.Lock()
	enabled, secret, parseErr := g.enabled, g.secret, g.parseErr
	g.mu.Unlock()
	if parseErr != nil {
		return ports.CallbackEvent{}, parseErr
	}
	if !enabled {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
	}
	if err := domain.VerifyPaymentSignature(secret, header.Get("X-Simbank-Signature"), body, now); err != nil {
		return ports.CallbackEvent{}, err
	}
	var cb callbackBody
	if err := json.Unmarshal(body, &cb); err != nil {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
	}
	return ports.CallbackEvent{
		EventID: cb.EventID, ChargeID: cb.ChargeID, Reference: cb.Reference,
		Kind: cb.Kind, AmountCents: cb.AmountCents,
	}, nil
}

func (g *fakeDepositGateway) chargeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.charges)
}

func (g *fakeDepositGateway) refundCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.refunds)
}

func (g *fakeDepositGateway) attemptCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refundAttempts
}

func (g *fakeDepositGateway) lastCharge() ports.ChargeRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.charges) == 0 {
		return ports.ChargeRequest{}
	}
	return g.charges[len(g.charges)-1]
}

func (g *fakeDepositGateway) lastRefund() fakeDepositRefund {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.refunds) == 0 {
		return fakeDepositRefund{}
	}
	return g.refunds[len(g.refunds)-1]
}

var _ ports.PaymentGateway = (*fakeDepositGateway)(nil)

// --- Aviso del turno (correo de confirmación) --------------------------------

type fakeDepositNotices struct {
	mu   sync.Mutex
	rows map[string]domain.BookingNotice
	err  error
}

func noticeKey(salonID, bookingID string) string { return salonID + "|" + bookingID }

func (f *fakeDepositNotices) seed(n domain.BookingNotice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]domain.BookingNotice{}
	}
	f.rows[noticeKey(n.SalonID, n.BookingID)] = n
}

func (f *fakeDepositNotices) Notice(salonID, bookingID string) (domain.BookingNotice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.BookingNotice{}, f.err
	}
	n, ok := f.rows[noticeKey(salonID, bookingID)]
	if !ok {
		return domain.BookingNotice{}, domain.ErrNotFound
	}
	return n, nil
}

var _ ports.BookingNoticeRepository = (*fakeDepositNotices)(nil)
