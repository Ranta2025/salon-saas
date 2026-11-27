// D8 (REMOVIDO) y D7 del canje de una invitación: una cuenta que ya existe
// SIEMPRE recibe el 409 y la cuenta creada por invitación nace SIN verificar.
// Vive con las invitaciones porque usa su entorno (`invitations_test.go`).
package team

import (
	"errors"
	"strings"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// wantMsg exige el error de contrato: la familia (errors.Is) Y el texto exacto.
func wantMsg(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("error = %v, esperaba (%v) %q", err, kind, text)
	}
}

// --- D8 (REMOVIDO): una cuenta que ya existe SIEMPRE recibe el 409 ---
//
// El enlace de la invitación lo recibe QUIEN INVITA (`POST /salons/:id/invitations`
// lo devuelve), así que tener el token NO prueba ser el dueño del correo. Por eso
// `AcceptNew` no toca una cuenta que ya existe —ni siquiera el cascarón sin
// verificar y sin membresías que deja quien registró un correo ajeno—: responde el
// 409 de siempre y el dueño real recupera su cuenta con `POST /auth/forgot` (el
// reinicio mata las sesiones del impostor).

// countingUsers cuenta las escrituras de cuentas: un canje que responde 409 no
// puede escribir NADA (ni el nombre, ni el hash, ni `email_verified_at`, ni
// `password_changed_at`, que solo se escribe dentro de la transacción del canje).
type countingUsers struct {
	*testutil.FakeUsers
	writes int
}

func (u *countingUsers) Save(x domain.User) error {
	u.writes++
	return u.FakeUsers.Save(x)
}

// addExistingAccount registra la cuenta que dejó quien usó un correo ajeno:
// existe, sin verificar y sin ninguna membresía.
func (e *inviteEnv) addExistingAccount(email string) domain.User {
	u, err := domain.NewUser("Impostor", email, "h:clave-del-impostor")
	if err != nil {
		panic(err)
	}
	u.ID = "u-ocupada-" + strings.Split(email, "@")[0]
	if err := e.users.Save(u); err != nil {
		panic(err)
	}
	return u
}

const hasAccountText = "Ya tenés cuenta: iniciá sesión para aceptar la invitación"

// assertAccountUntouched compara la cuenta con la foto previa al canje.
func assertAccountUntouched(t *testing.T, env *inviteEnv, want domain.User) {
	t.Helper()
	got, err := env.users.FindByID(want.ID)
	if err != nil {
		t.Fatalf("FindByID(%s): %v", want.ID, err)
	}
	if got.Name != want.Name || got.PasswordHash != want.PasswordHash || got.Email != want.Email {
		t.Fatalf("la cuenta cambió: %+v", got)
	}
	switch {
	case want.EmailVerifiedAt == nil && got.EmailVerifiedAt != nil:
		t.Fatalf("la cuenta quedó verificada: %v", got.EmailVerifiedAt)
	case want.EmailVerifiedAt != nil && (got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(*want.EmailVerifiedAt)):
		t.Fatalf("email_verified_at cambió: %v", got.EmailVerifiedAt)
	}
}

// assertInvitationsPending exige que NINGUNA invitación quedó aceptada ni revocada.
func assertInvitationsPending(t *testing.T, env *inviteEnv) {
	t.Helper()
	if len(env.inv.rows) == 0 {
		t.Fatal("no hay invitaciones")
	}
	for _, row := range env.inv.rows {
		if row.AcceptedAt != nil || row.RevokedAt != nil {
			t.Fatalf("la invitación no puede quedar tocada: %+v", row)
		}
	}
}

// assertRejectedWithoutWrites es el 409 de siempre y NADA más: ni sesión, ni
// escritura de la cuenta, ni transacción del canje (sin Claim no hay
// password_changed_at posible), ni la invitación gastada.
func assertRejectedWithoutWrites(t *testing.T, env *inviteEnv, users *countingUsers, saves, claims int, jwt string, err error, want domain.User) {
	t.Helper()
	wantMsg(t, err, domain.ErrAlreadyExists, hasAccountText)
	if jwt != "" {
		t.Fatalf("un 409 no emite sesión: %q", jwt)
	}
	assertAccountUntouched(t, env, want)
	if users.writes != 0 {
		t.Fatalf("escrituras de la cuenta: %d", users.writes)
	}
	if env.inv.claims != claims || env.inv.saves != saves {
		t.Fatalf("claims/saves: %d/%d, antes %d/%d", env.inv.claims, env.inv.saves, claims, saves)
	}
	assertInvitationsPending(t, env)
}

// D8 REMOVIDO: el cascarón sin verificar y sin membresías NO se toma. La
// invitación al mismo correo responde el 409 de siempre.
func TestAcceptNew_unverifiedExistingAccountKeepsThe409(t *testing.T) {
	env := newInviteEnv(t)
	ocupada := env.addExistingAccount("lucia@mail.com")
	users := &countingUsers{FakeUsers: env.users}
	pub := env.pub
	pub.Users = users
	_, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	saves, claims := env.inv.saves, env.inv.claims

	jwt, err := pub.AcceptNew(token, "Lucía Nueva", "claveNueva1")
	assertRejectedWithoutWrites(t, env, users, saves, claims, jwt, err, ocupada)
}

// D8 REMOVIDO: una cuenta VERIFICADA recibe el 409 de siempre, sin tocar nada.
func TestAcceptNew_verifiedAccountKeepsThe409(t *testing.T) {
	env := newInviteEnv(t)
	u, _ := domain.NewUser("Ana", "ana@mail.com", "h:clave-ana")
	u.ID = "u-ana"
	at := env.now
	u.EmailVerifiedAt = &at
	if err := env.users.Save(u); err != nil {
		t.Fatal(err)
	}
	users := &countingUsers{FakeUsers: env.users}
	pub := env.pub
	pub.Users = users
	_, token := env.invite(t, "ana@mail.com", domain.RoleReception)
	saves, claims := env.inv.saves, env.inv.claims

	jwt, err := pub.AcceptNew(token, "Ana Otra", "claveNueva1")
	assertRejectedWithoutWrites(t, env, users, saves, claims, jwt, err, u)
}

// D8 REMOVIDO: con una membresía ACTIVA (en cualquier salón) también es 409; y una
// membresía dada de baja YA NO abre la puerta a ninguna toma.
func TestAcceptNew_accountWithMembershipKeepsThe409(t *testing.T) {
	env := newInviteEnv(t)
	ocupada := env.addExistingAccount("lucia@mail.com")
	if err := env.mems.Save(domain.Membership{UserID: ocupada.ID, SalonID: "s2", Role: domain.RoleReception}); err != nil {
		t.Fatal(err)
	}
	users := &countingUsers{FakeUsers: env.users}
	pub := env.pub
	pub.Users = users
	_, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	saves, claims := env.inv.saves, env.inv.claims

	jwt, err := pub.AcceptNew(token, "Lucía Nueva", "claveNueva1")
	assertRejectedWithoutWrites(t, env, users, saves, claims, jwt, err, ocupada)

	// Dada de baja (ended_at): CERO activas, y aun así el 409 (antes D8 tomaba la
	// cuenta en este caso exacto).
	m, err := env.mems.FindMember(ocupada.ID, "s2")
	if err != nil {
		t.Fatal(err)
	}
	m.EndedAt = &env.now
	env.mems.ByKey[ocupada.ID+"|s2"] = m
	jwt, err = pub.AcceptNew(token, "Lucía Nueva", "claveNueva1")
	assertRejectedWithoutWrites(t, env, users, saves, claims, jwt, err, ocupada)
}

// D8 REMOVIDO: una invitación para OTRO correo no mira ni toca la cuenta que ya
// existe de una dirección distinta: crea la cuenta nueva de la invitada.
func TestAcceptNew_invitationForAnotherEmailCreatesANewAccount(t *testing.T) {
	env := newInviteEnv(t)
	otra := env.addExistingAccount("lucia@mail.com")
	pub := env.pub
	_, token := env.invite(t, "nueva@mail.com", domain.RoleProfessional)

	jwt, err := pub.AcceptNew(token, "Nueva", "claveNueva1")
	if err != nil || jwt == "" {
		t.Fatalf("AcceptNew: %q %v", jwt, err)
	}
	assertAccountUntouched(t, env, otra)
	u, err := env.users.FindByEmail("nueva@mail.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.PasswordHash != "h:claveNueva1" {
		t.Errorf("clave: %q", u.PasswordHash)
	}
	m, err := env.mems.FindMember(u.ID, "s1")
	if err != nil || m.Role != domain.RoleProfessional {
		t.Fatalf("membresía: %+v %v", m, err)
	}
	for _, row := range env.inv.rows {
		if row.AcceptedBy != u.ID {
			t.Fatalf("la invitación no quedó aceptada por la cuenta nueva: %+v", row)
		}
	}
}

// D7: una cuenta creada por invitación nace YA verificada (el token llegó a ese
// correo). Es la rama que más se usa y no puede quedar en NULL.
func TestAcceptNew_newAccountIsBornUnverified(t *testing.T) {
	env := newInviteEnv(t)
	pub := env.pub
	_, token := env.invite(t, "nueva@mail.com", domain.RoleProfessional)

	if _, err := pub.AcceptNew(token, "Nueva", "claveNueva1"); err != nil {
		t.Fatalf("AcceptNew: %v", err)
	}
	u, err := env.users.FindByEmail("nueva@mail.com")
	if err != nil {
		t.Fatal(err)
	}
	// The invitation link is also returned to the inviter, so holding it does
	// not prove the mailbox is the invitee's: the account is born UNVERIFIED,
	// like a registered one (F15 native review finding R1).
	if u.EmailVerifiedAt != nil {
		t.Fatal("la cuenta creada por invitación nace SIN verificar")
	}
	if u.PasswordHash != "h:claveNueva1" {
		t.Errorf("clave: %q", u.PasswordHash)
	}
}

// NOTA: la carrera de dos canjes del MISMO token está cubierta por el test de
// concurrencia contra Postgres y por `invitations_it_test.go`; acá no se repite
// porque el fake de invitaciones (`invitations_test.go`, preexistente) no es
// seguro para goroutines y este archivo no lo puede tocar. La toma del cascarón
// (D8) ya no existe: la retiró el hallazgo R1 de la revisión nativa, porque el
// enlace de la invitación lo recibe quien invita.
