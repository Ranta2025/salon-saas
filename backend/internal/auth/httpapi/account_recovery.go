// Handlers de F15: recuperar contraseña (olvidé / canjear) y verificar el email
// (canjear el enlace y reenviarlo).
//
// Todos son DELGADOS a propósito: la forma del body y el status del contrato; la
// validación, el anti-enumeración y la seguridad del token viven en `app`.
//
// Reglas que no se ven en el código pero son del contrato:
//   - `POST /auth/forgot` responde SIEMPRE 202 `{"ok":true}` para un email bien
//     formado, exista o no la cuenta (el trabajo corre después, en el runner
//     asíncrono). Es el mismo cuerpo y el mismo status en los dos casos.
//   - el token viaja en el CUERPO, nunca en la URL ni en el query: no queda en
//     ningún log.
//   - NUNCA se loguea el token ni la dirección de correo.
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/auth"
	"salon-saas/internal/domain"
)

// containsNUL detecta el carácter NUL (0x00). Un TEXT de Postgres no lo acepta:
// sin este chequeo, un token o una clave con NUL sería un 500.
func containsNUL(s string) bool { return strings.ContainsRune(s, 0) }

// AccountRecoveryHandler agrupa los dos casos de uso de F15 (parte A y parte B).
type AccountRecoveryHandler struct {
	Recovery auth.AccountRecoveryService
	Verif    auth.EmailVerificationService
}

// Forgot es `POST /api/v1/auth/forgot {email}` -> 202 `{"ok":true}`.
//
// El email mal formado (o con tipos equivocados en el body) sí es un 400: la
// forma del dato no revela si la cuenta existe. Todo lo demás es siempre 202.
func (h AccountRecoveryHandler) Forgot(c *gin.Context) {
	var in struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	if containsNUL(in.Email) {
		httpx.BadBody(c)
		return
	}
	if err := h.Recovery.Forgot(in.Email); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

// Reset es `POST /api/v1/auth/reset {token, password}` -> 204 SIN sesión: hay que
// iniciar sesión de nuevo (las sesiones anteriores mueren por D4).
func (h AccountRecoveryHandler) Reset(c *gin.Context) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	if containsNUL(in.Password) {
		httpx.BadBody(c)
		return
	}
	if err := h.Recovery.Reset(in.Token, in.Password); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// VerifyEmail es `POST /api/v1/auth/verify-email {token}` -> 204. Un token
// inválido, vencido o usado sin verificar da 400 con el texto uniforme; un token
// ya usado de una cuenta ya verificada es 204 (idempotente).
func (h AccountRecoveryHandler) VerifyEmail(c *gin.Context) {
	var in struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	if err := h.Verif.Verify(in.Token); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ResendVerification es `POST /api/v1/me/verify-email/resend` -> 202
// `{"ok":true}`; ya verificado 409 `El email ya esta verificado`; más de un
// pedido por minuto 429 `Espera un minuto antes de pedir otro correo`.
func (h AccountRecoveryHandler) ResendVerification(c *gin.Context) {
	uid, _ := c.Get("userID")
	userID, _ := uid.(string)
	if err := h.Verif.Resend(userID); err != nil {
		// 429 del throttle: el único status que `respondErr` no mapea solo.
		if errors.Is(err, domain.ErrTooManyAttempts) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
			return
		}
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}
