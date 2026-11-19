// Package security implementa ports.PasswordHasher y ports.TokenIssuer.
// Es el único lugar que conoce bcrypt y JWT: si mañana cambiás de algoritmo,
// solo se toca este paquete (OCP + SRP).
package security

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// BcryptHasher: costo Default (10). Seguro y portable; no inventamos crypto.
type BcryptHasher struct{ Cost int }

func (b BcryptHasher) Hash(plain string) (string, error) {
	cost := b.Cost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	sum, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	return string(sum), err
}

func (BcryptHasher) Compare(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// JWTIssuer emite tokens cortos (24h) firmados con HMAC.
type JWTIssuer struct {
	Secret string
	TTL    time.Duration
}

func (j JWTIssuer) ttl() time.Duration {
	if j.TTL == 0 {
		return 24 * time.Hour
	}
	return j.TTL
}

// Issue firma {sub: userID, exp}.
func (j JWTIssuer) Issue(userID string) (string, error) {
	claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(j.ttl()).Unix()}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(j.Secret))
}

// Parse valida firma + expiración y devuelve el sub.
func (j JWTIssuer) Parse(token string) (string, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		return []byte(j.Secret), nil
	})
	if err != nil || !t.Valid {
		return "", errOrUnauthorized(err)
	}
	sub, err := t.Claims.GetSubject()
	if err != nil || sub == "" {
		return "", errOrUnauthorized(err)
	}
	return sub, nil
}
