// Package httpx son los ayudantes HTTP compartidos por los handlers de todas
// las funcionalidades: el mapeo de errores de dominio a status, el cuerpo
// inválido, la distinción entre una clave JSON ausente y un `null` explícito y
// los límites de cuerpo. Vive fuera de `adapters/http` porque cada
// `internal/<feature>/httpapi/` lo usa y ese paquete importa a los handlers (no
// al revés).
package httpx

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"salon-saas/internal/domain"
	ports "salon-saas/internal/ports"
	"strings"

	"github.com/gin-gonic/gin"
)

// StatusOf traduce un error de dominio al status HTTP del contrato.
func StatusOf(err error) int {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrAlreadyExists):
		return http.StatusConflict
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// InternalErrorMessage es lo ÚNICO que ve el cliente ante un 500 (B-14): ni
// SQL, ni nombres de tabla, ni mensajes de pgx. El detalle va al log del
// servidor, que es donde sirve.
const InternalErrorMessage = "error interno"

// RespondErr es el único camino de error hacia el cliente. Un 500 se registra
// completo en el log del servidor y se responde genérico; los errores de
// dominio (400/401/403/404/409) llevan su mensaje en español, que es parte del
// contrato.
func RespondErr(c *gin.Context, err error) {
	if StatusOf(err) == http.StatusInternalServerError {
		RespondErrLogf(c, err, log.Printf)
		return
	}
	c.JSON(StatusOf(err), gin.H{"error": err.Error()})
}

// RespondErrLogf permite inyectar el logger (los tests verifican que el detalle
// queda en el log y no en el body).
func RespondErrLogf(c *gin.Context, err error, logf func(string, ...any)) {
	if StatusOf(err) == http.StatusInternalServerError {
		logf("500 %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": InternalErrorMessage})
		return
	}
	c.JSON(StatusOf(err), gin.H{"error": err.Error()})
}

// LoadActor resuelve la membresía del usuario del token en el salón de la ruta.
// Sin membresía no hay acceso y el 403 no distingue por qué.
func LoadActor(memberships ports.MembershipRepository, c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	userID, _ := uid.(string)
	m, err := memberships.Find(userID, salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// OptionalID devuelve nil para un id vacío: así un `professional_id` o un
// `service_id` opcional sale como null y no como cadena vacía.
func OptionalID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// BadBody es la respuesta única de JSON inválido o demasiado grande.
func BadBody(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
}

// PresentInt distingue una clave AUSENTE de una clave en `null` (y de un
// número): el PUT del catálogo exige `validity_days` presente, y `null`
// explícito significa «sin vencimiento». Un campo ausente no llama a
// UnmarshalJSON, así que Set queda en false.
type PresentInt struct {
	Set   bool
	Value *int
}

// UnmarshalJSON marca Set sea cual sea el valor: así el handler sabe si la
// clave vino en el cuerpo.
func (p *PresentInt) UnmarshalJSON(b []byte) error {
	p.Set = true
	if strings.TrimSpace(string(b)) == "null" {
		p.Value = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Value = &v
	return nil
}
