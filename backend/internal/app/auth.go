// Package app contiene los CASOS DE USO: orquestan dominio + puertos.
// No conoce Gin ni Postgres: solo llama interfaces. (SRP: un servicio por tema.)
package app

import (
	"errors"

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
//
// El email se normaliza ANTES de buscar: si no, "Ana@Mail.com" y "ana@mail.com"
// conviven como dos cuentas y el duplicado no se detecta.
func (s AuthService) Register(name, email, plain string) (domain.User, error) {
	email = domain.NormalizeEmail(email)
	// B-06: la clave se valida antes de gastar bcrypt (caro a propósito).
	if err := domain.ValidatePassword(plain); err != nil {
		return domain.User{}, err
	}
	if _, err := s.Users.FindByEmail(email); err == nil {
		return domain.User{}, domain.ErrAlreadyExists
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, err
	}
	hash, err := s.Hasher.Hash(plain)
	if err != nil {
		return domain.User{}, err
	}
	u, err := domain.NewUser(name, email, hash)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.Users.Save(u); err != nil {
		// Dos registros concurrentes con el mismo email: el índice único decide
		// y el perdedor recibe 409 (el repo traduce 23505), no un 500.
		return domain.User{}, err
	}
	return u, nil
}

// Login valida credenciales y emite token. No dice si falló el email o la clave
// (evita enumerar usuarios: misma respuesta ante ambos casos).
func (s AuthService) Login(email, plain string) (string, error) {
	u, err := s.Users.FindByEmail(domain.NormalizeEmail(email))
	if err != nil {
		// B-05: aunque el usuario no exista se gasta el mismo trabajo de bcrypt.
		// Sin esto la respuesta llega en microsegundos y delata qué emails están
		// registrados (enumeración por timing).
		s.Hasher.DummyCompare(plain)
		return "", domain.ErrUnauthorized
	}
	if !s.Hasher.Compare(u.PasswordHash, plain) {
		return "", domain.ErrUnauthorized
	}
	return s.Tokens.Issue(u.ID)
}
