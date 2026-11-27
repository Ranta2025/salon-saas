package settings

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

type publicWorld struct {
	svc      PublicSalonService
	salons   *testutil.FakeSalons
	services *testutil.FakeServices
	profiles *fakeSalonProfiles
	staff    *testutil.FakeStaffLister
	profs    *testutil.FakeProfessionalServices
	gallery  *fakeGallery
	packages *testutil.FakePackageCatalog
	settings *testutil.FakeSettings
}

func addService(t *testing.T, store *testutil.FakeServices, salon, name string, price int, active bool, description string) domain.Service {
	t.Helper()
	s, err := domain.NewService(salon, name, 45, price, 10)
	if err != nil {
		t.Fatal(err)
	}
	s.Active = active
	s.Description = description
	_ = store.Save(s)
	return s
}

// newPublicWorld: un salón con tres personas (profesional, dueño que atiende,
// recepción) y servicios; la asignación de servicios imita la base real (solo lo
// que se graba).
func newPublicWorld() *publicWorld {
	salons := &testutil.FakeSalons{}
	_ = salons.Save(domain.Salon{ID: "s1", Slug: "uno", Name: "Uno"})
	_ = salons.Save(domain.Salon{ID: "s2", Slug: "dos", Name: "Dos"})
	staff := &testutil.FakeStaffLister{BySalon: map[string][]domain.StaffMember{
		"s1": {
			{ID: "u-ana", Name: "Ana", Role: domain.RoleProfessional, Attends: true, Trade: "Estilista"},
			{ID: "u-own", Name: "Bruno", Role: domain.RoleOwner, Attends: true, Trade: ""},
		},
		"s2": {{ID: "u-otra", Name: "Otra", Role: domain.RoleProfessional, Attends: true, Trade: "Barbera"}},
	}}
	services := &testutil.FakeServices{}
	profs := &testutil.FakeProfessionalServices{Staff: staff}
	profiles := &fakeSalonProfiles{rows: map[string]domain.SalonProfile{
		"s1": {SalonID: "s1", Slug: "uno", Name: "Uno", Description: "Somos Uno", Address: "Calle 1", Phone: "555", Email: "a@b.com", Instagram: "uno"},
		"s2": {SalonID: "s2", Slug: "dos", Name: "Dos", Description: "Secreto de Dos"},
	}}
	gallery := &fakeGallery{slugs: map[string]string{"s1": "uno", "s2": "dos"}}
	packages := &testutil.FakePackageCatalog{Services: services}
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{
		"s1": {SalonID: "s1", Timezone: "America/Argentina/Buenos_Aires", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30},
	}}
	return &publicWorld{
		svc: PublicSalonService{
			Salons: salons, Services: services, Settings: settings, Hours: &testutil.FakeWeeklyHours{},
			Profiles: profiles, Staff: staff, Professionals: profs, Gallery: gallery, Packages: packages,
		},
		salons: salons, services: services, profiles: profiles, staff: staff, profs: profs,
		gallery: gallery, packages: packages, settings: settings,
	}
}

// D4: el salón trae el perfil completo y la zona horaria.
func TestPublicSalon_profileAndTimezone(t *testing.T) {
	w := newPublicWorld()
	p, err := w.svc.Public("uno")
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	want := domain.SalonProfile{SalonID: "s1", Slug: "uno", Name: "Uno", Description: "Somos Uno", Address: "Calle 1", Phone: "555", Email: "a@b.com", Instagram: "uno"}
	if p.Profile != want || p.Timezone != "America/Argentina/Buenos_Aires" || p.Salon.ID != "s1" {
		t.Fatalf("perfil: %+v tz=%q", p.Profile, p.Timezone)
	}
	// Sin settings guardados rige la zona por defecto del dominio.
	w.salons.BySlug["dos"] = domain.Salon{ID: "s2", Slug: "dos", Name: "Dos"}
	p2, err := w.svc.Public("dos")
	if err != nil || p2.Timezone != domain.DefaultSalonSettings("s2").Timezone || p2.Profile.Description != "Secreto de Dos" {
		t.Fatalf("s2: %+v %v", p2, err)
	}
}

// D4: la carta son SOLO los servicios activos que al menos una profesional
// activa y reservable hace (la misma regla de la disponibilidad).
func TestPublicSalon_servicesFilter(t *testing.T) {
	w := newPublicWorld()
	corte := addService(t, w.services, "s1", "Corte", 1500, true, "Corte clásico")
	tinte := addService(t, w.services, "s1", "Tinte", 5000, true, "")
	nadie := addService(t, w.services, "s1", "Nadie lo hace", 900, true, "")
	viejo := addService(t, w.services, "s1", "Viejo", 100, false, "")
	soloRecep := addService(t, w.services, "s1", "Solo recepción", 100, true, "")
	otroSalon := addService(t, w.services, "s2", "Del otro salón", 100, true, "")
	// Se graba la asignación: Corte -> Ana; Tinte -> el dueño que atiende;
	// Viejo -> Ana pero está inactivo; «Solo recepción» -> alguien que no atiende;
	// «Nadie lo hace» -> una lista vacía.
	_ = w.profs.SetServices("s1", "u-ana", []string{corte.ID, viejo.ID})
	_ = w.profs.SetServices("s1", "u-own", []string{tinte.ID})
	// «Nadie lo hace»: grabado con lista vacía (en la base real, sin filas).
	w.profs.ByService = map[string][]string{"s1|" + nadie.ID: {}}
	_ = otroSalon
	// «Solo recepción»: la única asignada es recepción, que no atiende.
	w.staff.BySalon["s1"] = append(w.staff.BySalon["s1"], domain.StaffMember{ID: "u-rec", Name: "Rita", Role: domain.RoleReception, Attends: true})
	_ = w.profs.SetServices("s1", "u-rec", []string{soloRecep.ID})

	p, err := w.svc.Public("uno")
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	names := []string{}
	for _, s := range p.Services {
		names = append(names, s.Name)
	}
	if len(names) != 2 || names[0] != "Corte" || names[1] != "Tinte" {
		t.Fatalf("la carta debe tener solo lo que alguien hace: %v", names)
	}
	if p.Services[0].Description != "Corte clásico" || p.Services[0].PriceCents != 1500 {
		t.Fatalf("la descripción y el precio viajan: %+v", p.Services[0])
	}
}

// D4: si la única profesional que lo hace deja de atender, el servicio sale.
func TestPublicSalon_servicesFilter_followsStaffChanges(t *testing.T) {
	w := newPublicWorld()
	corte := addService(t, w.services, "s1", "Corte", 1500, true, "")
	_ = w.profs.SetServices("s1", "u-ana", []string{corte.ID})
	p, _ := w.svc.Public("uno")
	if len(p.Services) != 1 {
		t.Fatalf("antes: %+v", p.Services)
	}
	w.staff.BySalon["s1"] = w.staff.BySalon["s1"][1:] // Ana ya no está
	p, _ = w.svc.Public("uno")
	if len(p.Services) != 0 || p.Services == nil {
		t.Fatalf("sin nadie que lo haga no se lista (y la lista no es nil): %+v", p.Services)
	}
}

// D4: el equipo publica SOLO nombre y oficio de quien atiende.
func TestPublicSalon_team(t *testing.T) {
	w := newPublicWorld()
	p, err := w.svc.Public("uno")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Team) != 2 || p.Team[0].Name != "Ana" || p.Team[0].Trade != "Estilista" || p.Team[1].Name != "Bruno" {
		t.Fatalf("equipo: %+v", p.Team)
	}
	// Quien no atiende (recepción, dueño sin marca) no aparece, aunque el lister
	// lo entregue.
	w.staff.BySalon["s1"] = append(w.staff.BySalon["s1"],
		domain.StaffMember{ID: "u-rec", Name: "Rita", Role: domain.RoleReception, Attends: true},
		domain.StaffMember{ID: "u-adm", Name: "Zoe", Role: domain.RoleAdmin, Attends: false})
	p, _ = w.svc.Public("uno")
	if len(p.Team) != 2 {
		t.Fatalf("solo quienes atienden: %+v", p.Team)
	}
	// Tenant: el equipo del otro salón no se mezcla.
	for _, m := range p.Team {
		if m.Name == "Otra" {
			t.Fatalf("equipo de otro salón: %+v", p.Team)
		}
	}
}

// D4: la galería trae la URL pública y en orden de subida; la de otro salón no entra.
func TestPublicSalon_gallery(t *testing.T) {
	w := newPublicWorld()
	now := time.Now()
	w.gallery.All = []domain.SalonPhoto{
		{ID: "f2", SalonID: "s1", Caption: "Segunda", CreatedAt: now.Add(time.Minute)},
		{ID: "f1", SalonID: "s1", Caption: "", CreatedAt: now},
		{ID: "fx", SalonID: "s2", CreatedAt: now},
	}
	p, err := w.svc.Public("uno")
	if err != nil || len(p.Gallery) != 2 {
		t.Fatalf("galería: %+v %v", p.Gallery, err)
	}
	if p.Gallery[0].ID != "f1" || p.Gallery[0].URL != "/api/v1/public/salons/uno/gallery/f1" || p.Gallery[1].Caption != "Segunda" {
		t.Fatalf("galería: %+v", p.Gallery)
	}
}

// D4: paquetes ACTIVOS cuyo servicio está activo; inactivos y de servicio
// inactivo quedan fuera.
func TestPublicSalon_packages(t *testing.T) {
	w := newPublicWorld()
	corte := addService(t, w.services, "s1", "Corte", 1500, true, "")
	viejo := addService(t, w.services, "s1", "Viejo", 100, false, "")
	_ = w.profs.SetServices("s1", "u-ana", []string{corte.ID})
	ten := 30
	mk := func(name, svc string, active bool, validity *int) {
		p, err := domain.NewServicePackage("s1", name, svc, 5, 6000, validity)
		if err != nil {
			t.Fatal(err)
		}
		p.Active = active
		_ = w.packages.Create(p)
	}
	mk("Pack 5 cortes", corte.ID, true, &ten)
	mk("Pack sin vencer", corte.ID, true, nil)
	mk("Pack apagado", corte.ID, false, nil)
	mk("Pack de servicio viejo", viejo.ID, true, nil)
	other, _ := domain.NewServicePackage("s2", "Pack ajeno", "x", 3, 1, nil)
	_ = w.packages.Create(other)

	p, err := w.svc.Public("uno")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Packages) != 2 || p.Packages[0].Name != "Pack 5 cortes" || p.Packages[1].Name != "Pack sin vencer" {
		t.Fatalf("paquetes: %+v", p.Packages)
	}
	first := p.Packages[0]
	if first.ServiceName != "Corte" || first.Sessions != 5 || first.PriceCents != 6000 || first.ValidityDays == nil || *first.ValidityDays != 30 || p.Packages[1].ValidityDays != nil {
		t.Fatalf("paquete: %+v", first)
	}
}

// Sin los repos opcionales el payload sale con las secciones vacías y la carta
// sin filtrar (el comportamiento anterior a F16): nunca un panic.
func TestPublicSalon_optionalDepsNil(t *testing.T) {
	salons := &testutil.FakeSalons{}
	_ = salons.Save(domain.Salon{ID: "s1", Slug: "uno", Name: "Uno"})
	services := &testutil.FakeServices{}
	addService(t, services, "s1", "Corte", 1, true, "")
	svc := PublicSalonService{Salons: salons, Services: services}
	p, err := svc.Public("uno")
	if err != nil || len(p.Services) != 1 || len(p.Team) != 0 || len(p.Gallery) != 0 || len(p.Packages) != 0 || p.Profile.Description != "" {
		t.Fatalf("payload: %+v %v", p, err)
	}
	if _, err := svc.Public("no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("slug inexistente: %v", err)
	}
}
