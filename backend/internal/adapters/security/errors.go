package security

import "salon-saas/internal/domain"

// errOrUnauthorized normaliza cualquier fallo de firma/expiración a 401 de dominio.
func errOrUnauthorized(err error) error {
	if err != nil {
		return domain.ErrUnauthorized
	}
	return domain.ErrUnauthorized
}
