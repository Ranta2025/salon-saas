package bookings_test

import (
	"errors"
	"salon-saas/internal/testutil/agendakit"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
)

// wideEnv es el salón público de siempre pero abierto 09:00-19:00: da lugar para
// probar tramos, descansos y cierres sin que la ventana de 3 horas del fixture
// tape el caso.

// servicioDe crea y guarda un servicio del salón de prueba.
func servicioDe(t *testing.T, env *agendakit.PublicEnv, name string, durationMin int) domain.Service {
	t.Helper()
	s, err := domain.NewService("s1", name, durationMin, 200000, 10)
	if err != nil {
		t.Fatalf("servicio %s: %v", name, err)
	}
	if err := env.Services.Save(s); err != nil {
		t.Fatalf("guardar servicio %s: %v", name, err)
	}
	return s
}

// startsLocal arma el instante de una hora local del salón.
func startsLocal(t *testing.T, year int, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

// startLabels devuelve los arranques de los huecos en hora local del salón.
func startLabels(t *testing.T, slots []bkg.Slot) []string {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.StartsAt.In(loc).Format("15:04"))
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// S-F2d1-5 (PRESERVE): un salón sin horario semanal y profesionales sin tramos se
// comportan como siempre: la ventana única de /settings vale TODOS los días,
// también el domingo, con los mismos huecos.
func TestAvailability_sin_horario_semanal_ventanaUnicaTodosLosDias(t *testing.T) {
	env := agendakit.NewPublicEnv(t) // 09:00-12:00, corte de 45 min, dos profesionales
	want := []string{"09:00", "09:00", "09:30", "09:30", "10:00", "10:00", "10:30", "10:30", "11:00", "11:00"}
	for _, date := range []string{
		"2026-12-01", "2026-12-02", "2026-12-03", "2026-12-04",
		"2026-12-05", "2026-12-06", "2026-12-07",
	} {
		day, err := env.Avail.Day("navajas-oro", date, env.Corte.ID, "")
		if err != nil {
			t.Fatalf("%s: %v", date, err)
		}
		if got := startLabels(t, day.Slots); !sameStrings(got, want) {
			t.Fatalf("%s: huecos %v (esperaba %v)", date, got, want)
		}
	}
}

// S-F2d1-5: con horario semanal propio, el día sin fila está cerrado y no ofrece
// nada, y la reserva de ese día es 409 (nunca 500).
func TestAvailability_diaCerradoDelSalon(t *testing.T) {
	env := agendakit.WideEnv(t)
	var days []domain.WeekdayHours
	for wd := 1; wd <= 6; wd++ { // lunes a sábado; el domingo queda cerrado
		days = append(days, domain.WeekdayHours{Weekday: wd, Open: "09:00", Close: "19:00"})
	}
	if err := env.Hours.SaveSalonHours("s1", days); err != nil {
		t.Fatalf("horario del salón: %v", err)
	}

	// Domingo 6/12/2026: cerrado.
	day, err := env.Avail.Day("navajas-oro", "2026-12-06", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("domingo: %v", err)
	}
	if len(day.Slots) != 0 {
		t.Fatalf("un día cerrado no ofrece nada: %+v", day.Slots)
	}
	// Lunes 7/12/2026: abierto, con huecos.
	day, err = env.Avail.Day("navajas-oro", "2026-12-07", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("lunes: %v", err)
	}
	if len(day.Slots) == 0 {
		t.Fatal("el lunes está abierto y tiene huecos")
	}

	// Reservar un domingo cerrado: 409, sin excepción de 500.
	_, err = env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID:  env.Corte.ID,
		ClientName: "Lía",
		StartsAt:   startsLocal(t, 2026, 12, 6, 10, 0),
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("reserva en un día cerrado: %v", err)
	}
	if len(env.Bookings.All) != 0 {
		t.Fatalf("no se puede guardar nada: %+v", env.Bookings.All)
	}
}

// S-F2d1-5: la profesional trabaja en SUS tramos: los huecos no caen en el
// descanso, no cruzan el cierre y un turno largo tampoco puede cruzar el
// descanso.
func TestAvailability_tramosDeLaProfesional(t *testing.T) {
	env := agendakit.WideEnv(t)
	alisado := servicioDe(t, env, "Alisado", 90)
	env.Profs.Assign("s1", env.Corte.ID, "prof1")
	env.Profs.Assign("s1", alisado.ID, "prof1")
	if err := env.Hours.SaveMemberHours("s1", "prof1", []domain.HoursSegment{
		{Weekday: 2, Start: "09:00", End: "13:00"},
		{Weekday: 2, Start: "14:00", End: "18:00"},
	}); err != nil {
		t.Fatalf("tramos: %v", err)
	}

	// Corte (45 min) el martes: mañana hasta las 12:00 (12:30 + 45 pasaría las
	// 13:00) y tarde desde las 14:00.
	day, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof1")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	want := []string{
		"09:00", "09:30", "10:00", "10:30", "11:00", "11:30", "12:00",
		"14:00", "14:30", "15:00", "15:30", "16:00", "16:30", "17:00",
	}
	if got := startLabels(t, day.Slots); !sameStrings(got, want) {
		t.Fatalf("huecos del martes: %v\n(esperaba %v)", got, want)
	}
	for _, label := range startLabels(t, day.Slots) {
		if label >= "13:00" && label < "14:00" {
			t.Fatalf("el descanso no puede ofrecer turnos: %v", label)
		}
	}

	// Alisado (90 min): 12:30 cruzaría el descanso y no se ofrece; 11:30 termina
	// justo a las 13:00 y sí.
	day, err = env.Avail.Day("navajas-oro", "2026-12-01", alisado.ID, "prof1")
	if err != nil {
		t.Fatalf("alisado: %v", err)
	}
	got := startLabels(t, day.Slots)
	for _, label := range got {
		if label > "11:30" && label < "14:00" {
			t.Fatalf("un turno de 90 minutos no entra antes del descanso: %v", got)
		}
	}
	if !contains(got, "11:30") || contains(got, "12:30") {
		t.Fatalf("alisado del martes: %v", got)
	}

	// Y la reserva de ese turno largo que cruza el descanso es 409.
	_, err = env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID:      alisado.ID,
		ProfessionalID: "prof1",
		ClientName:     "Lía",
		StartsAt:       startsLocal(t, 2026, 12, 1, 12, 30),
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("turno que cruza el descanso: %v", err)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// S-F2d1-5: una profesional con horario solo el martes no se ofrece el lunes y la
// asignación automática la saltea; si NADIE trabaja ese día, la reserva es 409.
func TestAvailability_profesionalConHorarioPropio_automatico(t *testing.T) {
	env := agendakit.WideEnv(t)
	// Ana trabaja solo los martes; Bruno sigue el horario del salón.
	if err := env.Hours.SaveMemberHours("s1", "prof1", []domain.HoursSegment{
		{Weekday: 2, Start: "09:00", End: "18:00"},
	}); err != nil {
		t.Fatalf("tramos: %v", err)
	}

	monday, err := env.Avail.Day("navajas-oro", "2026-12-07", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("lunes: %v", err)
	}
	if len(monday.Slots) == 0 {
		t.Fatal("Bruno sigue al salón y el lunes tiene huecos")
	}
	for _, s := range monday.Slots {
		if s.ProfessionalID == "prof1" {
			t.Fatalf("Ana no trabaja el lunes y no se ofrece: %+v", monday.Slots)
		}
	}

	// El lunes la asignación automática elige a Bruno.
	b, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID:  env.Corte.ID,
		ClientName: "Lía",
		StartsAt:   startsLocal(t, 2026, 12, 7, 10, 0),
	})
	if err != nil {
		t.Fatalf("reserva del lunes: %v", err)
	}
	if b.ProfessionalID != "prof2" {
		t.Fatalf("la asignación automática tiene que saltear a quien no trabaja: %s", b.ProfessionalID)
	}

	// Si tampoco Bruno trabaja el lunes, no hay huecos ni reserva (409).
	if err := env.Hours.SaveMemberHours("s1", "prof2", []domain.HoursSegment{
		{Weekday: 2, Start: "09:00", End: "18:00"},
	}); err != nil {
		t.Fatalf("tramos: %v", err)
	}
	monday, err = env.Avail.Day("navajas-oro", "2026-12-07", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("lunes sin nadie: %v", err)
	}
	if len(monday.Slots) != 0 {
		t.Fatalf("nadie trabaja el lunes: %+v", monday.Slots)
	}
	_, err = env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID:  env.Corte.ID,
		ClientName: "Otra",
		StartsAt:   startsLocal(t, 2026, 12, 7, 11, 0),
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("reserva sin nadie con ventana: %v", err)
	}

	// El martes Ana sí está y el turno se le puede asignar a ella.
	if _, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID:      env.Corte.ID,
		ProfessionalID: "prof1",
		ClientName:     "Caro",
		StartsAt:       startsLocal(t, 2026, 12, 1, 9, 0),
	}); err != nil {
		t.Fatalf("el martes Ana trabaja: %v", err)
	}
}

// S-F2d1-3/S-F2d1-5 (zona horaria): el día de la semana se calcula en la zona del
// salón. Un domingo 23:30 en CDMX es lunes en UTC: se ofrece como domingo si el
// salón abre el domingo, y el lunes (cerrado en el salón) no ofrece nada.
func TestAvailability_diaDeLaSemana_enLaZonaDelSalon(t *testing.T) {
	env := agendakit.WideEnv(t)
	servicio := servicioDe(t, env, "Retoque", 20)
	// El salón abre SOLO el domingo, de 20:00 a 23:59.
	if err := env.Hours.SaveSalonHours("s1", []domain.WeekdayHours{
		{Weekday: 7, Open: "20:00", Close: "23:59"},
	}); err != nil {
		t.Fatalf("horario del salón: %v", err)
	}

	sunday, err := env.Avail.Day("navajas-oro", "2026-12-06", servicio.ID, "")
	if err != nil {
		t.Fatalf("domingo: %v", err)
	}
	var lastUTC string
	for _, s := range sunday.Slots {
		lastUTC = s.StartsAt.Format(time.RFC3339)
	}
	// 23:30 local del domingo = 05:30 UTC del LUNES 7/12.
	if lastUTC != "2026-12-07T05:30:00Z" {
		t.Fatalf("último hueco del domingo: %q (esperaba 2026-12-07T05:30:00Z)", lastUTC)
	}

	// Ese instante, mirado desde UTC, ya es lunes: el lunes del salón está
	// cerrado, así que no ofrece nada.
	monday, err := env.Avail.Day("navajas-oro", "2026-12-07", servicio.ID, "")
	if err != nil {
		t.Fatalf("lunes: %v", err)
	}
	if len(monday.Slots) != 0 {
		t.Fatalf("el lunes está cerrado en el salón: %+v", monday.Slots)
	}

	// Y la reserva de las 23:30 del domingo entra como turno del domingo.
	if _, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID:  servicio.ID,
		ClientName: "Noche",
		StartsAt:   startsLocal(t, 2026, 12, 6, 23, 30),
	}); err != nil {
		t.Fatalf("reserva del domingo a las 23:30: %v", err)
	}
}

// S-F2d1-5: la reserva pública respeta los tramos y el cierre. Un turno fuera de
// la ventana del salón sigue siendo 400 (comportamiento de siempre); uno que cae
// en un descanso o en el cierre de la profesional es 409.
func TestPublicBooking_horarioSemanal(t *testing.T) {
	env := agendakit.WideEnv(t)
	tramos := []domain.HoursSegment{
		{Weekday: 2, Start: "09:00", End: "13:00"},
		{Weekday: 2, Start: "14:00", End: "18:00"},
	}
	for _, id := range []string{"prof1", "prof2"} {
		if err := env.Hours.SaveMemberHours("s1", id, tramos); err != nil {
			t.Fatalf("tramos de %s: %v", id, err)
		}

		// Fuera de la ventana del salón: 400 (validación, como antes).
		_, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
			ServiceID:      env.Corte.ID,
			ProfessionalID: id,
			ClientName:     "Lía",
			StartsAt:       startsLocal(t, 2026, 12, 1, 8, 0),
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("fuera de la ventana del salón: %v", err)
		}

		// En el descanso: 409 (el hueco no existe).
		_, err = env.Book.Book("navajas-oro", bkg.PublicBookingInput{
			ServiceID:      env.Corte.ID,
			ProfessionalID: id,
			ClientName:     "Lía",
			StartsAt:       startsLocal(t, 2026, 12, 1, 13, 0),
		})
		if !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("turno en el descanso: %v", err)
		}

		// Cruzando el cierre de la profesional: 409.
		_, err = env.Book.Book("navajas-oro", bkg.PublicBookingInput{
			ServiceID:      env.Corte.ID,
			ProfessionalID: id,
			ClientName:     "Lía",
			StartsAt:       startsLocal(t, 2026, 12, 1, 17, 30),
		})
		if !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("turno que cruza el cierre: %v", err)
		}
	}
}
