package app

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/staffing"
)

// fakeProfessionalServices: qué servicios hace cada profesional, en memoria y
// con la misma semántica que pgstore.
//
// El estado inicial imita lo que deja la migración 016: un servicio sin filas
// grabadas lo hace TODO el equipo reservable del salón (el backfill). Recién
// cuando un test asigna (aunque sea una lista vacía) el filtro por servicio se
// comporta como la base real.
type fakeProfessionalServices struct {
	staff     *fakeStaffLister
	byService map[string][]string // "salonID|serviceID" -> userIDs
	byMember  map[string][]string // "salonID|userID"    -> serviceIDs
}

func (f *fakeProfessionalServices) ListProfessionalsFor(salonID, serviceID string) ([]domain.StaffMember, error) {
	key := salonID + "|" + serviceID
	ids, recorded := f.byService[key]
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []domain.StaffMember
	for _, m := range f.staff.bySalon[salonID] {
		if !m.Bookable() {
			continue
		}
		if recorded && !want[m.ID] {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeProfessionalServices) ListServiceIDs(salonID, userID string) ([]string, error) {
	return append([]string{}, f.byMember[salonID+"|"+userID]...), nil
}

func (f *fakeProfessionalServices) SetServices(salonID, userID string, serviceIDs []string) error {
	key := salonID + "|" + userID
	if _, ok := f.staff.bySalon[salonID]; !ok {
		return domain.ErrNotFound
	}
	uniq := map[string]bool{}
	out := make([]string, 0, len(serviceIDs))
	for _, id := range serviceIDs {
		if id == "" || uniq[id] {
			continue
		}
		uniq[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	if f.byMember == nil {
		f.byMember = map[string][]string{}
	}
	if f.byService == nil {
		f.byService = map[string][]string{}
	}
	f.byMember[key] = out
	// Lo asignado pasa a ser el registro del servicio para quien lo ve: así el
	// camino de disponibilidad y el de la pantalla de equipo miran lo mismo.
	for _, id := range out {
		filename := salonID + "|" + id
		already := false
		for _, who := range f.byService[filename] {
			if who == userID {
				already = true
			}
		}
		if !already {
			f.byService[filename] = append(f.byService[filename], userID)
		}
	}
	return nil
}

// assign graba qué profesionales hacen un servicio (lista vacía = nadie lo hace).
func (f *fakeProfessionalServices) assign(salonID, serviceID string, userIDs ...string) {
	if f.byService == nil {
		f.byService = map[string][]string{}
	}
	f.byService[salonID+"|"+serviceID] = userIDs
}

var _ ports.ProfessionalServiceRepository = (*fakeProfessionalServices)(nil)

// teamFixture: pantalla de equipo con el dueño-barbero, la administración, una
// profesional y recepción, más los repos que necesita F2c.
type teamFixture struct {
	svc      MemberService
	mems     *fakeMemberships
	users    *fakeUsers
	members  *fakeMembers
	profs    *fakeProfessionalServices
	services *fakeServices
	corte    domain.Service
	keratina domain.Service
}

func newTeamFixture(t *testing.T) *teamFixture {
	t.Helper()
	users := &fakeUsers{}
	for _, u := range []domain.User{
		{ID: "u-owner", Name: "Ana", Email: "ana@mail.com"},
		{ID: "u-admin", Name: "Beto", Email: "beto@mail.com"},
		{ID: "u-prof", Name: "Caro", Email: "caro@mail.com"},
		{ID: "u-recep", Name: "Dani", Email: "dani@mail.com"},
		{ID: "u-otro", Name: "Eva", Email: "eva@mail.com"},
	} {
		if err := users.Save(u); err != nil {
			t.Fatalf("usuario: %v", err)
		}
	}
	mems := &fakeMemberships{}
	// El dueño de un salón real nace atendiendo (S-F2a-4): acá se replica.
	_ = mems.Save(domain.Membership{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner, Attends: true})
	_ = mems.Save(domain.Membership{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin})
	_ = mems.Save(domain.Membership{UserID: "u-prof", SalonID: "s1", Role: domain.RoleProfessional, Attends: true})
	_ = mems.Save(domain.Membership{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception})
	_ = mems.Save(domain.Membership{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner, Attends: true})

	staff := &fakeStaffLister{bySalon: map[string][]domain.StaffMember{"s1": {
		{ID: "u-owner", Name: "Ana", Role: domain.RoleOwner, Attends: true},
		{ID: "u-prof", Name: "Caro", Role: domain.RoleProfessional, Attends: true},
	}}}
	profs := &fakeProfessionalServices{staff: staff}
	services := &fakeServices{}
	corte, err := domain.NewService("s1", "Corte", 45, 150000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	keratina, err := domain.NewService("s1", "Keratina", 60, 300000, 15)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	for _, s := range []domain.Service{corte, keratina} {
		if err := services.Save(s); err != nil {
			t.Fatalf("guardar servicio: %v", err)
		}
	}
	otro, _ := domain.NewService("s2", "Corte", 30, 90000, 10)
	if err := services.Save(otro); err != nil {
		t.Fatalf("servicio de otro salón: %v", err)
	}
	return &teamFixture{
		svc: MemberService{
			Members: &fakeMembers{mems: mems, users: users}, Memberships: mems, Users: users,
			Professionals: profs, Services: services,
		},
		mems: mems, users: users, members: &fakeMembers{mems: mems, users: users},
		profs: profs, services: services, corte: corte, keratina: keratina,
	}
}

// S-F2c-4: la disponibilidad de un servicio la dan SOLO quienes lo hacen; un
// servicio que nadie hace responde {"slots":[]} y no un error.
func TestAvailability_filtra_por_servicio(t *testing.T) {
	env := newPublicEnv(t)
	keratina, err := domain.NewService("s1", "Keratina", 60, 300000, 15)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	manicura, _ := domain.NewService("s1", "Manicura", 30, 100000, 10)
	for _, s := range []domain.Service{keratina, manicura} {
		_ = env.services.Save(s)
	}
	env.profs.assign("s1", env.corte.ID, "prof1")
	env.profs.assign("s1", keratina.ID, "prof2")
	env.profs.assign("s1", manicura.ID) // nadie la hace

	// Corte: solo quien lo hace (Ana). Bruno no aparece aunque esté libre.
	day, err := env.avail.Day("navajas-oro", "2026-12-01", env.corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad de corte: %v", err)
	}
	if len(day.Slots) == 0 {
		t.Fatal("Ana hace corte y tiene huecos")
	}
	for _, s := range day.Slots {
		if s.ProfessionalID != "prof1" {
			t.Fatalf("Bruno no hace corte y no se ofrece: %+v", day.Slots)
		}
	}

	// Keratina: la otra profesional, con los huecos de una duración de 60 min.
	day, err = env.avail.Day("navajas-oro", "2026-12-01", keratina.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad de keratina: %v", err)
	}
	if len(day.Slots) == 0 {
		t.Fatal("Bruno hace keratina y tiene huecos")
	}
	for _, s := range day.Slots {
		if s.ProfessionalID != "prof2" {
			t.Fatalf("Ana no hace keratina y no se ofrece: %+v", day.Slots)
		}
	}

	// Nadie hace manicura: 200 con la lista vacía (nunca un error).
	day, err = env.avail.Day("navajas-oro", "2026-12-01", manicura.ID, "")
	if err != nil {
		t.Fatalf("un servicio que nadie hace no puede romper la disponibilidad: %v", err)
	}
	if day.Slots == nil || len(day.Slots) != 0 {
		t.Fatalf("esperaba [] y dio %+v", day.Slots)
	}

	// Pedir por un profesional que no hace ese servicio: no existe para esta
	// búsqueda (404), igual que quien no atiende.
	if _, err := env.avail.Day("navajas-oro", "2026-12-01", keratina.ID, "prof1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("profesional que no hace el servicio: %v", err)
	}
	// El que sí lo hace sigue respondiendo.
	if _, err := env.avail.Day("navajas-oro", "2026-12-01", keratina.ID, "prof2"); err != nil {
		t.Fatalf("profesional que sí lo hace: %v", err)
	}
}

// S-F2c-4: dos profesionales que hacen servicios distintos reservan la misma
// hora sin pisarse, y cada turno guarda el servicio reservado.
func TestPublicBooking_asigna_quien_hace_el_servicio(t *testing.T) {
	env := newPublicEnv(t)
	keratina, err := domain.NewService("s1", "Keratina", 60, 300000, 15)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	_ = env.services.Save(keratina)
	env.profs.assign("s1", env.corte.ID, "prof1")
	env.profs.assign("s1", keratina.ID, "prof2")

	start := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC) // 09:00 en CDMX
	corteBooking, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: env.corte.ID, ClientName: "Lía", ClientPhone: "555", StartsAt: start,
	})
	if err != nil {
		t.Fatalf("reserva de corte: %v", err)
	}
	keratinaBooking, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: keratina.ID, ClientName: "Ana", ClientPhone: "556", StartsAt: start,
	})
	if err != nil {
		t.Fatalf("la keratina a la misma hora la hace otra profesional: %v", err)
	}
	if corteBooking.ProfessionalID != "prof1" || keratinaBooking.ProfessionalID != "prof2" {
		t.Fatalf("asignación por servicio: %q %q", corteBooking.ProfessionalID, keratinaBooking.ProfessionalID)
	}
	if corteBooking.ServiceID != env.corte.ID || keratinaBooking.ServiceID != keratina.ID {
		t.Fatalf("el turno guarda el servicio reservado: %q %q", corteBooking.ServiceID, keratinaBooking.ServiceID)
	}
	if len(env.bookings.all) != 2 {
		t.Fatalf("dos turnos guardados, hay %d", len(env.bookings.all))
	}
}

// S-F2c-4: si nadie hace el servicio, la reserva pública es 409 (no 500) y no
// se guarda nada.
func TestPublicBooking_servicio_sin_profesional_409(t *testing.T) {
	env := newPublicEnv(t)
	manicura, err := domain.NewService("s1", "Manicura", 30, 100000, 10)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	_ = env.services.Save(manicura)
	env.profs.assign("s1", manicura.ID) // nadie la hace

	start := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	_, err = env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: manicura.ID, ClientName: "Lía", ClientPhone: "555", StartsAt: start,
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("sin gente que haga el servicio esperaba 409, dio %v", err)
	}
	if len(env.bookings.all) != 0 {
		t.Fatalf("el 409 no puede dejar un turno guardado: %+v", env.bookings.all)
	}

	// Con profesional explícito que no hace ese servicio: 400 con el motivo,
	// tampoco se guarda nada. (Antes se aceptaba porque solo se miraba que
	// atendiera.)
	_, err = env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: manicura.ID, ProfessionalID: "prof1", ClientName: "Lía", ClientPhone: "555", StartsAt: start,
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional que no hace el servicio: %v", err)
	}
	if len(env.bookings.all) != 0 {
		t.Fatalf("tampoco acá se guarda un turno: %+v", env.bookings.all)
	}
}

// PRESERVE (S-F2c-4): la agenda de staff no exige servicio. Antes del cambio
// tampoco lo exigía y no puede empezar a hacerlo por la espalda.
func TestBookingService_staff_sin_servicio(t *testing.T) {
	bookings := &fb{}
	mems := &fakeMemberships{}
	svc := BookingService{Bookings: bookings, Memberships: mems}
	rec := recepcionista(mems)
	miembro(mems, "prof1", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("alta de staff sin servicio: %v", err)
	}
	if b.ServiceID != "" {
		t.Fatalf("el turno del calendario nace sin servicio: %q", b.ServiceID)
	}
}

// S-F2c-3: PUT servicios de un miembro. Solo owner/admin, solo quien atiende,
// todo el salón o nada (un id inválido no toca la lista anterior).
func TestMemberService_setServices(t *testing.T) {
	fx := newTeamFixture(t)
	owner, _ := fx.mems.Find("u-owner", "s1")
	admin, _ := fx.mems.Find("u-admin", "s1")
	profe, _ := fx.mems.Find("u-prof", "s1")

	// El dueño asigna dos servicios (con un id repetido: la lista es un
	// conjunto, no una secuencia).
	got, err := fx.svc.SetServices(owner, "s1", "u-prof", []string{fx.keratina.ID, fx.corte.ID, fx.corte.ID})
	if err != nil {
		t.Fatalf("asignar servicios: %v", err)
	}
	want := []string{fx.corte.ID, fx.keratina.ID}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("servicios devueltos: %v", got)
	}
	if stored, _ := fx.profs.ListServiceIDs("s1", "u-prof"); strings.Join(stored, ",") != strings.Join(want, ",") {
		t.Fatalf("no se persistió: %v", stored)
	}

	// La administración también administra el equipo: deja la lista vacía.
	if got, err := fx.svc.SetServices(admin, "s1", "u-prof", nil); err != nil || len(got) != 0 {
		t.Fatalf("lista vacía permitida: %v %v", got, err)
	}
	if stored, _ := fx.profs.ListServiceIDs("s1", "u-prof"); len(stored) != 0 {
		t.Fatalf("la lista vacía tiene que limpiar: %v", stored)
	}

	// Un id que no es de este salón (o no existe) es 400 y no toca la lista.
	if _, err := fx.svc.SetServices(owner, "s1", "u-prof", []string{fx.corte.ID}); err != nil {
		t.Fatalf("dejar corte: %v", err)
	}
	before, _ := fx.profs.ListServiceIDs("s1", "u-prof")
	for _, bad := range []string{"svc-no-existe"} {
		_, err := fx.svc.SetServices(owner, "s1", "u-prof", []string{fx.keratina.ID, bad})
		if !errors.Is(err, domain.ErrValidation) || err.Error() != "Servicio inválido" {
			t.Fatalf("servicio inexistente: %v", err)
		}
	}
	after, _ := fx.profs.ListServiceIDs("s1", "u-prof")
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Fatalf("el id malo no puede escribir a medias: %v -> %v", before, after)
	}

	// El servicio de otro salón tampoco entra (aislamiento por salón).
	var otroSalon domain.Service
	for _, s := range fx.services.all {
		if s.SalonID == "s2" {
			otroSalon = s
		}
	}
	if _, err := fx.svc.SetServices(owner, "s1", "u-prof", []string{otroSalon.ID}); !errors.Is(err, domain.ErrValidation) || err.Error() != "Servicio inválido" {
		t.Fatalf("servicio de otro salón: %v", err)
	}

	// Recepción no hace servicios: 400 con el mensaje del contrato.
	if _, err := fx.svc.SetServices(owner, "s1", "u-recep", []string{fx.corte.ID}); !errors.Is(err, domain.ErrValidation) ||
		err.Error() != "Ese rol no hace servicios" {
		t.Fatalf("servicios para recepción: %v", err)
	}
	// Un miembro de otro salón es 404 (no existe para esta ruta).
	if _, err := fx.svc.SetServices(owner, "s1", "u-otro", []string{fx.corte.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro de otro salón: %v", err)
	}
	// Sin permiso de gestión: 403 (la profesional no se autoasigna servicios).
	if _, err := fx.svc.SetServices(profe, "s1", "u-prof", []string{fx.corte.ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional asignando servicios: %v", err)
	}
	ajenoActor, _ := fx.mems.Find("u-otro", "s2")
	if _, err := fx.svc.SetServices(ajenoActor, "s1", "u-prof", []string{fx.corte.ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("dueño de otro salón: %v", err)
	}
}

// S-F2c-3: el PATCH del equipo acepta trade y atribuye, al menos uno; el oficio
// solo existe en quien atiende.
func TestMemberService_update_trade(t *testing.T) {
	fx := newTeamFixture(t)
	owner, _ := fx.mems.Find("u-owner", "s1")
	profe, _ := fx.mems.Find("u-prof", "s1")
	barbero, colorista := "  Barbero  ", "Colorista"

	// Solo oficio: se guarda recortado y vuelve en el miembro.
	m, err := fx.svc.Update(owner, "s1", "u-prof", MemberPatch{Trade: &barbero})
	if err != nil {
		t.Fatalf("oficio: %v", err)
	}
	if m.ID != "u-prof" || m.Trade != "Barbero" || m.Role != domain.RoleProfessional {
		t.Fatalf("miembro devuelto: %+v", m)
	}
	if stored, _ := fx.mems.Find("u-prof", "s1"); stored.Trade != "Barbero" {
		t.Fatalf("el oficio no se persistió: %+v", stored)
	}
	if m.ServiceIDs == nil {
		t.Fatal("service_ids tiene que viajar como [] y nunca null")
	}

	// Sin campos no hay nada que cambiar: 400 con el texto del contrato.
	if _, err := fx.svc.Update(owner, "s1", "u-prof", MemberPatch{}); !errors.Is(err, domain.ErrValidation) ||
		err.Error() != "Indicá qué cambiar" {
		t.Fatalf("patch vacío: %v", err)
	}

	// Recepción no tiene oficio.
	if _, err := fx.svc.Update(owner, "s1", "u-recep", MemberPatch{Trade: &barbero}); !errors.Is(err, domain.ErrValidation) ||
		err.Error() != "Ese rol no tiene oficio" {
		t.Fatalf("oficio en recepción: %v", err)
	}

	// Las dos cosas a la vez: la administración se enciende y estrena oficio.
	on := true
	m, err = fx.svc.Update(owner, "s1", "u-admin", MemberPatch{Attends: &on, Trade: &colorista})
	if err != nil {
		t.Fatalf("attends + trade: %v", err)
	}
	if !m.Attends || m.Trade != "Colorista" {
		t.Fatalf("miembro con marca y oficio: %+v", m)
	}
	stored, _ := fx.mems.Find("u-admin", "s1")
	if !stored.Attends || stored.Trade != "Colorista" {
		t.Fatalf("no se persistió la marca ni el oficio: %+v", stored)
	}

	// Permisos: la profesional no edita el equipo; el dueño de otro salón
	// tampoco; el miembro de otro salón no existe para esta ruta (404).
	if _, err := fx.svc.Update(profe, "s1", "u-admin", MemberPatch{Trade: &colorista}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional editando el equipo: %v", err)
	}
	ajenoActor, _ := fx.mems.Find("u-otro", "s2")
	if _, err := fx.svc.Update(ajenoActor, "s1", "u-admin", MemberPatch{Trade: &colorista}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("dueño de otro salón: %v", err)
	}
	if _, err := fx.svc.Update(owner, "s1", "u-otro", MemberPatch{Trade: &colorista}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro de otro salón: %v", err)
	}
}

// PRESERVE (S-F2a-5): el PATCH de solo attends sigue funcionando igual.
func TestMemberService_update_attends_solo(t *testing.T) {
	fx := newTeamFixture(t)
	owner, _ := fx.mems.Find("u-owner", "s1")
	on := true
	m, err := fx.svc.Update(owner, "s1", "u-admin", MemberPatch{Attends: &on})
	if err != nil {
		t.Fatalf("attends: %v", err)
	}
	if !m.Attends || m.Trade != "" {
		t.Fatalf("el miembro devuelto: %+v", m)
	}
	// Un profesional no se apaga y recepción no se enciende: mismos mensajes.
	off := false
	if _, err := fx.svc.Update(owner, "s1", "u-prof", MemberPatch{Attends: &off}); err.Error() != "Las profesionales siempre atienden" {
		t.Fatalf("profesional en false: %v", err)
	}
	if _, err := fx.svc.Update(owner, "s1", "u-recep", MemberPatch{Attends: &on}); err.Error() != "Ese rol no atiende clientes" {
		t.Fatalf("recepción en true: %v", err)
	}
}

// S-F2c-3: el listado del equipo lleva el oficio y los servicios de cada quien.
func TestMemberService_list_con_oficio_y_servicios(t *testing.T) {
	fx := newTeamFixture(t)
	owner, _ := fx.mems.Find("u-owner", "s1")
	trade := "Barbero"
	if _, err := fx.svc.Update(owner, "s1", "u-prof", MemberPatch{Trade: &trade}); err != nil {
		t.Fatalf("oficio: %v", err)
	}
	if _, err := fx.svc.SetServices(owner, "s1", "u-prof", []string{fx.corte.ID}); err != nil {
		t.Fatalf("servicios: %v", err)
	}
	list, err := fx.svc.List(owner, "s1")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	for _, m := range list {
		if m.ServiceIDs == nil {
			t.Fatalf("%s viaja sin service_ids (tiene que ser [])", m.ID)
		}
		switch m.ID {
		case "u-prof":
			if m.Trade != "Barbero" || len(m.ServiceIDs) != 1 || m.ServiceIDs[0] != fx.corte.ID {
				t.Fatalf("profesional en el listado: %+v", m)
			}
		case "u-recep":
			if m.Trade != "" || len(m.ServiceIDs) != 0 {
				t.Fatalf("recepción no tiene oficio ni servicios: %+v", m)
			}
		}
	}
}

// S-F2c-5: crear un servicio no lo asigna a nadie; con professional_ids se
// asigna solo a quien atiende en el salón.
func TestServiceService_create_professional_ids(t *testing.T) {
	services := &fakeServices{}
	mems := &fakeMemberships{}
	staff := &fakeStaffLister{bySalon: map[string][]domain.StaffMember{"s1": {
		{ID: "u-prof", Name: "Caro", Role: domain.RoleProfessional},
		{ID: "u-recep", Name: "Dani", Role: domain.RoleReception},
	}}}
	profs := &fakeProfessionalServices{staff: staff}
	svc := staffing.ServiceService{Services: services, Memberships: mems, Professionals: profs}
	miembro(mems, "u-owner", "s1", domain.RoleOwner)
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	miembro(mems, "u-recep", "s1", domain.RoleReception)
	miembro(mems, "u-otro", "s2", domain.RoleProfessional)

	// Sin professional_ids: el servicio no lo hace nadie (la excepción
	// consciente de S-F2c-5; el aviso de la pantalla lo dice).
	corte, err := svc.Create(owner, "s1", "Corte", 30, 100000, 20, nil, "")
	if err != nil {
		t.Fatalf("crear sin profesionales: %v", err)
	}
	if ids, _ := profs.ListServiceIDs("s1", "u-prof"); len(ids) != 0 {
		t.Fatalf("nadie por defecto: %v", ids)
	}

	// Con una profesional: queda asignado a ella, y solo a ella.
	keratina, err := svc.Create(owner, "s1", "Keratina", 60, 250000, 15, []string{"u-prof"}, "")
	if err != nil {
		t.Fatalf("crear con profesional: %v", err)
	}
	if ids, _ := profs.ListServiceIDs("s1", "u-prof"); len(ids) != 1 || ids[0] != keratina.ID {
		t.Fatalf("asignación al crear: %v", ids)
	}
	if _, err := services.FindByID("s1", corte.ID); err != nil {
		t.Fatalf("el servicio creado antes sigue: %v", err)
	}

	// Recepción no es reservable: 400 y el servicio NO se crea.
	if _, err := svc.Create(owner, "s1", "Color", 60, 250000, 15, []string{"u-recep"}, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional no reservable: %v", err)
	}
	// Un id de otro salón (o inexistente) tampoco.
	if _, err := svc.Create(owner, "s1", "Color", 60, 250000, 15, []string{"u-otro"}, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional de otro salón: %v", err)
	}
	list, _ := services.List("s1", true)
	for _, s := range list {
		if s.Name == "Color" {
			t.Fatalf("el alta rechazada no puede quedar guardada: %+v", s)
		}
	}

	// Tope de 50 ids.
	tooMany := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		tooMany = append(tooMany, "u-prof")
	}
	if _, err := svc.Create(owner, "s1", "Planchado", 45, 200000, 10, tooMany, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("más de 50 profesionales: %v", err)
	}
}

// PRESERVE (S-F2c-1/S-F2c-4): lo que deja el backfill de la 016 sigue
// comportándose como antes del cambio: un servicio que aparece sin asignaciones
// grabadas (base con datos previos) lo ofrece todo el equipo reservable, y la
// reserva pública asigna a la primera persona libre de ese equipo.
func TestAvailability_backfill_todos_hacen_todo(t *testing.T) {
	env := newPublicEnv(t)
	day, err := env.avail.Day("navajas-oro", "2026-12-01", env.corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	who := map[string]bool{}
	for _, s := range day.Slots {
		who[s.ProfessionalID] = true
	}
	if len(who) != 2 || !who["prof1"] || !who["prof2"] {
		t.Fatalf("los dos reservables tienen que seguir ofreciéndose: %v", who)
	}

	start := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	first, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: env.corte.ID, ClientName: "Lía", ClientPhone: "555", StartsAt: start,
	})
	if err != nil {
		t.Fatalf("primera reserva: %v", err)
	}
	second, err := env.book.Book("navajas-oro", PublicBookingInput{
		ServiceID: env.corte.ID, ClientName: "Ana", ClientPhone: "556", StartsAt: start,
	})
	if err != nil {
		t.Fatalf("segunda reserva (otra profesional libre): %v", err)
	}
	if first.ProfessionalID == second.ProfessionalID {
		t.Fatalf("la asignación automática tiene que repartir entre los libres: %q %q",
			first.ProfessionalID, second.ProfessionalID)
	}
}
