// Package ports declara lo que el dominio NECESITA, sin decir cómo se hace.
// Los adaptadores (postgres, bcrypt, jwt, http) implementan estas interfaces.
// Esto es DIP: app depende de abstracciones, no de Gin ni SQL.
package ports

import "salon-saas/internal/domain"

// --- Repositorios (puertos de salida hacia persistencia) ---

type UserRepository interface {
	Save(u domain.User) error
	FindByEmail(email string) (domain.User, error)
}

type SalonRepository interface {
	Save(s domain.Salon) error
	FindBySlug(slug string) (domain.Salon, error)
}

type MembershipRepository interface {
	Save(m domain.Membership) error
	Find(userID, salonID string) (domain.Membership, error)
}

// --- Seguridad (puertos de salida hacia crypto/tokens) ---

type PasswordHasher interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) bool
}

type TokenIssuer interface {
	Issue(userID string) (string, error)
	Parse(token string) (string, error) // devuelve userID
}
