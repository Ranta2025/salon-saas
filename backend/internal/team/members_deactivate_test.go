// F2g2 en el caso de uso (S-F2g2-4): dar de baja en vez de borrar, reactivar,
// turnos futuros y revocación de invitaciones en la misma transacción.
package team

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

var f2g2Now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func deactivateFixture(t *testing.T) (*MemberService, *testutil.FakeMemberships, *fakeMembers) {
	t.Helper()
	svc, mems, admin := memberFixture(t)
	svc.Tx = admin
	admin.future = map[string]int{}
	svc.Now = func() time.Time { return f2g2Now }
	// Caro (profesional) y Dani (recepción) en s1 para las bajas.
	testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	return svc, mems, admin
}

// La baja NO borra la fila: queda ended_at y el listado la muestra inactiva.
func TestMemberService_deactivate_keeps_the_row(t *testing.T) {
	svc, mems, admin := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")

	if err := svc.Remove(owner, "s1", "u-prof"); err != nil {
		t.Fatalf("dar de baja: %v", err)
	}
	raw, ok := mems.ByKey["u-prof|s1"]
	if !ok {
		t.Fatal("la membresía no puede borrarse: la baja conserva la fila")
	}
	if raw.Active() || raw.EndedAt == nil || !raw.EndedAt.Equal(f2g2Now) {
		t.Fatalf("la fila tiene que quedar dada de baja con la fecha: %+v", raw)
	}
	if _, err := mems.Find("u-prof", "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Find (acceso) devuelve solo activas: %v", err)
	}
	list, err := svc.List(owner, "s1")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	var saw bool
	for _, m := range list {
		if m.ID == "u-prof" {
			saw = true
			if m.Active || m.EndedAt == nil {
				t.Fatalf("el listado tiene que traer la baja: %+v", m)
			}
		}
	}
	if !saw {
		t.Fatalf("el listado devuelve activos Y dados de baja: %+v", list)
	}
	_ = admin
}

// Con turnos futuros no cancelados: 409 con la oración exacta (singular y
// plural) y NADA cambia (ni la fila ni las invitaciones).
func TestMemberService_deactivate_future_bookings(t *testing.T) {
	svc, mems, admin := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin.future["s1|u-prof"] = 2

	err := svc.Remove(owner, "s1", "u-prof")
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("turnos futuros: %v", err)
	}
	if got, want := err.Error(), "Tiene 2 turnos pendientes: reasignalos o cancelalos antes de darla de baja"; got != want {
		t.Fatalf("plural = %q, esperaba %q", got, want)
	}
	if raw := mems.ByKey["u-prof|s1"]; !raw.Active() {
		t.Fatalf("el 409 no puede darla de baja: %+v", raw)
	}

	admin.future["s1|u-prof"] = 1
	err = svc.Remove(owner, "s1", "u-prof")
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("un turno futuro: %v", err)
	}
	if got, want := err.Error(), "Tiene 1 turno pendiente: reasignalo o cancelalo antes de darla de baja"; got != want {
		t.Fatalf("singular = %q, esperaba %q", got, want)
	}
	if raw := mems.ByKey["u-prof|s1"]; !raw.Active() {
		t.Fatalf("el 409 no puede darla de baja: %+v", raw)
	}
}

// S-F2g2-4: al dar de baja se revocan las invitaciones pendientes que emitió,
// en la MISMA transacción. Las de otras personas no se tocan.
func TestMemberService_deactivate_revokes_its_invitations(t *testing.T) {
	svc, mems, admin := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin.invitations = []domain.Invitation{
		{ID: "inv-prof", SalonID: "s1", CreatedBy: "u-prof", Email: "uno@mail.com"},
		{ID: "inv-owner", SalonID: "s1", CreatedBy: "u-owner", Email: "dos@mail.com"},
		{ID: "inv-otro", SalonID: "s2", CreatedBy: "u-prof", Email: "tres@mail.com"},
	}

	if err := svc.Remove(owner, "s1", "u-prof"); err != nil {
		t.Fatalf("dar de baja: %v", err)
	}
	byID := map[string]domain.Invitation{}
	for _, inv := range admin.invitations {
		byID[inv.ID] = inv
	}
	if byID["inv-prof"].RevokedAt == nil {
		t.Fatal("la invitación pendiente que emitió tiene que quedar revocada")
	}
	if byID["inv-owner"].RevokedAt != nil || byID["inv-otro"].RevokedAt != nil {
		t.Fatalf("solo se revocan las de esa persona en ESE salón: %+v", byID)
	}
}

// El último owner ACTIVO no se puede dar de baja; con otro owner activo, sí se
// puede (la regla es el último, no cualquier owner).
func TestMemberService_deactivate_last_active_owner(t *testing.T) {
	svc, mems, _ := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")

	err := svc.Remove(owner, "s1", "u-owner")
	// Sigue siendo ErrValidation (400), igual que antes de F2g2: el contrato
	// pide conservar el comportamiento de hoy, y cambiar un código de estado
	// que ya se consume no estaba pedido.
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("último owner: %v", err)
	}
	if got, want := err.Error(), "No se puede quitar al último dueño"; got != want {
		t.Fatalf("mensaje = %q, esperaba %q", got, want)
	}
	if raw := mems.ByKey["u-owner|s1"]; !raw.Active() {
		t.Fatal("el último owner sigue activo")
	}

	// Un segundo owner directo en la base (la API no reparte owner): con dos
	// owners activos ya no hay "último", así que el dueño SÍ da de baja a otro
	// dueño (la regla es el último ACTIVO, no el rol).
	mems.ByKey["u-owner2|s1"] = domain.Membership{UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner}
	if err := svc.Remove(owner, "s1", "u-owner2"); err != nil {
		t.Fatalf("owner dando de baja a otro owner: %v", err)
	}
	if raw := mems.ByKey["u-owner2|s1"]; raw.Active() {
		t.Fatalf("el otro owner tiene que quedar dado de baja: %+v", raw)
	}

	// Y también se da de baja a SÍ MISMO cuando queda otro owner activo.
	mems.ByKey["u-owner2|s1"] = domain.Membership{UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner}
	if err := svc.Remove(owner, "s1", "u-owner"); err != nil {
		t.Fatalf("owner dándose de baja a sí mismo con otro owner: %v", err)
	}
	if raw := mems.ByKey["u-owner|s1"]; raw.Active() {
		t.Fatalf("la fila propia queda dada de baja: %+v", raw)
	}
}

// Autorización: el dueño da de baja a cualquier rol (incluido otro dueño: ver el
// test del último owner activo); un admin nunca da de baja al dueño ni a otro
// admin, sí a profesional, recepción o cliente; quien no administra no da de
// baja a nadie.
func TestMemberService_deactivate_authorization(t *testing.T) {
	svc, mems, _ := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin, _ := mems.Find("u-admin", "s1")

	if err := svc.Remove(admin, "s1", "u-profile"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro inexistente: %v", err)
	}
	// Con dos owners activos, el dueño de la sesión ya no es "el último", pero
	// un admin nunca baja al dueño ni a otro admin (403).
	mems.ByKey["u-owner2|s1"] = domain.Membership{UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner}
	if err := svc.Remove(admin, "s1", "u-owner"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin bajando al dueño: %v", err)
	}
	// Segundo admin directo: un admin no baja a su igual.
	mems.ByKey["u-admin2|s1"] = domain.Membership{UserID: "u-admin2", SalonID: "s1", Role: domain.RoleAdmin}
	if err := svc.Remove(admin, "s1", "u-admin2"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin bajando a otro admin: %v", err)
	}
	// Un profesional no administra: manageActor manda 403.
	prof, _ := mems.Find("u-prof", "s1")
	if err := svc.Remove(prof, "s1", "u-recep"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional dando de baja: %v", err)
	}
	// El owner baja a la recepción y al admin (roles que puede dar de alta).
	if err := svc.Remove(owner, "s1", "u-recep"); err != nil {
		t.Fatalf("owner baja a recepción: %v", err)
	}
	if err := svc.Remove(owner, "s1", "u-admin"); err != nil {
		t.Fatalf("owner baja a administración: %v", err)
	}
	// Miembro de otro salón (o inexistente): 404 (scope).
	if err := svc.Remove(owner, "s1", "u-ajeno"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro de otro salón: %v", err)
	}
	// Ya dada de baja: 404, no se vuelve a escribir.
	if err := svc.Remove(owner, "s1", "u-recep"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("baja repetida: %v", err)
	}
}

// Reactivar: conserva rol, attends y oficio; 409 si ya está activa; 404 si no
// es de ese salón. La autorización es la misma de la baja.
func TestMemberService_reactivate(t *testing.T) {
	svc, mems, _ := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	adm, _ := mems.Find("u-admin", "s1")
	mems.ByKey["u-prof|s1"] = domain.Membership{
		UserID: "u-prof", SalonID: "s1", Role: domain.RoleProfessional,
		Attends: true, Trade: "Barbero", EndedAt: &f2g2Now,
	}

	if _, err := svc.Reactivate(owner, "s1", "u-prof"); err != nil {
		t.Fatalf("reactivar: %v", err)
	}
	m, err := mems.Find("u-prof", "s1")
	if err != nil {
		t.Fatalf("después de reactivar Find la ve: %v", err)
	}
	if !m.Active() || m.Role != domain.RoleProfessional || !m.Attends || m.Trade != "Barbero" {
		t.Fatalf("reactivar conserva rol/attends/trade: %+v", m)
	}
	// Ya activa: 409 con el texto del contrato.
	_, err = svc.Reactivate(adm, "s1", "u-prof")
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("reactivar a alguien activo: %v", err)
	}
	if got, want := err.Error(), "Esa persona ya está en el equipo"; got != want {
		t.Fatalf("mensaje = %q, esperaba %q", got, want)
	}
	// Otro salón / inexistente: 404.
	if _, err := svc.Reactivate(owner, "s1", "u-owner2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v", err)
	}
	if _, err := svc.Reactivate(owner, "s1", "u-nadie"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}
	// Un admin no reactiva al dueño: la misma regla de rol de la baja, al revés.
	mems.ByKey["u-owner2|s1"] = domain.Membership{
		UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner, EndedAt: &f2g2Now,
	}
	if _, err := svc.Reactivate(adm, "s1", "u-owner2"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin reactivando al dueño: %v", err)
	}
	// La misma regla al revés: un owner SÍ reactiva a otro owner.
	if _, err := svc.Reactivate(owner, "s1", "u-owner2"); err != nil {
		t.Fatalf("owner reactivando a otro owner: %v", err)
	}
	if raw := mems.ByKey["u-owner2|s1"]; !raw.Active() {
		t.Fatalf("el otro owner tiene que quedar activo: %+v", raw)
	}
}

// POST /members por email sobre alguien dado de baja lo REACTIVA con el rol
// pedido (201, como un alta) y el acceso vuelve.
func TestMemberService_add_reactivates_a_former_member(t *testing.T) {
	svc, mems, _ := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	mems.ByKey["u-prof|s1"] = domain.Membership{
		UserID: "u-prof", SalonID: "s1", Role: domain.RoleProfessional,
		Attends: true, Trade: "Barbero", EndedAt: &f2g2Now,
	}

	m, err := svc.Add(owner, "s1", "caro@mail.com", domain.RoleReception, nil)
	if err != nil {
		t.Fatalf("reactivar por alta: %v", err)
	}
	if m.ID != "u-prof" || m.Role != domain.RoleReception || !m.Active {
		t.Fatalf("miembro reactivado: %+v", m)
	}
	got, err := mems.Find("u-prof", "s1")
	if err != nil {
		t.Fatalf("la membresía tiene que volver a estar activa: %v", err)
	}
	if got.Role != domain.RoleReception || got.Attends {
		t.Fatalf("rol pedido y attends por default del rol: %+v", got)
	}
}
