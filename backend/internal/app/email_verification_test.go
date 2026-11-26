package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

type verFix struct {
	st     *f15Store
	mailer *fakeMailer
	run    *deferredRunner
	svc    EmailVerificationService
}

func newVerFix() *verFix {
	st := newF15Store()
	m := &fakeMailer{}
	r := &deferredRunner{}
	return &verFix{st: st, mailer: m, run: r, svc: EmailVerificationService{
		Users: st, Verifs: st, Mailer: m, AppURL: "https://app.test", Run: r.Run, Now: st.clock.Now,
	}}
}

func (f *verFix) token(t *testing.T) string {
	t.Helper()
	path, tok := lastLinkToken(t, f.mailer)
	if path != "verificar-email" {
		t.Fatalf("ruta del enlace: %q", path)
	}
	return tok
}

// D7: al registrarse se manda (async) el correo «Verifica tu email» con el
// enlace en el fragmento, vence a las 24 h y el token se guarda solo como hash.
func TestSendVerification_asyncMailWithFragmentLink(t *testing.T) {
	f := newVerFix()
	u := f.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f.svc.SendVerification(u)
	if f.run.Pending() != 1 || len(f.st.verifs) != 0 || f.mailer.count() != 0 {
		t.Fatalf("el trabajo debe correr DESPUÉS: pending=%d verifs=%d mails=%d",
			f.run.Pending(), len(f.st.verifs), f.mailer.count())
	}
	f.run.Flush()
	tok := f.token(t)
	if len(f.st.verifs) != 1 {
		t.Fatalf("verifs: %d", len(f.st.verifs))
	}
	v := f.st.verifs[0]
	if v.TokenHash != domain.HashRecoveryToken(tok) || v.Email != "ana@mail.com" || v.UserID != "u-ana" ||
		!v.ExpiresAt.Equal(f.st.now().Add(24*time.Hour)) {
		t.Fatalf("verificación guardada: %+v", *v)
	}
	if containsAny(fmt.Sprintf("%+v", *v), tok) {
		t.Fatal("el token en claro está en lo guardado")
	}
	msg := f.mailer.sent[0]
	if msg.Subject != "Verifica tu email" || msg.To != "ana@mail.com" {
		t.Errorf("asunto/destino: %q %q", msg.Subject, msg.To)
	}
	link := "https://app.test/verificar-email#" + tok
	if !strings.Contains(msg.Text, link) || !strings.Contains(msg.HTML, `href="`+link+`"`) {
		t.Errorf("falta el enlace:\n%s", msg.Text)
	}
}

func TestSendVerification_smtpDisabledAndErrorsDoNothingVisible(t *testing.T) {
	f := newVerFix()
	f.svc.Mailer = nil
	u := f.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f.svc.SendVerification(u)
	f.run.Flush()
	if len(f.st.verifs) != 0 {
		t.Errorf("sin SMTP no se emite token: %d", len(f.st.verifs))
	}
	f2 := newVerFix()
	f2.mailer.err = errors.New("smtp caído")
	u2 := f2.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f2.svc.SendVerification(u2)
	f2.run.Flush() // no entra en pánico
}

// D7: verificar marca el email y consume el token; el reuso es idempotente (204)
// porque el usuario ya está verificado; un token inválido o vencido es el texto
// uniforme.
func TestVerify(t *testing.T) {
	f := newVerFix()
	u := f.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f.svc.SendVerification(u)
	f.run.Flush()
	tok := f.token(t)

	// Forma inválida o desconocido -> texto uniforme.
	for _, bad := range []string{"", "x", strings.Repeat("A", 42) + "\x00", strings.Repeat("A", 43)} {
		f15WantMsg(t, f.svc.Verify(bad), domain.ErrValidation, "Enlace invalido o vencido")
	}
	if f.st.users["u-ana"].EmailVerifiedAt != nil {
		t.Fatal("un token inválido verificó la cuenta")
	}
	if err := f.svc.Verify(tok); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if f.st.users["u-ana"].EmailVerifiedAt == nil {
		t.Fatal("no marcó email_verified_at")
	}
	if err := f.svc.Verify(tok); err != nil {
		t.Fatalf("el reuso con el usuario ya verificado es idempotente: %v", err)
	}
}

func TestVerify_expiredAndUsedUnverifiedAreInvalid(t *testing.T) {
	f := newVerFix()
	u := f.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f.svc.SendVerification(u)
	f.run.Flush()
	tok := f.token(t)
	f.st.clock.Add(24 * time.Hour)
	f15WantMsg(t, f.svc.Verify(tok), domain.ErrValidation, "Enlace invalido o vencido")
	if f.st.users["u-ana"].EmailVerifiedAt != nil {
		t.Fatal("un token vencido verificó la cuenta")
	}
	// Vencido pero la cuenta ya está verificada por otro medio: sigue siendo
	// inválido (el 204 idempotente es solo para un token YA USADO).
	f.st.users["u-ana"].EmailVerifiedAt = &f15T0
	f15WantMsg(t, f.svc.Verify(tok), domain.ErrValidation, "Enlace invalido o vencido")
}

func TestVerify_thirtyParallelIsIdempotent(t *testing.T) {
	f := newVerFix()
	u := f.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f.svc.SendVerification(u)
	f.run.Flush()
	tok := f.token(t)
	var wg sync.WaitGroup
	errs := make([]error, 30)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = f.svc.Verify(tok) }(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("canje %d: %v", i, err)
		}
	}
}

// D7: reenvío. Ya verificado -> 409 con el texto del contrato; menos de un
// minuto -> 429 con el suyo; si no, 202 (nil) y el correo sale por el runner.
func TestResend(t *testing.T) {
	f := newVerFix()
	f.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f.st.addUser("u-bea", "Bea", "bea@mail.com", true)

	err := f.svc.Resend("u-bea")
	f15WantMsg(t, err, domain.ErrAlreadyExists, "El email ya esta verificado")

	if err := f.svc.Resend("u-ana"); err != nil {
		t.Fatalf("primer reenvío: %v", err)
	}
	// El token se emite al responder; el correo sale por el runner.
	if len(f.st.verifs) != 1 || f.mailer.count() != 0 || f.run.Pending() != 1 {
		t.Fatalf("verifs=%d mails=%d pending=%d", len(f.st.verifs), f.mailer.count(), f.run.Pending())
	}
	f.run.Flush()
	tok := f.token(t)
	if f.st.verifs[0].TokenHash != domain.HashRecoveryToken(tok) {
		t.Fatal("hash guardado distinto del token enviado")
	}

	f.st.clock.Add(59 * time.Second)
	err = f.svc.Resend("u-ana")
	f15WantMsg(t, err, domain.ErrTooManyAttempts, "Espera un minuto antes de pedir otro correo")
	if len(f.st.verifs) != 1 || f.run.Pending() != 0 {
		t.Fatal("un reenvío frenado no debe emitir ni enviar")
	}
	f.st.clock.Add(time.Second)
	if err := f.svc.Resend("u-ana"); err != nil {
		t.Fatalf("a los 60 s ya se puede: %v", err)
	}
	f.run.Flush()
	if f.mailer.count() != 2 {
		t.Fatalf("correos: %d", f.mailer.count())
	}
	// Usuario inexistente: error de dominio (401), no un 500.
	if err := f.svc.Resend("nadie"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("usuario inexistente: %v", err)
	}
}

// El primer correo del registro cuenta para el límite del reenvío.
func TestResend_registrationMailCountsTowardsTheLimit(t *testing.T) {
	f := newVerFix()
	u := f.st.addUser("u-ana", "Ana", "ana@mail.com", false)
	f.svc.SendVerification(u)
	f.run.Flush()
	f.st.clock.Add(10 * time.Second)
	f15WantMsg(t, f.svc.Resend("u-ana"), domain.ErrTooManyAttempts, "Espera un minuto antes de pedir otro correo")
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
	*fakeUsers
	writes int
}

func (u *countingUsers) Save(x domain.User) error {
	u.writes++
	return u.fakeUsers.Save(x)
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
	f15WantMsg(t, err, domain.ErrAlreadyExists, hasAccountText)
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
	users := &countingUsers{fakeUsers: env.users}
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
	users := &countingUsers{fakeUsers: env.users}
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
	users := &countingUsers{fakeUsers: env.users}
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
	env.mems.byKey[ocupada.ID+"|s2"] = m
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
