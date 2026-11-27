package security

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"salon-saas/internal/domain"
)

func sign(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const tfSecret = "secreto-de-prueba-para-2fa-1234567"

func TestChallengeIsNotASession(t *testing.T) {
	j := JWTIssuer{Secret: tfSecret}
	ch, err := j.IssueChallenge("u1")
	if err != nil || ch == "" {
		t.Fatalf("emitir: %v", err)
	}
	// Parse (AuthMiddleware, hub de tiempo real) lo rechaza.
	if sub, err := j.Parse(ch); err == nil || sub != "" {
		t.Fatalf("un desafío NO es una sesión: sub=%q err=%v", sub, err)
	}
	// El parser del desafío lo acepta y devuelve el sub.
	sub, err := j.ParseChallenge(ch)
	if err != nil || sub != "u1" {
		t.Fatalf("ParseChallenge: %q %v", sub, err)
	}
}

func TestSessionIsNotAChallenge(t *testing.T) {
	j := JWTIssuer{Secret: tfSecret}
	sess, _ := j.Issue("u1")
	if sub, err := j.ParseChallenge(sess); err == nil || sub != "" {
		t.Fatalf("un token de sesión NO es un desafío: %q %v", sub, err)
	}
	// Sesión explícita con typ:"session" tampoco; ni typ raro.
	for _, typ := range []any{"session", "", "refresh", 2, true, nil, "2FA", " 2fa", "2fa "} {
		tok := sign(t, tfSecret, jwt.MapClaims{"sub": "u1", "typ": typ, "exp": time.Now().Add(time.Minute).Unix()})
		if _, err := j.ParseChallenge(tok); err == nil {
			t.Errorf("typ=%#v no puede ser un desafío", typ)
		}
	}
}

func TestParseKeepsLegacySessionsAndRejectsOtherTypes(t *testing.T) {
	j := JWTIssuer{Secret: tfSecret}
	exp := time.Now().Add(time.Hour).Unix()
	// Ya emitidos (sin typ) siguen sirviendo, y typ:"session" también.
	for name, claims := range map[string]jwt.MapClaims{
		"sin typ":     {"sub": "u1", "exp": exp},
		"typ session": {"sub": "u1", "typ": "session", "exp": exp},
	} {
		if sub, err := j.Parse(sign(t, tfSecret, claims)); err != nil || sub != "u1" {
			t.Errorf("%s: %q %v", name, sub, err)
		}
	}
	for _, typ := range []any{"2fa", "refresh", "", 2, true, "Session", []string{"2fa"}} {
		if typ == "" { // typ vacío: no es ni «ausente» ni «session»
			if _, err := j.Parse(sign(t, tfSecret, jwt.MapClaims{"sub": "u1", "typ": typ, "exp": exp})); err == nil {
				t.Error(`typ:"" debe rechazarse`)
			}
			continue
		}
		if _, err := j.Parse(sign(t, tfSecret, jwt.MapClaims{"sub": "u1", "typ": typ, "exp": exp})); err == nil {
			t.Errorf("typ=%#v no es una sesión", typ)
		}
	}
	// Issue sigue emitiendo tokens que Parse acepta.
	tok, _ := j.Issue("u9")
	if sub, err := j.Parse(tok); err != nil || sub != "u9" {
		t.Errorf("Issue/Parse: %q %v", sub, err)
	}
}

func TestChallengeExpiryAndSignature(t *testing.T) {
	j := JWTIssuer{Secret: tfSecret}
	now := time.Now()
	// TTL de 5 minutos: vigente a los 4 y vencido a los 6.
	ch, _ := j.IssueChallenge("u1")
	tok, _, err := jwt.NewParser().ParseUnverified(ch, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	exp, _ := tok.Claims.GetExpirationTime()
	ttl := exp.Time.Sub(now)
	if ttl < 4*time.Minute+50*time.Second || ttl > 5*time.Minute+5*time.Second {
		t.Errorf("el desafío vive 5 minutos, vive %v", ttl)
	}
	if typ, _ := tok.Claims.(jwt.MapClaims)["typ"].(string); typ != "2fa" {
		t.Errorf("typ=%q", typ)
	}
	if domain.TwoFactorChallengeTTL != 5*time.Minute {
		t.Error("constante de dominio")
	}
	expired := sign(t, tfSecret, jwt.MapClaims{"sub": "u1", "typ": "2fa", "exp": now.Add(-time.Second).Unix()})
	if _, err := j.ParseChallenge(expired); err == nil {
		t.Error("desafío vencido")
	}
	noExp := sign(t, tfSecret, jwt.MapClaims{"sub": "u1", "typ": "2fa"})
	if _, err := j.ParseChallenge(noExp); err == nil {
		t.Error("desafío sin exp no vence nunca: se rechaza")
	}
	noSub := sign(t, tfSecret, jwt.MapClaims{"typ": "2fa", "exp": now.Add(time.Minute).Unix()})
	if _, err := j.ParseChallenge(noSub); err == nil {
		t.Error("desafío sin sub")
	}
	other := sign(t, "otro-secreto-de-prueba-1234567890", jwt.MapClaims{"sub": "u1", "typ": "2fa", "exp": now.Add(time.Minute).Unix()})
	if _, err := j.ParseChallenge(other); err == nil {
		t.Error("firmado con otra clave")
	}
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "u1", "typ": "2fa", "exp": now.Add(time.Minute).Unix()}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := j.ParseChallenge(none); err == nil {
		t.Error("alg none")
	}
	if _, err := j.ParseChallenge(ch + "x"); err == nil {
		t.Error("firma alterada")
	}
	if _, err := (JWTIssuer{}).IssueChallenge("u1"); err == nil {
		t.Error("sin secreto no se firma")
	}
	if _, err := (JWTIssuer{}).ParseChallenge(ch); err == nil {
		t.Error("sin secreto no se valida")
	}
}
