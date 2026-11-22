package app

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeTimeOff: ausencias en memoria con la misma semántica que pgstore (scope
// por salón, el rango se pregunta en SQL contra el período: [from, to) pisado).
type fakeTimeOff struct {
	all []domain.TimeOff
	// saveErr simula que la DB rechaza el alta (EXCLUDE ante dos altas a la vez).
	saveErr error
	// readErr simula la DB caída al leer: la disponibilidad no puede ofrecer
	// turnos a ciegas si no sabe quién está ausente.
	readErr error
}

func (f *fakeTimeOff) Save(t domain.TimeOff) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.all = append(f.all, t)
	return nil
}

func (f *fakeTimeOff) ListRange(salonID string, from, to time.Time) ([]domain.TimeOff, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	out := make([]domain.TimeOff, 0, len(f.all))
	for _, t := range f.all {
		if t.SalonID == salonID && t.Overlaps(from, to) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeTimeOff) FindByID(salonID, id string) (domain.TimeOff, error) {
	for _, t := range f.all {
		if t.SalonID == salonID && t.ID == id {
			return t, nil
		}
	}
	return domain.TimeOff{}, domain.ErrNotFound
}

func (f *fakeTimeOff) Delete(salonID, id string) error {
	for i, t := range f.all {
		if t.SalonID == salonID && t.ID == id {
			f.all = append(f.all[:i], f.all[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

var _ ports.TimeOffRepository = (*fakeTimeOff)(nil)
