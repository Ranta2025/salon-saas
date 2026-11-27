package auth

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	auditsvc "salon-saas/internal/audit"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

type tfEnv struct {
	svc    TwoFactorService
	repo   *fakeTwoFactorRepo
	clock  *fakeClock
	audit  *testutil.FakeAudit
	users  *testutil.FakeUsers
	mems   *testutil.FakeMemberships
	userID string
	box    domain.TOTPSecretBox
}

func newTFEnv(t *testing.T) *tfEnv {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1_700_000_010, 0).UTC()}
	users := &testutil.FakeUsers{}
	u, err := domain.NewUser("Ana Pérez", "ana@mail.com", "h:clave-secreta-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = users.Save(u)
	mems := &testutil.FakeMemberships{}
	_ = mems.Save(domain.Membership{UserID: u.ID, SalonID: "s-1", Role: domain.RoleOwner})
	_ = mems.Save(domain.Membership{UserID: u.ID, SalonID: "s-2", Role: domain.RoleAdmin})
	ended := clock.Now()
	_ = mems.Save(domain.Membership{UserID: u.ID, SalonID: "s-baja", Role: domain.RoleReception})
	m := mems.ByKey[u.ID+"|s-baja"]
	m.EndedAt = &ended
	mems.ByKey[u.ID+"|s-baja"] = m
	box, err := domain.NewTOTPSecretBox("jwt-secret-de-prueba")
	if err != nil {
		t.Fatal(err)
	}
	repo := newFakeTwoFactorRepo(clock.Now)
	audit := &testutil.FakeAudit{}
	return &tfEnv{
		svc: TwoFactorService{
			Repo: repo, Users: users, Hasher: testutil.FakeHasher{}, Tokens: testutil.FakeTokens{},
			Challenges: fakeChallenges{}, Box: box, Memberships: mems,
			Audit: auditsvc.AuditService{Events: audit, Users: users}, Now: clock.Now,
		},
		repo: repo, clock: clock, audit: audit, users: users, mems: mems, userID: u.ID, box: box,
	}
}

// secret abre el secreto guardado (el test hace de app autenticadora).
func (e *tfEnv) secret(t *testing.T) []byte {
	t.Helper()
	st, err := e.repo.Find(e.userID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.box.Open(st.SecretEnc, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// code da el código TOTP del paso actual + delta.
func (e *tfEnv) code(t *testing.T, delta int64) string {
	t.Helper()
	return domain.TOTPCode(e.secret(t), domain.TOTPStepAt(e.clock.Now())+delta, domain.TOTPDigits)
}

// enabled deja el 2FA activo y devuelve los códigos de recuperación. Avanza el
// reloj un paso para que el código con el que se activó no estorbe a los tests.
func (e *tfEnv) enabled(t *testing.T) []string {
	t.Helper()
	if _, err := e.svc.Setup(e.userID); err != nil {
		t.Fatalf("setup: %v", err)
	}
	codes, err := e.svc.Enable(e.userID, e.code(t, 0))
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	e.clock.Add(2 * domain.TOTPPeriodSeconds * time.Second)
	e.audit.Mu.Lock()
	e.audit.Rows = nil
	e.audit.Mu.Unlock()
	return codes
}

func tfWantMsg(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("esperaba error %q, no hubo", text)
	}
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("got %v (%T), want kind=%v text=%q", err, err, kind, text)
	}
}

func TestTwoFactorStatusAndSetup(t *testing.T) {
	e := newTFEnv(t)
	if on, err := e.svc.Status(e.userID); err != nil || on {
		t.Fatalf("sin setup: %v %v", on, err)
	}
	out, err := e.svc.Setup(e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Secret) != 32 || strings.ContainsAny(out.Secret, "=abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("base32 sin relleno esperado: %q", out.Secret)
	}
	if want := "otpauth://totp/Salon:ana@mail.com?secret=" + out.Secret + "&issuer=Salon"; out.OTPAuthURL != want {
		t.Errorf("url %q want %q", out.OTPAuthURL, want)
	}
	if on, _ := e.svc.Status(e.userID); on {
		t.Error("pendiente NO es activado")
	}
	// El secreto solo se guarda CIFRADO.
	st, _ := e.repo.Find(e.userID)
	if bytes.Contains(st.SecretEnc, []byte(out.Secret)) || len(st.SecretEnc) != 12+20+16 {
		t.Errorf("el secreto debe estar cifrado: len=%d", len(st.SecretEnc))
	}
	// Un setup nuevo REEMPLAZA el pendiente.
	out2, err := e.svc.Setup(e.userID)
	if err != nil || out2.Secret == out.Secret {
		t.Fatalf("reemplazo: %v", err)
	}
	// Ya habilitado = 409 con el texto exacto y no pisa nada.
	if _, err := e.svc.Enable(e.userID, e.code(t, 0)); err != nil {
		t.Fatal(err)
	}
	before, _ := e.repo.Find(e.userID)
	_, err = e.svc.Setup(e.userID)
	tfWantMsg(t, err, domain.ErrAlreadyExists, "La verificacion en dos pasos ya esta activada")
	after, _ := e.repo.Find(e.userID)
	if !bytes.Equal(before.SecretEnc, after.SecretEnc) || !after.Enabled {
		t.Error("el setup rechazado pisó el 2FA activo")
	}
	if on, _ := e.svc.Status(e.userID); !on {
		t.Error("debería estar activado")
	}
}

func TestTwoFactorStatusAndSetupErrors(t *testing.T) {
	e := newTFEnv(t)
	e.repo.findErr = errTwoFactorFindDown
	if _, err := e.svc.Status(e.userID); !errors.Is(err, errTwoFactorFindDown) {
		t.Errorf("Status debe propagar el error de la base: %v", err)
	}
	if _, err := e.svc.Setup(e.userID); !errors.Is(err, errTwoFactorFindDown) {
		t.Errorf("Setup debe propagar el error de la base: %v", err)
	}
	if _, err := e.svc.Setup("no-existe"); err == nil {
		t.Error("usuario inexistente no puede hacer setup")
	}
}

func TestTwoFactorEnable(t *testing.T) {
	e := newTFEnv(t)
	_, err := e.svc.Enable(e.userID, "123456")
	tfWantMsg(t, err, domain.ErrAlreadyExists, "Primero genera el codigo con la configuracion")

	if _, err := e.svc.Setup(e.userID); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "000000", "12345", "ABCDE-23456", e.code(t, 5)} {
		_, err := e.svc.Enable(e.userID, bad)
		tfWantMsg(t, err, domain.ErrValidation, "Codigo incorrecto")
	}
	if on, _ := e.svc.Status(e.userID); on {
		t.Fatal("un código malo no activa")
	}
	if len(e.audit.Rows) != 0 {
		t.Fatal("sin activación no hay auditoría")
	}

	good := e.code(t, 0)
	codes, err := e.svc.Enable(e.userID, " "+good[:3]+" "+good[3:]+" ")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if len(codes) != 8 {
		t.Fatalf("8 códigos, got %d", len(codes))
	}
	st, _ := e.repo.Find(e.userID)
	if !st.Enabled || st.LastStep != domain.TOTPStepAt(e.clock.Now()) {
		t.Errorf("estado: %+v", st)
	}
	// Se guarda solo el hash; el código en claro no está en ningún lado.
	stored := e.repo.codes[e.userID]
	if len(stored) != 8 {
		t.Fatalf("hashes guardados: %d", len(stored))
	}
	for _, c := range codes {
		n, ok := domain.NormalizeRecoveryCode(c)
		if !ok {
			t.Fatalf("código mal formado %q", c)
		}
		if _, ok := stored[domain.HashRecoveryCode(n)]; !ok {
			t.Errorf("falta el hash de %q", c)
		}
		if _, leaked := stored[n]; leaked || stored[c] {
			t.Errorf("el código está en claro: %q", c)
		}
	}
	// Activar otra vez: 409 y no entrega códigos nuevos.
	_, err = e.svc.Enable(e.userID, e.code(t, 1))
	tfWantMsg(t, err, domain.ErrAlreadyExists, "La verificacion en dos pasos ya esta activada")
	// El código con el que se activó NO sirve para entrar (anti-replay).
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	_, err = e.svc.VerifyLogin(ch, good)
	tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
}

func TestTwoFactorEnableAuditsEachActiveSalon(t *testing.T) {
	e2 := newTFEnv(t)
	if _, err := e2.svc.Setup(e2.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.svc.Enable(e2.userID, e2.code(t, 0)); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, ev := range e2.audit.Rows {
		if ev.ActorUserID != e2.userID || ev.ActorName != "Ana Pérez" || ev.EntityID != "" {
			t.Errorf("evento: %+v", ev)
		}
		got[ev.SalonID] = ev.Action
	}
	if len(e2.audit.Rows) != 2 || got["s-1"] != "auth.2fa_enabled" || got["s-2"] != "auth.2fa_enabled" {
		t.Errorf("un evento por salón ACTIVO (no el dado de baja): %+v", e2.audit.Rows)
	}
}

func TestTwoFactorAuditBestEffort(t *testing.T) {
	e := newTFEnv(t)
	e.audit.AppendErr = testutil.ErrAuditDown
	if _, err := e.svc.Setup(e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Enable(e.userID, e.code(t, 0)); err != nil {
		t.Fatalf("una caída de la auditoría NO rompe el enable: %v", err)
	}
	if on, _ := e.svc.Status(e.userID); !on {
		t.Error("debería quedar activado")
	}
	if e.audit.AppendCalls == 0 {
		t.Error("tenía que intentar escribir")
	}
	e.clock.Add(90 * time.Second)
	if err := e.svc.Disable(e.userID, "clave-secreta-1", e.code(t, 0)); err != nil {
		t.Fatalf("una caída de la auditoría NO rompe el disable: %v", err)
	}
}

func TestTwoFactorNoSalonsNoAuditEvent(t *testing.T) {
	e := newTFEnv(t)
	e.mems.ByKey = map[string]domain.Membership{}
	if _, err := e.svc.Setup(e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Enable(e.userID, e.code(t, 0)); err != nil {
		t.Fatal(err)
	}
	if e.audit.AppendCalls != 0 {
		t.Errorf("sin salones no se escribe evento: %d", e.audit.AppendCalls)
	}
}

func TestTwoFactorChallengeFor(t *testing.T) {
	e := newTFEnv(t)
	if ch, req, err := e.svc.ChallengeFor(e.userID); ch != "" || req || err != nil {
		t.Fatalf("sin fila: %q %v %v", ch, req, err)
	}
	if _, err := e.svc.Setup(e.userID); err != nil {
		t.Fatal(err)
	}
	if ch, req, err := e.svc.ChallengeFor(e.userID); ch != "" || req || err != nil {
		t.Fatalf("pendiente (enabled=false) = login de siempre: %q %v %v", ch, req, err)
	}
	if _, err := e.svc.Enable(e.userID, e.code(t, 0)); err != nil {
		t.Fatal(err)
	}
	ch, req, err := e.svc.ChallengeFor(e.userID)
	if ch != "ch:"+e.userID || !req || err != nil {
		t.Fatalf("activado: %q %v %v", ch, req, err)
	}
	// Una caída de la base NO deja pasar sin 2FA: falla cerrado.
	e.repo.findErr = errTwoFactorFindDown
	ch, req, err = e.svc.ChallengeFor(e.userID)
	if !errors.Is(err, errTwoFactorFindDown) || ch != "" || req {
		t.Errorf("debe devolver el error: %q %v %v", ch, req, err)
	}
}

func TestTwoFactorVerifyLoginSuccessAndUniformErrors(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	uniform := func(err error) {
		t.Helper()
		tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
	}
	// Desafío inválido / de otra clase (un token de sesión) / vacío.
	for _, bad := range []string{"", "ch:", "tok:" + e.userID, "basura"} {
		_, err := e.svc.VerifyLogin(bad, e.code(t, 0))
		uniform(err)
	}
	if e.repo.beginCalls != 0 {
		t.Error("un desafío inválido no toca la base")
	}
	// Desafío de un usuario sin 2FA activo: mismo error.
	_, err := e.svc.VerifyLogin("ch:otro", "123456")
	uniform(err)
	// Código malo.
	_, err = e.svc.VerifyLogin(ch, "000000")
	uniform(err)
	// Bueno (con espacios): token de sesión.
	good := e.code(t, 0)
	tok, err := e.svc.VerifyLogin(ch, good[:3]+" "+good[3:])
	if err != nil || tok != "tok:"+e.userID {
		t.Fatalf("login 2fa: %q %v", tok, err)
	}
	// El acierto reinició el contador.
	if r := e.repo.rows[e.userID]; r.failed != 0 || !r.lockedUntil.IsZero() {
		t.Errorf("contador tras acierto: %+v", r)
	}
	// Anti-replay: el mismo código otra vez falla, y cuenta como fallo.
	_, err = e.svc.VerifyLogin(ch, good)
	uniform(err)
	if r := e.repo.rows[e.userID]; r.failed != 1 {
		t.Errorf("el replay cuenta como fallo: failed=%d", r.failed)
	}
}

func TestTwoFactorVerifyLoginWindow(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	if _, err := e.svc.VerifyLogin(ch, e.code(t, -1)); err != nil {
		t.Errorf("el paso anterior vale: %v", err)
	}
	// El paso siguiente (> last_step) también vale; pero ya no el actual (<= last_step).
	if _, err := e.svc.VerifyLogin(ch, e.code(t, 1)); err != nil {
		t.Errorf("el paso siguiente vale: %v", err)
	}
	if _, err := e.svc.VerifyLogin(ch, e.code(t, 0)); err == nil {
		t.Error("un paso anterior a last_step es replay")
	}
	if _, err := e.svc.VerifyLogin(ch, e.code(t, 2)); err == nil {
		t.Error("fuera de la ventana")
	}
}

func TestTwoFactorRecoveryCodeSingleUse(t *testing.T) {
	e := newTFEnv(t)
	codes := e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	tok, err := e.svc.VerifyLogin(ch, strings.ToLower(codes[0]))
	if err != nil || tok == "" {
		t.Fatalf("código de recuperación: %v", err)
	}
	_, err = e.svc.VerifyLogin(ch, codes[0])
	tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
	// Otro código sigue sirviendo, sin guion también.
	if _, err := e.svc.VerifyLogin(ch, strings.ReplaceAll(codes[1], "-", "")); err != nil {
		t.Errorf("otro código sirve: %v", err)
	}
	// Uno inventado (con la forma) falla.
	_, err = e.svc.VerifyLogin(ch, "AAAAA-AAAAA")
	tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
}

func TestTwoFactorLockoutAfterFiveFailures(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	for i := 0; i < 5; i++ {
		_, err := e.svc.VerifyLogin(ch, "000000")
		tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
	}
	// Bloqueado: 429 AUN con el código correcto, y sin mirar el código.
	granted := e.repo.beginGranted
	_, err := e.svc.VerifyLogin(ch, e.code(t, 0))
	tfWantMsg(t, err, domain.ErrTooManyAttempts, "Demasiados intentos. Proba de nuevo en unos minutos")
	_, err = e.svc.VerifyLogin(ch, "000000")
	tfWantMsg(t, err, domain.ErrTooManyAttempts, "Demasiados intentos. Proba de nuevo en unos minutos")
	if e.repo.beginGranted != granted {
		t.Error("bloqueado: no se reserva ni se compara ningún código")
	}
	// Sigue bloqueado casi 15 minutos y se libera pasados.
	e.clock.Add(14 * time.Minute)
	_, err = e.svc.VerifyLogin(ch, e.code(t, 0))
	tfWantMsg(t, err, domain.ErrTooManyAttempts, "Demasiados intentos. Proba de nuevo en unos minutos")
	e.clock.Add(2 * time.Minute)
	if _, err := e.svc.VerifyLogin(ch, e.code(t, 0)); err != nil {
		t.Fatalf("pasado el bloqueo el código correcto entra: %v", err)
	}
	// Pasado el bloqueo el contador arranca de cero: no se vuelve a bloquear al primer fallo.
	e.clock.Add(time.Minute)
	_, err = e.svc.VerifyLogin(ch, "000000")
	tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
}

func TestTwoFactorSuccessResetsCounter(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			if _, err := e.svc.VerifyLogin(ch, "000000"); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("ronda %d intento %d: %v", round, i, err)
			}
		}
		e.clock.Add(31 * time.Second)
		if _, err := e.svc.VerifyLogin(ch, e.code(t, 0)); err != nil {
			t.Fatalf("ronda %d: 4 fallos + acierto no debe bloquear: %v", round, err)
		}
		e.clock.Add(61 * time.Second)
	}
}

func TestTwoFactorMalformedCodesCountAsFailures(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	for _, bad := range []string{"", "abc", strings.Repeat("9", 200), "12345", "ZZZZZ-ZZZZZZ"} {
		_, err := e.svc.VerifyLogin(ch, bad)
		tfWantMsg(t, err, domain.ErrUnauthorized, "Codigo incorrecto o desafio vencido")
	}
	_, err := e.svc.VerifyLogin(ch, e.code(t, 0))
	tfWantMsg(t, err, domain.ErrTooManyAttempts, "Demasiados intentos. Proba de nuevo en unos minutos")
}

func TestTwoFactorTamperedCiphertextFailsClosed(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	e.repo.rows[e.userID].secretEnc[20] ^= 0xff
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	tok, err := e.svc.VerifyLogin(ch, "123456")
	if err == nil || tok != "" {
		t.Fatalf("un secreto manipulado no puede dar sesión: %q %v", tok, err)
	}
	if errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("es un fallo del servidor (500), no un 401: %v", err)
	}
	if strings.Contains(err.Error(), "123456") {
		t.Error("el error no debe llevar el código")
	}
}

func TestTwoFactorDisable(t *testing.T) {
	e := newTFEnv(t)
	// Sin 2FA activo: 409 (después de la contraseña).
	err := e.svc.Disable(e.userID, "clave-secreta-1", "123456")
	tfWantMsg(t, err, domain.ErrAlreadyExists, "La verificacion en dos pasos no esta activada")

	codes := e.enabled(t)
	// Contraseña mala: 401 ANTES de mirar el código, sin gastar intentos.
	err = e.svc.Disable(e.userID, "mala", e.code(t, 0))
	tfWantMsg(t, err, domain.ErrUnauthorized, "Credenciales invalidas")
	if e.repo.beginCalls != 1 { // la del Disable inicial sin 2FA
		t.Errorf("contraseña mala no cuenta intentos: beginCalls=%d", e.repo.beginCalls)
	}
	// Usuario inexistente: mismo 401.
	err = e.svc.Disable("fantasma", "clave-secreta-1", "123456")
	tfWantMsg(t, err, domain.ErrUnauthorized, "Credenciales invalidas")
	// Código malo: 400.
	for _, bad := range []string{"000000", "", "AAAAA-AAAAA", "xx"} {
		err = e.svc.Disable(e.userID, "clave-secreta-1", bad)
		tfWantMsg(t, err, domain.ErrValidation, "Codigo incorrecto")
	}
	if on, _ := e.svc.Status(e.userID); !on {
		t.Fatal("un código malo no desactiva")
	}
	if len(e.audit.Rows) != 0 {
		t.Fatal("sin desactivar no hay auditoría")
	}
	// Con un código de recuperación (una sola vez).
	if err := e.svc.Disable(e.userID, "clave-secreta-1", codes[3]); err != nil {
		t.Fatalf("disable con recuperación: %v", err)
	}
	if _, err := e.repo.Find(e.userID); !errors.Is(err, domain.ErrNotFound) {
		t.Error("se borra el secreto")
	}
	if len(e.repo.codes[e.userID]) != 0 {
		t.Error("se borran los códigos de recuperación")
	}
	if on, _ := e.svc.Status(e.userID); on {
		t.Error("desactivado")
	}
	got := map[string]string{}
	for _, ev := range e.audit.Rows {
		got[ev.SalonID] = ev.Action
	}
	if len(e.audit.Rows) != 2 || got["s-1"] != "auth.2fa_disabled" || got["s-2"] != "auth.2fa_disabled" {
		t.Errorf("auditoría: %+v", e.audit.Rows)
	}
	// Y el login vuelve a ser el de siempre.
	if _, req, _ := e.svc.ChallengeFor(e.userID); req {
		t.Error("sin 2FA no hay desafío")
	}
}

func TestTwoFactorDisableWithTOTPAndLockout(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	if err := e.svc.Disable(e.userID, "clave-secreta-1", e.code(t, 0)); err != nil {
		t.Fatalf("disable con TOTP: %v", err)
	}
	e = newTFEnv(t)
	e.enabled(t)
	for i := 0; i < 5; i++ {
		_ = e.svc.Disable(e.userID, "clave-secreta-1", "000000")
	}
	err := e.svc.Disable(e.userID, "clave-secreta-1", e.code(t, 0))
	tfWantMsg(t, err, domain.ErrTooManyAttempts, "Demasiados intentos. Proba de nuevo en unos minutos")
	if on, _ := e.svc.Status(e.userID); !on {
		t.Error("bloqueado no desactiva")
	}
}

// 30 intentos paralelos con códigos malos: nunca más de 5 verificaciones, el
// contador queda en 5 y el bloqueo puesto (el invariante lo da el UPDATE atómico).
func TestTwoFactorParallelWrongCodes(t *testing.T) {
	e := newTFEnv(t)
	e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	var wg sync.WaitGroup
	var mu sync.Mutex
	unauth, locked, other := 0, 0, 0
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.VerifyLogin(ch, "000000")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, domain.ErrTooManyAttempts):
				locked++
			case errors.Is(err, domain.ErrUnauthorized):
				unauth++
			default:
				other++
			}
		}()
	}
	wg.Wait()
	if unauth != 5 || locked != 25 || other != 0 {
		t.Errorf("unauth=%d locked=%d other=%d (esperado 5/25/0)", unauth, locked, other)
	}
	if e.repo.beginGranted > 5 {
		t.Errorf("más de 5 verificaciones: %d", e.repo.beginGranted)
	}
	if r := e.repo.rows[e.userID]; r.failed != 5 || r.lockedUntil.IsZero() {
		t.Errorf("estado: %+v", r)
	}
}

func TestTwoFactorParallelSameCodeOneWinner(t *testing.T) {
	e := newTFEnv(t)
	codes := e.enabled(t)
	ch, _, _ := e.svc.ChallengeFor(e.userID)
	run := func(code string) int {
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.svc.VerifyLogin(ch, code); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		return ok
	}
	if n := run(e.code(t, 0)); n != 1 {
		t.Errorf("el mismo TOTP usado en paralelo: %d aciertos, esperado 1", n)
	}
	e.clock.Add(time.Hour) // reset del bloqueo posible
	e.repo.rows[e.userID].failed, e.repo.rows[e.userID].lockedUntil = 0, time.Time{}
	if n := run(codes[0]); n != 1 {
		t.Errorf("el mismo código de recuperación en paralelo: %d aciertos, esperado 1", n)
	}
}

func TestAuthenticateOldPathUnchangedWithoutTwoFactor(t *testing.T) {
	e := newTFEnv(t)
	auth := AuthService{Users: e.users, Hasher: testutil.FakeHasher{}, Tokens: testutil.FakeTokens{}, TwoFactor: e.svc}
	// Sin fila: login de siempre.
	out, err := auth.Authenticate("ana@mail.com", "clave-secreta-1")
	if err != nil || out.TwoFactorRequired || out.Token != "tok:"+e.userID || out.Challenge != "" {
		t.Fatalf("sin 2FA: %+v %v", out, err)
	}
	// Pendiente (enabled=false): igual.
	if _, err := e.svc.Setup(e.userID); err != nil {
		t.Fatal(err)
	}
	out, err = auth.Authenticate("ana@mail.com", "clave-secreta-1")
	if err != nil || out.TwoFactorRequired || out.Token == "" {
		t.Fatalf("pendiente: %+v %v", out, err)
	}
	// Mismos errores que Login con credenciales malas.
	for _, c := range [][2]string{{"ana@mail.com", "mala"}, {"nadie@mail.com", "x"}} {
		_, err := auth.Authenticate(c[0], c[1])
		_, want := auth.Login(c[0], c[1])
		if !errors.Is(err, domain.ErrUnauthorized) || err != want {
			t.Errorf("%v: got %v want %v", c, err, want)
		}
	}
	// Sin la dependencia (nil): idéntico a Login.
	plain := AuthService{Users: e.users, Hasher: testutil.FakeHasher{}, Tokens: testutil.FakeTokens{}}
	out, err = plain.Authenticate("ana@mail.com", "clave-secreta-1")
	if err != nil || out.Token != "tok:"+e.userID || out.TwoFactorRequired {
		t.Fatalf("nil: %+v %v", out, err)
	}
	// Activado: desafío y NINGÚN token.
	if _, err := e.svc.Enable(e.userID, e.code(t, 0)); err != nil {
		t.Fatal(err)
	}
	out, err = auth.Authenticate("ana@mail.com", "clave-secreta-1")
	if err != nil || !out.TwoFactorRequired || out.Challenge != "ch:"+e.userID || out.Token != "" {
		t.Fatalf("con 2FA: %+v %v", out, err)
	}
	// Contraseña mala con 2FA: el mismo 401, sin pista de que hay 2FA.
	if _, err := auth.Authenticate("ana@mail.com", "mala"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("clave mala con 2FA: %v", err)
	}
	// Caída de la base al mirar el 2FA: falla cerrado, no emite sesión.
	e.repo.findErr = errTwoFactorFindDown
	out, err = auth.Authenticate("ana@mail.com", "clave-secreta-1")
	if err == nil || out.Token != "" {
		t.Errorf("debe fallar cerrado: %+v %v", out, err)
	}
	// Login() (otros llamadores) no cambia.
	if tok, err := auth.Login("ana@mail.com", "clave-secreta-1"); err != nil || tok != "tok:"+e.userID {
		t.Errorf("Login intacto: %q %v", tok, err)
	}
}
