package app

import (
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeWeeklyHours: horario semanal del salón y de cada persona en memoria, con
// la misma semántica que pgstore (reemplazo total, scope por salón, sin filas =
// comportamiento de siempre: ventana única y la profesional sigue al salón).
type fakeWeeklyHours struct {
	salonDays map[string][]domain.WeekdayHours
	memberSeg map[string][]domain.HoursSegment // "salonID|userID"
	// readErr simula la DB caída al leer: la disponibilidad no puede inventar
	// horarios ni ofrecer turnos a ciegas.
	readErr error
}

func (f *fakeWeeklyHours) SalonHours(salonID string) ([]domain.WeekdayHours, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return append([]domain.WeekdayHours{}, f.salonDays[salonID]...), nil
}

func (f *fakeWeeklyHours) SaveSalonHours(salonID string, days []domain.WeekdayHours) error {
	if f.salonDays == nil {
		f.salonDays = map[string][]domain.WeekdayHours{}
	}
	f.salonDays[salonID] = append([]domain.WeekdayHours{}, days...)
	return nil
}

func (f *fakeWeeklyHours) MemberHours(salonID, userID string) ([]domain.HoursSegment, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return append([]domain.HoursSegment{}, f.memberSeg[salonID+"|"+userID]...), nil
}

func (f *fakeWeeklyHours) SaveMemberHours(salonID, userID string, segments []domain.HoursSegment) error {
	if f.memberSeg == nil {
		f.memberSeg = map[string][]domain.HoursSegment{}
	}
	f.memberSeg[salonID+"|"+userID] = append([]domain.HoursSegment{}, segments...)
	return nil
}

func (f *fakeWeeklyHours) AllMemberHours(salonID string) (map[string][]domain.HoursSegment, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	prefix := salonID + "|"
	out := map[string][]domain.HoursSegment{}
	for key, segs := range f.memberSeg {
		if strings.HasPrefix(key, prefix) {
			out[strings.TrimPrefix(key, prefix)] = append([]domain.HoursSegment{}, segs...)
		}
	}
	return out, nil
}

var _ ports.WeeklyHoursRepository = (*fakeWeeklyHours)(nil)
