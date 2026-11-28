package main

import (
	"reflect"
	"testing"
	"time"
)

func TestRNGIsDeterministic(t *testing.T) {
	first := newRNG(42)
	second := newRNG(42)
	for i := 0; i < 50; i++ {
		a, b := first.Intn(1000), second.Intn(1000)
		if a != b {
			t.Fatalf("misma semilla, distinto valor en la tirada %d: %d != %d", i, a, b)
		}
	}
	other := newRNG(43)
	same := true
	for i := 0; i < 20; i++ {
		if first.Intn(1000) != other.Intn(1000) {
			same = false
			break
		}
	}
	if same {
		t.Fatal("dos semillas distintas no pueden dar la misma secuencia")
	}
}

func TestRNGHelpers(t *testing.T) {
	r := newRNG(7)
	if got := r.Intn(0); got != 0 {
		t.Fatalf("Intn(0) = %d, quería 0 (sin pánico)", got)
	}
	for i := 0; i < 200; i++ {
		if v := r.Between(3, 5); v < 3 || v > 5 {
			t.Fatalf("Between(3,5) = %d", v)
		}
	}
	if v := r.Between(4, 2); v != 4 {
		t.Fatalf("Between invertido = %d, quería el mínimo", v)
	}
	if v := r.WeightedIndex([]int{0, 0}); v != 0 {
		t.Fatalf("pesos en cero: %d", v)
	}
	// Con un solo peso positivo, siempre ese índice.
	for i := 0; i < 20; i++ {
		if v := r.WeightedIndex([]int{0, 5, 0}); v != 1 {
			t.Fatalf("el único peso positivo es 1 y salió %d", v)
		}
	}
	list := []string{"a", "b", "c"}
	if got := pick(r, []string(nil)); got != "" {
		t.Fatalf("Pick de lista vacía = %q", got)
	}
	for i := 0; i < 50; i++ {
		if got := pick(r, list); got != "a" && got != "b" && got != "c" {
			t.Fatalf("pick devolvió %q", got)
		}
	}
	original := []int{1, 2, 3, 4, 5}
	shuffled := shuffle(newRNG(3), original)
	if !reflect.DeepEqual(original, []int{1, 2, 3, 4, 5}) {
		t.Fatal("shuffle no puede tocar la lista original")
	}
	if len(shuffled) != len(original) {
		t.Fatal("shuffle cambió el largo")
	}
}

func TestClockRelativeDates(t *testing.T) {
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	// 20:30 UTC = 17:30 en Buenos Aires: sigue siendo el mismo día local.
	now := time.Date(2026, time.March, 10, 20, 30, 0, 0, time.UTC)
	c := newClock(now, loc)

	if got := c.today(); !got.Equal(time.Date(2026, 3, 10, 0, 0, 0, 0, loc)) {
		t.Fatalf("today = %v", got)
	}
	if got := c.daysAgo(10); !got.Equal(time.Date(2026, 2, 28, 0, 0, 0, 0, loc)) {
		t.Fatalf("daysAgo(10) = %v", got)
	}
	if got := c.daysAhead(1); !got.Equal(time.Date(2026, 3, 11, 0, 0, 0, 0, loc)) {
		t.Fatalf("daysAhead(1) = %v", got)
	}
	at := c.at(c.today(), 9, 30)
	if at.Hour() != 9 || at.Minute() != 30 || at.Location() != loc {
		t.Fatalf("at = %v", at)
	}
	// El cambio de horario de verano no corre en Argentina, pero el cálculo tiene
	// que seguir dando medianoche local en cualquier fecha.
	if got := c.daysAgo(200); got.Hour() != 0 || got.Minute() != 0 {
		t.Fatalf("daysAgo(200) no es medianoche: %v", got)
	}
	if addMinutes(at, 45).Sub(at) != 45*time.Minute {
		t.Fatal("addMinutes")
	}
}

func TestWeekday(t *testing.T) {
	// 2026-03-09 es lunes.
	monday := time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC)
	for offset, want := range map[int]int{0: 1, 1: 2, 5: 6, 6: 7} {
		if got := weekday(monday.AddDate(0, 0, offset)); got != want {
			t.Fatalf("offset %d: weekday = %d, quería %d", offset, got, want)
		}
	}
}

func TestPickDayStaysInWindow(t *testing.T) {
	loc := time.UTC
	c := newClock(time.Date(2026, time.May, 20, 12, 0, 0, 0, loc), loc)
	r := newRNG(11)
	fridays := 0
	for i := 0; i < 200; i++ {
		day := r.pickDay(c, 10)
		diff := int(c.today().Sub(day).Hours() / 24)
		if diff < 0 || diff > 10 {
			t.Fatalf("pickDay devolvió un día fuera de la ventana: %v", day)
		}
		if weekday(day) == 5 {
			fridays++
		}
	}
	if fridays == 0 {
		t.Fatal("los viernes pesan más que el resto: deberían aparecer")
	}
}

func TestRoundSlotFitsInsideSegment(t *testing.T) {
	loc := time.UTC
	c := newClock(time.Date(2026, time.May, 20, 12, 0, 0, 0, loc), loc)
	r := newRNG(5)
	day := c.daysAhead(1)
	for i := 0; i < 100; i++ {
		start := roundSlotForTest(r, c, day, "09:00", "13:00", 120)
		if start.IsZero() {
			continue
		}
		if start.Minute()%15 != 0 {
			t.Fatalf("el comienzo no está alineado a 15 minutos: %v", start)
		}
		if start.Before(c.at(day, 9, 0)) || start.Add(120*time.Minute).After(c.at(day, 13, 0)) {
			t.Fatalf("el turno se sale del tramo: %v", start)
		}
	}
	if got := roundSlotForTest(r, c, day, "09:00", "10:00", 120); !got.IsZero() {
		t.Fatalf("un servicio de 120 minutos no entra en un tramo de 60: %v", got)
	}
}

// roundSlotForTest permite probar roundSlot (que vive en el Seed) sin armar un
// Seed completo: es la misma cuenta con el reloj y el azar del test.
func roundSlotForTest(r *rng, c clock, day time.Time, open, close string, minutes int) time.Time {
	s := &Seed{r: r, clk: c}
	return s.roundSlot(day, open, close, minutes)
}
