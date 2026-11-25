package app

import (
	"errors"
	"sort"
	"sync"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeAudit imita pgstore.Audit: scope por salón, orden (created_at, id) DESC,
// cursor estricto y el filtro de acción de domain.AuditActionMatches.
type fakeAudit struct {
	mu        sync.Mutex
	rows      []domain.AuditEvent
	appendErr error
	// listCalls cuenta las lecturas (el permiso va ANTES de tocar la base).
	listCalls   int
	appendCalls int
	lastQuery   domain.AuditQuery
}

func (f *fakeAudit) Append(e domain.AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendCalls++
	if f.appendErr != nil {
		return f.appendErr
	}
	f.rows = append(f.rows, e)
	return nil
}

func (f *fakeAudit) List(salonID string, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.lastQuery = q
	var out []domain.AuditEvent
	for _, e := range f.rows {
		if e.SalonID != salonID || !domain.AuditActionMatches(q.Action, e.Action) {
			continue
		}
		if q.Before != nil {
			b := q.Before
			if e.CreatedAt.After(b.CreatedAt) || (e.CreatedAt.Equal(b.CreatedAt) && e.ID >= b.ID) {
				continue
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

var _ ports.AuditRepository = (*fakeAudit)(nil)

var errAuditDown = errors.New("audit down")
