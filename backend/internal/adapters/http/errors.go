package httpapi

import (
	"github.com/gin-gonic/gin"
	"salon-saas/internal/adapters/http/httpx"
)

// El mapeo de errores a status vive en httpx: lo comparten este paquete y cada
// internal/<feature>/httpapi/. Aquí quedan los nombres cortos que ya usan los
// handlers que todavía no se movieron.
func statusOf(err error) int { return httpx.StatusOf(err) }

// InternalErrorMessage es lo ÚNICO que ve el cliente ante un 500 (B-14).
const InternalErrorMessage = httpx.InternalErrorMessage

// respondErr es el único camino de error hacia el cliente (ver httpx).
func respondErr(c *gin.Context, err error) { httpx.RespondErr(c, err) }

// RespondErrLogf permite inyectar el logger (los tests verifican que el detalle
// queda en el log y no en el body).
func RespondErrLogf(c *gin.Context, err error, logf func(string, ...any)) {
	httpx.RespondErrLogf(c, err, logf)
}

// badBody es la respuesta única de JSON inválido o demasiado grande.
func badBody(c *gin.Context) { httpx.BadBody(c) }
