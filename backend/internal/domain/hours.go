package domain

import "time"

// Interval es un tramo de un día LOCAL del salón en minutos desde la medianoche
// (09:00 = 540). Es la unidad con la que se comparan horarios, tramos y turnos:
// el día del negocio es el día del salón, no el de UTC (F2d1).
type Interval struct {
	Start int
	End   int
}

// Contains dice si [start, end) entra ENTERO en el tramo. Un turno no puede
// cruzar un descanso ni el cierre: si le falta un minuto, no entra (S-F2d1-3).
func (i Interval) Contains(start, end int) bool {
	return start < end && start >= i.Start && end <= i.End
}

// FitsOneOf dice si [start, end) entra entero en ALGUNO de los tramos. Nunca se
// arma un turno con la unión de dos tramos: el hueco del medio es el descanso.
func FitsOneOf(intervals []Interval, start, end int) bool {
	for _, i := range intervals {
		if i.Contains(start, end) {
			return true
		}
	}
	return false
}

// Clip devuelve la intersección de a y b, y si quedó algo.
func Clip(a, b Interval) (Interval, bool) {
	start := a.Start
	if b.Start > start {
		start = b.Start
	}
	end := a.End
	if b.End < end {
		end = b.End
	}
	if start >= end {
		return Interval{}, false
	}
	return Interval{Start: start, End: end}, true
}

// IsoWeekday devuelve el día ISO de t (1 = lunes ... 7 = domingo) en la zona
// del salón. Sin zona cae al día UTC: no se inventa una zona horaria.
//
// Esto no es un detalle: un domingo 23:30 en CDMX ya es lunes en UTC, y la
// agenda tiene que ofrecer el turno del domingo (el día del salón), no el del
// servidor.
func IsoWeekday(t time.Time, loc *time.Location) int {
	if loc != nil {
		t = t.In(loc)
	} else {
		t = t.UTC()
	}
	wd := int(t.Weekday()) // 0 = domingo
	if wd == 0 {
		return 7
	}
	return wd
}

// EveryDayWindow devuelve la ventana única de /settings (apertura y cierre)
// como tramo en minutos. Es la que vale para los 7 días mientras el salón no
// tenga horario semanal propio (S-F2d1-2).
func (s SalonSettings) EveryDayWindow() (Interval, error) {
	open, err := s.OpenMinute()
	if err != nil {
		return Interval{}, err
	}
	closeMin, err := s.CloseMinute()
	if err != nil {
		return Interval{}, err
	}
	if open >= closeMin {
		return Interval{}, NewMessage(ErrValidation, MsgHoursOrder)
	}
	return Interval{Start: open, End: closeMin}, nil
}
