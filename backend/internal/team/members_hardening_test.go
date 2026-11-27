// Endurecimiento de F2g2 después de la verificación y la revisión nativa: la
// carrera del último owner (F1), la reactivación por las otras dos puertas con
// el rol guardado (F2) y el comportamiento de siempre para quien está activo.
package team

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// F1: el chequeo del último owner también corre DENTRO de la transacción,
// después del UPDATE. El de afuera no alcanza: dos owners que se dan de baja
// mutuamente bloquean filas distintas, así que los dos veían «queda otro
// owner» y el salón quedaba sin dueño. Acá se simula esa carrera: la baja de la
// otra transacción ya commiteó cuando esta cuenta owners.
func TestMemberService_remove_checks_last_owner_inside_the_tx(t *testing.T) {
	svc, mems, admin := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	mems.ByKey["u-owner2|s1"] = domain.Membership{UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner}

	// El otro owner (el actor) ya fue dado de baja por la transacción que
	// ganó la carrera: el pre-chequeo de afuera vio dos, el conteo de adentro ve
	// cero.
	admin.concurrentBaja = "u-owner|s1"

	err := svc.Remove(owner, "s1", "u-owner2")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("último owner dentro de la transacción: %v", err)
	}
	if got, want := err.Error(), "No se puede quitar al último dueño"; got != want {
		t.Fatalf("mensaje = %q, esperaba %q", got, want)
	}
	// El 400 revierte ENTERA la transacción: la fila que esta baja desactivó
	// vuelve a estar activa (nada queda a medias).
	for _, key := range []string{"u-owner|s1", "u-owner2|s1"} {
		if raw := mems.ByKey[key]; !raw.Active() {
			t.Fatalf("el rechazo no puede dar de baja a %s: %+v", key, raw)
		}
	}
}

// F2: la administración no reactiva por el alta a un admin ni a un owner dados
// de baja (misma regla que la baja/reactivación), y el rol guardado se
// conserva. El dueño sí puede, y ahí manda el rol pedido.
func TestMemberService_add_admin_cannot_reactivate_deactivated_admin_or_owner(t *testing.T) {
	svc, mems, _ := deactivateFixture(t)
	if err := svc.Users.Save(domain.User{ID: "u-admin2", Name: "Fede", Email: "fede@mail.com"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	admin, _ := mems.Find("u-admin", "s1")
	owner, _ := mems.Find("u-owner", "s1")
	at := f2g2Now
	mems.ByKey["u-admin2|s1"] = domain.Membership{
		UserID: "u-admin2", SalonID: "s1", Role: domain.RoleAdmin, EndedAt: &at,
	}
	mems.ByKey["u-owner2|s1"] = domain.Membership{
		UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner, EndedAt: &at,
	}

	casos := []struct {
		email string
		key   string
		rol   domain.Role
	}{
		{"fede@mail.com", "u-admin2|s1", domain.RoleAdmin},
		{"eva@mail.com", "u-owner2|s1", domain.RoleOwner},
	}
	for _, c := range casos {
		if _, err := svc.Add(admin, "s1", c.email, domain.RoleProfessional, nil); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("admin reactivando una baja de %s: %v", c.rol.Label(), err)
		}
		raw := mems.ByKey[c.key]
		if raw.Active() {
			t.Fatalf("el 403 no puede reactivar: %+v", raw)
		}
		if raw.Role != c.rol {
			t.Fatalf("el rol guardado no se toca: %+v", raw)
		}
	}

	// El dueño sí: reactiva al admin dado de baja con el rol pedido (201 como un
	// alta) y una sola fila.
	m, err := svc.Add(owner, "s1", "fede@mail.com", domain.RoleReception, nil)
	if err != nil {
		t.Fatalf("el dueño reactiva al admin dado de baja: %v", err)
	}
	if m.ID != "u-admin2" || m.Role != domain.RoleReception || !m.Active {
		t.Fatalf("miembro reactivado por el dueño: %+v", m)
	}
	got, err := mems.Find("u-admin2", "s1")
	if err != nil {
		t.Fatalf("la membresía tiene que quedar activa: %v", err)
	}
	if got.Role != domain.RoleReception || !got.Active() {
		t.Fatalf("el dueño reactiva con el rol pedido: %+v", got)
	}
}

// F2, puerta de la invitación: una invitación a un admin o a un owner dados de
// baja tampoco puede emitirla la administración (403), porque al canjearla le
// cambiaría el rol. El dueño sí puede emitirla.
func TestInvitation_Invite_admin_cannot_invite_a_deactivated_admin_or_owner(t *testing.T) {
	env := newInviteEnv(t)
	at := env.now
	for _, u := range []domain.User{
		{ID: "u-admin", Name: "Beto", Email: "beto@mail.com"},
		{ID: "u-admin2", Name: "Fede", Email: "fede@mail.com"},
		{ID: "u-owner2", Name: "Eva", Email: "eva@mail.com"},
	} {
		if err := env.users.Save(u); err != nil {
			t.Fatalf("usuario: %v", err)
		}
	}
	admin := domain.Membership{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin}
	if err := env.mems.Save(admin); err != nil {
		t.Fatalf("admin: %v", err)
	}
	env.mems.ByKey["u-admin2|s1"] = domain.Membership{
		UserID: "u-admin2", SalonID: "s1", Role: domain.RoleAdmin, EndedAt: &at,
	}
	env.mems.ByKey["u-owner2|s1"] = domain.Membership{
		UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner, EndedAt: &at,
	}

	for _, email := range []string{"fede@mail.com", "eva@mail.com"} {
		_, err := env.svc.Invite(admin, "s1", InvitationInput{Email: email, Role: domain.RoleProfessional})
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("admin invitando a una baja de %s: %v", email, err)
		}
	}
	// Nada quedó emitido.
	if n, _ := env.svc.List(env.owner, "s1"); len(n) != 0 {
		t.Fatalf("una invitación rechazada no puede quedar guardada: %+v", n)
	}

	// El dueño sí puede invitarlo: al canjear, la baja vuelve con el rol de la
	// invitación (la prueba del canje está en el test siguiente).
	if _, err := env.svc.Invite(env.owner, "s1", InvitationInput{Email: "eva@mail.com", Role: domain.RoleProfessional}); err != nil {
		t.Fatalf("el dueño invita a un owner dado de baja: %v", err)
	}
}

// F2: el canje autenticado de una invitación emitida por el DUEÑO reactiva a un
// owner dado de baja con el rol de la invitación (AcceptExisting no necesita
// ninguna regla extra: la invitación solo existe si alguien con permiso la
// emitió).
func TestInvitation_accept_reactivates_a_deactivated_owner(t *testing.T) {
	env := newInviteEnv(t)
	if err := env.users.Save(domain.User{ID: "u-owner2", Name: "Eva", Email: "eva@mail.com"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	at := env.now
	env.mems.ByKey["u-owner2|s1"] = domain.Membership{
		UserID: "u-owner2", SalonID: "s1", Role: domain.RoleOwner, Attends: true, EndedAt: &at,
	}
	if _, err := env.mems.Find("u-owner2", "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("punto de partida: la membresía está dada de baja: %v", err)
	}

	_, token := env.invite(t, "eva@mail.com", domain.RoleReception)
	salon, _, err := env.pub.AcceptExisting("u-owner2", token)
	if err != nil {
		t.Fatalf("canje de una baja: %v", err)
	}
	if salon.ID != "s1" {
		t.Fatalf("salón del canje: %+v", salon)
	}
	got, err := env.mems.Find("u-owner2", "s1")
	if err != nil {
		t.Fatalf("el canje tiene que reactivar: %v", err)
	}
	if got.Role != domain.RoleReception || !got.Active() {
		t.Fatalf("el canje reactiva con el rol de la invitación: %+v", got)
	}
}

// PRESERVE: el alta de quien YA está activo sigue respondiendo los mismos 409
// (la autorización por rol guardado no cambia nada para quien está adentro).
func TestMemberService_add_active_member_unchanged(t *testing.T) {
	svc, mems, _ := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")

	_, err := svc.Add(owner, "s1", "caro@mail.com", domain.RoleProfessional, nil)
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != "Ya tiene ese rol" {
		t.Fatalf("mismo rol: %v", err)
	}
	_, err = svc.Add(owner, "s1", "caro@mail.com", domain.RoleReception, nil)
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != "Ya es parte del equipo como profesional" {
		t.Fatalf("otro rol: %v", err)
	}
	if raw := mems.ByKey["u-prof|s1"]; !raw.Active() || raw.Role != domain.RoleProfessional {
		t.Fatalf("un 409 no toca la membresía: %+v", raw)
	}
}

// PRESERVE: invitar a quien ya es parte ACTIVA del equipo sigue siendo 409.
func TestInvitation_Invite_active_member_unchanged(t *testing.T) {
	env := newInviteEnv(t)
	if err := env.users.Save(domain.User{ID: "u-prof", Name: "Caro", Email: "caro@mail.com"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	testutil.Miembro(env.mems, "u-prof", "s1", domain.RoleProfessional)

	_, err := env.svc.Invite(env.owner, "s1", InvitationInput{Email: "caro@mail.com", Role: domain.RoleProfessional})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("invitar a alguien activo: %v", err)
	}
	if got, want := err.Error(), "Esa persona ya es parte del equipo"; got != want {
		t.Fatalf("mensaje = %q, esperaba %q", got, want)
	}
}

// PRESERVE: la baja de alguien que no es owner sigue igual (fila conservada,
// invitaciones revocadas, sin el guard del último owner).
func TestMemberService_remove_non_owner_unchanged(t *testing.T) {
	svc, mems, admin := deactivateFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin.invitations = []domain.Invitation{
		{ID: "inv-prof", SalonID: "s1", CreatedBy: "u-prof", Email: "uno@mail.com"},
	}
	before := mems.ByKey["u-prof|s1"]

	if err := svc.Remove(owner, "s1", "u-prof"); err != nil {
		t.Fatalf("baja de un profesional: %v", err)
	}
	raw := mems.ByKey["u-prof|s1"]
	if raw.Active() || raw.EndedAt == nil || !raw.EndedAt.Equal(f2g2Now) {
		t.Fatalf("la fila queda dada de baja con la fecha: %+v", raw)
	}
	if raw.Role != before.Role || raw.Attends != before.Attends || raw.Trade != before.Trade {
		t.Fatalf("la baja conserva rol, marca y oficio: %+v", raw)
	}
	if admin.invitations[0].RevokedAt == nil {
		t.Fatal("la baja sigue revocando las invitaciones que emitió")
	}
}

// F1 (helper): la baja de la otra transacción ya commiteada mientras esta corre.
// Vive acá para que el test de la carrera se lea como el caso real.
