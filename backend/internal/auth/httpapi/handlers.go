// Package httpapi traduce HTTP <-> casos de uso.
// Es DELGADO a propósito: valida JSON, llama app, mapea errores a status.
// La lógica vive en app/domain, nunca aquí (SRP).
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/auth"
	"salon-saas/internal/ports"
)

// --- Auth ---

type AuthHandler struct {
	Auth auth.AuthService
	// Verification (F15, D7): manda el correo de verificación de una cuenta
	// recién creada. El envío es asíncrono y best effort (nunca demora ni cambia
	// la respuesta del registro, D6). nil = sin correo configurado.
	Verification ports.VerificationSender
}

func (h AuthHandler) Register(c *gin.Context) {
	var in struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Password == "" {
		httpx.BadBody(c)
		return
	}
	u, err := h.Auth.Register(in.Name, in.Email, in.Password)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	// F15/D7: el correo sale por el runner asíncrono (el token va en el enlace y
	// nunca en la respuesta). No cambia el status, el cuerpo ni el mensaje.
	if h.Verification != nil {
		h.Verification.SendVerification(u)
	}
	c.JSON(http.StatusCreated, gin.H{"id": u.ID, "name": u.Name, "email": u.Email})
}

func (h AuthHandler) Login(c *gin.Context) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	res, err := h.Auth.Authenticate(in.Email, in.Password)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	// F10: con 2FA activo la respuesta es el desafío y NO lleva `token`. Sin 2FA
	// (el caso de todas las cuentas hasta que lo activan) queda `{"token":...}`
	// exactamente como siempre.
	if res.TwoFactorRequired {
		c.JSON(http.StatusOK, gin.H{"two_factor_required": true, "challenge": res.Challenge})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": res.Token})
}
