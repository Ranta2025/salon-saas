package httpkit

import (
	"errors"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sort"
	"sync"
)

// AuditEvents imita pgstore.Audit: scope por salón, orden (created_at, id) DESC,
// cursor estricto y el filtro de acción de domain.AuditActionMatches. Puede
// fallar o entrar en pánico a pedido y recuerda el orden de las llamadas.
type AuditEvents struct {
	Mu        sync.Mutex
	Rows      []domain.AuditEvent
	AppendErr error
	PanicOn   bool
	ListErr   error
	// listCalls cuenta las lecturas: el permiso va antes de tocar la base.
	ListCalls   int
	AppendCalls int
	Log         *[]string
}

func (f *AuditEvents) note(s string) {
	if f.Log != nil {
		*f.Log = append(*f.Log, s)
	}
}

func (f *AuditEvents) Append(e domain.AuditEvent) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.AppendCalls++
	f.note("append:" + e.Action)
	if f.PanicOn {
		panic("audit writer panic")
	}
	if f.AppendErr != nil {
		return f.AppendErr
	}
	f.Rows = append(f.Rows, e)
	return nil
}

func (f *AuditEvents) List(salonID string, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.ListCalls++
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	var out []domain.AuditEvent
	for _, e := range f.Rows {
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
func (f *AuditEvents) Actions() []string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []string
	for _, e := range f.Rows {
		out = append(out, e.Action+"|"+e.EntityID)
	}
	return out
}

var _ ports.AuditRepository = (*AuditEvents)(nil)

var ErrAuditDown = errors.New("audit down")
