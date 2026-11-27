package testutil

import (
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeWeeklyHours: horario semanal del salón y de cada persona en memoria, con
// la misma semántica que pgstore (reemplazo total, scope por salón, sin filas =
// comportamiento de siempre: ventana única y la profesional sigue al salón).
//
// Es la copia exportada del fake de los tests de app: lo comparten la agenda
// (que sigue en app) y los módulos de horarios y ausencias.
type FakeWeeklyHours struct {
	SalonDays map[string][]domain.WeekdayHours
	MemberSeg map[string][]domain.HoursSegment // "salonID|userID"
	// ReadErr simula la DB caída al leer: la disponibilidad no puede inventar
	// horarios ni ofrecer turnos a ciegas.
	ReadErr error
}

func (f *FakeWeeklyHours) SalonHours(salonID string) ([]domain.WeekdayHours, error) {
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	return append([]domain.WeekdayHours{}, f.SalonDays[salonID]...), nil
}

func (f *FakeWeeklyHours) SaveSalonHours(salonID string, days []domain.WeekdayHours) error {
	if f.SalonDays == nil {
		f.SalonDays = map[string][]domain.WeekdayHours{}
	}
	f.SalonDays[salonID] = append([]domain.WeekdayHours{}, days...)
	return nil
}

func (f *FakeWeeklyHours) MemberHours(salonID, userID string) ([]domain.HoursSegment, error) {
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	return append([]domain.HoursSegment{}, f.MemberSeg[salonID+"|"+userID]...), nil
}

func (f *FakeWeeklyHours) SaveMemberHours(salonID, userID string, segments []domain.HoursSegment) error {
	if f.MemberSeg == nil {
		f.MemberSeg = map[string][]domain.HoursSegment{}
	}
	f.MemberSeg[salonID+"|"+userID] = append([]domain.HoursSegment{}, segments...)
	return nil
}

func (f *FakeWeeklyHours) AllMemberHours(salonID string) (map[string][]domain.HoursSegment, error) {
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	prefix := salonID + "|"
	out := map[string][]domain.HoursSegment{}
	for key, segs := range f.MemberSeg {
		if strings.HasPrefix(key, prefix) {
			out[strings.TrimPrefix(key, prefix)] = append([]domain.HoursSegment{}, segs...)
		}
	}
	return out, nil
}

var _ ports.WeeklyHoursRepository = (*FakeWeeklyHours)(nil)

// FakeTimeOff: ausencias en memoria con la misma semántica que pgstore (scope
// por salón; el rango se pregunta en SQL contra el período: [from, to) pisado).
type FakeTimeOff struct {
	All []domain.TimeOff
	// SaveErr simula que la DB rechaza el alta (EXCLUDE ante dos altas a la vez).
	SaveErr error
	// ReadErr simula la DB caída al leer: la disponibilidad no puede ofrecer
	// turnos a ciegas si no sabe quién está ausente.
	ReadErr error
}

func (f *FakeTimeOff) Save(t domain.TimeOff) error {
	if f.SaveErr != nil {
		return f.SaveErr
	}
	f.All = append(f.All, t)
	return nil
}

func (f *FakeTimeOff) ListRange(salonID string, from, to time.Time) ([]domain.TimeOff, error) {
	if f.ReadErr != nil {
		return nil, f.ReadErr
	}
	out := make([]domain.TimeOff, 0, len(f.All))
	for _, t := range f.All {
		if t.SalonID == salonID && t.Overlaps(from, to) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *FakeTimeOff) FindByID(salonID, id string) (domain.TimeOff, error) {
	for _, t := range f.All {
		if t.SalonID == salonID && t.ID == id {
			return t, nil
		}
	}
	return domain.TimeOff{}, domain.ErrNotFound
}

func (f *FakeTimeOff) Delete(salonID, id string) error {
	for i, t := range f.All {
		if t.SalonID == salonID && t.ID == id {
			f.All = append(f.All[:i], f.All[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

var _ ports.TimeOffRepository = (*FakeTimeOff)(nil)
