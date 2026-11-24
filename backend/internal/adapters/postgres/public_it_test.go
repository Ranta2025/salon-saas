// Integración con Postgres real de la vidriera pública (F16, D2 y D4). Se
// saltea sin SALON_IT=1: la descripción del servicio, el filtro de la carta por
// profesional ACTIVA y reservable (la regla de la disponibilidad, con SQL real),
// el equipo, la galería y los paquetes de la vidriera.
package pgstore

import (
	"context"
	"testing"
	"time"

	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

func TestServices_descriptionRoundTrip(t *testing.T) {
	pool := f16Fixtures(t)
	repo := Services{Pool: pool}
	svc, err := domain.NewService("s-it-f16", "Corte F16", 30, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Sin descripción: NULL en la DB, "" en el dominio.
	if err := repo.Save(svc); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByID("s-it-f16", svc.ID)
	if err != nil || got.Description != "" {
		t.Fatalf("sin descripción: %+v %v", got, err)
	}
	var isNull bool
	if err := pool.QueryRow(context.Background(), `SELECT description IS NULL FROM services WHERE id=$1`, svc.ID).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("debe ser NULL: %v %v", isNull, err)
	}

	svc, _ = svc.WithDescription("Corte clásico a tijera")
	if err := repo.Save(svc); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.FindByID("s-it-f16", svc.ID)
	if got.Description != "Corte clásico a tijera" {
		t.Fatalf("descripción: %+v", got)
	}
	list, _ := repo.List("s-it-f16", false)
	if len(list) != 1 || list[0].Description != "Corte clásico a tijera" {
		t.Fatalf("List: %+v", list)
	}
	// PRESERVE: editar otros campos con el servicio leído conserva la descripción.
	got, _ = got.WithDetails("Corte F16", 30, 2000, 10, true)
	if err := repo.Save(got); err != nil {
		t.Fatal(err)
	}
	again, _ := repo.FindByID("s-it-f16", svc.ID)
	if again.Description != "Corte clásico a tijera" || again.PriceCents != 2000 {
		t.Fatalf("PRESERVE: %+v", again)
	}
	// El CHECK de la migración es la última barrera: 400, no 500.
	svc.Description = "a\nb"
	if err := repo.Save(svc); err == nil {
		t.Fatal("el salto de línea debe rechazarlo la DB")
	}
}

// La vidriera completa con repos reales.
func TestPublicSalon_realRepos(t *testing.T) {
	pool := f16Fixtures(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-f16-rec','Rita F16','f16rec@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-f16-zoe','Zoe F16','f16zoe@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-f16-otra','Otra F16','f16otra@test.com','x')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	mems := Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-f16-ana", SalonID: "s-it-f16", Role: domain.RoleProfessional, Attends: true, Trade: "Estilista"},
		{UserID: "u-it-f16-bea", SalonID: "s-it-f16", Role: domain.RoleOwner, Attends: true},
		{UserID: "u-it-f16-rec", SalonID: "s-it-f16", Role: domain.RoleReception, Attends: true},
		{UserID: "u-it-f16-zoe", SalonID: "s-it-f16", Role: domain.RoleAdmin, Attends: false},
		{UserID: "u-it-f16-otra", SalonID: "s-it-f162", Role: domain.RoleProfessional, Attends: true, Trade: "Secreta"},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	if err := mems.SetTrade("u-it-f16-ana", "s-it-f16", "Estilista"); err != nil {
		t.Fatal(err)
	}

	services := Services{Pool: pool}
	mk := func(salon, name string, active bool, description string) domain.Service {
		s, _ := domain.NewService(salon, name, 45, 15000, 10)
		s, _ = s.WithDescription(description)
		s.Active = active
		if err := services.Save(s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	zeta := mk("s-it-f16", "Zeta (de Ana)", true, "Última por nombre")
	alfa := mk("s-it-f16", "Alfa (de la dueña)", true, "")
	sinNadie := mk("s-it-f16", "Sin nadie", true, "")
	soloRec := mk("s-it-f16", "Solo recepción", true, "")
	soloBaja := mk("s-it-f16", "Solo una baja", true, "")
	inactivo := mk("s-it-f16", "Inactivo", false, "")
	ajeno := mk("s-it-f162", "Del otro salón", true, "")

	ps := ProfessionalServices{Pool: pool}
	for user, ids := range map[string][]string{
		"u-it-f16-ana": {zeta.ID, inactivo.ID},
		"u-it-f16-bea": {alfa.ID},
		"u-it-f16-rec": {soloRec.ID},
		"u-it-f16-zoe": {sinNadie.ID},
	} {
		if err := ps.SetServices("s-it-f16", user, ids); err != nil {
			t.Fatalf("asignar %s: %v", user, err)
		}
	}
	_ = ajeno
	// «Solo una baja»: lo hace Cata, que ya no está en el equipo.
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-f16-cata','Cata F16','f16cata@test.com','x')`); err != nil {
		t.Fatal(err)
	}
	if err := mems.Save(domain.Membership{UserID: "u-it-f16-cata", SalonID: "s-it-f16", Role: domain.RoleProfessional, Attends: true}); err != nil {
		t.Fatal(err)
	}
	if err := ps.SetServices("s-it-f16", "u-it-f16-cata", []string{soloBaja.ID}); err != nil {
		t.Fatal(err)
	}
	if err := mems.DeactivateMember("u-it-f16-cata", "s-it-f16", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Galería y paquetes.
	gallery := SalonGallery{Pool: pool}
	photo := newPhoto(t, "s-it-f16")
	if err := gallery.Add(photo, 12); err != nil {
		t.Fatal(err)
	}
	if err := gallery.SetCaption("s-it-f16", photo.ID, "El local"); err != nil {
		t.Fatal(err)
	}
	if err := gallery.Add(newPhoto(t, "s-it-f162"), 12); err != nil {
		t.Fatal(err)
	}
	catalog := Packages{Pool: pool}
	ten := 90
	mkPkg := func(name, svc string, active bool, validity *int) {
		p, err := domain.NewServicePackage("s-it-f16", name, svc, 5, 60000, validity)
		if err != nil {
			t.Fatal(err)
		}
		p.Active = active
		if err := catalog.Create(p); err != nil {
			t.Fatalf("paquete %s: %v", name, err)
		}
	}
	mkPkg("Pack 5 Zeta", zeta.ID, true, &ten)
	mkPkg("Pack apagado", zeta.ID, false, nil)
	mkPkg("Pack de servicio inactivo", inactivo.ID, true, nil)

	salons := Salons{Pool: pool}
	profiles := SalonProfiles{Pool: pool}
	if err := profiles.SaveProfile(domain.SalonProfile{SalonID: "s-it-f16", Name: "Salón F16", Description: "Somos F16", Phone: "555", Instagram: "f16"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE salons SET timezone='Pacific/Auckland' WHERE id='s-it-f16'`); err != nil {
		t.Fatal(err)
	}
	pub := app.PublicSalonService{
		Salons: salons, Services: services, Settings: salons, Hours: WeeklyHours{Pool: pool},
		Profiles: profiles, Staff: mems, Professionals: ps, Gallery: gallery, Packages: catalog,
	}
	got, err := pub.Public("s-it-f16")
	if err != nil {
		t.Fatalf("vidriera: %v", err)
	}

	// Carta: activos con alguien activo y reservable que los hace, por nombre.
	var names []string
	for _, s := range got.Services {
		names = append(names, s.Name)
	}
	if len(names) != 2 || names[0] != "Alfa (de la dueña)" || names[1] != "Zeta (de Ana)" || got.Services[1].Description != "Última por nombre" {
		t.Fatalf("carta: %v", names)
	}
	// Equipo: solo quienes atienden (Ana y la dueña que atiende); recepción y la
	// admin sin la marca no. Ordenado por nombre.
	if len(got.Team) != 2 || got.Team[0].Name != "Ana F16" || got.Team[0].Trade != "Estilista" || got.Team[1].Name != "Bea F16" {
		t.Fatalf("equipo: %+v", got.Team)
	}
	if got.Profile.Description != "Somos F16" || got.Profile.Phone != "555" || got.Profile.Instagram != "f16" || got.Profile.Email != "" || got.Timezone != "Pacific/Auckland" {
		t.Fatalf("perfil: %+v tz=%s", got.Profile, got.Timezone)
	}
	if len(got.Gallery) != 1 || got.Gallery[0].ID != photo.ID || got.Gallery[0].Caption != "El local" || got.Gallery[0].URL != "/api/v1/public/salons/s-it-f16/gallery/"+photo.ID {
		t.Fatalf("galería: %+v", got.Gallery)
	}
	if len(got.Packages) != 1 || got.Packages[0].Name != "Pack 5 Zeta" || got.Packages[0].ServiceName != "Zeta (de Ana)" || got.Packages[0].ValidityDays == nil || *got.Packages[0].ValidityDays != 90 {
		t.Fatalf("paquetes: %+v", got.Packages)
	}

	// Dar de baja a Ana saca su servicio de la carta (lo hace nadie más).
	if err := mems.DeactivateMember("u-it-f16-ana", "s-it-f16", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = pub.Public("s-it-f16")
	if len(got.Services) != 1 || got.Services[0].Name != "Alfa (de la dueña)" || len(got.Team) != 1 {
		t.Fatalf("tras la baja: %+v team=%+v", got.Services, got.Team)
	}
}
