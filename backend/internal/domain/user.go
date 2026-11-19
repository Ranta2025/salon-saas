package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// User es la identidad global (una persona puede pertenecer a varios salones).
type User struct {
	ID           string
	Name         string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// NewUser valida y crea un User. El hash ya viene calculado (SRP: hashear es
// responsabilidad del adaptador security, no del dominio).
func NewUser(name, email, passwordHash string) (User, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(strings.ToLower(email))
	if name == "" {
		return User{}, fmt.Errorf("%w: nombre vacío", ErrValidation)
	}
	if !strings.Contains(email, "@") || len(email) < 5 {
		return User{}, fmt.Errorf("%w: email inválido", ErrValidation)
	}
	if passwordHash == "" {
		return User{}, fmt.Errorf("%w: falta hash de clave", ErrValidation)
	}
	return User{
		ID:           NewID(),
		Name:         name,
		Email:        email,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

// NewID genera un id aleatorio con stdlib (evita depender de uuid tan temprano).
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
