package httpkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/app"
	"salon-saas/internal/auth/security"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/settings"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Fakes de F11 con la MISMA semántica que el adaptador Postgres (lock simulado
// con un mutex, UPDATE condicional, libro mayor append-only y el índice único del
// evento del banco) y que el banco simulado. Un handler que dependiera de
// detalles de SQL no compilaría contra estos fakes.

// --- Política ---------------------------------------------------------------

type DepositPolicies struct {
	Mu   sync.Mutex
	Rows map[string]domain.DepositPolicy
}

func (f *DepositPolicies) Find(salonID string) (domain.DepositPolicy, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	p, ok := f.Rows[salonID]
	if !ok {
		return domain.DepositPolicy{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *DepositPolicies) Save(p domain.DepositPolicy) (domain.DepositPolicy, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Rows == nil {
		f.Rows = map[string]domain.DepositPolicy{}
	}
	p.Saved = true
	f.Rows[p.SalonID] = p
	return p, nil
}

func (f *DepositPolicies) Seed(p domain.DepositPolicy) {
	p.Saved = true
	if f.Rows == nil {
		f.Rows = map[string]domain.DepositPolicy{}
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Rows[p.SalonID] = p
}

var _ ports.DepositPolicyRepository = (*DepositPolicies)(nil)

// --- Señas y libro mayor ----------------------------------------------------

type Deposits struct {
	Mu       sync.Mutex
	SalonID  string
	Rows     map[string]domain.BookingDeposit
	Events   []domain.DepositEvent
	Bookings map[string]domain.Booking
}

func NewDeposits(salonID string) *Deposits {
	return &Deposits{SalonID: salonID, Rows: map[string]domain.BookingDeposit{}, Bookings: map[string]domain.Booking{}}
}

// addBooking deja el turno que la sena referencia (estado, inicio y clienta).
func (f *Deposits) addBooking(id string, status domain.BookingStatus, startsAt time.Time, clientName string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Bookings[id] = domain.Booking{
		ID: id, SalonID: f.SalonID, ClientName: clientName, Status: status,
		StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour),
	}
}

// seed crea una sena con su evento `created`, como el adaptador.
func (f *Deposits) Seed(d domain.BookingDeposit) domain.BookingDeposit {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if d.Status == "" {
		d.Status = domain.DepositPending
	}
	if d.ID == "" {
		d.ID = domain.NewID()
	}
	if d.SalonID == "" {
		d.SalonID = f.SalonID
	}
	d = f.withBooking(d)
	f.Rows[d.ID] = d
	f.Events = append(f.Events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventCreated, AmountCents: d.AmountCents, CreatedAt: d.CreatedAt})
	return d
}

func (f *Deposits) withBooking(d domain.BookingDeposit) domain.BookingDeposit {
	if b, ok := f.Bookings[d.BookingID]; ok {
		d.BookingStatus, d.StartsAt, d.ClientName = b.Status, b.StartsAt, b.ClientName
	}
	return d
}

func (f *Deposits) eventsFor(depositID string) []domain.DepositEvent {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []domain.DepositEvent
	for _, e := range f.Events {
		if e.DepositID == depositID {
			out = append(out, e)
		}
	}
	return out
}

func (f *Deposits) EventKinds(depositID string) []string {
	var out []string
	for _, e := range f.eventsFor(depositID) {
		out = append(out, e.Kind)
	}
	return out
}

func (f *Deposits) find(chargeID string) (domain.BookingDeposit, bool) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, d := range f.Rows {
		if d.ChargeID == chargeID {
			return f.withBooking(d), true
		}
	}
	return domain.BookingDeposit{}, false
}

func (f *Deposits) seen(eventID string) bool {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, e := range f.Events {
		if eventID != "" && e.BankEventID == eventID {
			return true
		}
	}
	return false
}

func (f *Deposits) Create(d domain.BookingDeposit) (domain.BookingDeposit, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, existing := range f.Rows {
		if existing.BookingID == d.BookingID ||
			(d.ClientOpID != "" && existing.SalonID == d.SalonID && existing.ClientOpID == d.ClientOpID) {
			return f.withBooking(existing), nil
		}
	}
	d = f.withBooking(d)
	f.Rows[d.ID] = d
	f.Events = append(f.Events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventCreated, AmountCents: d.AmountCents, CreatedAt: d.CreatedAt})
	return d, nil
}

func (f *Deposits) FindByBooking(salonID, bookingID string) (domain.BookingDeposit, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, d := range f.Rows {
		if d.SalonID == salonID && d.BookingID == bookingID {
			return f.withBooking(d), nil
		}
	}
	return domain.BookingDeposit{}, domain.ErrNotFound
}

func (f *Deposits) FindByCharge(chargeID string) (domain.BookingDeposit, error) {
	if d, ok := f.find(chargeID); ok {
		return d, nil
	}
	return domain.BookingDeposit{}, domain.ErrNotFound
}

func (f *Deposits) ApplyApproved(cb domain.DepositCallback) (domain.DepositApply, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
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
	f.Rows[d.ID] = d
	f.Events = append(f.Events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventPaid, AmountCents: d.AmountCents, BankEventID: cb.EventID, CreatedAt: now})
	return domain.DepositApply{Applied: true, Deposit: d}, nil
}

func (f *Deposits) ApplyDeclined(cb domain.DepositCallback) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	d, ok := f.findLocked(cb.ChargeID)
	if !ok {
		return false, domain.ErrNotFound
	}
	if f.seenLocked(cb.EventID) {
		return false, nil
	}
	f.Events = append(f.Events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: domain.DepositEventAttemptDeclined, AmountCents: cb.AmountCents, BankEventID: cb.EventID, CreatedAt: cb.Now.UTC()})
	return true, nil
}

func (f *Deposits) findLocked(chargeID string) (domain.BookingDeposit, bool) {
	for _, d := range f.Rows {
		if d.ChargeID == chargeID {
			return f.withBooking(d), true
		}
	}
	return domain.BookingDeposit{}, false
}

func (f *Deposits) seenLocked(eventID string) bool {
	if eventID == "" {
		return false
	}
	for _, e := range f.Events {
		if e.BankEventID == eventID {
			return true
		}
	}
	return false
}

func (f *Deposits) ExpirePending(now time.Time, limit int) ([]domain.BookingDeposit, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []domain.BookingDeposit
	for id, d := range f.Rows {
		if len(out) == limit {
			break
		}
		d = f.withBooking(d)
		if d.Status == domain.DepositPending && !now.Before(d.ExpiresAt) {
			d.Status = domain.DepositExpired
			f.Rows[id] = d
			f.Events = append(f.Events, domain.DepositEvent{ID: domain.NewID(), DepositID: id,
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

func (f *Deposits) ListPaidCancelled(limit int) ([]domain.BookingDeposit, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []domain.BookingDeposit
	for _, d := range f.Rows {
		d = f.withBooking(d)
		if d.Status == domain.DepositPaid && d.BookingStatus == domain.BookingCancelled && len(out) < limit {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *Deposits) SettleLateRefund(s domain.DepositSettlement) (bool, error) {
	return f.settle(s, domain.DepositEventLatePaymentRefunded, func(d domain.BookingDeposit) bool {
		return d.Status == domain.DepositPending || d.Status == domain.DepositExpired
	})
}

func (f *Deposits) SettlePaidCancelled(s domain.DepositSettlement) (bool, error) {
	kind := domain.DepositEventRefunded
	if s.Status == domain.DepositForfeited {
		kind = domain.DepositEventForfeited
	}
	return f.settle(s, kind, func(d domain.BookingDeposit) bool { return d.Status == domain.DepositPaid })
}

func (f *Deposits) settle(s domain.DepositSettlement, kind string, from func(domain.BookingDeposit) bool) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	d, ok := f.Rows[s.DepositID]
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
	f.Rows[d.ID] = d
	f.Events = append(f.Events, domain.DepositEvent{ID: domain.NewID(), DepositID: d.ID,
		Kind: kind, AmountCents: d.AmountCents, Detail: s.Detail, BankEventID: s.BankEventID, CreatedAt: at})
	return true, nil
}

func (f *Deposits) List(salonID string, q domain.DepositQuery) ([]domain.BookingDeposit, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []domain.BookingDeposit
	for _, d := range f.Rows {
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

var _ ports.DepositRepository = (*Deposits)(nil)

// --- Banco (PaymentGateway) --------------------------------------------------

type Gateway struct {
	Mu      sync.Mutex
	Secret  string
	On      bool
	Charges map[string]ports.ChargeRequest
	Refunds map[string]int
	Next    int
	Fail    error
}

func NewGateway(secret string) *Gateway {
	return &Gateway{Secret: secret, On: true, Charges: map[string]ports.ChargeRequest{}, Refunds: map[string]int{}}
}

func (g *Gateway) Enabled() bool { return g.On }

func (g *Gateway) CreateCharge(_ context.Context, r ports.ChargeRequest) (ports.Charge, error) {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	if g.Fail != nil {
		return ports.Charge{}, g.Fail
	}
	id, err := domain.NewDepositChargeID()
	if err != nil {
		return ports.Charge{}, err
	}
	g.Next++
	g.Charges[id] = r
	return ports.Charge{ID: id, PayPath: "/pagar#" + id}, nil
}

func (g *Gateway) Refund(_ context.Context, chargeID string, _ int, _ string) error {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	g.Refunds[chargeID]++
	return nil
}

func (g *Gateway) ParseCallback(header http.Header, body []byte, now time.Time) (ports.CallbackEvent, error) {
	if !g.On {
		return ports.CallbackEvent{}, domain.NewMessage(domain.ErrNotFound, domain.MsgDepositNotFound)
	}
	if err := domain.VerifyPaymentSignature(g.Secret, header.Get("X-Simbank-Signature"), body, now); err != nil {
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

func (g *Gateway) chargeCount() int {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	return len(g.Charges)
}

func (g *Gateway) RefundCount(chargeID string) int {
	g.Mu.Lock()
	defer g.Mu.Unlock()
	return g.Refunds[chargeID]
}

var _ ports.PaymentGateway = (*Gateway)(nil)

// --- Turnos (cancelación) ----------------------------------------------------

type DepositBookings struct {
	Mu      sync.Mutex
	Rows    map[string]domain.Booking
	Cancels []string
}

func (f *DepositBookings) CancelBooking(salonID, bookingID string, _ time.Time) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	b, ok := f.Rows[bookingID]
	if !ok || b.SalonID != salonID || b.Status != domain.BookingBooked {
		return nil
	}
	cancelled, err := b.SetStatus(domain.BookingCancelled)
	if err != nil {
		return err
	}
	f.Rows[bookingID] = cancelled
	f.Cancels = append(f.Cancels, bookingID)
	return nil
}

var _ ports.BookingCanceller = (*DepositBookings)(nil)

// --- Aviso de la confirmación ------------------------------------------------

type DepositNotices struct {
	NoticeField domain.BookingNotice
	Err         error
}

func (f *DepositNotices) Notice(salonID, bookingID string) (domain.BookingNotice, error) {
	if f.Err != nil {
		return domain.BookingNotice{}, f.Err
	}
	if f.NoticeField.SalonID == "" {
		return domain.BookingNotice{}, domain.ErrNotFound
	}
	return f.NoticeField, nil
}

var _ ports.BookingNoticeRepository = (*DepositNotices)(nil)

// --- Entorno ----------------------------------------------------------------

// DepWorld es un salón con todos los roles, una sena pagable y el banco de
// prueba cableado en las rutas.
type DepWorld struct {
	Env      *Env
	Policies *DepositPolicies
	Deposits *Deposits
	Gateway  *Gateway
	Notices  *DepositNotices
	Bookings *DepositBookings
	Salon    string
	Slug     string
	Secret   string
	// Tokens por rol.
	Owner, Admin, Recep, Prof, Removed, Outsider string
	OwnerID, ProfID                              string
	// chargeID es un cargo de 43 caracteres base64url (la credencial de D8).
	ChargeID  string
	DepositID string
	StartsAt  time.Time
}

// fastDepUser crea una cuenta y devuelve (token, id).
func (w *DepWorld) fastDepUser(t *testing.T, email string) (string, string) {
	t.Helper()
	u := domain.User{ID: domain.NewID(), Name: email, Email: email, PasswordHash: "x"}
	if err := w.Env.Users.Save(u); err != nil {
		t.Fatal(err)
	}
	tok, err := security.JWTIssuer{Secret: "test-secret"}.Issue(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tok, u.ID
}

// NewDepositEnv arma el salón, los roles y el cableado de F11 sobre los fakes.
func NewDepositEnv(t *testing.T) *DepWorld {
	t.Helper()
	env := NewPOSEnv()
	w := &DepWorld{
		Env: env, Policies: &DepositPolicies{}, Secret: "simbank-test-secret",
		Slug: "senas-uno", StartsAt: time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute),
	}
	w.Owner, w.OwnerID = w.fastDepUser(t, "owner-senas@mail.com")
	w.Salon = env.Salon(t, w.Owner, w.Slug)
	w.Deposits = NewDeposits(w.Salon)
	w.Gateway = NewGateway(w.Secret)
	w.Notices = &DepositNotices{}
	w.Bookings = &DepositBookings{Rows: map[string]domain.Booking{}}
	var id string
	w.Admin, id = w.fastDepUser(t, "admin-senas@mail.com")
	SeedRole(t, env, w.Salon, id, domain.RoleAdmin)
	w.Recep, id = w.fastDepUser(t, "recep-senas@mail.com")
	SeedRole(t, env, w.Salon, id, domain.RoleReception)
	w.Prof, w.ProfID = w.fastDepUser(t, "prof-senas@mail.com")
	SeedRole(t, env, w.Salon, w.ProfID, domain.RoleProfessional)
	w.Removed, id = w.fastDepUser(t, "removed-senas@mail.com")
	SeedRole(t, env, w.Salon, id, domain.RoleAdmin)
	at := time.Now().Add(-time.Hour)
	m := env.Mems.ByKey[id+"|"+w.Salon]
	m.EndedAt = &at
	env.Mems.ByKey[id+"|"+w.Salon] = m
	w.Outsider, _ = w.fastDepUser(t, "other-senas@mail.com")
	env.Salon(t, w.Outsider, "senas-dos")
	w.wire(t)
	return w
}

// wire monta los servicios de F11 en el router con sus handlers y rutas.
func (w *DepWorld) wire(t *testing.T) {
	t.Helper()
	svc := app.DepositService{
		Policies: w.Policies, Deposits: w.Deposits, Gateway: w.Gateway,
		Bookings: w.Bookings, Notices: w.Notices, Memberships: w.Env.Mems,
		AppURL: "http://localhost:8081", Settings: w.Env.Settings,
	}
	policySvc := app.DepositPolicyService{Policies: w.Policies, Memberships: w.Env.Mems, Gateway: w.Gateway}
	w.Env.Deps.DepositPolicy = httpapi.DepositPolicyHandler{Policy: policySvc, Memberships: w.Env.Mems}
	w.Env.Deps.Deposits = httpapi.DepositHandler{Deposits: svc, Memberships: w.Env.Mems}
	w.Env.Deps.PaymentsCallback = httpapi.PaymentsHandler{Deposits: svc}.Callback
	// El payload público (D11) lee la misma política.
	w.Env.Deps.PublicSalon.Public = settings.PublicSalonService{
		Salons: w.Env.Salons, Services: w.Env.Services, Settings: w.Env.Settings,
		Hours: w.Env.Weekly, Deposits: w.Policies,
	}
	w.Env.Router = gin.New()
	httpapi.RegisterRoutes(w.Env.Router, w.Env.Deps)
}

// seedCharge deja una sena pendiente con un cargo válido y su turno.
func (w *DepWorld) SeedCharge(t *testing.T, status domain.BookingStatus) domain.BookingDeposit {
	t.Helper()
	chargeID, err := domain.NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	w.ChargeID = chargeID
	bookingID := domain.NewID()
	w.Deposits.addBooking(bookingID, status, w.StartsAt, "Lía Pérez")
	d := w.Deposits.Seed(domain.BookingDeposit{
		SalonID: w.Salon, BookingID: bookingID, AmountCents: 4500, Percent: 30,
		Status: domain.DepositPending, ChargeID: chargeID,
		CreatedAt: time.Now().UTC().Add(-time.Minute),
		ExpiresAt: time.Now().UTC().Add(30 * time.Minute),
	})
	w.DepositID = d.ID
	w.Bookings.Rows[bookingID] = domain.Booking{ID: bookingID, SalonID: w.Salon, Status: status, StartsAt: w.StartsAt}
	return d
}

// callbackRaw arma el cuerpo del callback y su firma (D6/D7).
func (w *DepWorld) CallbackRaw(eventID, chargeID, kind string, amount int) (string, string) {
	body := `{"event_id":"` + eventID + `","charge_id":"` + chargeID + `","reference":"ref","kind":"` + kind + `","amount_cents":` + Itoa(amount) + `}`
	sig := domain.SignPayment(w.Secret, time.Now().UTC(), []byte(body))
	return body, sig
}

// postSigned hace un POST con la cabecera de firma (o sin ella si sig == "").
func (w *DepWorld) PostSigned(path, body, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Simbank-Signature", sig)
	}
	rec := httptest.NewRecorder()
	w.Env.Router.ServeHTTP(rec, req)
	return rec
}
