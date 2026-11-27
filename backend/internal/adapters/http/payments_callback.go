// PaymentsHandler: el callback del banco (F11, D7). Es la PRIMERA ruta pública
// no GET firmada de la API: el estado del pago lo fija SOLO este camino, nunca el
// navegador.
//
//	POST /api/v1/payments/callback   (pública, firmada, cuerpo máximo 8 KB)
//
// La firma la verifica el adaptador del banco (PaymentGateway.ParseCallback): el
// caso de uso y este handler nunca ven un cuerpo sin verificar. Un reenvío del
// banco (mismo evento) responde 200 {"ok":true} sin efecto.
package httpapi

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/deposits"
	"salon-saas/internal/domain"
)

// maxPaymentCallbackBody es el tope del cuerpo del callback: 8 KB (D7). El
// middleware de 1 MB queda lejos; este es el límite real de la ruta.
const maxPaymentCallbackBody = 8 << 10

type PaymentsHandler struct {
	Deposits deposits.DepositService
}

// Callback: POST /api/v1/payments/callback -> 200 {"ok":true}.
//
// Cualquier rechazo de la firma es 400 «Firma invalida» (uniforme: firma mala,
// sin cabecera, formato malo, t fuera de +-300 s, cuerpo alterado). Un cuerpo de
// más de 8 KB se rechaza ANTES de parsear. Cualquier otra falla es 500 sin
// detalles: el banco reintenta.
func (h PaymentsHandler) Callback(c *gin.Context) {
	// Se lee un byte de más para poder distinguir «8 KB exactos» de «más de 8
	// KB» sin leer el cuerpo entero de un atacante.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxPaymentCallbackBody+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": domain.MsgPaymentSignatureInvalid})
		return
	}
	if len(body) > maxPaymentCallbackBody {
		c.JSON(http.StatusBadRequest, gin.H{"error": domain.MsgPaymentSignatureInvalid})
		return
	}
	if err := h.Deposits.ApplyCallback(c.Request.Header, body); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
