// Package domain contiene las entidades del negocio.
// Regla hexagonal: aquí NO hay imports de Gin, SQL ni JWT.
// Solo stdlib: el dominio no sabe quién lo llama.
package domain

import "errors"

// Errores de dominio (los adaptadores los traducen a HTTP/SQL).
var (
	ErrNotFound      = errors.New("no encontrado")
	ErrAlreadyExists = errors.New("ya existe")
	ErrValidation    = errors.New("validación")
	ErrUnauthorized  = errors.New("no autenticado")
	ErrForbidden     = errors.New("sin permiso")
)
