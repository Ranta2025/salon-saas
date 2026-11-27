package security

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// B-02: un token forjado con el secreto por defecto viejo ("dev-secret-cambiala")
// NO puede entrar. Antes el servidor arrancaba con ese secreto si faltaba la
// variable de entorno, así que cualquiera podía firmarse un token de owner.
func TestJWT_rechaza_token_firmado_con_el_secreto_por_defecto(t *testing.T) {
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "u-atacante",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("dev-secret-cambiala"))
	if err != nil {
		t.Fatalf("firmar token de prueba: %v", err)
	}
	real := JWTIssuer{Secret: "secreto-real-de-produccion-con-32+", TTL: time.Hour}
	if _, err := real.Parse(legacy); err == nil {
		t.Fatal("el token firmado con el secreto por defecto debe rechazarse")
	}
}

// Un token firmado con OTRO secreto (misma longitud, otro valor) tampoco entra.
func TestJWT_rechaza_otro_secreto(t *testing.T) {
	other := JWTIssuer{Secret: "otro-secreto-igual-de-largo-123456", TTL: time.Hour}
	tok, _ := other.Issue("u1")
	ours := JWTIssuer{Secret: "nuestro-secreto-igual-de-largo-1234", TTL: time.Hour}
	if _, err := ours.Parse(tok); err == nil {
		t.Fatal("token de otro emisor debe rechazarse")
	}
}

// alg:none (o cualquier método que no sea HS256) se rechaza ANTES de mirar la
// firma: sin WithValidMethods el parser aceptaría el token sin firma.
func TestJWT_rechaza_alg_none(t *testing.T) {
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "u-atacante",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("armar token alg:none: %v", err)
	}
	j := JWTIssuer{Secret: "secreto-real-de-produccion-con-32+", TTL: time.Hour}
	if _, err := j.Parse(none); err == nil {
		t.Fatal("alg:none debe rechazarse")
	}
}

// Without ExpirationRequired, un token eterno (sin exp) seguiría siendo válido
// para siempre.
func TestJWT_exige_expiracion(t *testing.T) {
	noExp, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "u1"}).
		SignedString([]byte("secreto-real-de-produccion-con-32+"))
	if err != nil {
		t.Fatalf("armar token sin exp: %v", err)
	}
	j := JWTIssuer{Secret: "secreto-real-de-produccion-con-32+", TTL: time.Hour}
	if _, err := j.Parse(noExp); err == nil {
		t.Fatal("un token sin exp debe rechazarse")
	}
}

// Sin secreto configurado el emisor no puede verificar nada: falla cerrado en
// lugar de comparar contra la cadena vacía.
func TestJWT_sin_secreto_falla_cerrado(t *testing.T) {
	empty := JWTIssuer{}
	if _, err := empty.Parse("cualquier.cosa.aca"); err == nil {
		t.Fatal("un issuer sin secreto no debe validar tokens")
	}
	if _, err := empty.Issue("u1"); err == nil {
		t.Fatal("un issuer sin secreto no debe emitir tokens")
	}
}

// B-05: cuando el usuario no existe se compara contra un hash falso para que el
// tiempo de respuesta no delate qué emails están registrados.
func TestBcrypt_dummy_compare(t *testing.T) {
	h := BcryptHasher{}
	start := time.Now()
	if h.DummyCompare("cualquiera") {
		t.Fatal("el hash falso nunca debe dar verdadero")
	}
	elapsed := time.Since(start)
	// Un bcrypt real tarda decenas de milisegundos; comparar contra "" tarda
	// microsegundos. El umbral es conservador para no ser flaky.
	if elapsed < 5*time.Millisecond {
		t.Fatalf("DummyCompare debe gastar el mismo trabajo que un compare real, tardó %v", elapsed)
	}
}
