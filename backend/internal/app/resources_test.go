package app

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
)

// resourceEnv: tipos y unidades de equipo con el catálogo y la agenda fake.
type resourceEnv struct {
	res      ResourceService
	repo     *fakeResources
	services *fakeServices
	bookings *fb
	mems     *fakeMemberships
	owner    domain.Membership
}

func newResourceEnv() *resourceEnv {
	mems := &fakeMemberships{}
	bookings := &fb{}
	services := &fakeServices{}
	repo := &fakeResources{bookings: bookings}
	return &resourceEnv{
		res:      ResourceService{Resources: repo, Services: services, Memberships: mems},
		repo:     repo,
		services: services,
		bookings: bookings,
		mems:     mems,
		owner:    miembro(mems, "u-owner", "s1", domain.RoleOwner),
	}
}

// S-F2e-2/S-F2e-3: el alta crea el tipo y sus unidades con nombre automático,
// y el listado las devuelve con cuántos servicios usan el tipo.
func TestResourceService_create_list(t *testing.T) {
	env := newResourceEnv()
	view, err := env.res.Create(env.owner, "s1", "  Puesto de peluquería ", 3)
	if err != nil {
		t.Fatalf("crear tipo: %v", err)
	}
	if view.Type.Name != "Puesto de peluquería" || len(view.Units) != 3 {
		t.Fatalf("tipo con 3 unidades: %+v", view)
	}
	for i, u := range view.Units {
		want := "Puesto de peluquería " + string(rune('1'+i))
		if u.Name != want || !u.Active || u.TypeID != view.Type.ID {
			t.Fatalf("unidad %d: %+v (quería %q)", i, u, want)
		}
	}

	list, err := env.res.List(env.owner, "s1")
	if err != nil || len(list) != 1 {
		t.Fatalf("listar: %+v %v", list, err)
	}
	if len(list[0].Units) != 3 || list[0].Services != 0 {
		t.Fatalf("listado: %+v", list[0])
	}

	// Leer: cualquier miembro. Escribir: solo dueño/administración.
	profe := miembro(env.mems, "u-prof", "s1", domain.RoleProfessional)
	if _, err := env.res.List(profe, "s1"); err != nil {
		t.Fatalf("el equipo lee el equipamiento: %v", err)
	}
	if _, err := env.res.Create(profe, "s1", "Silla", 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional no crea equipo: %v", err)
	}
	ajeno := miembro(env.mems, "u-ajeno", "s2", domain.RoleOwner)
	if _, err := env.res.Create(ajeno, "s1", "Silla", 1); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner de otro salón no crea equipo: %v", err)
	}
}

// S-F2e-3: nombre único por salón sin importar mayúsculas; renombrarse a sí
// mismo (mismas letras, otra caja) sí se puede.
func TestResourceService_nombre_duplicado(t *testing.T) {
	env := newResourceEnv()
	if _, err := env.res.Create(env.owner, "s1", "Silla de barbería", 1); err != nil {
		t.Fatalf("crear: %v", err)
	}
	if _, err := env.res.Create(env.owner, "s1", "  silla de BARBERÍA ", 1); err == nil ||
		err.Error() != "Ya existe un equipo con ese nombre" {
		t.Fatalf("duplicado case-insensitive: %v", err)
	}
	// Mismo nombre en otro salón: no choca (el scope es el salón).
	miembro(env.mems, "u-otro", "s2", domain.RoleOwner)
	other := domain.Membership{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner}
	if _, err := env.res.Create(other, "s2", "Silla de barbería", 1); err != nil {
		t.Fatalf("mismo nombre en otro salón: %v", err)
	}

	list, _ := env.res.List(env.owner, "s1")
	renamed, err := env.res.Rename(env.owner, "s1", list[0].Type.ID, "SILLA DE BARBERÍA")
	if err != nil || renamed.Name != "SILLA DE BARBERÍA" {
		t.Fatalf("renombrar a sí mismo: %+v %v", renamed, err)
	}
	if _, err := env.res.Rename(env.owner, "s1", list[0].Type.ID, "  "); err == nil ||
		err.Error() != "El nombre del equipo es obligatorio" {
		t.Fatalf("renombrar a vacío: %v", err)
	}
}

// S-F2e-2: topes con los mensajes exactos (20 tipos, 50 unidades por tipo) y
// cantidad del alta 1..50.
func TestResourceService_limites(t *testing.T) {
	env := newResourceEnv()
	if _, err := env.res.Create(env.owner, "s1", "Lavacabezas", 0); err == nil ||
		err.Error() != "La cantidad de unidades debe estar entre 1 y 50" {
		t.Fatalf("count 0: %v", err)
	}
	if _, err := env.res.Create(env.owner, "s1", "Lavacabezas", 51); err == nil ||
		err.Error() != "La cantidad de unidades debe estar entre 1 y 50" {
		t.Fatalf("count 51: %v", err)
	}
	for i := 0; i < domain.MaxResourceTypes; i++ {
		if _, err := env.res.Create(env.owner, "s1", "Tipo "+string(rune('a'+i)), 1); err != nil {
			t.Fatalf("tipo %d: %v", i, err)
		}
	}
	if _, err := env.res.Create(env.owner, "s1", "Uno más", 1); err == nil ||
		err.Error() != "Máximo 20 tipos de equipo" {
		t.Fatalf("21º tipo: %v", err)
	}

	list, _ := env.res.List(env.owner, "s1")
	tipo := list[0].Type.ID
	if _, err := env.res.AddUnits(env.owner, "s1", tipo, 49); err != nil {
		t.Fatalf("49 unidades más: %v", err)
	}
	if _, err := env.res.AddUnits(env.owner, "s1", tipo, 1); err == nil ||
		err.Error() != "Máximo 50 unidades por tipo" {
		t.Fatalf("51ª unidad: %v", err)
	}
}

// S-F2e-3: borrar un tipo se rechaza si un servicio lo usa o si alguna unidad
// tiene turnos, con el mensaje exacto; si no lo usa nadie, se borra.
func TestResourceService_borrar_tipo_en_uso(t *testing.T) {
	env := newResourceEnv()
	used, err := env.res.Create(env.owner, "s1", "Puesto", 2)
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	corte, _ := domain.NewService("s1", "Corte", 30, 1000, 0)
	corte.ResourceTypeID = used.Type.ID
	_ = env.services.Save(corte)

	if err := env.res.DeleteType(env.owner, "s1", used.Type.ID); !errors.Is(err, domain.ErrAlreadyExists) ||
		err.Error() != "Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo" {
		t.Fatalf("tipo con servicio: %v", err)
	}

	// Un tipo sin servicios pero con turnos agendados tampoco se borra.
	withBooking, _ := env.res.Create(env.owner, "s1", "Silla", 1)
	b, _ := domain.NewBooking("s1", "prof1", "Ana", "", at(1, 10, 0), at(1, 11, 0))
	b.ResourceUnitID = withBooking.Units[0].ID
	_ = env.bookings.Save(b)
	if err := env.res.DeleteType(env.owner, "s1", withBooking.Type.ID); err == nil ||
		err.Error() != "Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo" {
		t.Fatalf("tipo con turnos: %v", err)
	}

	// Uno libre se borra.
	free, _ := env.res.Create(env.owner, "s1", "Lavacabezas", 1)
	if err := env.res.DeleteType(env.owner, "s1", free.Type.ID); err != nil {
		t.Fatalf("tipo sin uso se borra: %v", err)
	}
	if _, err := env.repo.FindType("s1", free.Type.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el tipo borrado no existe: %v", err)
	}
}

// S-F2e-3: desactivar una unidad la saca de la disponibilidad y la respuesta
// informa cuántos turnos futuros la usan (los conservan).
func TestResourceService_desactivar_unidad(t *testing.T) {
	env := newResourceEnv()
	view, err := env.res.Create(env.owner, "s1", "Puesto", 2)
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	unit := view.Units[0]
	b, _ := domain.NewBooking("s1", "prof1", "Ana", "", at(1, 10, 0), at(1, 11, 0))
	b.ResourceUnitID = unit.ID
	_ = env.bookings.Save(b)

	off := false
	got, err := env.res.UpdateUnit(env.owner, "s1", unit.ID, "", &off)
	if err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	if got.Unit.Active || got.FutureBookings != 1 || got.Unit.Name != unit.Name {
		t.Fatalf("desactivar: %+v", got)
	}
	active, _ := env.repo.ActiveUnits("s1", view.Type.ID)
	if len(active) != 1 || active[0].ID != view.Units[1].ID {
		t.Fatalf("la desactivada no se ofrece: %+v", active)
	}
	// Los turnos ya agendados conservan su unidad.
	stored, _ := env.bookings.FindByID("s1", b.ID)
	if stored.ResourceUnitID != unit.ID {
		t.Fatalf("el turno conserva su unidad: %+v", stored)
	}

	// Renombrar a un nombre que ya usa otra unidad del tipo es duplicado.
	if _, err := env.res.UpdateUnit(env.owner, "s1", view.Units[1].ID, unit.Name, nil); err == nil ||
		err.Error() != "Ya existe un equipo con ese nombre" {
		t.Fatalf("unidad duplicada: %v", err)
	}
}

// S-F2e-3: agregar unidades usa el siguiente número libre (no reusa un nombre).
func TestResourceService_add_units_siguiente_numero(t *testing.T) {
	env := newResourceEnv()
	view, err := env.res.Create(env.owner, "s1", "Puesto", 3)
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	// La 2 se renombra: 2 queda libre.
	if _, err := env.res.UpdateUnit(env.owner, "s1", view.Units[1].ID, "Puesto A", nil); err != nil {
		t.Fatalf("renombrar: %v", err)
	}
	added, err := env.res.AddUnits(env.owner, "s1", view.Type.ID, 2)
	if err != nil {
		t.Fatalf("agregar: %v", err)
	}
	if len(added) != 2 || added[0].Name != "Puesto 2" || added[1].Name != "Puesto 4" {
		t.Fatalf("números libres: %+v", added)
	}
}

// S-F2e-3: el equipamiento de otro salón no existe (404), se lea o se escriba.
func TestResourceService_aislamiento_entre_salones(t *testing.T) {
	env := newResourceEnv()
	other := miembro(env.mems, "u-otro", "s2", domain.RoleOwner)
	ajeno, err := env.res.Create(other, "s2", "Puesto", 2)
	if err != nil {
		t.Fatalf("crear en s2: %v", err)
	}
	if list, _ := env.res.List(env.owner, "s1"); len(list) != 0 {
		t.Fatalf("el equipo de otro salón no se lista: %+v", list)
	}
	if _, err := env.res.Rename(env.owner, "s1", ajeno.Type.ID, "Hack"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("renombrar tipo ajeno: %v", err)
	}
	if _, err := env.res.AddUnits(env.owner, "s1", ajeno.Type.ID, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("agregar unidades a un tipo ajeno: %v", err)
	}
	if _, err := env.res.UpdateUnit(env.owner, "s1", ajeno.Units[0].ID, "Hack", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tocar una unidad ajena: %v", err)
	}
	if err := env.res.DeleteType(env.owner, "s1", ajeno.Type.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar tipo ajeno: %v", err)
	}
}
