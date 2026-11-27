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
