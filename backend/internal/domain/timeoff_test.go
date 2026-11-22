package domain

// F2d2: la ausencia de una profesional es un período [starts_at, ends_at) de UNA
// persona. Estas son las reglas del dominio (S-F2d2-2), sin DB ni HTTP: la
// disponibilidad, la reserva pública y la pantalla de equipo las consultan acá.
import (
	"errors"
	"strings"
	"testing"
	"time"
)

// nowF2d2 es un "ahora" fijo para que el chequeo de pasado sea determinista.
var nowF2d2 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func timeOffInput(start, end time.Time) TimeOffInput {
	return TimeOffInput{
		SalonID: "s1", UserID: "u-pro", StartsAt: start, EndsAt: end,
		Reason: "  Vacaciones  ", CreatedBy: "u-own",
	}
}

// S-F2d2-2: una ausencia válida nace con id, motivo recortado y el período tal
// cual (en UTC, como los turnos).
func TestNewTimeOff_valid(t *testing.T) {
	start := nowF2d2.Add(24 * time.Hour)
	end := start.Add(48 * time.Hour)

	got, err := NewTimeOff(timeOffInput(start, end), nowF2d2)
	if err != nil {
		t.Fatalf("ausencia válida: %v", err)
	}
	if got.ID == "" {
		t.Fatal("la ausencia tiene que nacer con id")
	}
	if got.Reason != "Vacaciones" {
		t.Fatalf("motivo: %q", got.Reason)
	}
	if !got.StartsAt.Equal(start.UTC()) || !got.EndsAt.Equal(end.UTC()) {
		t.Fatalf("período: %v - %v", got.StartsAt, got.EndsAt)
	}
	if got.SalonID != "s1" || got.UserID != "u-pro" || got.CreatedBy != "u-own" {
		t.Fatalf("scope: %+v", got)
	}
	if !got.CreatedAt.Equal(nowF2d2) {
		t.Fatalf("created_at: %v", got.CreatedAt)
	}
}

// S-F2d2-5: «El fin debe ser posterior al inicio».
func TestNewTimeOff_order(t *testing.T) {
	start := nowF2d2.Add(24 * time.Hour)
	for _, end := range []time.Time{start, start.Add(-time.Minute)} {
		_, err := NewTimeOff(timeOffInput(start, end), nowF2d2)
		if got, want := messageOf(err), MsgTimeOffOrder; got != want {
			t.Fatalf("fin %v: %q (esperaba %q)", end, got, want)
		}
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("fin %v: tiene que ser validación, fue %v", end, err)
		}
	}
}

// S-F2d2-2/5: el tope de 90 días, con su borde exacto.
func TestNewTimeOff_too_long(t *testing.T) {
	start := nowF2d2.Add(24 * time.Hour)
	if _, err := NewTimeOff(timeOffInput(start, start.Add(90*24*time.Hour)), nowF2d2); err != nil {
		t.Fatalf("90 días exactos es válido: %v", err)
	}
	_, err := NewTimeOff(timeOffInput(start, start.Add(90*24*time.Hour+time.Minute)), nowF2d2)
	if got, want := messageOf(err), MsgTimeOffTooLong; got != want {
		t.Fatalf("91 días: %q (esperaba %q)", got, want)
	}
}

// S-F2d2-2/5: «No se puede cargar una ausencia que ya terminó». Una ausencia en
// curso (empezó antes de ahora, termina después) SÍ se puede cargar: es el caso
// de cargar hoy el día libre de hoy.
func TestNewTimeOff_past(t *testing.T) {
	for _, end := range []time.Time{nowF2d2, nowF2d2.Add(-time.Hour)} {
		_, err := NewTimeOff(timeOffInput(nowF2d2.Add(-8*time.Hour), end), nowF2d2)
		if got, want := messageOf(err), MsgTimeOffPast; got != want {
			t.Fatalf("fin %v: %q (esperaba %q)", end, got, want)
		}
	}
	got, err := NewTimeOff(timeOffInput(nowF2d2.Add(-8*time.Hour), nowF2d2.Add(time.Minute)), nowF2d2)
	if err != nil {
		t.Fatalf("ausencia en curso: %v", err)
	}
	if got.Reason != "Vacaciones" {
		t.Fatalf("motivo: %q", got.Reason)
	}
}

// S-F2d2-2: motivo trim, máx 120 caracteres y sin saltos de línea.
func TestNewTimeOff_reason(t *testing.T) {
	start := nowF2d2.Add(24 * time.Hour)
	end := start.Add(2 * time.Hour)

	long := strings.Repeat("a", MaxTextTimeOffReason+1)
	in := timeOffInput(start, end)
	in.Reason = long
	if _, err := NewTimeOff(in, nowF2d2); !errors.Is(err, ErrValidation) || messageOf(err) == "" {
		t.Fatalf("motivo largo: tiene que ser validación, fue %v", err)
	}

	for _, bad := range []string{"me voy\nal médico", "me voy\r\n"} {
		in = timeOffInput(start, end)
		in.Reason = bad
		if _, err := NewTimeOff(in, nowF2d2); !errors.Is(err, ErrValidation) || messageOf(err) == "" {
			t.Fatalf("motivo %q: tiene que ser validación, fue %v", bad, err)
		}
	}

	// El borde exacto del tope y el motivo vacío son válidos.
	in = timeOffInput(start, end)
	in.Reason = strings.Repeat("a", MaxTextTimeOffReason)
	if _, err := NewTimeOff(in, nowF2d2); err != nil {
		t.Fatalf("motivo de 120: %v", err)
	}
	in.Reason = ""
	got, err := NewTimeOff(in, nowF2d2)
	if err != nil || got.Reason != "" {
		t.Fatalf("motivo vacío: %v %q", err, got.Reason)
	}
}

// S-F2d2-2: dos ausencias de la misma persona no pueden pisarse. El período es
// [starts_at, ends_at): una que empieza EXACTAMENTE cuando termina la otra no se
// pisa (el turno de las 12:00 puede seguir existiendo si la ausencia termina a
// las 12:00).
func TestTimeOff_Overlaps(t *testing.T) {
	start := nowF2d2.Add(24 * time.Hour)
	tOff, err := NewTimeOff(timeOffInput(start, start.Add(2*time.Hour)), nowF2d2)
	if err != nil {
		t.Fatalf("ausencia: %v", err)
	}
	cases := []struct {
		name       string
		from, to   time.Time
		wantOverla bool
	}{
		{"arranca justo cuando termina", start.Add(2 * time.Hour), start.Add(3 * time.Hour), false},
		{"termina justo cuando arranca", start.Add(-time.Hour), start, false},
		{"pisado por dentro", start.Add(time.Hour), start.Add(3 * time.Hour), true},
		{"arranca dentro", start.Add(-time.Hour), start.Add(time.Hour), true},
		{"idéntica", start, start.Add(2 * time.Hour), true},
		{"la cubre entera", start.Add(-time.Hour), start.Add(3 * time.Hour), true},
	}
	for _, c := range cases {
		if got := tOff.Overlaps(c.from, c.to); got != c.wantOverla {
			t.Fatalf("%s: overlaps=%v (esperaba %v)", c.name, got, c.wantOverla)
		}
	}
}

// S-F2d2-2: el rol decide si puede tener ausencias. Recepción y cliente nunca;
// dueño/administración solo si atienden (la marca attends, F2a).
func TestTimeOffAllowed(t *testing.T) {
	cases := []struct {
		name string
		m    Membership
		want bool
	}{
		{"profesional", Membership{Role: RoleProfessional, Attends: true}, true},
		{"dueño que atiende", Membership{Role: RoleOwner, Attends: true}, true},
		{"administración que atiende", Membership{Role: RoleAdmin, Attends: true}, true},
		{"dueño que no atiende", Membership{Role: RoleOwner, Attends: false}, false},
		{"recepción", Membership{Role: RoleReception}, false},
		{"cliente", Membership{Role: RoleClient}, false},
	}
	for _, c := range cases {
		if got := c.m.HasTimeOff(); got != c.want {
			t.Fatalf("%s: %v", c.name, got)
		}
	}
}

// messageOf devuelve el texto visible del error (sin el prefijo interno) o "".
func messageOf(err error) string {
	if err == nil {
		return ""
	}
	var m Message
	if errors.As(err, &m) {
		return m.Text
	}
	return err.Error()
}

// S-F2d2-5: el rango de la LISTA (GET ?from&to) tiene tope de 92 días y las
// fechas tienen que venir y estar ordenadas. Son errores de la consulta, no de
// una ausencia.
func TestValidateTimeOffRange(t *testing.T) {
	from := nowF2d2
	if err := ValidateTimeOffRange(from, from.Add(92*24*time.Hour)); err != nil {
		t.Fatalf("92 días exactos es válido: %v", err)
	}
	err := ValidateTimeOffRange(from, from.Add(92*24*time.Hour+time.Minute))
	if got, want := messageOf(err), MsgTimeOffRangeTooLong; got != want {
		t.Fatalf("93 días: %q (esperaba %q)", got, want)
	}
	for _, c := range []struct{ from, to time.Time }{
		{from, from},
		{from, from.Add(-time.Hour)},
	} {
		if err := ValidateTimeOffRange(c.from, c.to); !errors.Is(err, ErrValidation) || messageOf(err) != MsgTimeOffRangeOrder {
			t.Fatalf("rango %v-%v: %v", c.from, c.to, err)
		}
	}
	if err := ValidateTimeOffRange(time.Time{}, from); !errors.Is(err, ErrValidation) || messageOf(err) != MsgTimeOffRangeMissing {
		t.Fatalf("sin from: %v", err)
	}
	if err := ValidateTimeOffRange(from, time.Time{}); !errors.Is(err, ErrValidation) || messageOf(err) != MsgTimeOffRangeMissing {
		t.Fatalf("sin to: %v", err)
	}
}
