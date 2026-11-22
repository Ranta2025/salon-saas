package app

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
)

// hoursFixture: equipo con dueño, administración, una profesional, recepción y
// alguien de otro salón, más el horario semanal en memoria.
type hoursFixture struct {
	salon    SalonHoursService
	member   ProfessionalHoursService
	mems     *fakeMemberships
	hours    *fakeWeeklyHours
	settings *fakeSettings
}

func newHoursFixture(t *testing.T) *hoursFixture {
	t.Helper()
	mems := &fakeMemberships{}
	for _, m := range []domain.Membership{
		{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner, Attends: true},
		{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin},
		{UserID: "u-prof", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-prof2", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception},
		{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner, Attends: true},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membresía %s: %v", m.UserID, err)
		}
	}
	settings := &fakeSettings{bySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}}}
	hours := &fakeWeeklyHours{}
	return &hoursFixture{
		salon:    SalonHoursService{Hours: hours, Settings: settings, Memberships: mems},
		member:   ProfessionalHoursService{Hours: hours, Memberships: mems},
		mems:     mems,
		hours:    hours,
		settings: settings,
	}
}

// member devuelve la membresía tal como la arma el handler desde el token.
func member(t *testing.T, mems *fakeMemberships, userID, salonID string) domain.Membership {
	t.Helper()
	m, err := mems.Find(userID, salonID)
	if err != nil {
		t.Fatalf("membresía %s: %v", userID, err)
	}
	return m
}

// S-F2d1-2/S-F2d1-4: sin filas propias el salón responde la ventana única
// expandida a los 7 días con custom=false; con filas, custom=true y solo los
// días abiertos. Cualquier miembro lee.
func TestSalonHours_get(t *testing.T) {
	f := newHoursFixture(t)
	owner := member(t, f.mems, "u-owner", "s1")

	days, custom, err := f.salon.Get(owner, "s1")
	if err != nil {
		t.Fatalf("leer horario: %v", err)
	}
	if custom {
		t.Fatal("sin filas custom tiene que ser false")
	}
	if len(days) != 7 {
		t.Fatalf("la ventana única se expande a 7 días: %+v", days)
	}
	for i, d := range days {
		if d.Weekday != i+1 || d.Open != "09:00" || d.Close != "19:00" {
			t.Fatalf("día %d: %+v", i+1, d)
		}
	}

	// Cualquier miembro lee: la profesional necesita el horario para su agenda.
	prof := member(t, f.mems, "u-prof", "s1")
	if _, _, err := f.salon.Get(prof, "s1"); err != nil {
		t.Fatalf("la profesional tiene que poder leer: %v", err)
	}
	// Un ajeno al salón no.
	stranger := member(t, f.mems, "u-otro", "s2")
	if _, _, err := f.salon.Get(stranger, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ajeno leyendo horario: %v", err)
	}

	// Con días propios: custom=true y tal cual se guardaron.
	if _, _, err := f.salon.Set(owner, "s1", []domain.WeekdayHours{
		{Weekday: 1, Open: "09:00", Close: "18:00"},
		{Weekday: 3, Open: "10:00", Close: "16:00"},
	}); err != nil {
		t.Fatalf("guardar horario: %v", err)
	}
	days, custom, err = f.salon.Get(owner, "s1")
	if err != nil {
		t.Fatalf("releer horario: %v", err)
	}
	if !custom || len(days) != 2 || days[0].Weekday != 1 || days[1].Weekday != 3 {
		t.Fatalf("horario propio: %+v custom=%v", days, custom)
	}

	// Lista vacía = volver al horario único, sin filas.
	days, custom, err = f.salon.Set(owner, "s1", nil)
	if err != nil {
		t.Fatalf("volver al horario único: %v", err)
	}
	if custom || len(days) != 7 {
		t.Fatalf("volver al horario único: %+v custom=%v", days, custom)
	}
	if rows, _ := f.hours.SalonHours("s1"); len(rows) != 0 {
		t.Fatalf("no tiene que quedar ninguna fila: %+v", rows)
	}
}

// S-F2d1-4: el PUT del horario del salón es de owner/admin y valida ANTES de
// escribir: un cuerpo inválido deja el horario anterior intacto.
func TestSalonHours_set_permissions_and_atomicity(t *testing.T) {
	f := newHoursFixture(t)
	owner := member(t, f.mems, "u-owner", "s1")
	admin := member(t, f.mems, "u-admin", "s1")
	prof := member(t, f.mems, "u-prof", "s1")

	previous := []domain.WeekdayHours{{Weekday: 1, Open: "09:00", Close: "18:00"}}
	if _, _, err := f.salon.Set(owner, "s1", previous); err != nil {
		t.Fatalf("horario inicial: %v", err)
	}

	// Administración también configura el salón.
	adminDays := []domain.WeekdayHours{{Weekday: 2, Open: "10:00", Close: "20:00"}}
	if _, _, err := f.salon.Set(admin, "s1", adminDays); err != nil {
		t.Fatalf("la administración guarda el horario: %v", err)
	}

	// La profesional y recepción no editan.
	for _, who := range []domain.Membership{prof, member(t, f.mems, "u-recep", "s1")} {
		if _, _, err := f.salon.Set(who, "s1", previous); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no puede editar el horario: %v", who.Role, err)
		}
	}

	// Cuerpos inválidos: el mensaje exacto del contrato y ninguna escritura.
	before, _ := f.hours.SalonHours("s1")
	for _, bad := range []struct {
		days []domain.WeekdayHours
		msg  string
	}{
		{[]domain.WeekdayHours{{Weekday: 1, Open: "19:00", Close: "09:00"}}, domain.MsgHoursOrder},
		{[]domain.WeekdayHours{{Weekday: 9, Open: "09:00", Close: "19:00"}}, domain.MsgWeekday},
		{[]domain.WeekdayHours{{Weekday: 1, Open: "9:00", Close: "19:00"}}, domain.MsgHoursFormat},
		{[]domain.WeekdayHours{
			{Weekday: 1, Open: "09:00", Close: "13:00"},
			{Weekday: 1, Open: "14:00", Close: "19:00"},
		}, domain.MsgWeekdayRepeat},
	} {
		_, _, err := f.salon.Set(owner, "s1", bad.days)
		if err == nil || err.Error() != bad.msg {
			t.Fatalf("esperaba %q, dio %v", bad.msg, err)
		}
	}
	after, _ := f.hours.SalonHours("s1")
	if len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("un cuerpo inválido no puede tocar el horario: antes %+v, después %+v", before, after)
	}
}

// S-F2d1-4: los tramos de una profesional. La propia persona los lee, cualquier
// otra profesional no; solo owner/admin los escribe; un miembro de otro salón es
// 404 y un rol que no atiende no tiene horario.
func TestProfessionalHours_get_and_set(t *testing.T) {
	f := newHoursFixture(t)
	owner := member(t, f.mems, "u-owner", "s1")
	prof := member(t, f.mems, "u-prof", "s1")
	otra := member(t, f.mems, "u-prof2", "s1")

	segments := []domain.HoursSegment{
		{Weekday: 2, Start: "09:00", End: "13:00"},
		{Weekday: 2, Start: "14:00", End: "18:00"},
	}
	got, custom, err := f.member.Set(owner, "s1", "u-prof", segments)
	if err != nil {
		t.Fatalf("guardar tramos: %v", err)
	}
	if !custom || len(got) != 2 {
		t.Fatalf("tramos guardados: %+v custom=%v", got, custom)
	}

	// La propia persona los lee; el dueño también.
	read, custom, err := f.member.Get(prof, "s1", "u-prof")
	if err != nil {
		t.Fatalf("la profesional lee sus tramos: %v", err)
	}
	if !custom || len(read) != 2 || read[0] != segments[0] {
		t.Fatalf("tramos leídos: %+v", read)
	}
	if _, _, err := f.member.Get(owner, "s1", "u-prof2"); err != nil {
		t.Fatalf("el dueño lee los tramos de cualquiera: %v", err)
	}

	// Otra profesional: 403 (no espía la agenda de la compañera).
	if _, _, err := f.member.Get(otra, "s1", "u-prof"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otra profesional: %v", err)
	}
	// Miembro de otro salón: 404.
	if _, _, err := f.member.Get(owner, "s1", "u-otro"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro de otro salón: %v", err)
	}
	// Editar: solo owner/admin, aunque sea su propio horario.
	if _, _, err := f.member.Set(prof, "s1", "u-prof", segments); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("la profesional no edita su horario: %v", err)
	}
	// Un rol que no atiende no tiene horario propio.
	if _, _, err := f.member.Set(owner, "s1", "u-recep", segments); err == nil ||
		err.Error() != "Ese rol no tiene horario" {
		t.Fatalf("recepción: %v", err)
	}
	// Miembro de otro salón: 404 también al escribir.
	if _, _, err := f.member.Set(owner, "s1", "u-otro", segments); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("escribir en otro salón: %v", err)
	}
}

// S-F2d1-4: los tramos se validan ANTES de escribir: un cuerpo inválido deja los
// tramos anteriores intactos (el reemplazo es atómico).
func TestProfessionalHours_set_atomicity(t *testing.T) {
	f := newHoursFixture(t)
	owner := member(t, f.mems, "u-owner", "s1")
	previous := []domain.HoursSegment{{Weekday: 2, Start: "09:00", End: "13:00"}}
	if _, _, err := f.member.Set(owner, "s1", "u-prof", previous); err != nil {
		t.Fatalf("tramos iniciales: %v", err)
	}

	for _, bad := range []struct {
		segments []domain.HoursSegment
		msg      string
	}{
		{[]domain.HoursSegment{{Weekday: 2, Start: "14:00", End: "13:00"}}, domain.MsgHoursOrder},
		{[]domain.HoursSegment{
			{Weekday: 2, Start: "09:00", End: "13:00"},
			{Weekday: 2, Start: "12:00", End: "15:00"},
		}, domain.MsgHoursOverlap},
		{[]domain.HoursSegment{
			{Weekday: 2, Start: "08:00", End: "09:00"},
			{Weekday: 2, Start: "09:00", End: "10:00"},
			{Weekday: 2, Start: "10:00", End: "11:00"},
			{Weekday: 2, Start: "11:00", End: "12:00"},
			{Weekday: 2, Start: "12:00", End: "13:00"},
		}, domain.MsgHoursTooMany},
	} {
		if _, _, err := f.member.Set(owner, "s1", "u-prof", bad.segments); err == nil || err.Error() != bad.msg {
			t.Fatalf("esperaba %q, dio %v", bad.msg, err)
		}
	}
	after, _, err := f.member.Get(owner, "s1", "u-prof")
	if err != nil {
		t.Fatalf("releer tramos: %v", err)
	}
	if len(after) != 1 || after[0] != previous[0] {
		t.Fatalf("un cuerpo inválido no puede tocar los tramos: %+v", after)
	}

	// Lista vacía = vuelve a seguir el horario del salón.
	got, custom, err := f.member.Set(owner, "s1", "u-prof", nil)
	if err != nil {
		t.Fatalf("volver al horario del salón: %v", err)
	}
	if custom || len(got) != 0 {
		t.Fatalf("sin tramos propios: %+v custom=%v", got, custom)
	}
}
