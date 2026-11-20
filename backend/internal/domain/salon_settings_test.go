package domain

import (
	"errors"
	"testing"
	"time"
)

// T6: la configuración del salón (zona horaria IANA, horario de apertura y
// paso de turnos) es una regla de dominio: cualquier camino de escritura la
// valida igual.
func TestSalonSettings_defaults(t *testing.T) {
	s := DefaultSalonSettings("s1")
	if s.SalonID != "s1" || s.Timezone != "America/Mexico_City" ||
		s.OpenTime != "09:00" || s.CloseTime != "19:00" || s.SlotStepMin != 30 {
		t.Fatalf("defaults: %+v", s)
	}
	loc, err := s.Location()
	if err != nil {
		t.Fatalf("la zona por defecto debe resolverse: %v", err)
	}
	if loc.String() != "America/Mexico_City" {
		t.Fatalf("zona: %q", loc.String())
	}
	if open, err := s.OpenMinute(); err != nil || open != 9*60 {
		t.Fatalf("apertura en minutos: %d %v", open, err)
	}
	if close, err := s.CloseMinute(); err != nil || close != 19*60 {
		t.Fatalf("cierre en minutos: %d %v", close, err)
	}
	// Una configuración válida se acepta tal cual.
	if err := s.Validate(); err != nil {
		t.Fatalf("la default debe ser válida: %v", err)
	}
}

func TestSalonSettings_validacion(t *testing.T) {
	base := func() SalonSettings { return DefaultSalonSettings("s1") }

	tz := base()
	tz.Timezone = "Marte/Olympus"
	if err := tz.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("zona inexistente debe ser ErrValidation, dio %v", err)
	}
	tz = base()
	tz.Timezone = ""
	if err := tz.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("zona vacía debe ser ErrValidation, dio %v", err)
	}
	for _, hora := range []string{"", "9:00", "24:00", "19:60", "09:0", "nueve"} {
		s := base()
		s.OpenTime = hora
		if err := s.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("apertura %q debe ser ErrValidation, dio %v", hora, err)
		}
	}
	for _, hora := range []string{"", "25:00", "19:60"} {
		s := base()
		s.CloseTime = hora
		if err := s.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("cierre %q debe ser ErrValidation, dio %v", hora, err)
		}
	}
	// Apertura y cierre tienen que dejar una ventana real.
	for _, par := range [][2]string{{"19:00", "09:00"}, {"10:00", "10:00"}} {
		s := base()
		s.OpenTime, s.CloseTime = par[0], par[1]
		if err := s.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("ventana %v debe ser ErrValidation, dio %v", par, err)
		}
	}
	for _, step := range []int{0, -30, 4, 241} {
		s := base()
		s.SlotStepMin = step
		if err := s.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("paso %d debe ser ErrValidation, dio %v", step, err)
		}
	}
	s := base()
	s.SalonID = ""
	if err := s.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("salón vacío debe ser ErrValidation, dio %v", err)
	}
	// Aceitado: la ventana de un salón de otro país también vale.
	ok := SalonSettings{SalonID: "s1", Timezone: "Asia/Tokyo", OpenTime: "08:30", CloseTime: "20:15", SlotStepMin: 15}
	if err := ok.Validate(); err != nil {
		t.Fatalf("config válida rechazada: %v", err)
	}
	if open, _ := ok.OpenMinute(); open != 8*60+30 {
		t.Fatalf("apertura: %d", open)
	}
	if close, _ := ok.CloseMinute(); close != 20*60+15 {
		t.Fatalf("cierre: %d", close)
	}
}

func TestSalonSettings_Window(t *testing.T) {
	s := SalonSettings{SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30}
	// La fecha se interpreta en la zona del salón y se devuelve en el mismo
	// instante, así el 2026-12-01 09:00 local es 15:00Z.
	day := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	open, closeAt, err := s.Window(day)
	if err != nil {
		t.Fatalf("ventana: %v", err)
	}
	if got := open.UTC().Format(time.RFC3339); got != "2026-12-01T15:00:00Z" {
		t.Fatalf("apertura: %s", got)
	}
	if got := closeAt.UTC().Format(time.RFC3339); got != "2026-12-02T01:00:00Z" {
		t.Fatalf("cierre: %s", got)
	}
	if open.Location().String() != "America/Mexico_City" {
		t.Fatalf("la ventana debe conservar la zona del salón: %v", open.Location())
	}
	// Config inválida (cierre antes de apertura): no inventa una ventana.
	bad := s
	bad.CloseTime = "08:00"
	if _, _, err := bad.Window(day); !errors.Is(err, ErrValidation) {
		t.Fatalf("ventana inválida: %v", err)
	}
}

func TestNewSalonSettings_normaliza(t *testing.T) {
	s, err := NewSalonSettings("s1", " America/Bogota ", " 08:00 ", " 17:00 ", 20)
	if err != nil {
		t.Fatalf("inesperado: %v", err)
	}
	if s.Timezone != "America/Bogota" || s.OpenTime != "08:00" || s.CloseTime != "17:00" || s.SlotStepMin != 20 {
		t.Fatalf("normalización: %+v", s)
	}
	if _, err := NewSalonSettings("s1", "no/existe", "08:00", "17:00", 20); !errors.Is(err, ErrValidation) {
		t.Fatalf("zona inválida: %v", err)
	}
}
