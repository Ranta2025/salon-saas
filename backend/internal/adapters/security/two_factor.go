package security

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Tipos de token (claim `typ`, F10 D8). La sesión NO lleva claim: los tokens ya
// emitidos siguen valiendo y Issue no cambia. Solo el desafío del segundo paso
// del login lleva typ:"2fa".
const (
	tokenTypeSession   = "session"
	tokenTypeChallenge = "2fa"
)

// IssueChallenge firma el desafío del segundo paso del login: {sub, typ:"2fa",
// exp: 5 minutos}. NO es una sesión: Parse lo rechaza.
func (j JWTIssuer) IssueChallenge(userID string) (string, error) {
	if j.Secret == "" {
		return "", ErrMissingSecret
	}
	claims := jwt.MapClaims{
		"sub": userID,
		"typ": tokenTypeChallenge,
		"exp": time.Now().Add(domain.TwoFactorChallengeTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(j.Secret))
}

// ParseChallenge valida un desafío vigente y devuelve el sub. Exige typ EXACTO
// "2fa": un token de sesión (sin typ o typ:"session") o cualquier otro se
// rechaza.
func (j JWTIssuer) ParseChallenge(token string) (string, error) {
	claims, err := j.parseClaims(token)
	if err != nil {
		return "", err
	}
	if typ, _ := claims["typ"].(string); typ != tokenTypeChallenge {
		return "", domain.ErrUnauthorized
	}
	return subjectOf(claims)
}

var _ ports.ChallengeIssuer = JWTIssuer{}
