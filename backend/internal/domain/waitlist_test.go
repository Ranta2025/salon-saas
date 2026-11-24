package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Contrato F7: dominó de la lista de espera. Los tests fijan las validaciones,
// los topes, los estados (D3), el día local (D4) y el token de la baja (D5).

func mx(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	return loc
}

// wlNow es un instante fijo: 24/11/2026 12:00 en CDMX.
func wlNow(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 11, 24, 12, 0, 0, 0, mx(t))
}

// D4: el día es LOCAL del salón y tiene que estar entre HOY y HOY+30.
func TestWaitlistDay_rango_y_formato(t *testing.T) {
	now := wlNow(t)
	loc := mx(t)

	ok := []string{"2026-11-24", "2026-12-24"}
	for _, d := range ok {
		got, err := WaitlistDay(d, now, loc)
		if err != nil || got != d {
			t.Fatalf("día %q debía ser válido, dio %q %v", d, got, err)
		}
	}
	bad := []struct{ day, msg string }{
		{"24/11/2026", MsgWaitlistDate},
		{"2026-13-01", MsgWaitlistDate},
		{"", MsgWaitlistDate},
		{"2026-11-23", MsgWaitlistDateRange}, // ayer
		{"2026-12-25", MsgWaitlistDateRange}, // hoy + 31
	}
	for _, c := range bad {
		_, err := WaitlistDay(c.day, now, loc)
		if err == nil || !errors.Is(err, ErrValidation) || err.Error() != c.msg {
			t.Fatalf("día %q: esperaba %q, dio %v", c.day, c.msg, err)
		}
	}
	// El borde es el día LOCAL: a las 23:30 de CDMX (ya es el día siguiente en
	// UTC) «hoy» sigue siendo el 24.
	late := time.Date(2026, 11, 24, 23, 30, 0, 0, loc)
	if _, err := WaitlistDay("2026-11-24", late, loc); err != nil {
		t.Fatalf("el borde del día tiene que ser local: %v", err)
	}
}

// D2/D4: el contacto se valida en el dominio y un NUL es 400, nunca 500.
func TestWaitlistContact_validaciones(t *testing.T) {
	cases := []struct{ name, email, phone, msg string }{
		{"", "a@mail.com", "", MsgWaitlistNameRequired},
		{"   ", "a@mail.com", "", MsgWaitlistNameRequired},
		{strings.Repeat("a", 121), "a@mail.com", "", MsgWaitlistNameTooLong},
		{"Ana\x00", "a@mail.com", "", MsgWaitlistInvalidChars},
		{"Ana", "", "", MsgWaitlistEmailRequired},
		{"Ana", "no-es-email", "", "Email inválido"},
		{"Ana", "a\x00@mail.com", "", MsgWaitlistInvalidChars},
		{"Ana", "a@mail.com", strings.Repeat("5", 41), MsgWaitlistPhoneTooLong},
		{"Ana", "a@mail.com", "555\x00", MsgWaitlistInvalidChars},
	}
	for i, c := range cases {
		_, err := NewWaitlistContact(c.name, c.email, c.phone)
		if err == nil || !errors.Is(err, ErrValidation) || err.Error() != c.msg {
			t.Fatalf("caso %d (%q): esperaba %q, dio %v", i, c.name, c.msg, err)
		}
	}
	// Válido: nombre y email recortados, email normalizado, teléfono opcional.
	c, err := NewWaitlistContact("  Lía  ", "  LIA@Mail.com ", " 11 5555-1234 ")
	if err != nil {
		t.Fatalf("contacto válido: %v", err)
	}
	if c.Name != "Lía" || c.Email != "lia@mail.com" || c.Phone != "11 5555-1234" {
		t.Fatalf("normalización: %+v", c)
	}
	if _, err := NewWaitlistContact("Lía", "lia@mail.com", ""); err != nil {
		t.Fatalf("teléfono vacío es válido: %v", err)
	}
}

// D2/D5: la entrada necesita salón, servicio y día ya validado; nace SIN token
// (se emite recién con el aviso) y solo se guarda su SHA-256.
func TestNewWaitlistEntry_exige_identidad_y_token(t *testing.T) {
	c, _ := NewWaitlistContact("Lía", "lia@mail.com", "")
	hash := strings.Repeat("a", 64)
	if _, err := NewWaitlistEntry("", "sv1", "", "2026-12-01", c, wlNow(t)); !errors.Is(err, ErrValidation) {
		t.Fatalf("sin salón: %v", err)
	}
	if _, err := NewWaitlistEntry("s1", "", "", "2026-12-01", c, wlNow(t)); !errors.Is(err, ErrValidation) {
		t.Fatalf("sin servicio: %v", err)
	}
	e, err := NewWaitlistEntry("s1", "sv1", "prof1", "2026-12-01", c, wlNow(t))
	if err != nil {
		t.Fatalf("entrada válida: %v", err)
	}
	if e.ID == "" || e.SalonID != "s1" || e.ServiceID != "sv1" || e.ProfessionalID != "prof1" ||
		e.Day != "2026-12-01" || e.Email != "lia@mail.com" || e.NotifyAttempts != 0 ||
		e.TokenHash != "" || !e.CreatedAt.Equal(wlNow(t).UTC()) {
		t.Fatalf("entrada: %+v", e)
	}
	// El token en claro NUNCA se guarda: un hash que no es SHA-256 en hex es un bug.
	for _, bad := range []string{"", "abc", strings.Repeat("a", 63), strings.Repeat("z", 64)} {
		if _, err := e.WithTokenHash(bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("hash %q: %v", bad, err)
		}
	}
	withToken, err := e.WithTokenHash(hash)
	if err != nil || withToken.TokenHash != hash || withToken.ID != e.ID {
		t.Fatalf("WithTokenHash: %+v %v", withToken, err)
	}
	// Sin profesional = «cualquiera».
	any, _ := NewWaitlistEntry("s1", "sv1", "  ", "2026-12-01", c, wlNow(t))
	if any.ProfessionalID != "" {
		t.Fatalf("profesional vacío debe ser cualquiera: %q", any.ProfessionalID)
	}
}

// D3: waiting (sin notified_at ni left_at), notified (notified_at) y left
// (left_at). left gana sobre notified.
func TestWaitlistEntry_estados(t *testing.T) {
	c, _ := NewWaitlistContact("Lía", "lia@mail.com", "")
	e, err := NewWaitlistEntry("s1", "sv1", "", "2026-12-01", c, wlNow(t))
	if err != nil {
		t.Fatalf("entrada: %v", err)
	}
	if e.Status() != WaitlistStatusWaiting || !e.Active() {
		t.Fatalf("recién creada: %q active=%v", e.Status(), e.Active())
	}
	notifiedAt := wlNow(t).Add(time.Minute)
	e.NotifiedAt = &notifiedAt
	e.NotifyClaimedAt = &notifiedAt
	if e.Status() != WaitlistStatusNotified {
		t.Fatalf("avisada: %q", e.Status())
	}
	leftAt := wlNow(t).Add(time.Hour)
	e.LeftAt = &leftAt
	if e.Status() != WaitlistStatusLeft || e.Active() {
		t.Fatalf("dada de baja: %q active=%v", e.Status(), e.Active())
	}
}

// D5: token de 32 bytes base64url (43) y SOLO su SHA-256 (64 hex).
func TestWaitlistTokens(t *testing.T) {
	tok, err := NewWaitlistToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if len(tok) != WaitlistTokenLength || !ValidWaitlistToken(tok) {
		t.Fatalf("token: %q", tok)
	}
	hash := HashWaitlistToken(tok)
	if len(hash) != 64 || strings.Contains(hash, tok) {
		t.Fatalf("hash: %q", hash)
	}
	if !WaitlistTokenMatches(hash, tok) {
		t.Fatal("el token tiene que coincidir con su hash")
	}
	if WaitlistTokenMatches(hash, tok+"x") || WaitlistTokenMatches(hash, "") {
		t.Fatal("otro token no puede coincidir")
	}
	for _, bad := range []string{"", "corto", strings.Repeat("a", 44), tok[:42] + "!"} {
		if ValidWaitlistToken(bad) {
			t.Fatalf("token inválido aceptado: %q", bad)
		}
	}
}

// D6: se reclaman libres - retenidos avisos, con el tope de 5 huecos.
func TestWaitlistNoticesToClaim(t *testing.T) {
	cases := []struct{ libres, retained, want int }{
		{1, 0, 1},
		{1, 1, 0},
		{3, 1, 2},
		{3, 5, 0},
		{9, 0, WaitlistNoticeSlotsCap}, // el tope del contrato
		{9, 6, 0},
		{0, 0, 0},
		{-1, 0, 0},
	}
	for _, c := range cases {
		if got := WaitlistNoticesToClaim(c.libres, c.retained); got != c.want {
			t.Fatalf("libres=%d retenidos=%d: esperaba %d, dio %d", c.libres, c.retained, c.want, got)
		}
	}
}
