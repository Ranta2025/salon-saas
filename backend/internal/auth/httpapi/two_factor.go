// TwoFactorHandler: 2FA TOTP opcional por usuario (F10, parte B del contrato de
// la fase 6, D7 y D8). Es DELGADO: valida el cuerpo, llama a auth.TwoFactorService
// y mapea errores. Las reglas (bloqueo, replay, auditoría) viven en el servicio.
//
//   - GET  /me/2fa           -> {"enabled":bool}
//   - POST /me/2fa/setup     -> {"secret","otpauth_url"} (el ÚNICO lugar donde el
//     secreto sale en claro)
//   - POST /me/2fa/enable    {"code"} -> {"recovery_codes":[8]}
//   - POST /me/2fa/disable   {"password","code"} -> 204
//   - POST /auth/login/2fa   {"challenge","code"} -> {"token"} (público; cuelga de
//     /api/v1/auth/, así que el límite por IP de ese prefijo ya lo cubre)
//
// Cuerpo con tipos equivocados, JSON roto o NUL en una cadena = 400
// `body invalido` (texto del contrato D7). El bloqueo por fuerza bruta es 429; el
// resto de los errores de dominio salen por respondErr (un 500 nunca lleva
// detalle).
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

type TwoFactorHandler struct {
	Service auth.TwoFactorService
}

// msgTwoFactorBadBody es el texto exacto de D7 para cuerpos inválidos de las
// rutas de 2FA.
const msgTwoFactorBadBody = "body invalido"

func twoFactorBadBody(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msgTwoFactorBadBody})
}

// bindTwoFactor lee el cuerpo y rechaza tipos equivocados y NUL en cualquier
// cadena. false = ya respondió 400.
func bindTwoFactor(c *gin.Context, in any, fields ...*string) bool {
	if err := c.ShouldBindJSON(in); err != nil {
		twoFactorBadBody(c)
		return false
	}
	for _, f := range fields {
		if strings.ContainsRune(*f, 0) {
			twoFactorBadBody(c)
			return false
		}
	}
	return true
}

// respondTwoFactorErr agrega el 429 del bloqueo; el resto va por respondErr.
func respondTwoFactorErr(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrTooManyAttempts) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		return
	}
	httpx.RespondErr(c, err)
}

func sessionUserID(c *gin.Context) string {
	uid, _ := c.Get("userID")
	id, _ := uid.(string)
	return id
}

// Status: GET /me/2fa.
func (h TwoFactorHandler) Status(c *gin.Context) {
	enabled, err := h.Service.Status(sessionUserID(c))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": enabled})
}

// Setup: POST /me/2fa/setup.
func (h TwoFactorHandler) Setup(c *gin.Context) {
	out, err := h.Service.Setup(sessionUserID(c))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"secret": out.Secret, "otpauth_url": out.OTPAuthURL})
}

// Enable: POST /me/2fa/enable.
func (h TwoFactorHandler) Enable(c *gin.Context) {
	var in struct {
		Code string `json:"code"`
	}
	if !bindTwoFactor(c, &in, &in.Code) {
		return
	}
	codes, err := h.Service.Enable(sessionUserID(c), in.Code)
	if err != nil {
		respondTwoFactorErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

// Disable: POST /me/2fa/disable.
func (h TwoFactorHandler) Disable(c *gin.Context) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !bindTwoFactor(c, &in, &in.Password, &in.Code) {
		return
	}
	if err := h.Service.Disable(sessionUserID(c), in.Password, in.Code); err != nil {
		respondTwoFactorErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Login: POST /auth/login/2fa. Desafío inválido, vencido o código malo = el
// MISMO 401 (lo decide el servicio); bloqueado = 429.
func (h TwoFactorHandler) Login(c *gin.Context) {
	var in struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if !bindTwoFactor(c, &in, &in.Challenge, &in.Code) {
		return
	}
	tok, err := h.Service.VerifyLogin(in.Challenge, in.Code)
	if err != nil {
		respondTwoFactorErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tok})
}
