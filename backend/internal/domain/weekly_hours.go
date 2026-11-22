package domain

import "sort"

// Horario semanal del salón y de cada profesional (F2d1).
//
// Semántica (S-F2d1-2):
//   - Salón sin NINGUNA fila = la ventana única open_time/close_time de
//     /settings vale todos los días (es lo que hay hoy). Con una o más filas, un
//     día sin fila está CERRADO.
//   - Profesional sin NINGUNA fila = sigue el horario del salón. Con filas, solo
//     trabaja en esos tramos: un día sin tramo es día libre, hasta 4 tramos por
//     día y sin solaparse. Los descansos son los huecos entre tramos.
//   - La ventana efectiva de una profesional un día = ventana del salón ∩ sus
//     tramos, recortados.
//
// Los días son ISO (1 = lunes ... 7 = domingo) en la zona horaria del salón, y
// las horas son "HH:MM" de 24 h.

const (
	FirstWeekday = 1
	LastWeekday  = 7
	// MaxSegmentsPerDay es el tope de tramos de una profesional en un día: más
	// de cuatro no es una jornada con descansos, es una agenda rota.
	MaxSegmentsPerDay = 4
)

// Mensajes exactos del contrato (S-F2d1-4): los ve la persona en Ajustes y en
// Equipo, así que son parte de la API.
const (
	MsgHoursOrder    = "Horario inválido: el inicio debe ser anterior al fin"
	MsgHoursOverlap  = "Los tramos de un mismo día no pueden solaparse"
	MsgHoursTooMany  = "Máximo 4 tramos por día"
	MsgWeekday       = "Día inválido"
	MsgHoursFormat   = "Horario inválido: usá horas HH:MM"
	MsgWeekdayRepeat = "El día está repetido"
)

// WeekdayHours es la ventana de UN día del salón. Open y Close son "HH:MM"
// locales (la forma en que la guarda la tabla y la devuelve la API).
type WeekdayHours struct {
	Weekday int
	Open    string
	Close   string
}

// WeeklyHours es el horario semanal del salón. Sin días = no hay horario propio
// y vale la ventana única de /settings; con días, el que falta está cerrado.
type WeeklyHours struct {
	days []WeekdayHours
}

// NewWeeklyHours valida y normaliza el horario del salón (ordenado por día, sin
// repetidos). Un horario inválido no se guarda: el llamador valida ANTES de
// escribir, así el reemplazo atómico nunca deja media semana.
func NewWeeklyHours(days []WeekdayHours) (WeeklyHours, error) {
	out := make([]WeekdayHours, 0, len(days))
	seen := make(map[int]bool, len(days))
	for _, d := range days {
		if d.Weekday < FirstWeekday || d.Weekday > LastWeekday {
			return WeeklyHours{}, NewMessage(ErrValidation, MsgWeekday)
		}
		if seen[d.Weekday] {
			return WeeklyHours{}, NewMessage(ErrValidation, MsgWeekdayRepeat)
		}
		open, err := hoursClock(d.Open)
		if err != nil {
			return WeeklyHours{}, err
		}
		closeAt, err := hoursClock(d.Close)
		if err != nil {
			return WeeklyHours{}, err
		}
		if open >= closeAt {
			return WeeklyHours{}, NewMessage(ErrValidation, MsgHoursOrder)
		}
		seen[d.Weekday] = true
		out = append(out, WeekdayHours{Weekday: d.Weekday, Open: d.Open, Close: d.Close})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Weekday < out[j].Weekday })
	return WeeklyHours{days: out}, nil
}

// Custom dice si el salón tiene horario propio. Falso = ventana única todos los
// días (comportamiento de siempre).
func (w WeeklyHours) Custom() bool { return len(w.days) > 0 }

// Days devuelve una copia ordenada por día de las filas del horario.
func (w WeeklyHours) Days() []WeekdayHours {
	out := make([]WeekdayHours, len(w.days))
	copy(out, w.days)
	return out
}

// Interval devuelve la ventana del día como tramo en minutos. El segundo valor
// es falso cuando ese día no está abierto (sin fila en un horario propio).
func (w WeeklyHours) Interval(weekday int) (Interval, bool, error) {
	for _, d := range w.days {
		if d.Weekday != weekday {
			continue
		}
		open, err := hoursClock(d.Open)
		if err != nil {
			return Interval{}, false, err
		}
		closeAt, err := hoursClock(d.Close)
		if err != nil {
			return Interval{}, false, err
		}
		return Interval{Start: open, End: closeAt}, true, nil
	}
	return Interval{}, false, nil
}

// HoursSegment es un tramo de trabajo de una profesional: día ISO + "HH:MM".
type HoursSegment struct {
	Weekday int
	Start   string
	End     string
}

// ProfessionalHours son los tramos semanales de una profesional. Sin tramos =
// sigue el horario del salón; con tramos, un día sin tramo es día libre.
type ProfessionalHours struct {
	segments []HoursSegment
}

// NewProfessionalHours valida y normaliza los tramos: día 1..7, formato HH:MM,
// inicio < fin, hasta 4 por día y sin solaparse (un duplicado es un solape).
func NewProfessionalHours(segments []HoursSegment) (ProfessionalHours, error) {
	byDay := make(map[int][]Interval, LastWeekday)
	for _, s := range segments {
		if s.Weekday < FirstWeekday || s.Weekday > LastWeekday {
			return ProfessionalHours{}, NewMessage(ErrValidation, MsgWeekday)
		}
		start, err := hoursClock(s.Start)
		if err != nil {
			return ProfessionalHours{}, err
		}
		end, err := hoursClock(s.End)
		if err != nil {
			return ProfessionalHours{}, err
		}
		if start >= end {
			return ProfessionalHours{}, NewMessage(ErrValidation, MsgHoursOrder)
		}
		byDay[s.Weekday] = append(byDay[s.Weekday], Interval{Start: start, End: end})
		if len(byDay[s.Weekday]) > MaxSegmentsPerDay {
			return ProfessionalHours{}, NewMessage(ErrValidation, MsgHoursTooMany)
		}
	}
	for _, intervals := range byDay {
		sort.Slice(intervals, func(i, j int) bool { return intervals[i].Start < intervals[j].Start })
		for i := 1; i < len(intervals); i++ {
			if intervals[i].Start < intervals[i-1].End {
				return ProfessionalHours{}, NewMessage(ErrValidation, MsgHoursOverlap)
			}
		}
	}
	out := make([]HoursSegment, len(segments))
	copy(out, segments)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Weekday != out[j].Weekday {
			return out[i].Weekday < out[j].Weekday
		}
		return out[i].Start < out[j].Start
	})
	return ProfessionalHours{segments: out}, nil
}

// Custom dice si la profesional tiene horario propio. Falso = sigue al salón.
func (p ProfessionalHours) Custom() bool { return len(p.segments) > 0 }

// Segments devuelve una copia ordenada (por día y hora) de los tramos.
func (p ProfessionalHours) Segments() []HoursSegment {
	out := make([]HoursSegment, len(p.segments))
	copy(out, p.segments)
	return out
}

// DayPlan junta lo que hace falta para saber si un turno se puede ofrecer: la
// ventana única de /settings, el horario semanal del salón y los tramos de UNA
// profesional. Es el cálculo puro de F2d1: sin DB, sin reloj, sin HTTP.
type DayPlan struct {
	// Single es la ventana única de /settings (vale todos los días mientras el
	// salón no tenga horario propio).
	Single Interval
	// Weekly es el horario semanal del salón (vacío = ventana única).
	Weekly WeeklyHours
	// Segments son los tramos de la profesional (vacío = sigue al salón).
	Segments []HoursSegment
}

// Windows devuelve los tramos reservables de la profesional un día: ventana del
// salón ∩ sus tramos, ya recortados. Un día cerrado del salón o un día libre de
// la profesional devuelve una lista vacía (no es un error: no hay nada que
// ofrecer).
func (p DayPlan) Windows(weekday int) ([]Interval, error) {
	window := p.Single
	if p.Weekly.Custom() {
		day, open, err := p.Weekly.Interval(weekday)
		if err != nil {
			return nil, err
		}
		if !open {
			return nil, nil // el día está cerrado
		}
		window = day
	}
	if len(p.Segments) == 0 {
		return []Interval{window}, nil // sigue al salón
	}
	own, err := p.ownIntervals(weekday)
	if err != nil {
		return nil, err
	}
	if len(own) == 0 {
		return nil, nil // con horario propio, un día sin tramo es libre
	}
	out := make([]Interval, 0, len(own))
	for _, seg := range own {
		if clipped, ok := Clip(seg, window); ok {
			out = append(out, clipped)
		}
	}
	return out, nil
}

// Fits dice si el turno [start, end) cabe ENTERO en uno de los tramos del día:
// ni cruzando un descanso ni pasando el cierre.
func (p DayPlan) Fits(weekday, start, end int) (bool, error) {
	windows, err := p.Windows(weekday)
	if err != nil {
		return false, err
	}
	return FitsOneOf(windows, start, end), nil
}

// ownIntervals parsea los tramos de la profesional para ese día.
func (p DayPlan) ownIntervals(weekday int) ([]Interval, error) {
	out := make([]Interval, 0, MaxSegmentsPerDay)
	for _, s := range p.Segments {
		if s.Weekday != weekday {
			continue
		}
		start, err := hoursClock(s.Start)
		if err != nil {
			return nil, err
		}
		end, err := hoursClock(s.End)
		if err != nil {
			return nil, err
		}
		if start >= end {
			return nil, NewMessage(ErrValidation, MsgHoursOrder)
		}
		out = append(out, Interval{Start: start, End: end})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out, nil
}

// hoursClock parsea "HH:MM" a minutos. Mismo formato que la configuración del
// salón (clockMinutes), con el mensaje de esta feature.
func hoursClock(value string) (int, error) {
	if !clockRe.MatchString(value) {
		return 0, NewMessage(ErrValidation, MsgHoursFormat)
	}
	minutes, _ := clockMinutes(value, "")
	return minutes, nil
}
