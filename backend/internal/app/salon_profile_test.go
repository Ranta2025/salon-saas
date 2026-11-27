package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/staffing"
)

func strp(v string) *string { return &v }

type profileWorld struct {
	svc      SalonProfileService
	profiles *fakeSalonProfiles
	mems     *fakeMemberships
}

func newProfileWorld() *profileWorld {
	mems := &fakeMemberships{}
	for _, m := range []domain.Membership{
		{UserID: "owner", SalonID: "s1", Role: domain.RoleOwner},
		{UserID: "admin", SalonID: "s1", Role: domain.RoleAdmin},
		{UserID: "recep", SalonID: "s1", Role: domain.RoleReception},
		{UserID: "prof", SalonID: "s1", Role: domain.RoleProfessional},
		{UserID: "client", SalonID: "s1", Role: domain.RoleClient},
		{UserID: "owner2", SalonID: "s2", Role: domain.RoleOwner},
	} {
		_ = mems.Save(m)
	}
	// Una baja (se marca en el test que la usa): no administra ni lee.
	_ = mems.Save(domain.Membership{UserID: "gone", SalonID: "s1", Role: domain.RoleOwner})
	profiles := &fakeSalonProfiles{rows: map[string]domain.SalonProfile{
		"s1": {SalonID: "s1", Slug: "uno", Name: "Uno", Description: "Vieja"},
		"s2": {SalonID: "s2", Slug: "dos", Name: "Dos"},
	}}
	return &profileWorld{svc: SalonProfileService{Profiles: profiles, Memberships: mems}, profiles: profiles, mems: mems}
}

func profActor(w *profileWorld, user, salon string) domain.Membership {
	return domain.Membership{UserID: user, SalonID: salon}
}

func okInput() domain.SalonProfileInput {
	return domain.SalonProfileInput{Name: "Uno Nuevo", Description: strp("Nueva"), Address: strp("Calle 1"),
		Phone: strp("555"), Email: strp("a@b.com"), Instagram: strp("uno")}
}

// D1: GET lo lee cualquier miembro activo; quien no es miembro (o es de otro
// salón) recibe 403 y nada se filtra.
func TestSalonProfile_Get_anyActiveMember(t *testing.T) {
	w := newProfileWorld()
	for _, u := range []string{"owner", "admin", "recep", "prof", "client"} {
		p, err := w.svc.Get(profActor(w, u, "s1"), "s1")
		if err != nil || p.Name != "Uno" || p.Slug != "uno" || p.Description != "Vieja" {
			t.Fatalf("%s: %+v %v", u, p, err)
		}
	}
	for _, c := range []struct{ user, salon string }{{"owner2", "s1"}, {"nadie", "s1"}} {
		if _, err := w.svc.Get(profActor(w, c.user, c.salon), "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%v: %v", c, err)
		}
	}
	// El actor dice ser de s1 pero la ruta es de s2: sin acceso.
	if _, err := w.svc.Get(profActor(w, "owner", "s1"), "s2"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón: %v", err)
	}
}

// D1: solo owner/admin editan; el resto recibe 403 y NO se escribe nada.
func TestSalonProfile_Update_permissions(t *testing.T) {
	w := newProfileWorld()
	for _, u := range []string{"recep", "prof", "client", "owner2", "nadie"} {
		salon := "s1"
		if u == "owner2" {
			salon = "s2"
		}
		_, err := w.svc.Update(profActor(w, u, salon), "s1", okInput())
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s: %v", u, err)
		}
		if err := w.svc.RequireManager(profActor(w, u, salon), "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("RequireManager %s: %v", u, err)
		}
	}
	if w.profiles.saves != 0 || w.profiles.rows["s1"].Name != "Uno" {
		t.Fatalf("un rechazo no escribe: %+v saves=%d", w.profiles.rows["s1"], w.profiles.saves)
	}
	for _, u := range []string{"owner", "admin"} {
		if err := w.svc.RequireManager(profActor(w, u, "s1"), "s1"); err != nil {
			t.Fatalf("RequireManager %s: %v", u, err)
		}
	}
}

func TestSalonProfile_Update_replacesAndKeepsSlug(t *testing.T) {
	w := newProfileWorld()
	got, err := w.svc.Update(profActor(w, "admin", "s1"), "s1", okInput())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	want := domain.SalonProfile{SalonID: "s1", Slug: "uno", Name: "Uno Nuevo", Description: "Nueva",
		Address: "Calle 1", Phone: "555", Email: "a@b.com", Instagram: "uno"}
	if got != want || w.profiles.rows["s1"] != want {
		t.Fatalf("perfil: %+v / %+v", got, w.profiles.rows["s1"])
	}
	// Tenant: el otro salón no se tocó.
	if w.profiles.rows["s2"].Name != "Dos" {
		t.Fatalf("s2 cambió: %+v", w.profiles.rows["s2"])
	}
	// null borra el dato (no «conserva el anterior»).
	in := okInput()
	in.Description = nil
	got, err = w.svc.Update(profActor(w, "owner", "s1"), "s1", in)
	if err != nil || got.Description != "" || w.profiles.rows["s1"].Description != "" {
		t.Fatalf("null: %+v %v", got, err)
	}
}

// D1: un dato inválido es 400 y no escribe NADA, ni los campos que sí venían bien.
func TestSalonProfile_Update_invalidWritesNothing(t *testing.T) {
	w := newProfileWorld()
	for name, mut := range map[string]func(*domain.SalonProfileInput){
		"email":  func(i *domain.SalonProfileInput) { i.Email = strp("mal") },
		"nul":    func(i *domain.SalonProfileInput) { i.Address = strp("a\x00b") },
		"salto":  func(i *domain.SalonProfileInput) { i.Description = strp("a\nb") },
		"nombre": func(i *domain.SalonProfileInput) { i.Name = " " },
	} {
		in := okInput()
		mut(&in)
		if _, err := w.svc.Update(profActor(w, "owner", "s1"), "s1", in); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if w.profiles.saves != 0 || w.profiles.rows["s1"].Name != "Uno" || w.profiles.rows["s1"].Description != "Vieja" {
		t.Fatalf("un 400 no escribe: %+v saves=%d", w.profiles.rows["s1"], w.profiles.saves)
	}
}

func TestSalonProfile_endedMemberLosesAccess(t *testing.T) {
	w := newProfileWorld()
	m := w.mems.byKey["gone|s1"]
	ended := time.Now().Add(-time.Hour)
	m.EndedAt = &ended
	w.mems.byKey["gone|s1"] = m
	if _, err := w.svc.Get(profActor(w, "gone", "s1"), "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("get: %v", err)
	}
	if _, err := w.svc.Update(profActor(w, "gone", "s1"), "s1", okInput()); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("update: %v", err)
	}
}

// ---- D2: descripción de servicio ---------------------------------------

func newServiceWorld() (staffing.ServiceService, *fakeServices, domain.Membership) {
	mems := &fakeMemberships{}
	_ = mems.Save(domain.Membership{UserID: "owner", SalonID: "s1", Role: domain.RoleOwner})
	_ = mems.Save(domain.Membership{UserID: "recep", SalonID: "s1", Role: domain.RoleReception})
	services := &fakeServices{}
	return staffing.ServiceService{Services: services, Memberships: mems}, services, domain.Membership{UserID: "owner", SalonID: "s1"}
}

func TestServiceService_CreateDescribed(t *testing.T) {
	svc, store, owner := newServiceWorld()
	got, err := svc.CreateDescribed(owner, "s1", "Corte", 30, 1000, 10, nil, "", "  Corte clásico ")
	if err != nil || got.Description != "Corte clásico" || store.all[0].Description != "Corte clásico" {
		t.Fatalf("alta: %+v %v", got, err)
	}
	// PRESERVE: el alta de siempre sigue sin descripción.
	plain, err := svc.Create(owner, "s1", "Barba", 20, 500, 0, nil, "")
	if err != nil || plain.Description != "" {
		t.Fatalf("Create sin descripción: %+v %v", plain, err)
	}
	// Inválida: 400 y no se guarda el servicio.
	before := len(store.all)
	for _, bad := range []string{strings.Repeat("a", 301), "a\nb", "a\x00b"} {
		if _, err := svc.CreateDescribed(owner, "s1", "Tinte", 30, 1000, 10, nil, "", bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if len(store.all) != before {
		t.Fatalf("un 400 no crea el servicio")
	}
	if _, err := svc.CreateDescribed(domain.Membership{UserID: "recep", SalonID: "s1"}, "s1", "X", 30, 1, 0, nil, "", "d"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción: %v", err)
	}
}

func TestServiceService_UpdateDescribed(t *testing.T) {
	svc, store, owner := newServiceWorld()
	created, _ := svc.CreateDescribed(owner, "s1", "Corte", 30, 1000, 10, nil, "", "Original")
	// Ausente (nil) conserva la descripción: PUT de un cliente anterior a F16.
	got, err := svc.Update(owner, "s1", created.ID, "Corte", 30, 1200, 10, true, "")
	if err != nil || got.Description != "Original" || store.all[0].Description != "Original" || store.all[0].PriceCents != 1200 {
		t.Fatalf("Update legacy conserva: %+v %v", got, err)
	}
	got, err = svc.UpdateDescribed(owner, "s1", created.ID, "Corte", 30, 1200, 10, true, "", nil)
	if err != nil || got.Description != "Original" {
		t.Fatalf("nil conserva: %+v %v", got, err)
	}
	got, err = svc.UpdateDescribed(owner, "s1", created.ID, "Corte", 30, 1200, 10, true, "", strp("Nueva"))
	if err != nil || got.Description != "Nueva" || store.all[0].Description != "Nueva" {
		t.Fatalf("reemplaza: %+v %v", got, err)
	}
	got, err = svc.UpdateDescribed(owner, "s1", created.ID, "Corte", 30, 1200, 10, true, "", strp(""))
	if err != nil || got.Description != "" || store.all[0].Description != "" {
		t.Fatalf("vacía quita: %+v %v", got, err)
	}
	store.all[0].Description = "Queda"
	if _, err := svc.UpdateDescribed(owner, "s1", created.ID, "Otro", 30, 1, 0, true, "", strp(strings.Repeat("a", 301))); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("larga: %v", err)
	}
	if store.all[0].Description != "Queda" || store.all[0].Name != "Corte" {
		t.Fatalf("un 400 no escribe nada: %+v", store.all[0])
	}
}
