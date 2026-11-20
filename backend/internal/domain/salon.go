package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Salon es el tenant: todo dato del negocio cuelga de un Salon.
type Salon struct {
	ID        string
	Slug      string // único global, ej: "navajas-oro" (para /b/:slug)
	Name      string
	CreatedAt time.Time
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// NewSalon valida y crea un Salon.
func NewSalon(name, slug string) (Salon, error) {
	name = strings.TrimSpace(name)
	slug = strings.TrimSpace(strings.ToLower(slug))
	if name == "" {
		return Salon{}, fmt.Errorf("%w: nombre del salón vacío", ErrValidation)
	}
	if err := validateText("el nombre del salón", name, MaxTextName); err != nil {
		return Salon{}, err
	}
	if !slugRe.MatchString(slug) {
		return Salon{}, fmt.Errorf("%w: slug inválido (minúsculas, números y guiones)", ErrValidation)
	}
	return Salon{
		ID:        NewID(),
		Slug:      slug,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}, nil
}
