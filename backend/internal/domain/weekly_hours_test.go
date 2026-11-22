package domain

import (
	"errors"
	"testing"
	"time"
)

// sevenDays arma un horario semanal completo (todos los días 09:00-19:00).
func sevenDays() []WeekdayHours {
	days := make([]WeekdayHours, 0, 7)
	for wd := FirstWeekday; wd <= LastWeekday; wd++ {
		days = append(days, WeekdayHours{Weekday: wd, Open: "09:00", Close: "19:00"})
	}
	return days
}

// S-F2d1-3: validación del horario semanal del salón. Los mensajes de error son
// contrato: los ve la persona en la pantalla de ajustes.
func TestNewWeeklyHours_validation(t *testing.T) {
	tests := []struct {
		name    string
		days    []WeekdayHours
		wantMsg string // "" = válido
	}{
		{"sin filas (horario único)", nil, ""},
		{"una fila", []WeekdayHours{{Weekday: 1, Open: "09:00", Close: "19:00"}}, ""},
		{"siete filas", sevenDays(), ""},
		{"día 0", []WeekdayHours{{Weekday: 0, Open: "09:00", Close: "19:00"}}, MsgWeekday},
		{"día 8", []WeekdayHours{{Weekday: 8, Open: "09:00", Close: "19:00"}}, MsgWeekday},
		{"apertura sin cero", []WeekdayHours{{Weekday: 1, Open: "9:00", Close: "19:00"}}, MsgHoursFormat},
		{"cierre imposible", []WeekdayHours{{Weekday: 1, Open: "09:00", Close: "25:00"}}, MsgHoursFormat},
		{"vacío", []WeekdayHours{{Weekday: 1, Open: "", Close: "19:00"}}, MsgHoursFormat},
		{"inicio igual al fin", []WeekdayHours{{Weekday: 1, Open: "09:00", Close: "09:00"}}, MsgHoursOrder},
		{"inicio después del fin", []WeekdayHours{{Weekday: 1, Open: "19:00", Close: "09:00"}}, MsgHoursOrder},
		{"día repetido", []WeekdayHours{
			{Weekday: 1, Open: "09:00", Close: "13:00"},
			{Weekday: 1, Open: "14:00", Close: "19:00"},
		}, MsgWeekdayRepeat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewWeeklyHours(tt.days)
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("esperaba válido: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("esperaba error %q", tt.wantMsg)
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("tiene que ser un error de validación: %v", err)
			}
			if err.Error() != tt.wantMsg {
				t.Fatalf("mensaje %q (esperaba %q)", err.Error(), tt.wantMsg)
			}
		})
	}

	// El orden de la respuesta no depende del orden del body: por día ISO.
	got, err := NewWeeklyHours([]WeekdayHours{
		{Weekday: 5, Open: "10:00", Close: "18:00"},
		{Weekday: 1, Open: "09:00", Close: "13:00"},
	})
	if err != nil {
		t.Fatalf("horario: %v", err)
	}
	days := got.Days()
	if len(days) != 2 || days[0].Weekday != 1 || days[1].Weekday != 5 {
		t.Fatalf("orden por día: %+v", days)
	}
}

// S-F2d1-3: los tramos de una profesional. Hasta 4 por día, sin solaparse: los
// huecos entre tramos son los descansos.
func TestNewProfessionalHours_validation(t *testing.T) {
	tests := []struct {
		name     string
		segments []HoursSegment
		wantMsg  string
	}{
		{"sin filas (sigue al salón)", nil, ""},
		{"un tramo", []HoursSegment{{Weekday: 2, Start: "09:00", End: "13:00"}}, ""},
		{"cuatro tramos", []HoursSegment{
			{Weekday: 2, Start: "09:00", End: "10:00"},
			{Weekday: 2, Start: "10:30", End: "12:00"},
			{Weekday: 2, Start: "14:00", End: "16:00"},
			{Weekday: 2, Start: "17:00", End: "18:00"},
		}, ""},
		{"tramos pegados", []HoursSegment{
			{Weekday: 2, Start: "09:00", End: "13:00"},
			{Weekday: 2, Start: "13:00", End: "18:00"},
		}, ""},
		{"día 9", []HoursSegment{{Weekday: 9, Start: "09:00", End: "13:00"}}, MsgWeekday},
		{"formato", []HoursSegment{{Weekday: 2, Start: "9:00", End: "13:00"}}, MsgHoursFormat},
		{"inicio igual al fin", []HoursSegment{{Weekday: 2, Start: "13:00", End: "13:00"}}, MsgHoursOrder},
		{"inicio después del fin", []HoursSegment{{Weekday: 2, Start: "14:00", End: "13:00"}}, MsgHoursOrder},
		{"cinco tramos", []HoursSegment{
			{Weekday: 2, Start: "08:00", End: "09:00"},
			{Weekday: 2, Start: "09:00", End: "10:00"},
			{Weekday: 2, Start: "10:00", End: "11:00"},
			{Weekday: 2, Start: "11:00", End: "12:00"},
			{Weekday: 2, Start: "12:00", End: "13:00"},
		}, MsgHoursTooMany},
		{"solapados", []HoursSegment{
			{Weekday: 2, Start: "09:00", End: "13:00"},
			{Weekday: 2, Start: "12:00", End: "15:00"},
		}, MsgHoursOverlap},
		{"duplicado exacto", []HoursSegment{
			{Weekday: 2, Start: "09:00", End: "13:00"},
			{Weekday: 2, Start: "09:00", End: "13:00"},
		}, MsgHoursOverlap},
		{"el tope es por día", []HoursSegment{
			{Weekday: 1, Start: "09:00", End: "13:00"},
			{Weekday: 1, Start: "14:00", End: "18:00"},
			{Weekday: 2, Start: "09:00", End: "13:00"},
			{Weekday: 2, Start: "14:00", End: "18:00"},
			{Weekday: 2, Start: "18:00", End: "19:00"},
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProfessionalHours(tt.segments)
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("esperaba válido: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("esperaba error %q", tt.wantMsg)
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("tiene que ser un error de validación: %v", err)
			}
			if err.Error() != tt.wantMsg {
				t.Fatalf("mensaje %q (esperaba %q)", err.Error(), tt.wantMsg)
			}
		})
	}

	// Orden: día y después hora de inicio.
	got, err := NewProfessionalHours([]HoursSegment{
		{Weekday: 3, Start: "14:00", End: "18:00"},
		{Weekday: 3, Start: "09:00", End: "13:00"},
	})
	if err != nil {
		t.Fatalf("tramos: %v", err)
	}
	segs := got.Segments()
	if len(segs) != 2 || segs[0].Start != "09:00" || segs[1].Start != "14:00" {
		t.Fatalf("orden por hora: %+v", segs)
	}
}

// S-F2d1-2: la ventana efectiva de un día. Salón sin filas = la ventana única de
// /settings todos los días; con filas, el día sin fila está cerrado. La
// profesional sin filas sigue al salón; con filas, un día sin tramo es libre.
func TestDayPlan_windows(t *testing.T) {
	single := Interval{Start: 9 * 60, End: 19 * 60}

	t.Run("salón sin filas y profesional sin filas", func(t *testing.T) {
		plan := DayPlan{Single: single}
		for wd := FirstWeekday; wd <= LastWeekday; wd++ {
			got, err := plan.Windows(wd)
			if err != nil {
				t.Fatalf("día %d: %v", wd, err)
			}
			if len(got) != 1 || got[0] != single {
				t.Fatalf("día %d: %+v (esperaba la ventana única)", wd, got)
			}
		}
	})

	t.Run("salón con filas: el día sin fila está cerrado", func(t *testing.T) {
		weekly, err := NewWeeklyHours([]WeekdayHours{
			{Weekday: 1, Open: "10:00", Close: "14:00"},
			{Weekday: 3, Open: "09:00", Close: "12:00"},
		})
		if err != nil {
			t.Fatalf("horario: %v", err)
		}
		plan := DayPlan{Single: single, Weekly: weekly}
		got, err := plan.Windows(1)
		if err != nil {
			t.Fatalf("día 1: %v", err)
		}
		if len(got) != 1 || got[0] != (Interval{Start: 10 * 60, End: 14 * 60}) {
			t.Fatalf("día 1: %+v", got)
		}
		if got, err := plan.Windows(2); err != nil || len(got) != 0 {
			t.Fatalf("día 2 tiene que estar cerrado: %+v %v", got, err)
		}
	})

	t.Run("profesional con filas: día sin tramo es libre", func(t *testing.T) {
		plan := DayPlan{Single: single, Segments: []HoursSegment{
			{Weekday: 2, Start: "09:00", End: "13:00"},
			{Weekday: 2, Start: "14:00", End: "18:00"},
		}}
		got, err := plan.Windows(2)
		if err != nil {
			t.Fatalf("día 2: %v", err)
		}
		want := []Interval{{Start: 9 * 60, End: 13 * 60}, {Start: 14 * 60, End: 18 * 60}}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("tramos del martes: %+v", got)
		}
		if got, err := plan.Windows(1); err != nil || len(got) != 0 {
			t.Fatalf("el lunes es libre: %+v %v", got, err)
		}
	})

	t.Run("los tramos se recortan a la ventana del salón", func(t *testing.T) {
		weekly, err := NewWeeklyHours([]WeekdayHours{{Weekday: 1, Open: "10:00", Close: "16:00"}})
		if err != nil {
			t.Fatalf("horario: %v", err)
		}
		plan := DayPlan{Single: single, Weekly: weekly, Segments: []HoursSegment{
			{Weekday: 1, Start: "08:00", End: "11:00"},
			{Weekday: 1, Start: "15:00", End: "20:00"},
			{Weekday: 3, Start: "09:00", End: "12:00"}, // otro día: no aplica
		}}
		got, err := plan.Windows(1)
		if err != nil {
			t.Fatalf("día 1: %v", err)
		}
		want := []Interval{{Start: 10 * 60, End: 11 * 60}, {Start: 15 * 60, End: 16 * 60}}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("tramos recortados: %+v", got)
		}
		// Un tramo totalmente fuera de la ventana no deja nada.
		outside := DayPlan{Single: single, Weekly: weekly, Segments: []HoursSegment{
			{Weekday: 1, Start: "07:00", End: "08:00"},
		}}
		if got, err := outside.Windows(1); err != nil || len(got) != 0 {
			t.Fatalf("tramo fuera de la ventana: %+v %v", got, err)
		}
	})

	t.Run("un tramo inválido no se acepta al calcular", func(t *testing.T) {
		plan := DayPlan{Single: single, Segments: []HoursSegment{{Weekday: 1, Start: "11:00", End: "10:00"}}}
		if _, err := plan.Windows(1); err == nil {
			t.Fatal("un tramo con inicio después del fin tiene que fallar")
		}
	})
}

// S-F2d1-3: un turno NO puede cruzar un descanso ni el cierre: se ofrece solo si
// entra entero en UN tramo.
func TestDayPlan_fits(t *testing.T) {
	plan := DayPlan{Single: Interval{Start: 9 * 60, End: 19 * 60}, Segments: []HoursSegment{
		{Weekday: 2, Start: "09:00", End: "13:00"},
		{Weekday: 2, Start: "14:00", End: "18:00"},
	}}
	cases := []struct {
		name       string
		weekday    int
		start, end int
		want       bool
	}{
		{"entra entero en el primer tramo", 2, 9 * 60, 13 * 60, true},
		{"entra entero en el segundo", 2, 14 * 60, 18 * 60, true},
		{"cae en el descanso", 2, 13 * 60, 14 * 60, false},
		{"cruza el descanso", 2, 12*60 + 30, 13*60 + 30, false},
		{"cruza el cierre", 2, 17*60 + 30, 18*60 + 30, false},
		{"empieza antes del salón", 2, 8*60 + 30, 9*60 + 30, false},
		{"otro día es libre", 1, 9 * 60, 13 * 60, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan.Fits(tt.weekday, tt.start, tt.end)
			if err != nil {
				t.Fatalf("fits: %v", err)
			}
			if got != tt.want {
				t.Fatalf("fits(%d-%d) = %v (esperaba %v)", tt.start, tt.end, got, tt.want)
			}
		})
	}

	// Sin tramos propios sigue al salón: tampoco cruza el cierre del salón.
	solo := DayPlan{Single: Interval{Start: 9 * 60, End: 19 * 60}}
	if fits, _ := solo.Fits(3, 18*60+30, 19*60+30); fits {
		t.Fatal("un turno que pasa el cierre del salón no entra")
	}
	if fits, _ := solo.Fits(3, 9*60, 10*60); !fits {
		t.Fatal("un turno dentro de la ventana única entra")
	}
}

// S-F2d1-3 (zona horaria): el día de la semana es el del SALÓN, no el de UTC. Un
// domingo 23:30 en CDMX ya es lunes en UTC: sigue siendo domingo para la agenda.
func TestIsoWeekday_zonaDelSalon(t *testing.T) {
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	// Domingo 6/12/2026 23:30 local = lunes 7/12/2026 05:30 UTC.
	instant := time.Date(2026, 12, 7, 5, 30, 0, 0, time.UTC)
	if got := IsoWeekday(instant, loc); got != 7 {
		t.Fatalf("domingo local: %d (esperaba 7)", got)
	}
	if instant.Weekday() != time.Monday {
		t.Fatalf("el instante tiene que ser lunes en UTC: %s", instant.Weekday())
	}
	// Lunes 00:30 local = lunes 06:30 UTC.
	monday := time.Date(2026, 12, 7, 6, 30, 0, 0, time.UTC)
	if got := IsoWeekday(monday, loc); got != 1 {
		t.Fatalf("lunes local: %d (esperaba 1)", got)
	}
	// Sin zona (nil) cae al día UTC: no se inventa una zona.
	if got := IsoWeekday(instant, nil); got != 1 {
		t.Fatalf("sin zona: %d (esperaba 1)", got)
	}
}
