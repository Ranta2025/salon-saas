package app

// F2d2: la ausencia manda en la agenda pública (S-F2d2-3). Una profesional
// ausente no se ofrece ni se asigna; el resto del equipo sigue igual. La reserva
// de staff desde el calendario NO se restringe (solo el choque, como siempre).
import (
	"errors"
	"salon-saas/internal/testutil/agendakit"
	"strings"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// seedTimeOff deja una ausencia en la DB fake del entorno público, sin pasar por
// el caso de uso (acá se prueba la disponibilidad y la reserva, no el alta).
func seedTimeOff(t *testing.T, env *agendakit.PublicEnv, userID string, start, end time.Time, reason string) domain.TimeOff {
	t.Helper()
	off := domain.TimeOff{
		ID: "off-" + userID, SalonID: "s1", UserID: userID,
		StartsAt: start.UTC(), EndsAt: end.UTC(), Reason: reason,
		CreatedAt: time.Now().UTC(),
	}
	env.TimeOff.All = append(env.TimeOff.All, off)
	return off
}

// slotsOf devuelve los arranques locales ("15:04") de una profesional.

func localDay(t *testing.T, day, hour, minute int) time.Time {
	t.Helper()
	return time.Date(2026, 12, day, hour, minute, 0, 0, testutil.MxLocation(t)).UTC()
}

// S-F2d2-3: una ausencia que cubre el día entero saca SOLO a esa profesional de
// la disponibilidad; la otra sigue ofreciendo sus huecos y el filtro por
// profesional devuelve [] (sin error).
func TestAvailability_timeOff_whole_day_removes_only_that_professional(t *testing.T) {
	env := agendakit.WideEnv(t)
	// Día completo en la zona del salón: 00:00 a 00:00 del día siguiente.
	seedTimeOff(t, env, "prof1", localDay(t, 1, 0, 0), localDay(t, 2, 0, 0), "Vacaciones")

	day, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	if got := len(agendakit.SlotsOf(t, day.Slots, "prof1")); got != 0 {
		t.Fatalf("prof1 está ausente: %d huecos", got)
	}
	// prof2 conserva todos sus arranques: 09:00 a 18:00 cada 30 min.
	prof2 := agendakit.SlotsOf(t, day.Slots, "prof2")
	if len(prof2) != 19 || prof2[0] != "09:00" || prof2[len(prof2)-1] != "18:00" {
		t.Fatalf("prof2: %v", prof2)
	}

	// Pedirle el día a la ausente no es un error: no hay huecos, es todo.
	only, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof1")
	if err != nil {
		t.Fatalf("filtro por la ausente: %v", err)
	}
	if len(only.Slots) != 0 {
		t.Fatalf("la ausente no ofrece nada: %+v", only.Slots)
	}

	// El día anterior a la ausencia no cambia nada.
	prev, err := env.Avail.Day("navajas-oro", "2026-11-30", env.Corte.ID, "prof1")
	if err != nil {
		t.Fatalf("día anterior: %v", err)
	}
	if len(prev.Slots) != 19 {
		t.Fatalf("el día anterior a la ausencia sigue completo: %d", len(prev.Slots))
	}
}

// S-F2d2-3: una ausencia parcial (10:00-12:00 local) bloquea SOLO los huecos que
// la pisan, incluido el turno de 90 minutos que arranca a las 09:00 (termina
// 10:30: se pisa). Un turno que arranca EXACTAMENTE cuando la ausencia termina
// (12:00) se ofrece: el período es [starts_at, ends_at).
func TestAvailability_timeOff_partial_day_and_boundary(t *testing.T) {
	env := agendakit.WideEnv(t)
	seedTimeOff(t, env, "prof1", localDay(t, 1, 10, 0), localDay(t, 1, 12, 0), "Médico")

	day, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "prof1")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	got := agendakit.SlotsOf(t, day.Slots, "prof1")
	// Todo el día salvo los arranques que se pisan con 10:00-12:00: 09:30,
	// 10:00, 10:30, 11:00 y 11:30 (11:30 + 45 = 12:15 se pisa). Las 12:00 se
	// ofrecen: la ausencia termina exactamente ahí (período semiabierto).
	want := []string{"09:00", "12:00", "12:30", "13:00", "13:30", "14:00", "14:30", "15:00",
		"15:30", "16:00", "16:30", "17:00", "17:30", "18:00"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("huecos de prof1 con la ausencia 10:00-12:00:\n%d %v\nesperaba\n%d %v",
			len(got), got, len(want), want)
	}

	// El turno de 90 minutos de las 09:00 termina 10:30: se pisa con la ausencia y
	// no se ofrece. El de las 12:00 (termina 13:30) sí.
	largo := servicioDe(t, env, "Alisado", 90)
	day, err = env.Avail.Day("navajas-oro", "2026-12-01", largo.ID, "prof1")
	if err != nil {
		t.Fatalf("disponibilidad con servicio largo: %v", err)
	}
	got = agendakit.SlotsOf(t, day.Slots, "prof1")
	if len(got) == 0 || got[0] != "12:00" {
		t.Fatalf("el turno de 90 min de las 09:00 se pisa con la ausencia: %v", got)
	}
}

// S-F2d2-3: la asignación automática saltea a quien está ausente, y con todo el
// equipo ausente la reserva es 409 (nunca 500 ni un turno sin profesional).
func TestPublicBooking_timeOff_auto_assignment(t *testing.T) {
	env := agendakit.WideEnv(t)
	// prof2 (el primero por orden alfabético es prof1; se cubren los dos casos
	// dejando ausente a prof1 para que la asignación caiga en prof2).
	seedTimeOff(t, env, "prof1", localDay(t, 1, 0, 0), localDay(t, 2, 0, 0), "")

	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID
	in.StartsAt = localDay(t, 1, 10, 0)
	b, err := env.Book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if b.ProfessionalID != "prof2" {
		t.Fatalf("la asignación tiene que saltear a la ausente, dio %q", b.ProfessionalID)
	}

	// Con las dos ausentes: 409 con el mensaje de siempre.
	seedTimeOff(t, env, "prof2", localDay(t, 1, 0, 0), localDay(t, 2, 0, 0), "")
	in.StartsAt = localDay(t, 1, 11, 0)
	_, err = env.Book.Book("navajas-oro", in)
	if !errors.Is(err, domain.ErrAlreadyExists) ||
		!strings.Contains(err.Error(), "no hay profesional libre en ese horario") {
		t.Fatalf("sin nadie disponible: %v (esperaba 409 de siempre)", err)
	}

	// Con profesional elegido y ausente: tampoco entra (409, no un turno para
	// quien no está).
	in.ProfessionalID = "prof1"
	in.StartsAt = localDay(t, 1, 12, 0)
	_, err = env.Book.Book("navajas-oro", in)
	if !errors.Is(err, domain.ErrAlreadyExists) ||
		!strings.Contains(err.Error(), "el horario ya no está disponible") {
		t.Fatalf("profesional ausente elegido a mano: %v", err)
	}
}

// S-F2d2-3: la reserva de staff desde el calendario NO mira las ausencias: solo
// la frena el choque. Es una decisión del contrato (igual que F2d1 con el
// horario): el salón reasigna o cancela a mano (F2d3).
func TestStaffBooking_ignores_time_off(t *testing.T) {
	env := agendakit.WideEnv(t)
	seedTimeOff(t, env, "prof1", localDay(t, 1, 0, 0), localDay(t, 2, 0, 0), "Vacaciones")
	mems := &testutil.FakeMemberships{}
	reception := domain.Membership{UserID: "u-rec", SalonID: "s1", Role: domain.RoleReception}
	if err := mems.Save(reception); err != nil {
		t.Fatalf("membership: %v", err)
	}
	pro := domain.Membership{UserID: "prof1", SalonID: "s1", Role: domain.RoleProfessional, Attends: true}
	if err := mems.Save(pro); err != nil {
		t.Fatalf("membership: %v", err)
	}
	staff := bkg.BookingService{Bookings: env.Bookings, Memberships: mems}

	start := localDay(t, 1, 10, 0)
	b, err := staff.CreateBooking(reception, "s1", "prof1", "Lía", "555", start, start.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("el calendario tiene que poder agendar igual: %v", err)
	}
	if b.ProfessionalID != "prof1" || b.Status != domain.BookingBooked {
		t.Fatalf("reserva de staff: %+v", b)
	}
}

// PRESERVE: sin ausencias, la disponibilidad es EXACTAMENTE la de F2c + F2d1
// (servicio que hace la persona ∩ horario del salón ∩ sus tramos). El filtro
// nuevo no puede cambiar nada cuando no hay filas de time_off.
func TestAvailability_without_timeOff_keeps_weekly_hours_and_services(t *testing.T) {
	env := agendakit.WideEnv(t)
	// Martes (2026-12-01): el salón abre 09:00-13:00 y prof1 trabaja 09:00-10:00
	// y 11:00-13:00 (descanso de 10 a 11). prof2 sigue el horario del salón.
	weekday := domain.IsoWeekday(time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC), testutil.MxLocation(t))
	if err := env.Hours.SaveSalonHours("s1", []domain.WeekdayHours{
		{Weekday: weekday, Open: "09:00", Close: "13:00"},
	}); err != nil {
		t.Fatalf("horario del salón: %v", err)
	}
	if err := env.Hours.SaveMemberHours("s1", "prof1", []domain.HoursSegment{
		{Weekday: weekday, Start: "09:00", End: "10:00"},
		{Weekday: weekday, Start: "11:00", End: "13:00"},
	}); err != nil {
		t.Fatalf("tramos de prof1: %v", err)
	}
	// Solo prof2 hace "Color" (F2c): el servicio filtra, no el horario.
	color := servicioDe(t, env, "Color", 45)
	env.Profs.ByService = map[string][]string{"s1|" + color.ID: {"prof2"}}

	day, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	wantProf1 := "09:00,11:00,11:30,12:00"
	wantProf2 := "09:00,09:30,10:00,10:30,11:00,11:30,12:00"
	if got := strings.Join(agendakit.SlotsOf(t, day.Slots, "prof1"), ","); got != wantProf1 {
		t.Fatalf("prof1 con sus tramos: %v (esperaba %v)", got, wantProf1)
	}
	if got := strings.Join(agendakit.SlotsOf(t, day.Slots, "prof2"), ","); got != wantProf2 {
		t.Fatalf("prof2 siguiendo al salón: %v (esperaba %v)", got, wantProf2)
	}

	day, err = env.Avail.Day("navajas-oro", "2026-12-01", color.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad de Color: %v", err)
	}
	if got := agendakit.SlotsOf(t, day.Slots, "prof1"); len(got) != 0 {
		t.Fatalf("prof1 no hace Color: %v", got)
	}
	if got := strings.Join(agendakit.SlotsOf(t, day.Slots, "prof2"), ","); got != wantProf2 {
		t.Fatalf("prof2 hace Color: %v (esperaba %v)", got, wantProf2)
	}
}
