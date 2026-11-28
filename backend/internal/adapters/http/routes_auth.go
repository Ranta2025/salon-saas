package httpapi

import "github.com/gin-gonic/gin"

// registerAuthPublicRoutes: login, registro, segundo paso del 2FA y la
// recuperación de contraseña / verificación de email (F15). Todas cuelgan de
// /api/v1/auth/, así que el límite por IP de ese prefijo ya las cubre.
func registerAuthPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	v1.POST("/auth/register", d.Auth.Register)
	v1.POST("/auth/login", d.Auth.Login)
	// F10b: segundo paso del login con 2FA. Cuelga de /api/v1/auth/, así que el
	// límite por IP de ese prefijo ya lo cubre.
	v1.POST("/auth/login/2fa", d.TwoFactor.Login)
	// F15: recuperar contraseña y verificar email. Las tres son públicas y cuelgan
	// de /api/v1/auth/, así que el límite por IP de ese prefijo ya las cubre. El
	// token viaja SIEMPRE en el cuerpo, nunca en la ruta ni en el query.
	v1.POST("/auth/forgot", d.Recovery.Forgot)
	v1.POST("/auth/reset", d.Recovery.Reset)
	v1.POST("/auth/verify-email", d.Recovery.VerifyEmail)
}

// registerMeRoutes: quién soy, el reenvío del correo de verificación y el 2FA de
// la cuenta propia (no dependen del salón).
func registerMeRoutes(authed *gin.RouterGroup, d Deps) {
	// Quién soy y en qué salones estoy (el frontend elige el salón activo).
	authed.GET("/me", d.Me.Get)
	// F15/D7: reenvío del correo de verificación (202; 409 si ya está verificado;
	// 429 si pidió otro hace menos de un minuto).
	authed.POST("/me/verify-email/resend", d.Recovery.ResendVerification)
	// F10b: 2FA del propio usuario (no dependen del salón).
	authed.GET("/me/2fa", d.TwoFactor.Status)
	authed.POST("/me/2fa/setup", d.TwoFactor.Setup)
	authed.POST("/me/2fa/enable", d.TwoFactor.Enable)
	authed.POST("/me/2fa/disable", d.TwoFactor.Disable)
}
