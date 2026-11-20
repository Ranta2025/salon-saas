package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Límites de dominio: valen para CUALQUIER camino de escritura (HTTP hoy, cola
// offline, importador mañana). Vivir acá y no en el handler es lo que evita que
// un body enorme entre a la DB por una ruta nueva que nadie acordó.
const (
	// Texto libre.
	MaxTextName  = 120  // nombre de salón, servicio, cliente, usuario
	MaxTextPhone = 40   // teléfonos con código de país, guiones y espacios
	MaxTextNotes = 2000 // notas de ficha: alergias, color usado, preferencias
	MaxTextEmail = 254  // límite práctico de un email (RFC 5321)
	MaxTextTitle = 120  // título de un aviso
	MaxTextBody  = 500  // cuerpo de un aviso

	// Credenciales.
	MinPasswordBytes = 8
	// MaxPasswordBytes es 72 porque bcrypt trunca en 72 bytes: aceptar más
	// sería mentirle al usuario sobre qué parte de su clave protege la cuenta.
	MaxPasswordBytes = 72

	// Dinero (B-03). El techo por línea y por venta es lo que mantiene las
	// sumas del dashboard y del cierre dentro de los tipos de la DB.
	MaxSaleItemQty        = 1000          // unidades por línea
	MaxSaleUnitPriceCents = 10_000_000    // 100.000,00 unidades por línea
	MaxSaleItems          = 50            // líneas por venta
	MaxSaleTotalCents     = 2_000_000_000 // cabe en INTEGER de Postgres
	// MaxCashCents es el techo de un cierre de caja. No es una regla de
	// negocio caprichosa: system_cash_cents y counted_cents son INTEGER en la
	// DB, así que un valor mayor no se puede guardar. Mejor 400 claro que 500.
	MaxCashCents = 2_000_000_000

	// Agenda (B-18): un turno de más de medio día es un error de tipeo, no un
	// servicio; y no se agenda hacia atrás.
	MaxBookingDuration = 12 * time.Hour
)

// ValidatePassword exige entre 8 y 72 BYTES (bcrypt trabaja en bytes, no en
// runes: una "ñ" ocupa dos).
func ValidatePassword(plain string) error {
	n := len(plain)
	if n < MinPasswordBytes {
		return fmt.Errorf("%w: la clave debe tener al menos %d caracteres", ErrValidation, MinPasswordBytes)
	}
	if n > MaxPasswordBytes {
		return fmt.Errorf("%w: la clave no puede superar los %d caracteres", ErrValidation, MaxPasswordBytes)
	}
	return nil
}

// NormalizeEmail es la única forma de comparar emails: sin espacios y en
// minúsculas. Sin esto el login depende de cómo se tipeó el email.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validateText centraliza el chequeo de largo de un campo de texto. El tope se
// mide en runes (lo que ve la persona), no en bytes.
func validateText(field, value string, max int) error {
	if utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%w: %s no puede superar los %d caracteres", ErrValidation, field, max)
	}
	return nil
}
