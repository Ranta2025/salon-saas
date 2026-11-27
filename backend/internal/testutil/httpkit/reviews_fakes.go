package httpkit

import (
	"sort"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Reviews es el doble en memoria de las reseñas posvisita (F13): la misma
// semántica del adaptador de Postgres para lo que se prueba por HTTP (D4..D7).
// Tiene mutex porque la confirmación del pedido viaja por el barrido periódico.
type Reviews struct {
	mu     sync.Mutex
	conf   map[string]ports.ReviewSettings
	rows   []ports.Review
	reqs   []ReviewRequestRow
	due    []ports.DueReview
	claims int

	// Fallos inyectables (la sección pública se degrada, nunca un 500).
	VisibleErr, ListErr, LinkErr, SubmitErr, HideErr, SaveErr, ClaimErr error
}

// ReviewRequestRow es un pedido de reseña sembrado en el doble.
type ReviewRequestRow struct {
	ID          string
	SalonID     string
	BookingID   string
	TokenHash   string
	ClientName  string
	ServiceName string
	StartsAt    time.Time
	CreatedAt   time.Time
	ExpiresAt   time.Time
	SentAt      *time.Time
	UsedAt      *time.Time
	DeliveredAt *time.Time
}

func NewReviews() *Reviews {
	return &Reviews{conf: map[string]ports.ReviewSettings{}}
}

// SeedReview siembra una reseña publicada.
func (f *Reviews) SeedReview(r ports.Review) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, r)
}

// SeedRequest siembra un pedido (el enlace del correo).
func (f *Reviews) SeedRequest(r ReviewRequestRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
}

// SeedDue siembra un turno atendido listo para pedir reseña.
func (f *Reviews) SeedDue(d ports.DueReview) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.due = append(f.due, d)
}

// TheSettings devuelve la configuración guardada de un salón.
func (f *Reviews) TheSettings(salonID string) (ports.ReviewSettings, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.conf[salonID]
	return st, ok
}

// Rows devuelve las reseñas guardadas (copia).
func (f *Reviews) Rows() []ports.Review {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ports.Review(nil), f.rows...)
}

// Requests devuelve los pedidos (copia).
func (f *Reviews) Requests() []ReviewRequestRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ReviewRequestRow(nil), f.reqs...)
}

// --- ports.ReviewRepository -------------------------------------------------

func (f *Reviews) Settings(salonID string) (ports.ReviewSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.conf[salonID]
	if !ok {
		return ports.ReviewSettings{}, domain.ErrNotFound
	}
	return st, nil
}

func (f *Reviews) SaveSettings(s ports.ReviewSettings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SaveErr != nil {
		return f.SaveErr
	}
	s.Saved = true
	f.conf[s.SalonID] = s
	return nil
}

func (f *Reviews) List(salonID string, q ports.ReviewQuery) ([]ports.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	var out []ports.Review
	for _, r := range f.rows {
		if r.SalonID != salonID {
			continue
		}
		if q.Before != nil {
			before := q.Before.CreatedAt
			if !r.CreatedAt.Before(before) && !(r.CreatedAt.Equal(before) && r.ID < q.Before.ID) {
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

func (f *Reviews) SetHidden(salonID, reviewID string, hidden bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.HideErr != nil {
		return f.HideErr
	}
	for i := range f.rows {
		if f.rows[i].SalonID == salonID && f.rows[i].ID == reviewID {
			f.rows[i].Hidden = hidden
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Reviews) Visible(salonID string) (ports.PublicReviews, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.VisibleErr != nil {
		return ports.PublicReviews{}, f.VisibleErr
	}
	sum := 0
	var visible []ports.Review
	for _, r := range f.rows {
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
		if i == 6 {
			break
		}
		out.Items = append(out.Items, ports.PublicReview{
			Rating: r.Rating, Comment: r.Comment, Author: r.AuthorName,
			Date: r.CreatedAt.UTC().Format("2006-01-02"),
		})
	}
	return out, nil
}

func (f *Reviews) Submit(req ports.ReviewSubmit) (ports.Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SubmitErr != nil {
		return ports.Review{}, f.SubmitErr
	}
	for i := range f.reqs {
		r := &f.reqs[i]
		if r.TokenHash != req.TokenHash {
			continue
		}
		if r.UsedAt != nil || !req.Now.Before(r.ExpiresAt) {
			return ports.Review{}, domain.ErrNotFound
		}
		for _, other := range f.rows {
			if other.BookingID == r.BookingID {
				return ports.Review{}, domain.ErrAlreadyExists
			}
		}
		used := req.Now
		r.UsedAt = &used
		review := ports.Review{
			ID: "rv-" + r.BookingID, SalonID: r.SalonID, BookingID: r.BookingID,
			Rating: req.Rating, Comment: req.Comment, AuthorName: r.ClientName,
			ServiceName: r.ServiceName, CreatedAt: req.Now,
		}
		f.rows = append(f.rows, review)
		return review, nil
	}
	return ports.Review{}, domain.ErrNotFound
}

// --- ports.ReviewRequestRepository ------------------------------------------

func (f *Reviews) Link(tokenHash string, now time.Time) (ports.ReviewLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LinkErr != nil {
		return ports.ReviewLink{}, f.LinkErr
	}
	for _, r := range f.reqs {
		if r.TokenHash != tokenHash {
			continue
		}
		if r.UsedAt != nil || !now.Before(r.ExpiresAt) {
			return ports.ReviewLink{}, domain.ErrNotFound
		}
		return ports.ReviewLink{SalonName: r.SalonID, ServiceName: r.ServiceName,
			StartsAt: r.StartsAt, Timezone: "UTC"}, nil
	}
	return ports.ReviewLink{}, domain.ErrNotFound
}

// claimable replica la condición del adaptador de Postgres (arriendo incluido).
func reviewClaimable(r ReviewRequestRow, now time.Time) bool {
	if r.DeliveredAt != nil || r.UsedAt != nil || !r.ExpiresAt.After(now) {
		return false
	}
	return r.SentAt == nil || !r.SentAt.After(now.Add(-ports.ClaimLease))
}

func (f *Reviews) Due(now time.Time, limit int) ([]ports.DueReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.DueReview, 0, len(f.due))
	for _, d := range f.due {
		skip := false
		for _, r := range f.reqs {
			if r.BookingID == d.BookingID && !reviewClaimable(r, now) {
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

func (f *Reviews) Claim(req ports.ReviewClaim) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ClaimErr != nil {
		return "", false, f.ClaimErr
	}
	for i := range f.reqs {
		r := &f.reqs[i]
		if r.BookingID != req.BookingID {
			continue
		}
		if !reviewClaimable(*r, req.Now) {
			return "", false, nil
		}
		r.TokenHash, r.ExpiresAt = req.TokenHash, req.ExpiresAt
		sent := req.Now
		r.SentAt = &sent
		f.claims++
		return r.ID, true, nil
	}
	sent := req.Now
	f.reqs = append(f.reqs, ReviewRequestRow{
		ID: req.ID, SalonID: req.SalonID, BookingID: req.BookingID, TokenHash: req.TokenHash,
		CreatedAt: req.Now, ExpiresAt: req.ExpiresAt, SentAt: &sent,
	})
	f.claims++
	return req.ID, true, nil
}

func (f *Reviews) Unclaim(requestID string, sentAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.reqs {
		if f.reqs[i].ID == requestID && f.reqs[i].SentAt != nil && f.reqs[i].SentAt.Equal(sentAt) {
			f.reqs[i].SentAt = nil
		}
	}
	return nil
}

// MarkDelivered sella la entrega (D3): solo si este proceso tiene el arriendo.
func (f *Reviews) MarkDelivered(requestID string, sentAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.reqs {
		r := &f.reqs[i]
		if r.ID != requestID || r.SentAt == nil || !r.SentAt.Equal(sentAt) || r.DeliveredAt != nil {
			continue
		}
		delivered := sentAt
		r.DeliveredAt = &delivered
	}
	return nil
}

// PublicSummary es la vista que consume la vidriera pública (D7).
func (f *Reviews) PublicSummary(salonID string) (ports.PublicReviews, error) {
	return f.Visible(salonID)
}

var (
	_ ports.ReviewRepository        = (*Reviews)(nil)
	_ ports.ReviewRequestRepository = (*Reviews)(nil)
	_ ports.PublicReviewReader      = (*Reviews)(nil)
)
