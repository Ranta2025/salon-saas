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
	// EmailVerifiedAt (F15, D1): cuándo se demostró que la persona lee ese
	// correo. nil = sin verificar (el registro abierto). Las cuentas anteriores a
	// la migración 036 quedan con created_at (no se puede demostrar otra cosa).
	EmailVerifiedAt *time.Time
}

// NewUser valida y crea un User. El hash ya viene calculado (SRP: hashear es
// responsabilidad del adaptador security, no del dominio).
func NewUser(name, email, passwordHash string) (User, error) {
	name = strings.TrimSpace(name)
	email = NormalizeEmail(email)
	if name == "" {
		return User{}, fmt.Errorf("%w: nombre vacío", ErrValidation)
	}
	if err := validateText("el nombre", name, MaxTextName); err != nil {
		return User{}, err
	}
	// La forma del email vive en ValidateEmail: el registro y la invitación de
	// equipo (F2g1) comparten la misma política, no dos parecidas.
	if err := ValidateEmail(email); err != nil {
		return User{}, err
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
