// Package app contiene los CASOS DE USO: orquestan dominio + puertos.
// No conoce Gin ni Postgres: solo llama interfaces. (SRP: un servicio por tema.)
package app

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// AuthService: registro y login. Patrón Service + Repository.
type AuthService struct {
	Users  ports.UserRepository
	Hasher ports.PasswordHasher
	Tokens ports.TokenIssuer
}

// Register crea el usuario si el email no existe.
func (s AuthService) Register(name, email, plain string) (domain.User, error) {
	if _, err := s.Users.FindByEmail(email); err == nil {
		return domain.User{}, domain.ErrAlreadyExists
	}
	hash, err := s.Hasher.Hash(plain)
	if err != nil {
		return domain.User{}, err
	}
	u, err := domain.NewUser(name, email, hash)
	if err != nil {
		return domain.User{}, err
	}
	return u, s.Users.Save(u)
}

// Login valida credenciales y emite token. No dice si falló el email o la clave
// (evita enumerar usuarios: misma respuesta ante ambos casos).
func (s AuthService) Login(email, plain string) (string, error) {
	u, err := s.Users.FindByEmail(email)
	if err != nil {
		return "", domain.ErrUnauthorized
	}
	if !s.Hasher.Compare(u.PasswordHash, plain) {
		return "", domain.ErrUnauthorized
	}
	return s.Tokens.Issue(u.ID)
}
