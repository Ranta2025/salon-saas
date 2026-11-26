package security

import (
	"math"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ParseSession es el Parse completo de una sesión (F15, D4): las MISMAS
// exigencias de siempre (método, firma, vencimiento y tipo) y además el instante
// de emisión (`ia`, milisegundos Unix). Parse delega acá: una sola validación.
//
// Tipo: solo es una SESIÓN un token sin claim `typ` (los emitidos antes de F10) o
// con typ:"session". Un desafío de 2FA (typ:"2fa") o cualquier otro tipo se
// rechaza: AuthMiddleware y el hub de tiempo real parsean con esto, así que un
// desafío nunca autentica nada.
//
// Un token sin `ia` (emitido antes de F15), o con un `ia` que no es un número,
// devuelve HasIA=false: vale solo mientras la contraseña nunca cambió.
func (j JWTIssuer) ParseSession(token string) (domain.SessionClaims, error) {
	claims, err := j.parseClaims(token)
	if err != nil {
		return domain.SessionClaims{}, err
	}
	if typ, present := claims["typ"]; present && typ != tokenTypeSession {
		return domain.SessionClaims{}, domain.ErrUnauthorized
	}
	sub, err := subjectOf(claims)
	if err != nil {
		return domain.SessionClaims{}, err
	}
	out := domain.SessionClaims{UserID: sub}
	if raw, ok := claims["ia"].(float64); ok && !math.IsNaN(raw) && !math.IsInf(raw, 0) && raw >= 0 && raw < 1<<53 {
		out.IssuedAtMs, out.HasIA = int64(raw), true
	}
	return out, nil
}

var _ ports.SessionTokenParser = JWTIssuer{}
