package httpapi

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/domain"
)

func statusOf(err error) int {
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

// respondErr es el único camino de error hacia el cliente. Un 500 se registra
// completo en el log del servidor y se responde genérico; los errores de
// dominio (400/401/403/404/409) llevan su mensaje en español, que es parte del
// contrato.
func respondErr(c *gin.Context, err error) {
	if statusOf(err) == http.StatusInternalServerError {
		RespondErrLogf(c, err, log.Printf)
		return
	}
	c.JSON(statusOf(err), gin.H{"error": err.Error()})
}

// RespondErrLogf permite inyectar el logger (los tests verifican que el detalle
// queda en el log y no en el body).
func RespondErrLogf(c *gin.Context, err error, logf func(string, ...any)) {
	if statusOf(err) == http.StatusInternalServerError {
		logf("500 %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": InternalErrorMessage})
		return
	}
	c.JSON(statusOf(err), gin.H{"error": err.Error()})
}

// badBody es la respuesta única de JSON inválido o demasiado grande.
func badBody(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
}
