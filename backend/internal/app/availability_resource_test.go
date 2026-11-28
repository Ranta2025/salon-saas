package app

import (
	"errors"
	"salon-saas/internal/testutil/agendakit"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
)

// equipa agrega un tipo de equipo con `count` unidades activas al salón de la
// fixture pública y devuelve el tipo y sus unidades.
func equipa(t *testing.T, env *agendakit.PublicEnv, typeName string, count int) (domain.ResourceType, []domain.ResourceUnit) {
	t.Helper()
	rt, err := domain.NewResourceType("s1", typeName)
	if err != nil {
		t.Fatalf("tipo: %v", err)
	}
	units := make([]domain.ResourceUnit, 0, count)
	for i := 1; i <= count; i++ {
		u, err := domain.NewResourceUnit("s1", rt.ID, rt.UnitName(i))
		if err != nil {
			t.Fatalf("unidad: %v", err)
		}
		units = append(units, u)
	}
	if err := env.Resources.CreateType(rt, units); err != nil {
		t.Fatalf("guardar equipo: %v", err)
	}
	return rt, units
}

// conEquipo devuelve un servicio del catálogo de la fixture que exige ese tipo.
func conEquipo(t *testing.T, env *agendakit.PublicEnv, name string, durationMin int, typeID string) domain.Service {
	t.Helper()
	svc, err := domain.NewService("s1", name, durationMin, 100000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	svc.ResourceTypeID = typeID
	if err := env.Services.Save(svc); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	// Quién lo hace: la fixture tiene prof1 y prof2; se agregan tres más para
	// poder mirar el tope del equipamiento y no el de personas.
	env.Staff.BySalon["s1"] = []domain.StaffMember{
		{ID: "prof1", Name: "Ana", Role: domain.RoleProfessional},
		{ID: "prof2", Name: "Bruno", Role: domain.RoleProfessional},
		{ID: "prof3", Name: "Caro", Role: domain.RoleProfessional},
		{ID: "prof4", Name: "Dani", Role: domain.RoleProfessional},
		{ID: "prof5", Name: "Eva", Role: domain.RoleProfessional},
	}
	return svc
}

// S-F2e-4: con 5 profesionales y 3 unidades activas, nunca se ofrecen más de 3
// turnos a la misma hora: la capacidad la pone el equipamiento, no la plantilla.
func TestAvailability_tope_por_unidades(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	rt, _ := equipa(t, env, "Puesto de peluquería", 3)
	svc := conEquipo(t, env, "Corte con equipo", 45, rt.ID)

	day, err := env.Avail.Day("navajas-oro", "2026-12-01", svc.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	perStart := map[time.Time]int{}
	for _, s := range day.Slots {
		perStart[s.StartsAt]++
	}
	if len(perStart) == 0 {
		t.Fatal("tiene que haber huecos")
	}
	for start, n := range perStart {
		if n > 3 {
			t.Fatalf("a las %s se ofrecen %d turnos con 3 unidades: %+v", start, n, day.Slots)
		}
	}
	// 09:00 local (15:00Z): 3 huecos, uno por unidad disponible.
	if got := perStart[time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)]; got != 3 {
		t.Fatalf("a las 09:00 con 3 unidades y 5 profesionales esperaba 3, dio %d", got)
	}

	// Y aceptar: los 3 primeros entran, el cuarto es 409 (nunca 500).
	start := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	used := map[string]bool{}
	for i := 0; i < 3; i++ {
		b, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
			ServiceID: svc.ID, ClientName: "Clienta", StartsAt: start,
		})
		if err != nil {
			t.Fatalf("reserva %d: %v", i+1, err)
		}
		if b.ResourceUnitID == "" || used[b.ResourceUnitID] {
			t.Fatalf("cada reserva ocupa una unidad distinta: %+v (usadas %v)", b, used)
		}
		used[b.ResourceUnitID] = true
	}
	if _, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID: svc.ID, ClientName: "Cuarta", StartsAt: start,
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("la cuarta con las 3 unidades tomadas: %v", err)
	}
}

// S-F2e-4: sin unidades activas el servicio no ofrece nada y reservar es 409,
// nunca un 500.
func TestAvailability_sin_unidades_activas(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	rt, units := equipa(t, env, "Lavacabezas", 2)
	svc := conEquipo(t, env, "Lavado", 30, rt.ID)

	// Se desactivan las dos unidades: el equipo existe pero no está disponible.
	for _, u := range units {
		u.Active = false
		if err := env.Resources.SaveUnit(u); err != nil {
			t.Fatalf("desactivar: %v", err)
		}
	}
	day, err := env.Avail.Day("navajas-oro", "2026-12-01", svc.ID, "")
	if err != nil {
		t.Fatalf("sin unidades la respuesta es vacía, no un error: %v", err)
	}
	if day.Slots == nil || len(day.Slots) != 0 {
		t.Fatalf("esperaba [] sin unidades activas: %+v", day.Slots)
	}
	_, err = env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID: svc.ID, ClientName: "Clienta",
		StartsAt: time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("reservar sin equipo activo es 409: %v", err)
	}
}

// S-F2e-4: las duraciones cuentan enteras. Un turno de 3 horas mantiene su
// unidad ocupada 3 horas; mientras tanto otro servicio de 30 minutos entra en
// otra unidad.
func TestAvailability_duraciones_distintas(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	// El salón de la fixture abre 09:00-12:00; para el turno largo se extiende
	// el cierre a 13:00 (la ventana única de /settings).
	env.Settings.BySalon["s1"] = domain.SalonSettings{
		SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "13:00", SlotStepMin: 30,
	}
	rt, _ := equipa(t, env, "Puesto", 3)
	larga := conEquipo(t, env, "Alisado", 180, rt.ID)
	corta := conEquipo(t, env, "Corte", 30, rt.ID)

	// 3 horas: 09:00 local = 15:00Z.
	start := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	b, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID: larga.ID, ClientName: "Clienta", StartsAt: start,
	})
	if err != nil {
		t.Fatalf("reservar el alisado: %v", err)
	}
	if b.EndsAt.Sub(b.StartsAt) != 180*time.Minute || b.ResourceUnitID == "" {
		t.Fatalf("el turno largo ocupa su unidad 3 horas: %+v", b)
	}

	// 11:00 local (17:00Z): la unidad del alisado sigue tomada -> 2 huecos.
	day, err := env.Avail.Day("navajas-oro", "2026-12-01", corta.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad del corte: %v", err)
	}
	at11 := 0
	for _, s := range day.Slots {
		if s.StartsAt.Equal(time.Date(2026, 12, 1, 17, 0, 0, 0, time.UTC)) {
			at11++
		}
	}
	if at11 != 2 {
		t.Fatalf("a las 11:00 quedan 2 unidades libres (una la ocupa el alisado), dio %d", at11)
	}
	// 12:00 local (18:00Z): el alisado terminó, vuelven las 3.
	at12 := 0
	for _, s := range day.Slots {
		if s.StartsAt.Equal(time.Date(2026, 12, 1, 18, 0, 0, 0, time.UTC)) {
			at12++
		}
	}
	if at12 != 3 {
		t.Fatalf("a las 12:00 la unidad del alisado está libre: esperaba 3, dio %d", at12)
	}
	// Un corte de 30 minutos entra a las 11:00 en otra unidad.
	if _, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID: corta.ID, ClientName: "Otro", StartsAt: time.Date(2026, 12, 1, 17, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("el corte de 30 minutos entra en otra unidad: %v", err)
	}
}

// S-F2e-4: desactivar una unidad la saca de la disponibilidad pero el turno que
// ya la tenía la conserva (y sigue ocupándola hasta que se cancele).
func TestAvailability_desactivar_unidad_conserva_turno(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	rt, _ := equipa(t, env, "Puesto", 3)
	svc := conEquipo(t, env, "Corte con equipo", 45, rt.ID)

	b, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID: svc.ID, ClientName: "Clienta",
		StartsAt: time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	unit, _ := env.Resources.FindUnit("s1", b.ResourceUnitID)
	unit.Active = false
	if err := env.Resources.SaveUnit(unit); err != nil {
		t.Fatalf("desactivar la unidad del turno: %v", err)
	}
	stored, _ := env.Bookings.FindByID("s1", b.ID)
	if stored.ResourceUnitID != unit.ID {
		t.Fatalf("el turno conserva su unidad: %+v", stored)
	}
	// La unidad desactivada no se ofrece: a las 09:00 quedan 2.
	day, err := env.Avail.Day("navajas-oro", "2026-12-01", svc.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	at09 := 0
	for _, s := range day.Slots {
		if s.StartsAt.Equal(time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)) {
			at09++
		}
	}
	// Quedan 2 unidades activas libres (la tercera, desactivada, conserva el
	// turno) y 4 profesionales libres: el tope son las 2 unidades.
	if at09 != 2 {
		t.Fatalf("esperaba 2 huecos (las 2 unidades activas libres), dio %d", at09)
	}
}

// PRESERVE: un servicio SIN tipo de equipo ofrece exactamente los mismos huecos
// que antes de F2e y la reserva no ocupa unidad.
func TestAvailability_servicio_sin_equipo_no_cambia(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Staff.BySalon["s1"] = []domain.StaffMember{
		{ID: "prof1", Name: "Ana", Role: domain.RoleProfessional},
		{ID: "prof2", Name: "Bruno", Role: domain.RoleProfessional},
	}
	day, err := env.Avail.Day("navajas-oro", "2026-12-01", env.Corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	// 5 huecos por profesional (09:00..11:00 cada 30 min), como antes.
	if len(day.Slots) != 10 {
		t.Fatalf("sin equipo: 10 huecos como antes, dio %d", len(day.Slots))
	}
	b, err := env.Book.Book("navajas-oro", bkg.PublicBookingInput{
		ServiceID: env.Corte.ID, ClientName: "Clienta",
		StartsAt: time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if b.ResourceUnitID != "" {
		t.Fatalf("sin tipo de equipo no se ocupa unidad: %+v", b)
	}
}
