// Dobles en memoria del módulo de reseñas: la misma semántica que el adaptador
// de Postgres para las reglas que se prueban sin base (D4..D7).
package reviews

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeMemberships: membership activa por (usuario, salón).
type fakeMemberships struct {
	rows map[string]domain.Membership
	// err fuerza un fallo de la lectura (membership ilegible).
	err error
}

func (f *fakeMemberships) Save(m domain.Membership) error {
	if f.rows == nil {
		f.rows = map[string]domain.Membership{}
	}
	f.rows[m.UserID+"|"+m.SalonID] = m
	return nil
}

func (f *fakeMemberships) Find(userID, salonID string) (domain.Membership, error) {
	if f.err != nil {
		return domain.Membership{}, f.err
	}
	m, ok := f.rows[userID+"|"+salonID]
	if !ok || !m.Active() {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// fakeStore es el adaptador en memoria: configuración, reseñas, pedidos y enlace.
type fakeStore struct {
	mu       sync.Mutex
	settings map[string]ports.ReviewSettings
	reviews  []ports.Review
	requests []fakeRequest
	due      []ports.DueReview
	// visibleErr fuerza el fallo de la sección pública (D7: degradar, nunca 500).
	visibleErr error
	// linkErr y submitErr fuerzan fallos de la base.
	linkErr, submitErr, claimErr, saveErr, listErr, hiddenErr error
	// clock de los pedidos creados (el de Claim).
	claims int
	sentTo []string
}

type fakeRequest struct {
	id, salonID, bookingID, tokenHash, clientName, serviceName string
	createdAt, expiresAt                                       time.Time
	sentAt, usedAt, deliveredAt                                *time.Time
	startsAt                                                   time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{settings: map[string]ports.ReviewSettings{}}
}

func (f *fakeStore) Settings(salonID string) (ports.ReviewSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.settings[salonID]
	if !ok {
		return ports.ReviewSettings{}, domain.ErrNotFound
	}
	return st, nil
}

func (f *fakeStore) SaveSettings(s ports.ReviewSettings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	s.Saved = true
	f.settings[s.SalonID] = s
	return nil
}

func (f *fakeStore) List(salonID string, q ports.ReviewQuery) ([]ports.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []ports.Review
	for _, r := range f.reviews {
		if r.SalonID != salonID {
			continue
		}
		if q.Before != nil {
			if !r.CreatedAt.Before(q.Before.CreatedAt) && !(r.CreatedAt.Equal(q.Before.CreatedAt) && r.ID < q.Before.ID) {
				continue
			}
		}
		out = append(out, r)
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

func (f *fakeStore) SetHidden(salonID, reviewID string, hidden bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hiddenErr != nil {
		return f.hiddenErr
	}
	for i := range f.reviews {
		if f.reviews[i].SalonID == salonID && f.reviews[i].ID == reviewID {
			f.reviews[i].Hidden = hidden
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeStore) Visible(salonID string) (ports.PublicReviews, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.visibleErr != nil {
		return ports.PublicReviews{}, f.visibleErr
	}
	sum := 0
	var visible []ports.Review
	for _, r := range f.reviews {
		if r.SalonID != salonID || r.Hidden {
			continue
		}
		sum += r.Rating
		visible = append(visible, r)
	}
	out := ports.PublicReviews{Items: []ports.PublicReview{}}
	if len(visible) == 0 {
		return out, nil
	}
	sort.Slice(visible, func(i, j int) bool {
		if !visible[i].CreatedAt.Equal(visible[j].CreatedAt) {
			return visible[i].CreatedAt.After(visible[j].CreatedAt)
		}
		return visible[i].ID > visible[j].ID
	})
	out.Count = len(visible)
	out.Average = float64(sum) / float64(len(visible))
	for i, r := range visible {
		if i >= MaxPublicReviews {
			break
		}
		out.Items = append(out.Items, ports.PublicReview{
			Rating: r.Rating, Comment: r.Comment, Author: r.AuthorName,
			Date: r.CreatedAt.In(time.UTC).Format("2006-01-02"),
		})
	}
	return out, nil
}

func (f *fakeStore) Submit(req ports.ReviewSubmit) (ports.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return ports.Review{}, f.submitErr
	}
	for i := range f.requests {
		r := &f.requests[i]
		if r.tokenHash != req.TokenHash {
			continue
		}
		if r.usedAt != nil || !req.Now.Before(r.expiresAt) {
			return ports.Review{}, domain.ErrNotFound
		}
		used := req.Now
		r.usedAt = &used
		review := ports.Review{
			ID: "rv-" + r.bookingID, SalonID: r.salonID, BookingID: r.bookingID,
			Rating: req.Rating, Comment: req.Comment,
			AuthorName: AuthorName(r.clientName), ServiceName: ServiceName(r.serviceName),
			CreatedAt: req.Now,
		}
		for _, other := range f.reviews {
			if other.BookingID == r.bookingID {
				return ports.Review{}, domain.ErrAlreadyExists
			}
		}
		f.reviews = append(f.reviews, review)
		return review, nil
	}
	return ports.Review{}, domain.ErrNotFound
}

func (f *fakeStore) Link(tokenHash string, now time.Time) (ports.ReviewLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.linkErr != nil {
		return ports.ReviewLink{}, f.linkErr
	}
	for _, r := range f.requests {
		if r.tokenHash != tokenHash {
			continue
		}
		if r.usedAt != nil || !now.Before(r.expiresAt) {
			return ports.ReviewLink{}, domain.ErrNotFound
		}
		return ports.ReviewLink{SalonName: r.salonID, ServiceName: r.serviceName, StartsAt: r.startsAt, Timezone: "UTC"}, nil
	}
	return ports.ReviewLink{}, domain.ErrNotFound
}

// claimable replica la condición del adaptador: entregado, usado o vencido no se
// reclama; un arriendo vivo tampoco.
func claimable(r fakeRequest, now time.Time) bool {
	if r.deliveredAt != nil || r.usedAt != nil || !r.expiresAt.After(now) {
		return false
	}
	return r.sentAt == nil || !r.sentAt.After(now.Add(-ports.ClaimLease))
}

func (f *fakeStore) Due(now time.Time, limit int) ([]ports.DueReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.DueReview, 0, len(f.due))
	for _, d := range f.due {
		skip := false
		for _, r := range f.requests {
			if r.bookingID == d.BookingID && !claimable(r, now) {
				skip = true
			}
		}
		if skip {
			continue
		}
		out = append(out, d)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) Claim(req ports.ReviewClaim) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return "", false, f.claimErr
	}
	for i := range f.requests {
		r := &f.requests[i]
		if r.bookingID != req.BookingID {
			continue
		}
		if !claimable(*r, req.Now) {
			return "", false, nil
		}
		// Reclamable: rota el token y renueva el arriendo.
		r.tokenHash, r.expiresAt = req.TokenHash, req.ExpiresAt
		sent := req.Now
		r.sentAt = &sent
		f.claims++
		return r.id, true, nil
	}
	sent := req.Now
	f.requests = append(f.requests, fakeRequest{
		id: req.ID, salonID: req.SalonID, bookingID: req.BookingID, tokenHash: req.TokenHash,
		createdAt: req.Now, expiresAt: req.ExpiresAt, sentAt: &sent,
	})
	f.claims++
	return req.ID, true, nil
}

func (f *fakeStore) Unclaim(requestID string, sentAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.requests {
		r := &f.requests[i]
		if r.id != requestID {
			continue
		}
		if r.sentAt != nil && r.sentAt.Equal(sentAt) {
			r.sentAt = nil
		}
	}
	return nil
}

func (f *fakeStore) MarkDelivered(requestID string, sentAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.requests {
		r := &f.requests[i]
		if r.id != requestID || r.sentAt == nil || !r.sentAt.Equal(sentAt) || r.deliveredAt != nil {
			continue
		}
		delivered := sentAt
		r.deliveredAt = &delivered
	}
	return nil
}

// requestsOf devuelve una copia de los pedidos (para inspeccionar en los tests).
func (f *fakeStore) requestsOf() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

// addReview siembra una reseña publicada.
func (f *fakeStore) addReview(r ports.Review) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviews = append(f.reviews, r)
}

// addRequest siembra un pedido (enlace) en el estado que pida el test.
func (f *fakeStore) addRequest(r fakeRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
}

// fakeMailer cuenta correos y puede fallar.
type fakeMailer struct {
	mu    sync.Mutex
	sent  []ports.Message
	fails int
	// failNext hace fallar los próximos N envíos.
	failNext int
}

func (f *fakeMailer) Send(_ context.Context, msg ports.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	if f.failNext > 0 {
		f.failNext--
		f.fails++
		return errors.New("smtp caído")
	}
	return nil
}

func (f *fakeMailer) messages() []ports.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ports.Message(nil), f.sent...)
}

// containsToken dice si un texto lleva el token en claro (por el alfabeto y el
// largo no alcanza: se compara contra el hash guardado).
func containsToken(text, token string) bool {
	return token != "" && strings.Contains(text, token)
}
