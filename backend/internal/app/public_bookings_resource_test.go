package app

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// flakyBookings simula la carrera real de la EXCLUDE: el INSERT rechaza el
// primer intento aunque la lectura previa lo viera libre (otra reserva entró
// entre el chequeo amable y el guardado). Es el caso que el caso de uso tiene
// que reintentar en otro par, nunca devolver 500.
type flakyBookings struct {
	*fb
	failSaves int
	attempts  []domain.Booking
}

func (f *flakyBookings) Save(b domain.Booking) error {
	f.attempts = append(f.attempts, b)
	if f.failSaves > 0 {
		f.failSaves--
		return domain.ErrAlreadyExists
	}
	return f.fb.Save(b)
}

var _ ports.BookingRepository = (*flakyBookings)(nil)

// S-F2e-4: si la DB rechaza el par (profesional o unidad) por la carrera, el
// caso de uso reintenta con otro par. El turno entra con una unidad distinta.
func TestPublicBooking_reintenta_con_otra_unidad(t *testing.T) {
	env := newPublicEnv(t)
	rt, units := equipa(t, env, "Puesto", 2)
	svc := conEquipo(t, env, "Corte con equipo", 45, rt.ID)

	flaky := &flakyBookings{fb: env.bookings, failSaves: 1}
	env.book.Bookings = flaky

	b, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: svc.ID, ClientName: "Clienta",
		StartsAt: time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("el reintento tiene que entrar: %v", err)
	}
	if len(flaky.attempts) != 2 {
		t.Fatalf("esperaba 2 intentos (el primero rechazado), dio %d", len(flaky.attempts))
	}
	if flaky.attempts[0].ResourceUnitID != units[0].ID {
		t.Fatalf("el primer intento va a la primera unidad: %+v", flaky.attempts[0])
	}
	if b.ResourceUnitID != units[1].ID {
		t.Fatalf("el reintento tiene que ocupar OTRA unidad: %+v (unidades %v)", b, units)
	}
}

// S-F2e-4: si TODOS los pares (profesional, unidad) son rechazados el resultado
// es 409, nunca 500 ni un turno duplicado. Se prueba cada par antes de rendirse
// (más de los 3 del primer diseño) y el barrido está acotado.
func TestPublicBooking_agota_todos_los_pares_409(t *testing.T) {
	env := newPublicEnv(t)
	rt, _ := equipa(t, env, "Puesto", 3)
	svc := conEquipo(t, env, "Corte con equipo", 45, rt.ID)

	flaky := &flakyBookings{fb: env.bookings, failSaves: 1000}
	env.book.Bookings = flaky

	_, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: svc.ID, ClientName: "Clienta",
		StartsAt: time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("esperaba 409 tras agotar los pares, dio %v", err)
	}
	if len(flaky.attempts) <= maxBookingAttempts {
		t.Fatalf("tiene que barrer todos los pares, no rendirse a los %d intentos: dio %d", maxBookingAttempts, len(flaky.attempts))
	}
	if len(flaky.attempts) > maxPairAttempts {
		t.Fatalf("el barrido está acotado a %d, dio %d", maxPairAttempts, len(flaky.attempts))
	}
	seen := map[string]bool{}
	for _, a := range flaky.attempts {
		key := a.ProfessionalID + "/" + a.ResourceUnitID
		if seen[key] {
			t.Fatalf("el mismo par %s no se reintenta dos veces", key)
		}
		seen[key] = true
	}
	if len(env.bookings.all) != 0 {
		t.Fatalf("no puede quedar ningún turno guardado: %+v", env.bookings.all)
	}
}

// S-F2e-4: tres rechazos seguidos NO alcanzan para dar "sin lugar" si todavía
// quedan pares libres: la reserva entra con el siguiente. Un "sin lugar" falso
// hace que la clienta se vaya aunque haya un puesto libre.
func TestPublicBooking_tres_rechazos_no_alcanzan_si_quedan_pares(t *testing.T) {
	env := newPublicEnv(t)
	rt, _ := equipa(t, env, "Puesto", 3)
	svc := conEquipo(t, env, "Corte con equipo", 45, rt.ID)

	flaky := &flakyBookings{fb: env.bookings, failSaves: 3}
	env.book.Bookings = flaky

	b, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: svc.ID, ClientName: "Clienta",
		StartsAt: time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("quedan pares libres: la reserva tiene que entrar, dio %v", err)
	}
	if len(flaky.attempts) != 4 {
		t.Fatalf("3 rechazos y el cuarto intento entra, dio %d intentos", len(flaky.attempts))
	}
	if b.ResourceUnitID == "" || len(env.bookings.all) != 1 {
		t.Fatalf("un turno con unidad: %+v (guardados %d)", b, len(env.bookings.all))
	}
}

// S-F2e-4: elegir profesional a mano sigue funcionando, y el turno ocupa una
// unidad libre; cuando las 3 unidades están tomadas, la cuarta es 409.
func TestPublicBooking_profesional_elegido_ocupa_unidad_libre(t *testing.T) {
	env := newPublicEnv(t)
	rt, _ := equipa(t, env, "Puesto", 3)
	svc := conEquipo(t, env, "Corte con equipo", 45, rt.ID)
	start := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)

	seen := map[string]bool{}
	for i, prof := range []string{"prof1", "prof2", "prof3"} {
		b, err := env.book.Book("navajas-oro", PublicBookingInput{
			ServiceID: svc.ID, ProfessionalID: prof, ClientName: "Clienta", StartsAt: start,
		})
		if err != nil {
			t.Fatalf("reserva %d de %s: %v", i+1, prof, err)
		}
		if b.ProfessionalID != prof || b.ResourceUnitID == "" {
			t.Fatalf("turno de %s: %+v", prof, b)
		}
		seen[b.ResourceUnitID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("3 turnos ocupan 3 unidades distintas: %v", seen)
	}
	if _, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: svc.ID, ProfessionalID: "prof4", ClientName: "Cuarta", StartsAt: start,
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("sin unidad libre la reserva es 409: %v", err)
	}
}
