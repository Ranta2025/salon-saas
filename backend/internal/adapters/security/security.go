// Package security implementa ports.PasswordHasher y ports.TokenIssuer.
// Es el único lugar que conoce bcrypt y JWT: si mañana cambiás de algoritmo,
// solo se toca este paquete (OCP + SRP).
package security

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// BcryptHasher: costo Default (10). Seguro y portable; no inventamos crypto.
type BcryptHasher struct{ Cost int }

func (b BcryptHasher) Hash(plain string) (string, error) {
	sum, err := bcrypt.GenerateFromPassword([]byte(plain), b.cost())
	return string(sum), err
}

func (b BcryptHasher) Compare(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// dummyHash se calcula UNA vez con el mismo costo que un hash real. Comparar
// contra esto cuando el usuario no existe gasta el mismo tiempo que un login
// real: sin eso, la respuesta rápida delata qué emails están registrados
// (enumeración de usuarios por timing).
var dummyHash = func() []byte {
	sum, err := bcrypt.GenerateFromPassword([]byte("salon-saas-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		// Imposible con un costo válido; si pasara, comparar contra vacío
		// devuelve error y el login sigue fallando cerrado.
		return []byte("")
	}
	return sum
}()

// DummyCompare gasta el trabajo de un Compare real y siempre devuelve false.
func (b BcryptHasher) DummyCompare(plain string) bool {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))
	return false
}

func (b BcryptHasher) cost() int {
	if b.Cost == 0 {
		return bcrypt.DefaultCost
	}
	return b.Cost
}

// JWTIssuer emite tokens cortos (24h) firmados con HMAC.
type JWTIssuer struct {
	Secret string
	TTL    time.Duration
}

// ErrMissingSecret: sin secreto no se firma ni se valida nada. Es un error de
// configuración, no del usuario: el proceso no debe arrancar así.
var ErrMissingSecret = errors.New("JWT_SECRET no configurado")

func (j JWTIssuer) ttl() time.Duration {
	if j.TTL == 0 {
		return 24 * time.Hour
	}
	return j.TTL
}

// Issue firma {sub: userID, exp}.
func (j JWTIssuer) Issue(userID string) (string, error) {
	if j.Secret == "" {
		return "", ErrMissingSecret
	}
	claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(j.ttl()).Unix()}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(j.Secret))
}

// Parse valida método, firma y expiración, y devuelve el sub.
//
//   - WithValidMethods: solo HS256. Sin esto el parser acepta cualquier alg
//     declarado en el token, incluido "none" (token sin firma).
//   - WithExpirationRequired: un token sin exp no vence nunca.
func (j JWTIssuer) Parse(token string) (string, error) {
	if j.Secret == "" {
		return "", ErrMissingSecret
	}
	t, err := jwt.Parse(token, func(*jwt.Token) (interface{}, error) {
		return []byte(j.Secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || !t.Valid {
		return "", errOrUnauthorized(err)
	}
	sub, err := t.Claims.GetSubject()
	if err != nil || sub == "" {
		return "", errOrUnauthorized(err)
	}
	return sub, nil
}
