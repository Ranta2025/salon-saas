package waitlist_test

import (
	"sort"
	"strings"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de la lista de espera (F7). Reproducen la semántica que importa del
// adaptador de Postgres:
//
//   - una entrada ACTIVA idéntica (email + servicio + profesional + día) choca
//     con el índice único parcial: Create devuelve ErrAlreadyExists;
//   - el día es un string "YYYY-MM-DD" y solo se barre si es >= hoy local;
//   - el barrido toma el lock de escritura del salón y RECUENTA los retenidos
//     dentro de la transacción (el mutex hace de `pg_advisory_xact_lock`), y el
//     reclamo es FIFO por (created_at, id) sobre las entradas reclamables;
//   - el reclamo incrementa notify_attempts; un envío fallido queda como
//     reclamo viejo y se reintenta pasada la gracia, hasta 3 intentos.
type fakeWaitlist struct {
	mu      sync.Mutex
	entries []domain.WaitlistEntry
	// Errores simulados de la DB.
	createErr error
	findErr   error
	listErr   error
	// locks registra los locks pedidos dentro de la transacción del barrido.
	locks []string
	// claimCalls cuenta los reclamos (para probar que sin email no se reclama).
	claimCalls int
	// countGate, si no es nil, se llama al entrar a un conteo de topes y ANTES de
	// tomar el mutex: el test de atomicidad lo usa para forzar que todas las
	// altas simultáneas lean el conteo antes de que alguna escriba.
	countGate func()
}

func (f *fakeWaitlist) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return domain.WaitlistEntry{}, f.findErr
	}
	for _, e := range f.entries {
		if e.Active() && e.SalonID == salonID && e.ServiceID == serviceID &&
			e.ProfessionalID == professionalID && e.Day == day &&
			strings.EqualFold(e.Email, email) {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (f *fakeWaitlist) Create(e domain.WaitlistEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	for _, other := range f.entries {
		if other.Active() && other.SalonID == e.SalonID && other.ServiceID == e.ServiceID &&
			other.ProfessionalID == e.ProfessionalID && other.Day == e.Day &&
			strings.EqualFold(other.Email, e.Email) {
			return domain.ErrAlreadyExists
		}
	}
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeWaitlist) FindByTokenHash(tokenHash string) (domain.WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entries {
		if e.TokenHash == tokenHash {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (f *fakeWaitlist) Leave(tokenHash string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		// UPDATE condicional: lo ya dado de baja no se toca (idempotente).
		if f.entries[i].TokenHash == tokenHash && f.entries[i].LeftAt == nil {
			left := now.UTC()
			f.entries[i].LeftAt = &left
			return nil
		}
	}
	return nil
}

func (f *fakeWaitlist) ListUpcoming(salonID, fromDay string) ([]domain.WaitlistStaffEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []domain.WaitlistStaffEntry
	for _, e := range f.entries {
		if e.SalonID != salonID || !e.Active() || e.Day < fromDay {
			continue
		}
		out = append(out, domain.WaitlistStaffEntry{
			WaitlistEntry: e,
			ServiceName:   "Corte",
			// El nombre de la profesional lo resuelve el adaptador (JOIN users):
			// en el fake la entrada ya trae el id.
			ProfessionalName: map[string]string{"prof1": "Ana", "prof2": "Bruno"}[e.ProfessionalID],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (f *fakeWaitlist) RemoveByID(salonID, id string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		// UPDATE condicional con el scope del salón: otro tenant es 0 filas.
		if f.entries[i].ID == id && f.entries[i].SalonID == salonID && f.entries[i].LeftAt == nil {
			left := now.UTC()
			f.entries[i].LeftAt = &left
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeWaitlist) ListGroups(now time.Time) ([]domain.WaitlistGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	seen := map[string]bool{}
	var out []domain.WaitlistGroup
	for _, e := range f.entries {
		if !wlClaimable(e, now) {
			continue
		}
		key := e.SalonID + "|" + e.ServiceID + "|" + e.ProfessionalID + "|" + e.Day
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, domain.WaitlistGroup{
			SalonID: e.SalonID, SalonSlug: "navajas-oro", SalonName: "Navajas de Oro",
			ServiceID: e.ServiceID, ServiceName: "Corte",
			ProfessionalID: e.ProfessionalID, Day: e.Day,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SalonID != out[j].SalonID {
			return out[i].SalonID < out[j].SalonID
		}
		return out[i].Day < out[j].Day
	})
	return out, nil
}

func (f *fakeWaitlist) MarkNotified(id string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		if f.entries[i].ID == id {
			notified := now.UTC()
			f.entries[i].NotifiedAt = &notified
			return nil
		}
	}
	return domain.ErrNotFound
}

// wlClaimable es el WHERE del barrido: sin baja, sin aviso, con intentos, con el
// reclamo vencido y con un día que todavía no pasó.
func wlClaimable(e domain.WaitlistEntry, now time.Time) bool {
	if !e.Active() || e.NotifiedAt != nil || e.NotifyAttempts >= domain.MaxWaitlistAttempts {
		return false
	}
	if e.NotifyClaimedAt != nil && !e.NotifyClaimedAt.Before(now.UTC().Add(-domain.WaitlistRetryGrace)) {
		return false
	}
	return true
}

// wlRetained es el recuento de D6: enviados con reclamo de las últimas 2 h y los
// que están en vuelo (reclamo de los últimos 5 minutos sin aviso). Un envío que
// falló no retiene nada.
func wlRetained(e domain.WaitlistEntry, now time.Time) bool {
	if !e.Active() || e.NotifyClaimedAt == nil {
		return false
	}
	if e.NotifiedAt != nil {
		return e.NotifyClaimedAt.After(now.UTC().Add(-domain.WaitlistRetention))
	}
	return e.NotifyClaimedAt.After(now.UTC().Add(-domain.WaitlistRetryGrace))
}

// fakeWaitlistTx es el adaptador transaccional del barrido.
type fakeWaitlistTx struct{ repo *fakeWaitlist }

func (t *fakeWaitlistTx) LockWrites(salonID string) error {
	// El mutex ya lo tomó InWaitlistTx (hace de advisory lock de la transacción).
	t.repo.locks = append(t.repo.locks, "waitlist-write:"+salonID)
	return nil
}

// --- Primitivas del alta dentro de la transacción (D4/D6) ------------------
// InWaitlistTx ya tiene tomado el mutex del repo (hace de advisory lock), así que
// estas versiones NO lo vuelven a tomar.

func (t *fakeWaitlistTx) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	if t.repo.findErr != nil {
		return domain.WaitlistEntry{}, t.repo.findErr
	}
	for _, e := range t.repo.entries {
		if e.Active() && e.SalonID == salonID && e.ServiceID == serviceID &&
			e.ProfessionalID == professionalID && e.Day == day &&
			strings.EqualFold(e.Email, email) {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (t *fakeWaitlistTx) CountActiveByEmail(salonID, email, fromDay string) (int, error) {
	if t.repo.countGate != nil {
		t.repo.countGate()
	}
	n := 0
	for _, e := range t.repo.entries {
		if e.Active() && e.SalonID == salonID && strings.EqualFold(e.Email, email) && e.Day >= fromDay {
			n++
		}
	}
	return n, nil
}

func (t *fakeWaitlistTx) CountActiveBySalon(salonID, fromDay string) (int, error) {
	n := 0
	for _, e := range t.repo.entries {
		if e.Active() && e.SalonID == salonID && e.Day >= fromDay {
			n++
		}
	}
	return n, nil
}

func (t *fakeWaitlistTx) Create(e domain.WaitlistEntry) error {
	if t.repo.createErr != nil {
		return t.repo.createErr
	}
	for _, other := range t.repo.entries {
		if other.Active() && other.SalonID == e.SalonID && other.ServiceID == e.ServiceID &&
			other.ProfessionalID == e.ProfessionalID && other.Day == e.Day &&
			strings.EqualFold(other.Email, e.Email) {
			return domain.ErrAlreadyExists
		}
	}
	t.repo.entries = append(t.repo.entries, e)
	return nil
}

func (t *fakeWaitlistTx) MarkLeft(id string, now time.Time) error {
	for i := range t.repo.entries {
		if t.repo.entries[i].ID == id {
			left := now.UTC()
			t.repo.entries[i].LeftAt = &left
			return nil
		}
	}
	return domain.ErrNotFound
}

func (t *fakeWaitlistTx) CountRetained(g domain.WaitlistGroup, now time.Time) (int, error) {
	n := 0
	for _, e := range t.repo.entries {
		if e.SalonID == g.SalonID && e.ServiceID == g.ServiceID &&
			e.ProfessionalID == g.ProfessionalID && e.Day == g.Day && wlRetained(e, now) {
			n++
		}
	}
	return n, nil
}

func (t *fakeWaitlistTx) Claim(g domain.WaitlistGroup, now time.Time, limit int) ([]domain.WaitlistEntry, error) {
	t.repo.claimCalls++
	var picked []int
	for i, e := range t.repo.entries {
		if e.SalonID != g.SalonID || e.ServiceID != g.ServiceID ||
			e.ProfessionalID != g.ProfessionalID || e.Day != g.Day || !wlClaimable(e, now) {
			continue
		}
		picked = append(picked, i)
	}
	sort.SliceStable(picked, func(a, b int) bool {
		ea, eb := t.repo.entries[picked[a]], t.repo.entries[picked[b]]
		if !ea.CreatedAt.Equal(eb.CreatedAt) {
			return ea.CreatedAt.Before(eb.CreatedAt)
		}
		return ea.ID < eb.ID
	})
	if len(picked) > limit {
		picked = picked[:limit]
	}
	var out []domain.WaitlistEntry
	for _, i := range picked {
		claimed := now.UTC()
		t.repo.entries[i].NotifyClaimedAt = &claimed
		t.repo.entries[i].NotifyAttempts++
		out = append(out, t.repo.entries[i])
	}
	return out, nil
}

func (t *fakeWaitlistTx) SetToken(id, tokenHash string) error {
	for i := range t.repo.entries {
		if t.repo.entries[i].ID == id {
			t.repo.entries[i].TokenHash = tokenHash
			return nil
		}
	}
	return domain.ErrNotFound
}

func (t *fakeWaitlistTx) InWaitlistTx(fn func(tx ports.WaitlistTx) error) error {
	// El mutex del repo hace de advisory lock: una transacción a la vez, así dos
	// pasadas simultáneas no reclaman la misma entrada.
	t.repo.mu.Lock()
	defer t.repo.mu.Unlock()
	return fn(t)
}

var (
	_ ports.WaitlistRepository = (*fakeWaitlist)(nil)
	_ ports.WaitlistTxRunner   = (*fakeWaitlistTx)(nil)
)
