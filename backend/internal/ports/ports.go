// Package ports declara lo que el dominio NECESITA, sin decir cómo se hace.
// Los adaptadores (postgres, bcrypt, jwt, http) implementan estas interfaces.
// Esto es DIP: app depende de abstracciones, no de Gin ni SQL.
package ports

import (
	"time"

	"salon-saas/internal/domain"
)

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

// --- Reservas ---

type BookingRepository interface {
	Save(b domain.Booking) error
	// FindOverlapping trae activas del barbero que pisan [start,end).
	// El caso de uso la usa como filtro amable; la EXCLUDE de la DB decide.
	FindOverlapping(salonID, barberID string, start, end time.Time) ([]domain.Booking, error)
	// ListRange trae todo lo del salón en [from,to) para el calendario.
	ListRange(salonID string, from, to time.Time) ([]domain.Booking, error)
}
