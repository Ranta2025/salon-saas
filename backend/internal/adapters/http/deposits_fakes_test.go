package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de F11 con la MISMA semántica que el adaptador Postgres (lock simulado
// con un mutex, UPDATE condicional, libro mayor append-only y el índice único del
// evento del banco) y que el banco simulado. Un handler que dependiera de
// detalles de SQL no compilaría contra estos fakes.

// --- Política ---------------------------------------------------------------

type mDepositPolicies struct {
	mu   sync.Mutex
	rows map[string]domain.DepositPolicy
}

func (f *mDepositPolicies) Find(salonID string) (domain.DepositPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[salonID]
	if !ok {
		return domain.DepositPolicy{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *mDepositPolicies) Save(p domain.DepositPolicy) (domain.DepositPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]domain.DepositPolicy{}
	}
	p.Saved = true
	f.rows[p.SalonID] = p
	return p, nil
}

func (f *mDepositPolicies) seed(p domain.DepositPolicy) {
	p.Saved = true
	if f.rows == nil {
		f.rows = map[string]domain.DepositPolicy{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[p.SalonID] = p
}

var _ ports.DepositPolicyRepository = (*mDepositPolicies)(nil)

// --- Señas y libro mayor ----------------------------------------------------

type mDeposits struct {
	mu       sync.Mutex
	salonID  string
	rows     map[string]domain.BookingDeposit
	events   []domain.DepositEvent
	bookings map[string]domain.Booking
}

func newMDeposits(salonID string) *mDeposits {
	return &mDeposits{salonID: salonID, rows: map[string]domain.BookingDeposit{}, bookings: map[string]domain.Booking{}}
}

// addBooking deja el turno que la sena referencia (estado, inicio y clienta).
func (f *mDeposits) addBooking(id string, status domain.BookingStatus, startsAt time.Time, clientName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bookings[id] = domain.Booking{
		ID: id, SalonID: f.salonID, ClientName: clientName, Status: status,
		StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour),
	}
}

// seed crea una sena con su evento `created`, como el adaptador.
func (f *mDeposits) seed(d domain.BookingDeposit) domain.BookingDeposit {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d.Status == "" {
		d.Status = domain.DepositPending
	}
	if d.ID == "" {
		d.ID = domain.NewID()
	}
	if d.SalonID == "" {
		d.SalonID = f.salonID
	}
	d = f.withBooking(d)
	f.rows[d.ID] = d
	f.events = append(f.events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventCreated, AmountCents: d.AmountCents, CreatedAt: d.CreatedAt})
	return d
}

func (f *mDeposits) withBooking(d domain.BookingDeposit) domain.BookingDeposit {
	if b, ok := f.bookings[d.BookingID]; ok {
		d.BookingStatus, d.StartsAt, d.ClientName = b.Status, b.StartsAt, b.ClientName
	}
	return d
}

func (f *mDeposits) eventsFor(depositID string) []domain.DepositEvent {
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

func (f *mDeposits) eventKinds(depositID string) []string {
	var out []string
	for _, e := range f.eventsFor(depositID) {
		out = append(out, e.Kind)
	}
	return out
}

func (f *mDeposits) find(chargeID string) (domain.BookingDeposit, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.rows {
		if d.ChargeID == chargeID {
			return f.withBooking(d), true
		}
	}
	return domain.BookingDeposit{}, false
}

func (f *mDeposits) seen(eventID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if eventID != "" && e.BankEventID == eventID {
			return true
		}
	}
	return false
}

func (f *mDeposits) Create(d domain.BookingDeposit) (domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.rows {
		if existing.BookingID == d.BookingID ||
			(d.ClientOpID != "" && existing.SalonID == d.SalonID && existing.ClientOpID == d.ClientOpID) {
			return f.withBooking(existing), nil
		}
	}
	d = f.withBooking(d)
	f.rows[d.ID] = d
	f.events = append(f.events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventCreated, AmountCents: d.AmountCents, CreatedAt: d.CreatedAt})
	return d, nil
}

func (f *mDeposits) FindByBooking(salonID, bookingID string) (domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.rows {
		if d.SalonID == salonID && d.BookingID == bookingID {
			return f.withBooking(d), nil
		}
	}
	return domain.BookingDeposit{}, domain.ErrNotFound
}

func (f *mDeposits) FindByCharge(chargeID string) (domain.BookingDeposit, error) {
	if d, ok := f.find(chargeID); ok {
		return d, nil
	}
	return domain.BookingDeposit{}, domain.ErrNotFound
}

func (f *mDeposits) ApplyApproved(cb domain.DepositCallback) (domain.DepositApply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(cb.ChargeID)
	if !ok {
		return domain.DepositApply{}, domain.ErrNotFound
	}
	if f.seenLocked(cb.EventID) {
		return domain.DepositApply{Deposit: d}, nil
	}
	now := cb.Now.UTC()
	if !d.Payable(now) {
		if d.LatePayment(now) {
			return domain.DepositApply{LatePayment: true, Deposit: d}, nil
		}
		return domain.DepositApply{Deposit: d}, nil
	}
	paid := now
	d.Status, d.PaidAt = domain.DepositPaid, &paid
	f.rows[d.ID] = d
	f.events = append(f.events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventPaid, AmountCents: d.AmountCents, BankEventID: cb.EventID, CreatedAt: now})
	return domain.DepositApply{Applied: true, Deposit: d}, nil
}

func (f *mDeposits) ApplyDeclined(cb domain.DepositCallback) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.findLocked(cb.ChargeID)
	if !ok {
		return false, domain.ErrNotFound
	}
	if f.seenLocked(cb.EventID) {
		return false, nil
	}
	f.events = append(f.events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventAttemptDeclined, AmountCents: cb.AmountCents, BankEventID: cb.EventID, CreatedAt: cb.Now.UTC()})
	return true, nil
}

func (f *mDeposits) findLocked(chargeID string) (domain.BookingDeposit, bool) {
	for _, d := range f.rows {
		if d.ChargeID == chargeID {
			return f.withBooking(d), true
		}
	}
	return domain.BookingDeposit{}, false
}

func (f *mDeposits) seenLocked(eventID string) bool {
	if eventID == "" {
		return false
	}
	for _, e := range f.events {
		if e.BankEventID == eventID {
			return true
		}
	}
	return false
}

func (f *mDeposits) ExpirePending(now time.Time, limit int) ([]domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BookingDeposit
	for id, d := range f.rows {
		if len(out) == limit {
			break
		}
		d = f.withBooking(d)
		if d.Status == domain.DepositPending && !now.Before(d.ExpiresAt) {
			d.Status = domain.DepositExpired
			f.rows[id] = d
			f.events = append(f.events, domain.DepositEvent{ID: domain.NewID(), DepositID: id,
				Kind: domain.DepositEventExpired, AmountCents: d.AmountCents, CreatedAt: now})
			out = append(out, d)
			continue
		}
		if d.Status == domain.DepositExpired && d.BookingStatus == domain.BookingBooked {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *mDeposits) ListPaidCancelled(limit int) ([]domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BookingDeposit
	for _, d := range f.rows {
		d = f.withBooking(d)
		if d.Status == domain.DepositPaid && d.BookingStatus == domain.BookingCancelled && len(out) < limit {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *mDeposits) SettleLateRefund(s domain.DepositSettlement) (bool, error) {
	return f.settle(s, domain.DepositEventLatePaymentRefunded, func(d domain.BookingDeposit) bool {
		return d.Status == domain.DepositPending || d.Status == domain.DepositExpired
	})
}

func (f *mDeposits) SettlePaidCancelled(s domain.DepositSettlement) (bool, error) {
	kind := domain.DepositEventRefunded
	if s.Status == domain.DepositForfeited {
		kind = domain.DepositEventForfeited
	}
	return f.settle(s, kind, func(d domain.BookingDeposit) bool { return d.Status == domain.DepositPaid })
}

func (f *mDeposits) settle(s domain.DepositSettlement, kind string, from func(domain.BookingDeposit) bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.rows[s.DepositID]
	if !ok {
		return false, domain.ErrNotFound
	}
	if f.seenLocked(s.BankEventID) {
		return false, nil
	}
	if !from(d) {
		return false, nil
	}
	at := s.Now.UTC()
	d.Status, d.SettledAt = s.Status, &at
	f.rows[d.ID] = d
	f.events = append(f.events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: kind, AmountCents: d.AmountCents, Detail: s.Detail, BankEventID: s.BankEventID, CreatedAt: at})
	return true, nil
}

func (f *mDeposits) List(salonID string, q domain.DepositQuery) ([]domain.BookingDeposit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BookingDeposit
	for _, d := range f.rows {
		if d.SalonID != salonID {
			continue
		}
		if q.Status != "" && d.Status != q.Status {
			continue
		}
		if q.Before != nil {
			if d.CreatedAt.After(q.Before.CreatedAt) ||
				(d.CreatedAt.Equal(q.Before.CreatedAt) && d.ID >= q.Before.ID) {
				continue
			}
		}
		out = append(out, f.withBooking(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

var _ ports.DepositRepository = (*mDeposits)(nil)

// --- Banco (PaymentGateway) --------------------------------------------------

type mGateway struct {
	mu      sync.Mutex
	secret  string
	enabled bool
	charges map[string]ports.ChargeRequest
	refunds map[string]int
	next    int
	fail    error
}

func newMGateway(secret string) *mGateway {
	return &mGateway{secret: secret, enabled: true, charges: map[string]ports.ChargeRequest{}, refunds: map[string]int{}}
}

func (g *mGateway) Enabled() bool { return g.enabled }

func (g *mGateway) CreateCharge(_ context.Context, r ports.ChargeRequest) (ports.Charge, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail != nil {
		return ports.Charge{}, g.fail
	}
	id, err := domain.NewDepositChargeID()
	if err != nil {
		return ports.Charge{}, err
	}
	g.next++
	g.charges[id] = r
	return ports.Charge{ID: id, PayPath: "/pagar#" + id}, nil
}

func (g *mGateway) Refund(_ context.Context, chargeID string, _ int, _ string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refunds[chargeID]++
	return nil
}

func (g *mGateway) ParseCallback(header http.Header, body []byte, now time.Time) (ports.CallbackEvent, error) {
	if !g.enabled {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
	}
	if err := domain.VerifyPaymentSignature(g.secret, header.Get("X-Simbank-Signature"), body, now); err != nil {
		return ports.CallbackEvent{}, err
	}
	var p struct {
		EventID     string `json:"event_id"`
		ChargeID    string `json:"charge_id"`
		Reference   string `json:"reference"`
		Kind        string `json:"kind"`
		AmountCents int    `json:"amount_cents"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
	}
	return ports.CallbackEvent{
		EventID: p.EventID, ChargeID: p.ChargeID, Reference: p.Reference,
		Kind: p.Kind, AmountCents: p.AmountCents,
	}, nil
}

func (g *mGateway) chargeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.charges)
}

func (g *mGateway) refundCount(chargeID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refunds[chargeID]
}

var _ ports.PaymentGateway = (*mGateway)(nil)

// --- Turnos (cancelación) ----------------------------------------------------

type mDepositBookings struct {
	mu      sync.Mutex
	rows    map[string]domain.Booking
	cancels []string
}

func (f *mDepositBookings) CancelBooking(salonID, bookingID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.rows[bookingID]
	if !ok || b.SalonID != salonID || b.Status != domain.BookingBooked {
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

var _ ports.BookingCanceller = (*mDepositBookings)(nil)

// --- Aviso de la confirmación ------------------------------------------------

type mDepositNotices struct {
	notice domain.BookingNotice
	err    error
}

func (f *mDepositNotices) Notice(salonID, bookingID string) (domain.BookingNotice, error) {
	if f.err != nil {
		return domain.BookingNotice{}, f.err
	}
	if f.notice.SalonID == "" {
		return domain.BookingNotice{}, domain.ErrNotFound
	}
	return f.notice, nil
}

var _ ports.BookingNoticeRepository = (*mDepositNotices)(nil)

// --- Entorno ----------------------------------------------------------------

// depWorld es un salón con todos los roles, una sena pagable y el banco de
// prueba cableado en las rutas.
type depWorld struct {
	env      *posEnv
	policies *mDepositPolicies
	deposits *mDeposits
	gateway  *mGateway
	notices  *mDepositNotices
	bookings *mDepositBookings
	salon    string
	slug     string
	secret   string
	// Tokens por rol.
	owner, admin, recep, prof, removed, outsider string
	ownerID, profID                              string
	// chargeID es un cargo de 43 caracteres base64url (la credencial de D8).
	chargeID  string
	depositID string
	startsAt  time.Time
}

// fastDepUser crea una cuenta y devuelve (token, id).
func (w *depWorld) fastDepUser(t *testing.T, email string) (string, string) {
	t.Helper()
	u := domain.User{ID: domain.NewID(), Name: email, Email: email, PasswordHash: "x"}
	if err := w.env.users.Save(u); err != nil {
		t.Fatal(err)
	}
	tok, err := security.JWTIssuer{Secret: "test-secret"}.Issue(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tok, u.ID
}

// newDepositEnv arma el salón, los roles y el cableado de F11 sobre los fakes.
func newDepositEnv(t *testing.T) *depWorld {
	t.Helper()
	env := newPOSEnv()
	w := &depWorld{
		env: env, policies: &mDepositPolicies{}, secret: "simbank-test-secret",
		slug: "senas-uno", startsAt: time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute),
	}
	w.owner, w.ownerID = w.fastDepUser(t, "owner-senas@mail.com")
	w.salon = env.salon(t, w.owner, w.slug)
	w.deposits = newMDeposits(w.salon)
	w.gateway = newMGateway(w.secret)
	w.notices = &mDepositNotices{}
	w.bookings = &mDepositBookings{rows: map[string]domain.Booking{}}
	var id string
	w.admin, id = w.fastDepUser(t, "admin-senas@mail.com")
	seedRole(t, env, w.salon, id, domain.RoleAdmin)
	w.recep, id = w.fastDepUser(t, "recep-senas@mail.com")
	seedRole(t, env, w.salon, id, domain.RoleReception)
	w.prof, w.profID = w.fastDepUser(t, "prof-senas@mail.com")
	seedRole(t, env, w.salon, w.profID, domain.RoleProfessional)
	w.removed, id = w.fastDepUser(t, "removed-senas@mail.com")
	seedRole(t, env, w.salon, id, domain.RoleAdmin)
	at := time.Now().Add(-time.Hour)
	m := env.mems.byKey[id+"|"+w.salon]
	m.EndedAt = &at
	env.mems.byKey[id+"|"+w.salon] = m
	w.outsider, _ = w.fastDepUser(t, "other-senas@mail.com")
	env.salon(t, w.outsider, "senas-dos")
	w.wire(t)
	return w
}

// wire monta los servicios de F11 en el router con sus handlers y rutas.
func (w *depWorld) wire(t *testing.T) {
	t.Helper()
	svc := app.DepositService{
		Policies: w.policies, Deposits: w.deposits, Gateway: w.gateway,
		Bookings: w.bookings, Notices: w.notices, Memberships: w.env.mems,
		AppURL: "http://localhost:8081", Settings: w.env.settings,
	}
	policySvc := app.DepositPolicyService{Policies: w.policies, Memberships: w.env.mems, Gateway: w.gateway}
	w.env.d.DepositPolicy = DepositPolicyHandler{Policy: policySvc, Memberships: w.env.mems}
	w.env.d.Deposits = DepositHandler{Deposits: svc, Memberships: w.env.mems}
	w.env.d.PaymentsCallback = PaymentsHandler{Deposits: svc}.Callback
	// El payload público (D11) lee la misma política.
	w.env.d.Public.Public = app.PublicSalonService{
		Salons: w.env.salons, Services: w.env.services, Settings: w.env.settings,
		Hours: w.env.weekly, Deposits: w.policies,
	}
	w.env.r = gin.New()
	RegisterRoutes(w.env.r, w.env.d)
}

// seedCharge deja una sena pendiente con un cargo válido y su turno.
func (w *depWorld) seedCharge(t *testing.T, status domain.BookingStatus) domain.BookingDeposit {
	t.Helper()
	chargeID, err := domain.NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	w.chargeID = chargeID
	bookingID := domain.NewID()
	w.deposits.addBooking(bookingID, status, w.startsAt, "Lía Pérez")
	d := w.deposits.seed(domain.BookingDeposit{
		SalonID: w.salon, BookingID: bookingID, AmountCents: 4500, Percent: 30,
		Status: domain.DepositPending, ChargeID: chargeID,
		CreatedAt: time.Now().UTC().Add(-time.Minute),
		ExpiresAt: time.Now().UTC().Add(30 * time.Minute),
	})
	w.depositID = d.ID
	w.bookings.rows[bookingID] = domain.Booking{ID: bookingID, SalonID: w.salon, Status: status, StartsAt: w.startsAt}
	return d
}

// callbackRaw arma el cuerpo del callback y su firma (D6/D7).
func (w *depWorld) callbackRaw(eventID, chargeID, kind string, amount int) (string, string) {
	body := `{"event_id":"` + eventID + `","charge_id":"` + chargeID + `","reference":"ref","kind":"` + kind + `","amount_cents":` + itoa(amount) + `}`
	sig := domain.SignPayment(w.secret, time.Now().UTC(), []byte(body))
	return body, sig
}

// postSigned hace un POST con la cabecera de firma (o sin ella si sig == "").
func (w *depWorld) postSigned(path, body, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Simbank-Signature", sig)
	}
	rec := httptest.NewRecorder()
	w.env.r.ServeHTTP(rec, req)
	return rec
}
