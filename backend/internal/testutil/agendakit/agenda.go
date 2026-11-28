package agendakit

import (
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// LinkAppURL es la URL pública que usan los enlaces de la reserva y la lista de
// espera en los tests.
const LinkAppURL = "http://localhost:8081"

// PublicEnv es el fixture de la agenda pública: un salón en CDMX que abre
// 09:00-12:00 cada 30 minutos, un servicio ACTIVO de 45 minutos y dos
// profesionales. 09:00 local = 15:00Z (CDMX no tiene DST desde 2022). Es la
// copia exportada del fixture de los tests de app.
type PublicEnv struct {
	Avail    bkg.AvailabilityService
	Book     bkg.PublicBookingService
	Bookings *testutil.FakeBookings
	Staff    *testutil.FakeStaffLister
	Profs    *testutil.FakeProfessionalServices
	Settings *testutil.FakeSettings
	Services *testutil.FakeServices
	Clients  *testutil.FakeClients
	// F2d1: horario semanal del salón y de las profesionales (sin filas = el
	// comportamiento de siempre).
	Hours *testutil.FakeWeeklyHours
	// F2d2: ausencias de las profesionales (vacío = nadie está ausente).
	TimeOff *testutil.FakeTimeOff
	// F2e: tipos y unidades de equipo del salón (sin tipos = sin equipamiento).
	Resources *testutil.FakeResources
	Corte     domain.Service
	Viejo     domain.Service
	Events    *testutil.FakeEvents
	Notifier  *testutil.FakeNotifier
	// T12: email de la reserva pública. nil por defecto = email deshabilitado
	// (los tests que no lo necesitan no mandan nada).
	Mailer *testutil.FakeMailer
	Emails *testutil.FakeBookingEmails
}

func NewPublicEnv(t *testing.T) *PublicEnv {
	t.Helper()
	salon := domain.Salon{ID: "s1", Slug: "navajas-oro", Name: "Navajas de Oro"}
	salons := &testutil.FakeSalons{BySlug: map[string]domain.Salon{"navajas-oro": salon}}
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "12:00", SlotStepMin: 30,
	}}}
	services := &testutil.FakeServices{}
	corte, err := domain.NewService("s1", "Corte", 45, 150000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	if err := services.Save(corte); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	viejo, _ := domain.NewService("s1", "Viejo", 30, 1000, 0)
	viejo.Active = false
	_ = services.Save(viejo)

	staff := &testutil.FakeStaffLister{BySalon: map[string][]domain.StaffMember{"s1": {
		{ID: "prof1", Name: "Ana", Role: domain.RoleProfessional},
		{ID: "prof2", Name: "Bruno", Role: domain.RoleProfessional},
	}}}
	profs := &testutil.FakeProfessionalServices{Staff: staff}
	bookings := &testutil.FakeBookings{}
	events := &testutil.FakeEvents{}
	notifier := &testutil.FakeNotifier{}
	clients := &testutil.FakeClients{}
	hours := &testutil.FakeWeeklyHours{}
	timeOff := &testutil.FakeTimeOff{}
	resources := &testutil.FakeResources{Bookings: bookings}
	return &PublicEnv{
		Avail: bkg.AvailabilityService{
			Salons: salons, Services: services, Settings: settings, Staff: staff,
			Bookings: bookings, Professionals: profs, Hours: hours, TimeOff: timeOff,
			Resources: resources,
		},
		Book: bkg.PublicBookingService{
			Salons: salons, Services: services, Settings: settings, Staff: staff,
			Bookings: bookings, Professionals: profs, Clients: clients,
			Events: events, Notifier: notifier, Hours: hours, TimeOff: timeOff,
			Resources: resources,
		},
		Bookings: bookings, Staff: staff, Profs: profs, Settings: settings, Services: services, Clients: clients,
		Hours: hours, TimeOff: timeOff, Resources: resources,
		Corte: corte, Viejo: viejo, Events: events, Notifier: notifier,
	}
}

// WideEnv es el fixture público con la ventana ancha 09:00-19:00 (corte de 45,
// paso 30, dos profesionales).
func WideEnv(t *testing.T) *PublicEnv {
	t.Helper()
	env := NewPublicEnv(t)
	env.Settings.BySalon["s1"] = domain.SalonSettings{
		SalonID: "s1", Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}
	return env
}

// PublicInput es una reserva pública válida del fixture (09:00 local del 1/12).
func PublicInput() bkg.PublicBookingInput {
	return bkg.PublicBookingInput{
		ServiceID:   "",
		ClientName:  "Lía",
		ClientPhone: "555",
		StartsAt:    time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	}
}

// SlotsOf son las horas de inicio (etiqueta local) de los huecos de una
// profesional.
func SlotsOf(t *testing.T, slots []bkg.Slot, professionalID string) []string {
	t.Helper()
	out := []string{}
	for _, s := range slots {
		if s.ProfessionalID == professionalID {
			out = append(out, testutil.LabelOf(t, s.StartsAt))
		}
	}
	return out
}
