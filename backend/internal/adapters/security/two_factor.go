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
// exp: 5 minutos, ia}. NO es una sesión: Parse lo rechaza.
//
// `ia` (F15, D4) son los milisegundos Unix del instante de emisión, el mismo
// claim de las sesiones: el login rechaza un desafío emitido antes del último
// cambio de contraseña, así que un reinicio también anula un desafío pendiente.
func (j JWTIssuer) IssueChallenge(userID string) (string, error) {
	if j.Secret == "" {
		return "", ErrMissingSecret
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"typ": tokenTypeChallenge,
		"exp": now.Add(domain.TwoFactorChallengeTTL).Unix(),
		"ia":  now.UnixMilli(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(j.Secret))
}

// ParseChallenge valida un desafío vigente y devuelve el sub. Exige typ EXACTO
// "2fa": un token de sesión (sin typ o typ:"session") o cualquier otro se
// rechaza.
func (j JWTIssuer) ParseChallenge(token string) (string, error) {
	c, err := j.ParseChallengeClaims(token)
	if err != nil {
		return "", err
	}
	return c.UserID, nil
}

// ParseChallengeClaims es ParseChallenge MÁS el instante de emisión (`ia`,
// milisegundos Unix), igual que ParseSession para las sesiones (F15, D4): el
// login rechaza un desafío emitido antes del último cambio de contraseña.
// HasIA=false es un desafío emitido por la versión anterior (sin `ia`).
func (j JWTIssuer) ParseChallengeClaims(token string) (domain.SessionClaims, error) {
	claims, err := j.parseClaims(token)
	if err != nil {
		return domain.SessionClaims{}, err
	}
	if typ, _ := claims["typ"].(string); typ != tokenTypeChallenge {
		return domain.SessionClaims{}, domain.ErrUnauthorized
	}
	sub, err := subjectOf(claims)
	if err != nil {
		return domain.SessionClaims{}, err
	}
	out := domain.SessionClaims{UserID: sub}
	out.IssuedAtMs, out.HasIA = issuedAtMsOf(claims)
	return out, nil
}

var (
	_ ports.ChallengeIssuer       = JWTIssuer{}
	_ ports.ChallengeClaimsParser = JWTIssuer{}
)
