package httpapi

import (
	"errors"
	"sort"
	"sync"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// maudit imita pgstore.Audit: scope por salón, orden (created_at, id) DESC,
// cursor estricto y el filtro de acción de domain.AuditActionMatches. Puede
// fallar o entrar en pánico a pedido y recuerda el orden de las llamadas.
type maudit struct {
	mu        sync.Mutex
	rows      []domain.AuditEvent
	appendErr error
	panicOn   bool
	listErr   error
	// listCalls cuenta las lecturas: el permiso va antes de tocar la base.
	listCalls   int
	appendCalls int
	log         *[]string
}

func (f *maudit) note(s string) {
	if f.log != nil {
		*f.log = append(*f.log, s)
	}
}

func (f *maudit) Append(e domain.AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendCalls++
	f.note("append:" + e.Action)
	if f.panicOn {
		panic("audit writer panic")
	}
	if f.appendErr != nil {
		return f.appendErr
	}
	f.rows = append(f.rows, e)
	return nil
}

func (f *maudit) List(salonID string, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
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

// actions devuelve "acción|entidad" de lo escrito, en orden de escritura.
func (f *maudit) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.rows {
		out = append(out, e.Action+"|"+e.EntityID)
	}
	return out
}

var _ ports.AuditRepository = (*maudit)(nil)

var errAuditDown = errors.New("audit down")
