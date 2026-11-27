package security

import (
	"strings"
	"testing"
	"time"
)

func TestBcrypt_roundtrip(t *testing.T) {
	h := BcryptHasher{}
	sum, err := h.Hash("secreta")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if sum == "secreta" || !strings.HasPrefix(sum, "$2a$") {
		t.Fatalf("hash sospechoso: %q", sum)
	}
	if !h.Compare(sum, "secreta") {
		t.Fatal("debió aceptar la clave correcta")
	}
	if h.Compare(sum, "otra") {
		t.Fatal("debió rechazar la clave incorrecta")
	}
}

func TestJWT_roundtrip_expira(t *testing.T) {
	j := JWTIssuer{Secret: "test-secret", TTL: time.Hour}
	tok, err := j.Issue("u1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	sub, err := j.Parse(tok)
	if err != nil || sub != "u1" {
		t.Fatalf("parse: %v %q", err, sub)
	}
	if _, err := j.Parse(tok + "x"); err == nil {
		t.Fatal("token adulterado debe fallar")
	}
	fast := JWTIssuer{Secret: "test-secret", TTL: time.Millisecond}
	old, _ := fast.Issue("u1")
	time.Sleep(5 * time.Millisecond)
	if _, err := fast.Parse(old); err == nil {
		t.Fatal("token expirado debe fallar")
	}
}
