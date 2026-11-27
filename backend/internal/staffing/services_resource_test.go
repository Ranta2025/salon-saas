package staffing

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// S-F2e-3: el servicio indica qué tipo de equipo usa. Un tipo de OTRO salón es
// 400 con el mensaje exacto; sin tipo, el servicio sigue como antes.
func TestServiceService_resource_type(t *testing.T) {
	mems := &testutil.FakeMemberships{}
	services := &testutil.FakeServices{}
	bookings := &testutil.FakeBookings{}
	res := &fakeResources{bookings: bookings}
	svc := ServiceService{Services: services, Memberships: mems, Resources: res}
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)

	rt, err := domain.NewResourceType("s1", "Puesto")
	if err != nil {
		t.Fatalf("tipo: %v", err)
	}
	unit, _ := domain.NewResourceUnit("s1", rt.ID, "Puesto 1")
	if err := res.CreateType(rt, []domain.ResourceUnit{unit}); err != nil {
		t.Fatalf("equipo: %v", err)
	}

	got, err := svc.Create(owner, "s1", "Corte", 30, 100000, 20, nil, rt.ID)
	if err != nil {
		t.Fatalf("alta con equipo: %v", err)
	}
	if got.ResourceTypeID != rt.ID {
		t.Fatalf("el servicio guarda su equipo: %+v", got)
	}
	list, _ := services.List("s1", false)
	if len(list) != 1 || list[0].ResourceTypeID != rt.ID {
		t.Fatalf("el catálogo devuelve el equipo: %+v", list)
	}

	// Otro salón tiene su propio equipo: no se puede usar desde acá.
	otro, _ := domain.NewResourceType("s2", "Silla")
	if err := res.CreateType(otro, nil); err != nil {
		t.Fatalf("equipo de s2: %v", err)
	}
	if _, err := svc.Create(owner, "s1", "Corte ajeno", 30, 100000, 20, nil, otro.ID); err == nil ||
		err.Error() != "Equipo inválido" {
		t.Fatalf("equipo de otro salón: %v", err)
	}
	if _, err := svc.Create(owner, "s1", "Inventado", 30, 100000, 20, nil, "no-existe"); err == nil ||
		err.Error() != "Equipo inválido" {
		t.Fatalf("equipo inexistente: %v", err)
	}

	// PUT: se puede asignar, cambiar y quitar (null/"").
	updated, err := svc.Update(owner, "s1", got.ID, got.Name, got.DurationMin, got.PriceCents, got.CommissionPct, true, rt.ID)
	if err != nil || updated.ResourceTypeID != rt.ID {
		t.Fatalf("update con equipo: %+v %v", updated, err)
	}
	if _, err := svc.Update(owner, "s1", got.ID, got.Name, 30, 100000, 20, true, otro.ID); err == nil ||
		err.Error() != "Equipo inválido" {
		t.Fatalf("update con equipo ajeno: %v", err)
	}
	cleared, err := svc.Update(owner, "s1", got.ID, got.Name, 30, 100000, 20, true, "")
	if err != nil || cleared.ResourceTypeID != "" {
		t.Fatalf("quitar el equipo: %+v %v", cleared, err)
	}
}

// PRESERVE: un servicio sin tipo de equipo se crea y actualiza como antes.
func TestServiceService_sin_equipo_sigue_igual(t *testing.T) {
	mems := &testutil.FakeMemberships{}
	services := &testutil.FakeServices{}
	svc := ServiceService{Services: services, Memberships: mems}
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)

	got, err := svc.Create(owner, "s1", "Corte", 30, 100000, 20, nil, "")
	if err != nil {
		t.Fatalf("alta sin equipo: %v", err)
	}
	if got.ResourceTypeID != "" {
		t.Fatalf("sin equipo no se inventa uno: %+v", got)
	}
	if _, err := svc.Update(owner, "s1", got.ID, got.Name, 45, 120000, 25, true, ""); err != nil {
		t.Fatalf("update sin equipo: %v", err)
	}
	if _, err := svc.Create(owner, "s1", "Peinado", 30, 100000, 20, nil, "cualquiera"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("un equipo inexistente es validación, no un 500: %v", err)
	}
}
