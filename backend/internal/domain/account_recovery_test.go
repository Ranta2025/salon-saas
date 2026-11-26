package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

// D2/D3: el token son 32 bytes de crypto/rand en base64url sin relleno (43
// caracteres), distintos cada vez; solo se guarda su SHA-256.
func TestRecoveryToken_shapeHashAndMatch(t *testing.T) {
	a, err := NewRecoveryToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewRecoveryToken()
	if a == b {
		t.Fatal("dos tokens iguales")
	}
	if len(a) != 43 || strings.ContainsAny(a, "+/=") {
		t.Fatalf("forma inesperada: %q", a)
	}
	if !ValidRecoveryTokenShape(a) {
		t.Fatalf("un token generado debe pasar el chequeo de forma: %q", a)
	}
	h := HashRecoveryToken(a)
	if len(h) != 64 || h == a || h != HashRecoveryToken(a) {
		t.Fatalf("hash sospechoso: %q", h)
	}
	if h != HashInvitationToken(a) {
		t.Error("el hash debe ser el mismo SHA-256 hex de las invitaciones")
	}
	if !RecoveryTokenMatches(h, a) || RecoveryTokenMatches(h, b) || RecoveryTokenMatches("", a) || RecoveryTokenMatches(h, "") {
		t.Error("RecoveryTokenMatches")
	}
}

func TestRecoveryToken_shapeRejects(t *testing.T) {
	good := strings.Repeat("A", 43)
	if !ValidRecoveryTokenShape(good) {
		t.Fatal("43 caracteres base64url deben pasar")
	}
	for name, tok := range map[string]string{
		"vacío":        "",
		"corto":        strings.Repeat("A", 42),
		"largo":        strings.Repeat("A", 44),
		"con relleno":  strings.Repeat("A", 42) + "=",
		"con +":        strings.Repeat("A", 42) + "+",
		"con barra":    strings.Repeat("A", 42) + "/",
		"con espacio":  strings.Repeat("A", 42) + " ",
		"con NUL":      strings.Repeat("A", 42) + "\x00",
		"con salto":    strings.Repeat("A", 42) + "\n",
		"con acento":   strings.Repeat("A", 41) + "ñ",
		"con # (frag)": strings.Repeat("A", 42) + "#",
	} {
		if ValidRecoveryTokenShape(tok) {
			t.Errorf("%s: no debió pasar el chequeo de forma", name)
		}
	}
}

func TestNewPasswordReset_and_Verification(t *testing.T) {
	h := HashRecoveryToken("x")
	r, err := NewPasswordReset("u1", h, t0)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == "" || r.UserID != "u1" || r.TokenHash != h || !r.CreatedAt.Equal(t0) ||
		!r.ExpiresAt.Equal(t0.Add(60*time.Minute)) || r.UsedAt != nil {
		t.Fatalf("reset: %+v", r)
	}
	if _, err := NewPasswordReset("", h, t0); !errors.Is(err, ErrValidation) {
		t.Errorf("sin usuario: %v", err)
	}
	if _, err := NewPasswordReset("u1", "", t0); !errors.Is(err, ErrValidation) {
		t.Errorf("sin hash: %v", err)
	}
	v, err := NewEmailVerification("u1", "Ana@Mail.com", h, t0)
	if err != nil {
		t.Fatal(err)
	}
	if v.Email != "ana@mail.com" || !v.ExpiresAt.Equal(t0.Add(24*time.Hour)) || v.UserID != "u1" || v.TokenHash != h || v.ID == "" {
		t.Fatalf("verificación: %+v", v)
	}
	if _, err := NewEmailVerification("u1", "sin-arroba", h, t0); !errors.Is(err, ErrValidation) {
		t.Errorf("email inválido: %v", err)
	}
	if _, err := NewEmailVerification("", "a@b.com", h, t0); !errors.Is(err, ErrValidation) {
		t.Errorf("sin usuario: %v", err)
	}
}

// El borde de vigencia es el mismo del UPDATE del canje (expires_at > now()):
// en el instante exacto del vencimiento ya no sirve.
func TestUsable_boundaries(t *testing.T) {
	r, _ := NewPasswordReset("u1", "h", t0)
	if !r.Usable(t0) || !r.Usable(r.ExpiresAt.Add(-time.Nanosecond)) {
		t.Error("vigente debe servir")
	}
	if r.Usable(r.ExpiresAt) || r.Usable(r.ExpiresAt.Add(time.Second)) {
		t.Error("vencido no debe servir")
	}
	used := t0
	r.UsedAt = &used
	if r.Usable(t0) {
		t.Error("usado no debe servir")
	}
	v, _ := NewEmailVerification("u1", "a@b.com", "h", t0)
	if !v.Usable(t0) || v.Usable(v.ExpiresAt) {
		t.Error("verificación: vigencia")
	}
	v.UsedAt = &used
	if v.Usable(t0) {
		t.Error("verificación usada no debe servir")
	}
}

// D2: throttle por usuario. Menos de 60 s desde el último, o 5 en la última
// hora, no se emite.
func TestResetThrottled(t *testing.T) {
	ago := func(d time.Duration) *time.Time { x := t0.Add(-d); return &x }
	cases := []struct {
		name   string
		last   *time.Time
		inHour int
		want   bool
	}{
		{"sin previos", nil, 0, false},
		{"hace 59 s", ago(59 * time.Second), 1, true},
		{"hace 60 s exactos", ago(60 * time.Second), 1, false},
		{"hace 61 s", ago(61 * time.Second), 1, false},
		{"4 en la hora", ago(10 * time.Minute), 4, false},
		{"5 en la hora", ago(10 * time.Minute), 5, true},
		{"6 en la hora", ago(10 * time.Minute), 6, true},
		{"ahora mismo", ago(0), 1, true},
	}
	for _, c := range cases {
		if got := ResetThrottled(c.last, c.inHour, t0); got != c.want {
			t.Errorf("%s: %v, esperaba %v", c.name, got, c.want)
		}
	}
}

func TestVerificationThrottled(t *testing.T) {
	ago := func(d time.Duration) *time.Time { x := t0.Add(-d); return &x }
	if VerificationThrottled(nil, t0) || !VerificationThrottled(ago(59*time.Second), t0) ||
		VerificationThrottled(ago(60*time.Second), t0) {
		t.Error("throttle de reenvío: 59 s sí, 60 s no, sin previos no")
	}
}

// D4: un token vale solo si la contraseña nunca cambió o si trae `ia` y `ia` es
// ESTRICTAMENTE posterior al cambio (milisegundos). El empate (mismo
// milisegundo) se rechaza: es el borde conservador, un token no sobrevive a un
// cambio que pasó en su mismo milisegundo.
func TestSessionValid(t *testing.T) {
	changed := time.Date(2026, 5, 1, 12, 0, 0, 500_000, time.UTC) // .000500 s => 12:00:00.000
	ms := changed.UnixMilli()
	cases := []struct {
		name  string
		pca   *time.Time
		ia    int64
		hasIA bool
		want  bool
	}{
		{"sin cambio y sin ia (token viejo)", nil, 0, false, true},
		{"sin cambio y con ia", nil, ms, true, true},
		{"con cambio y sin ia", &changed, 0, false, false},
		{"ia anterior", &changed, ms - 1, true, false},
		{"ia en el mismo ms (conservador)", &changed, ms, true, false},
		{"ia un ms después", &changed, ms + 1, true, true},
		{"ia muy posterior", &changed, ms + 86_400_000, true, true},
		{"ia 0 presente con cambio", &changed, 0, true, false},
	}
	for _, c := range cases {
		if got := SessionValid(c.pca, c.ia, c.hasIA); got != c.want {
			t.Errorf("%s: %v, esperaba %v", c.name, got, c.want)
		}
	}
	// El cambio con microsegundos se compara truncado a milisegundos: un cambio
	// a las 12:00:00.000999 y un token con ia = .001 es posterior.
	late := time.Date(2026, 5, 1, 12, 0, 0, 999_000, time.UTC)
	if !SessionValid(&late, late.UnixMilli()+1, true) || SessionValid(&late, late.UnixMilli(), true) {
		t.Error("truncado a milisegundos")
	}
}

func TestRecoveryMessages_exact(t *testing.T) {
	if MsgLinkInvalid != "Enlace invalido o vencido" ||
		MsgEmailAlreadyVerified != "El email ya esta verificado" ||
		MsgVerificationThrottled != "Espera un minuto antes de pedir otro correo" {
		t.Errorf("mensajes del contrato: %q / %q / %q", MsgLinkInvalid, MsgEmailAlreadyVerified, MsgVerificationThrottled)
	}
}
