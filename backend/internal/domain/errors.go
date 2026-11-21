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

// Message es un error de dominio con el texto que ve la persona. Envuelve el
// error de dominio (errors.Is sigue funcionando, así que el status HTTP no
// cambia) pero Error() devuelve SOLO el mensaje: sin el prefijo interno que
// existe para el log ("validación:", "ya existe:"), que en la pantalla no dice
// nada. Es la excepción deliberada a la regla general de los errores de dominio,
// no un reemplazo: se usa donde el mensaje se escribe para quien lo lee.
type Message struct {
	Kind error
	Text string
}

func (m Message) Error() string { return m.Text }

func (m Message) Unwrap() error { return m.Kind }

// NewMessage arma un error de dominio con un texto listo para mostrar.
func NewMessage(kind error, text string) error { return Message{Kind: kind, Text: text} }
