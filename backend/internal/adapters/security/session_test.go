package security

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"salon-saas/internal/domain"
)

func rawToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// D4: Issue agrega `ia` = milisegundos Unix del instante de emisión, sin tocar
// `sub` ni `exp`.
func TestIssue_carriesIssuedAtInMilliseconds(t *testing.T) {
	j := JWTIssuer{Secret: "test-secret", TTL: time.Hour}
	before := time.Now().UnixMilli()
	tok, err := j.Issue("u1")
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(tok, claims, func(*jwt.Token) (any, error) { return []byte("test-secret"), nil }); err != nil {
		t.Fatal(err)
	}
	ia, ok := claims["ia"].(float64)
	if !ok {
		t.Fatalf("el token no trae ia: %v", claims)
	}
	if int64(ia) < before || int64(ia) > after {
		t.Errorf("ia=%d fuera de [%d,%d]: no es el instante de emisión en ms", int64(ia), before, after)
	}
	if claims["sub"] != "u1" {
		t.Errorf("sub: %v", claims["sub"])
	}
	exp, _ := claims["exp"].(float64)
	if int64(exp) < time.Now().Add(59*time.Minute).Unix() {
		t.Errorf("exp cambió: %v", claims["exp"])
	}
	if _, has := claims["typ"]; has {
		t.Error("la sesión sigue sin claim typ")
	}
}

func TestParseSession(t *testing.T) {
	j := JWTIssuer{Secret: "test-secret", TTL: time.Hour}
	before := time.Now().UnixMilli()
	tok, _ := j.Issue("u1")
	c, err := j.ParseSession(tok)
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}
	if c.UserID != "u1" || !c.HasIA || c.IssuedAtMs < before || c.IssuedAtMs > time.Now().UnixMilli() {
		t.Fatalf("claims: %+v", c)
	}
	// Parse sigue dando el sub, igual que siempre.
	if sub, err := j.Parse(tok); err != nil || sub != "u1" {
		t.Fatalf("Parse: %q %v", sub, err)
	}
}

// Un token ya emitido (sin ia) sigue siendo una sesión válida: HasIA=false.
func TestParseSession_tokenWithoutIA(t *testing.T) {
	j := JWTIssuer{Secret: "test-secret"}
	old := rawToken(t, "test-secret", jwt.MapClaims{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	c, err := j.ParseSession(old)
	if err != nil || c.UserID != "u1" || c.HasIA || c.IssuedAtMs != 0 {
		t.Fatalf("token sin ia: %+v %v", c, err)
	}
	// Un `ia` que no es número no cuenta como ia (valdría solo mientras la
	// contraseña nunca cambió).
	weird := rawToken(t, "test-secret", jwt.MapClaims{"sub": "u1", "ia": "123", "exp": time.Now().Add(time.Hour).Unix()})
	if c, err := j.ParseSession(weird); err != nil || c.HasIA {
		t.Fatalf("ia no numérico: %+v %v", c, err)
	}
}

// Misma exigencia que Parse: firma, método, vencimiento y tipo. El desafío de
// 2FA NUNCA es una sesión.
func TestParseSession_rejects(t *testing.T) {
	j := JWTIssuer{Secret: "test-secret", TTL: time.Hour}
	good, _ := j.Issue("u1")
	challenge, _ := j.IssueChallenge("u1")
	cases := map[string]string{
		"desafío 2fa":     challenge,
		"adulterado":      good + "x",
		"otra firma":      rawToken(t, "otro-secreto", jwt.MapClaims{"sub": "u1", "ia": time.Now().UnixMilli(), "exp": time.Now().Add(time.Hour).Unix()}),
		"vencido":         rawToken(t, "test-secret", jwt.MapClaims{"sub": "u1", "ia": time.Now().UnixMilli(), "exp": time.Now().Add(-time.Hour).Unix()}),
		"sin exp":         rawToken(t, "test-secret", jwt.MapClaims{"sub": "u1", "ia": time.Now().UnixMilli()}),
		"typ desconocido": rawToken(t, "test-secret", jwt.MapClaims{"sub": "u1", "typ": "otro", "ia": time.Now().UnixMilli(), "exp": time.Now().Add(time.Hour).Unix()}),
		"sin sub":         rawToken(t, "test-secret", jwt.MapClaims{"ia": time.Now().UnixMilli(), "exp": time.Now().Add(time.Hour).Unix()}),
		"vacío":           "",
	}
	for name, tok := range cases {
		if _, err := j.ParseSession(tok); err == nil {
			t.Errorf("%s: debió fallar", name)
		}
	}
	// typ:"session" explícito sí es una sesión.
	ok := rawToken(t, "test-secret", jwt.MapClaims{"sub": "u1", "typ": "session", "ia": int64(5), "exp": time.Now().Add(time.Hour).Unix()})
	if c, err := j.ParseSession(ok); err != nil || c.UserID != "u1" || !c.HasIA || c.IssuedAtMs != 5 {
		t.Errorf("typ session: %+v %v", c, err)
	}
	// Sin secreto no se valida nada.
	if _, err := (JWTIssuer{}).ParseSession(good); !errors.Is(err, ErrMissingSecret) {
		t.Errorf("sin secreto: %v", err)
	}
	_ = domain.ErrUnauthorized
}
