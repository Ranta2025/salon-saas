package app

import (
	"errors"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// agendaEnv: agenda de staff (calendario) + equipamiento, para probar la
// reprogramación y la reasignación.
type agendaEnv struct {
	svc      bkg.BookingService
	bookings *testutil.FakeBookings
	res      *testutil.FakeResources
	services *testutil.FakeServices
	mems     *testutil.FakeMemberships
	owner    domain.Membership
	ana      domain.Membership
	bea      domain.Membership
	corteID  string
	unitA    string
	unitB    string
}

func newAgendaEnv(t *testing.T) *agendaEnv {
	t.Helper()
	mems := &testutil.FakeMemberships{}
	bookings := &testutil.FakeBookings{}
	services := &testutil.FakeServices{}
	res := &testutil.FakeResources{Bookings: bookings}

	corte, err := domain.NewService("s1", "Corte con puesto", 60, 100000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	rt, err := domain.NewResourceType("s1", "Puesto")
	if err != nil {
		t.Fatalf("tipo: %v", err)
	}
	units := make([]domain.ResourceUnit, 0, 2)
	for i := 1; i <= 2; i++ {
		u, _ := domain.NewResourceUnit("s1", rt.ID, rt.UnitName(i))
		units = append(units, u)
	}
	if err := res.CreateType(rt, units); err != nil {
		t.Fatalf("equipo: %v", err)
	}
	corte.ResourceTypeID = rt.ID
	_ = services.Save(corte)

	return &agendaEnv{
		svc: bkg.BookingService{
			Bookings: bookings, Memberships: mems, Services: services, Resources: res,
		},
		bookings: bookings, res: res, services: services, mems: mems,
		owner:   testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner),
		ana:     testutil.Miembro(mems, "ana", "s1", domain.RoleProfessional),
		bea:     testutil.Miembro(mems, "bea", "s1", domain.RoleProfessional),
		corteID: corte.ID, unitA: units[0].ID, unitB: units[1].ID,
	}
}

// seedUnidad deja un turno con unidad y servicio (los que ocupan equipo).
func (e *agendaEnv) seedUnidad(t *testing.T, professionalID, unitID string, start, end time.Time) domain.Booking {
	t.Helper()
	b, err := domain.NewBooking("s1", professionalID, "Clienta", "", start, end)
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	b.ServiceID = e.corteID
	b.ResourceUnitID = unitID
	if err := e.bookings.Save(b); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}
	return b
}

// S-F2e-4: reprogramar un turno con unidad conserva su unidad si está libre en
// el horario nuevo; si no, toma otra libre; si no hay ninguna, 409.
func TestBooking_reschedule_con_unidad(t *testing.T) {
	env := newAgendaEnv(t)
	day := testutil.At(1, 0, 0)
	b := env.seedUnidad(t, "ana", env.unitA, day.Add(10*time.Hour), day.Add(11*time.Hour))

	// Mover a las 12: la unidad A sigue libre -> se conserva.
	start, end := day.Add(12*time.Hour), day.Add(13*time.Hour)
	moved, err := env.svc.Update(env.owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &start, EndsAt: &end})
	if err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	if moved.ResourceUnitID != env.unitA {
		t.Fatalf("la unidad libre se conserva: %+v", moved)
	}

	// Otra persona ocupa la unidad A a las 14 -> el turno se mueve a la B.
	env.seedUnidad(t, "bea", env.unitA, day.Add(14*time.Hour), day.Add(15*time.Hour))
	start, end = day.Add(14*time.Hour), day.Add(15*time.Hour)
	moved, err = env.svc.Update(env.owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &start, EndsAt: &end})
	if err != nil {
		t.Fatalf("reprogramar a unidad libre: %v", err)
	}
	if moved.ResourceUnitID != env.unitB {
		t.Fatalf("tiene que tomar la otra unidad libre: %+v", moved)
	}

	// Las dos unidades tomadas a las 16 -> 409 con el mensaje del contrato.
	env.seedUnidad(t, "ana", env.unitA, day.Add(16*time.Hour), day.Add(17*time.Hour))
	env.seedUnidad(t, "bea", env.unitB, day.Add(16*time.Hour), day.Add(17*time.Hour))
	start, end = day.Add(16*time.Hour), day.Add(17*time.Hour)
	if _, err := env.svc.Update(env.owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &start, EndsAt: &end}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("sin equipo libre es 409: %v", err)
	}
}

// S-F2e-4: cancelar libera la unidad (la EXCLUDE solo mira las reservadas).
func TestBooking_cancelar_libera_unidad(t *testing.T) {
	env := newAgendaEnv(t)
	day := testutil.At(1, 0, 0)
	b := env.seedUnidad(t, "ana", env.unitA, day.Add(10*time.Hour), day.Add(11*time.Hour))

	busy, err := env.res.OccupiedUnits("s1", day.Add(9*time.Hour), day.Add(12*time.Hour), "")
	if err != nil || len(busy) != 1 {
		t.Fatalf("antes de cancelar la unidad está ocupada: %+v %v", busy, err)
	}
	cancelled := domain.BookingCancelled
	if _, err := env.svc.Update(env.owner, "s1", b.ID, bkg.BookingPatch{Status: &cancelled}); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	busy, err = env.res.OccupiedUnits("s1", day.Add(9*time.Hour), day.Add(12*time.Hour), "")
	if err != nil || len(busy) != 0 {
		t.Fatalf("cancelar libera la unidad: %+v %v", busy, err)
	}
}

// PRESERVE: un turno del calendario SIN servicio no ocupa unidad y se reprograma
// como siempre, aunque todo el equipo esté tomado.
func TestBooking_sin_unidad_no_necesita_equipo(t *testing.T) {
	env := newAgendaEnv(t)
	day := testutil.At(1, 0, 0)
	env.seedUnidad(t, "bea", env.unitA, day.Add(14*time.Hour), day.Add(15*time.Hour))
	env.seedUnidad(t, "bea", env.unitB, day.Add(15*time.Hour), day.Add(16*time.Hour))

	b, err := domain.NewBooking("s1", "ana", "Clienta", "", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	if err := env.bookings.Save(b); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	start, end := day.Add(14*time.Hour), day.Add(15*time.Hour)
	moved, err := env.svc.Update(env.owner, "s1", b.ID, bkg.BookingPatch{StartsAt: &start, EndsAt: &end})
	if err != nil {
		t.Fatalf("un turno sin servicio no ocupa equipo: %v", err)
	}
	if moved.ResourceUnitID != "" {
		t.Fatalf("no se le inventa una unidad: %+v", moved)
	}
}
