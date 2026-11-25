package domain

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func wantMsg(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("esperaba error %q, no hubo", text)
	}
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("esperaba %v/%q, obtuve %v/%q", kind, text, err, err.Error())
	}
}

func TestNewAuditEvent_ok(t *testing.T) {
	now := time.Date(2026, 5, 1, 10, 0, 0, 123456000, time.UTC)
	e, err := NewAuditEvent("s1", "u1", "  Ana Pérez ", "member.add", "m1", now)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || e.SalonID != "s1" || e.ActorUserID != "u1" || e.ActorName != "Ana Pérez" ||
		e.Action != "member.add" || e.EntityID != "m1" || !e.CreatedAt.Equal(now) {
		t.Fatalf("evento inesperado: %+v", e)
	}
	e2, err := NewAuditEvent("s1", "u1", "", "settings.update", "", now)
	if err != nil || e2.EntityID != "" || e2.ActorName != "" {
		t.Fatalf("sin entidad ni nombre debe valer: %+v %v", e2, err)
	}
	if e.ID == e2.ID {
		t.Fatal("cada evento lleva su propio id")
	}
}

func TestNewAuditEvent_rejects(t *testing.T) {
	now := time.Now()
	bad := []struct{ salon, actor, action, entity string }{
		{"", "u1", "a.b", ""},
		{"s1", "", "a.b", ""},
		{"s1", "u1", "", ""},
		{"s1", "u1", strings.Repeat("x", 61), ""},
		{"s1", "u1", "a\x00b", ""},
		{"s1", "u1", "a\nb", ""},
		{"s1", "u1", "a.b", strings.Repeat("x", 101)},
		{"s1", "u1", "a.b", "x\x00y"},
	}
	for i, b := range bad {
		if _, err := NewAuditEvent(b.salon, b.actor, "N", b.action, b.entity, now); err == nil || !errors.Is(err, ErrValidation) {
			t.Errorf("caso %d: esperaba ErrValidation, obtuve %v", i, err)
		}
	}
	// El borde: 60 y 100 caracteres valen.
	if _, err := NewAuditEvent("s1", "u1", "N", strings.Repeat("x", 60), strings.Repeat("y", 100), now); err != nil {
		t.Fatalf("60/100 deben valer: %v", err)
	}
	// El nombre del actor nunca hace fallar el evento: se limpia.
	e, err := NewAuditEvent("s1", "u1", "A\x00na\n", "a.b", "", now)
	if err != nil || e.ActorName != "Ana" {
		t.Fatalf("nombre sucio debe limpiarse: %q %v", e.ActorName, err)
	}
}

func TestAuditCursor_roundtrip(t *testing.T) {
	c := AuditCursor{CreatedAt: time.Date(2026, 5, 1, 10, 0, 0, 123456000, time.FixedZone("x", 3600)), ID: "abc123"}
	s := EncodeAuditCursor(c)
	if s == "" || strings.ContainsAny(s, "+/=") {
		t.Fatalf("cursor debe ser base64url sin relleno: %q", s)
	}
	got, err := DecodeAuditCursor(s)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) || got.ID != "abc123" {
		t.Fatalf("roundtrip: %+v", got)
	}
}

func TestAuditCursor_invalid(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, s := range []string{
		"", "!!!", "abc", enc("sin-barra"), enc("no-es-fecha|id1"), enc("2026-05-01T10:00:00Z|"),
		enc("2026-05-01T10:00:00Z|a\x00b"), enc("2026-05-01T10:00:00Z|" + strings.Repeat("x", 101)),
		base64.URLEncoding.EncodeToString([]byte("2026-05-01T10:00:00Z|id")), // con relleno
		strings.Repeat("A", 400),
	} {
		_, err := DecodeAuditCursor(s)
		wantMsg(t, err, ErrValidation, "Cursor invalido")
	}
}

func TestParseAuditQuery(t *testing.T) {
	q, err := ParseAuditQuery("", "", "")
	if err != nil || q.Limit != 50 || q.Before != nil || q.Action != "" {
		t.Fatalf("defaults: %+v %v", q, err)
	}
	q, err = ParseAuditQuery("100", "", " member ")
	if err != nil || q.Limit != 100 || q.Action != "member" {
		t.Fatalf("100: %+v %v", q, err)
	}
	if q, err = ParseAuditQuery("1", "", ""); err != nil || q.Limit != 1 {
		t.Fatalf("1: %+v %v", q, err)
	}
	for _, l := range []string{"0", "101", "-1", "abc", "1.5", "+5", " 5", "99999999999999999999", "1e1"} {
		_, err := ParseAuditQuery(l, "", "")
		wantMsg(t, err, ErrValidation, "El limite debe ser entre 1 y 100")
	}
	cur := EncodeAuditCursor(AuditCursor{CreatedAt: time.Now(), ID: "x1"})
	if q, err = ParseAuditQuery("", cur, ""); err != nil || q.Before == nil || q.Before.ID != "x1" {
		t.Fatalf("cursor: %+v %v", q, err)
	}
	_, err = ParseAuditQuery("", "basura", "")
	wantMsg(t, err, ErrValidation, "Cursor invalido")
	for _, a := range []string{"a\x00", "a\n", "a\r", "a\tb", strings.Repeat("x", 61), "\xff"} {
		_, err := ParseAuditQuery("", "", a)
		wantMsg(t, err, ErrValidation, "Accion invalida")
	}
	// El límite se valida antes que el cursor y el cursor antes que la acción.
	_, err = ParseAuditQuery("0", "basura", "a\x00")
	wantMsg(t, err, ErrValidation, "El limite debe ser entre 1 y 100")
	_, err = ParseAuditQuery("5", "basura", "a\x00")
	wantMsg(t, err, ErrValidation, "Cursor invalido")
}

func TestAuditActionMatches(t *testing.T) {
	cases := []struct {
		filter, action string
		want           bool
	}{
		{"", "member.add", true},
		{"member.add", "member.add", true},
		{"member", "member.add", true},
		{"member", "member", true},
		{"mem", "member.add", false},
		{"member", "memberx.add", false},
		{"member.add", "member", false},
		{"payroll_run", "payroll_run.pay", true},
		{"payroll_run", "payrollXrun.pay", false}, // el _ no es comodín
		{"export", "export.sales", true},
		{"member.", "member.add", false},
		{"%", "member.add", false},
	}
	for _, c := range cases {
		if got := AuditActionMatches(c.filter, c.action); got != c.want {
			t.Errorf("match(%q,%q) = %v, quería %v", c.filter, c.action, got, c.want)
		}
	}
}
