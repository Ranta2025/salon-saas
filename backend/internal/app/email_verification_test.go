package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
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

// --- D8: la invitación toma el cascarón de una cuenta no verificada ---

// shellInvitations envuelve el fake de invitaciones con la toma del cascarón
// (F15, D8) con la MISMA semántica que el adaptador Postgres: solo si la cuenta
// existe, NO está verificada y no tiene NINGUNA membresía activa. Guarda también
// el `password_changed_at` (lo que mata las sesiones del impostor por D4).
type shellInvitations struct {
	*fakeInvitations
	changedAt map[string]*time.Time
	taken     int
}

func (f *shellInvitations) InInvitationTx(fn func(tx ports.InvitationTx) error) error {
	// password_changed_at vive fuera del fake de invitaciones, así que se
	// restaura a mano cuando la transacción falla (en Postgres se revierte con el
	// UPDATE de users).
	before := map[string]*time.Time{}
	for k, v := range f.changedAt {
		before[k] = v
	}
	err := f.fakeInvitations.InInvitationTx(func(ports.InvitationTx) error {
		return fn(shellTx{fakeInvitations: f.fakeInvitations, parent: f})
	})
	if err != nil {
		f.changedAt = before
	}
	return err
}

type shellTx struct {
	*fakeInvitations
	parent *shellInvitations
}

func (t shellTx) TakeOverShell(userID, name, passHash string) (bool, error) {
	u, err := t.users.FindByID(userID)
	if err != nil {
		return false, nil
	}
	if u.EmailVerifiedAt != nil {
		return false, nil
	}
	for _, m := range t.mems.byKey {
		if m.UserID == userID && m.Active() {
			return false, nil
		}
	}
	at := t.now
	u.Name = name
	u.PasswordHash = passHash
	u.EmailVerifiedAt = &at
	if err := t.users.Save(u); err != nil {
		return false, err
	}
	t.parent.changedAt[userID] = &at
	t.parent.taken++
	return true, nil
}

var (
	_ ports.InvitationTxRunner = (*shellInvitations)(nil)
	_ ports.ShellTakeoverTx    = shellTx{}
)

// withShell devuelve el servicio público cableado con la transacción del cascarón.
func (e *inviteEnv) withShell() (*InvitationPublicService, *shellInvitations) {
	shell := &shellInvitations{fakeInvitations: e.inv, changedAt: map[string]*time.Time{}}
	pub := e.pub
	pub.Tx = shell
	return &pub, shell
}

// addShell registra la cuenta que dejó quien usó un correo ajeno: existe, sin
// verificar y sin ninguna membresía.
func (e *inviteEnv) addShell(email string) domain.User {
	u, err := domain.NewUser("Impostor", email, "h:clave-del-impostor")
	if err != nil {
		panic(err)
	}
	u.ID = "u-shell-" + strings.Split(email, "@")[0]
	if err := e.users.Save(u); err != nil {
		panic(err)
	}
	return u
}

const hasAccountText = "Ya tenés cuenta: iniciá sesión para aceptar la invitación"

// D8: el cascarón se toma en la MISMA transacción del canje (contraseña nueva,
// verificada y password_changed_at para matar las sesiones del impostor) y la
// membresía de la invitación queda creada.
func TestAcceptNew_takesOverAnUnverifiedShell(t *testing.T) {
	env := newInviteEnv(t)
	shellUser := env.addShell("lucia@mail.com")
	pub, shell := env.withShell()
	_, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)

	jwt, err := pub.AcceptNew(token, "Lucía Nueva", "claveNueva1")
	if err != nil {
		t.Fatalf("AcceptNew: %v", err)
	}
	if jwt == "" {
		t.Fatal("la sesión de la cuenta tomada se emite después del commit")
	}
	got, err := env.users.FindByID(shellUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Lucía Nueva" || got.PasswordHash != "h:claveNueva1" {
		t.Fatalf("la cuenta no se tomó: %+v", got)
	}
	if got.EmailVerifiedAt == nil {
		t.Error("la cuenta tomada tiene que quedar verificada")
	}
	if shell.changedAt[shellUser.ID] == nil {
		t.Error("password_changed_at tiene que quedar puesto (las sesiones del impostor mueren, D4)")
	}
	if shell.taken != 1 {
		t.Errorf("tomas: %d", shell.taken)
	}
	// La membresía de la invitación, con su rol, y la invitación aceptada.
	m, err := env.mems.FindMember(shellUser.ID, "s1")
	if err != nil || m.Role != domain.RoleProfessional {
		t.Fatalf("membresía: %+v %v", m, err)
	}
	if len(env.inv.rows) != 1 {
		t.Fatalf("invitaciones: %d", len(env.inv.rows))
	}
	for _, row := range env.inv.rows {
		if row.AcceptedAt == nil || row.AcceptedBy != shellUser.ID {
			t.Fatalf("la invitación no quedó aceptada por la cuenta tomada: %+v", row)
		}
	}
	// Un segundo canje del mismo token ya no sirve (404 genérico).
	if _, err := pub.AcceptNew(token, "Otra", "claveNueva1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el token usado: %v", err)
	}
}

// D8: una cuenta VERIFICADA sigue recibiendo el 409 de siempre, sin tocar nada.
func TestAcceptNew_verifiedAccountKeepsThe409(t *testing.T) {
	env := newInviteEnv(t)
	u, _ := domain.NewUser("Ana", "ana@mail.com", "h:clave-ana")
	u.ID = "u-ana"
	at := env.now
	u.EmailVerifiedAt = &at
	if err := env.users.Save(u); err != nil {
		t.Fatal(err)
	}
	pub, shell := env.withShell()
	_, token := env.invite(t, "ana@mail.com", domain.RoleReception)

	_, err := pub.AcceptNew(token, "Ana Otra", "claveNueva1")
	f15WantMsg(t, err, domain.ErrAlreadyExists, hasAccountText)
	if shell.taken != 0 || shell.changedAt[u.ID] != nil {
		t.Fatal("una cuenta verificada no se toma")
	}
	got, _ := env.users.FindByID(u.ID)
	if got.Name != "Ana" || got.PasswordHash != "h:clave-ana" {
		t.Fatalf("la cuenta cambió: %+v", got)
	}
	for _, row := range env.inv.rows {
		if row.AcceptedAt != nil {
			t.Fatal("una toma rechazada no puede aceptar la invitación")
		}
	}
}

// D8: con una membresía ACTIVA (en cualquier salón) la cuenta no es un cascarón;
// una membresía dada de baja no cuenta.
func TestAcceptNew_shellWithActiveMembershipKeepsThe409(t *testing.T) {
	env := newInviteEnv(t)
	shellUser := env.addShell("lucia@mail.com")
	// Membresía activa en OTRO salón: alcanza para no ser un cascarón.
	if err := env.mems.Save(domain.Membership{UserID: shellUser.ID, SalonID: "s2", Role: domain.RoleReception}); err != nil {
		t.Fatal(err)
	}
	pub, shell := env.withShell()
	_, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)

	_, err := pub.AcceptNew(token, "Lucía Nueva", "claveNueva1")
	f15WantMsg(t, err, domain.ErrAlreadyExists, hasAccountText)
	if shell.taken != 0 {
		t.Fatal("una cuenta con membresía activa no se toma")
	}
	got, _ := env.users.FindByID(shellUser.ID)
	if got.EmailVerifiedAt != nil || got.PasswordHash != "h:clave-del-impostor" {
		t.Fatalf("la cuenta cambió: %+v", got)
	}
	// Dada de baja (ended_at) = CERO activas: ahora sí es un cascarón.
	m, _ := env.mems.FindMember(shellUser.ID, "s2")
	m.EndedAt = &env.now
	env.mems.byKey[shellUser.ID+"|s2"] = m
	if _, err := pub.AcceptNew(token, "Lucía Nueva", "claveNueva1"); err != nil {
		t.Fatalf("sin membresías activas se toma: %v", err)
	}
}

// D8: la toma es parte de la MISMA transacción: si el último paso falla, no queda
// ni la toma ni la invitación aceptada.
func TestAcceptNew_shellTakeoverIsAtomic(t *testing.T) {
	env := newInviteEnv(t)
	shellUser := env.addShell("lucia@mail.com")
	pub, shell := env.withShell()
	_, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	env.inv.failMembership = true

	if _, err := pub.AcceptNew(token, "Lucía Nueva", "claveNueva1"); err == nil {
		t.Fatal("la membresía falla: el canje entero tiene que fallar")
	}
	got, _ := env.users.FindByID(shellUser.ID)
	if got.EmailVerifiedAt != nil || got.PasswordHash != "h:clave-del-impostor" || shell.changedAt[shellUser.ID] != nil {
		t.Fatalf("la toma quedó a medias: %+v %+v", got, shell.changedAt)
	}
	for _, row := range env.inv.rows {
		if row.AcceptedAt != nil {
			t.Fatal("la invitación quedó aceptada con la transacción revertida")
		}
	}
}

// D7: una cuenta creada por invitación nace YA verificada (el token llegó a ese
// correo). Es la rama que más se usa y no puede quedar en NULL.
func TestAcceptNew_newAccountIsBornVerified(t *testing.T) {
	env := newInviteEnv(t)
	pub, _ := env.withShell()
	_, token := env.invite(t, "nueva@mail.com", domain.RoleProfessional)

	if _, err := pub.AcceptNew(token, "Nueva", "claveNueva1"); err != nil {
		t.Fatalf("AcceptNew: %v", err)
	}
	u, err := env.users.FindByEmail("nueva@mail.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.EmailVerifiedAt == nil {
		t.Fatal("la cuenta creada por invitación nace verificada")
	}
	if u.PasswordHash != "h:claveNueva1" {
		t.Errorf("clave: %q", u.PasswordHash)
	}
}

// NOTA: la carrera de dos canjes del MISMO token está cubierta por
// `TestF15_TakeOverShell` y por la prueba de concurrencia contra Postgres; acá no
// se repite porque el fake de invitaciones (`invitations_test.go`, preexistente)
// no es seguro para goroutines y este archivo no lo puede tocar.
