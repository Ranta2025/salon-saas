package main

import (
	"math/rand"
	"time"
)

// rng es el azar del seed: SIEMPRE el mismo dataset para la misma semilla.
//
// Usa math/rand (v1) con una fuente propia, no la global: la secuencia de una
// fuente con semilla fija está garantizada por la compatibilidad de Go 1, así
// que dos corridas producen los mismos turnos, las mismas ventas y los mismos
// comentarios. Nunca se usa para nada que importe (claves, tokens): esos los
// genera la API.
type rng struct {
	src  *rand.Rand
	seed int64
}

func newRNG(seed int64) *rng {
	return &rng{src: rand.New(rand.NewSource(seed)), seed: seed}
}

// Intn devuelve un entero en [0,n). Con n<=0 devuelve 0 (no entra en pánico).
func (r *rng) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return r.src.Intn(n)
}

// Between devuelve un entero en [min,max] (inclusive).
func (r *rng) Between(min, max int) int {
	if max <= min {
		return min
	}
	return min + r.Intn(max-min+1)
}

// Chance devuelve true en pct de cada 100 tiradas.
func (r *rng) Chance(pct int) bool {
	return r.Intn(100) < pct
}

// pick elige un elemento de una lista no vacía.
func pick[T any](r *rng, list []T) T {
	var zero T
	if len(list) == 0 {
		return zero
	}
	return list[r.Intn(len(list))]
}

// WeightedIndex elige un índice según pesos positivos. Si todos los pesos son
// cero (o la lista está vacía) devuelve 0.
func (r *rng) WeightedIndex(weights []int) int {
	total := 0
	for _, w := range weights {
		if w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return 0
	}
	roll := r.Intn(total)
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		if roll < w {
			return i
		}
		roll -= w
	}
	return len(weights) - 1
}

// shuffle mezcla una copia de la lista (no toca el original).
func shuffle[T any](r *rng, list []T) []T {
	out := make([]T, len(list))
	copy(out, list)
	r.src.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// clock es el reloj del seed: todos los datos son RELATIVOS a `now` en la zona
// horaria del salón, así la demo siempre parece recién usada.
type clock struct {
	now time.Time
	loc *time.Location
}

func newClock(now time.Time, loc *time.Location) clock {
	if loc == nil {
		loc = time.UTC
	}
	return clock{now: now.In(loc), loc: loc}
}

// today es la medianoche de hoy en la zona del salón.
func (c clock) today() time.Time { return dayStart(c.now, c.loc) }

// daysAgo es la medianoche de hace n días.
func (c clock) daysAgo(n int) time.Time {
	return dayStart(c.now.AddDate(0, 0, -n), c.loc)
}

// daysAhead es la medianoche de dentro de n días.
func (c clock) daysAhead(n int) time.Time {
	return dayStart(c.now.AddDate(0, 0, n), c.loc)
}

// at arma un instante en un día local.
func (c clock) at(day time.Time, hour, minute int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, c.loc)
}

// dayStart lleva un instante a la medianoche de su día en loc.
func dayStart(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// addMinutes devuelve t más n minutos.
func addMinutes(t time.Time, n int) time.Time { return t.Add(time.Duration(n) * time.Minute) }

// weekday returns Monday=1 ... Sunday=7, the convention of the schedule tables.
func weekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// weekdayWeight es el peso de cada día para elegir turnos: viernes y sábado
// llenos, lunes flojo (el dataset tiene que parecer un salón de verdad).
var weekdayWeight = map[int]int{1: 3, 2: 6, 3: 7, 4: 9, 5: 16, 6: 18, 7: 5}

// pickDay elige un día entre los últimos `back` días (0 = hoy, back = el más
// viejo) respetando el peso del día de la semana.
func (r *rng) pickDay(c clock, back int) time.Time {
	weights := make([]int, back+1)
	for i := 0; i <= back; i++ {
		weights[i] = weekdayWeight[weekday(c.daysAgo(i))]
	}
	return c.daysAgo(r.WeightedIndex(weights))
}
