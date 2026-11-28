package main

import (
	"log"

	"salon-saas/internal/auth"
	authpostgres "salon-saas/internal/auth/postgres"
	"salon-saas/internal/domain"
)

// wireAuth arma el login de siempre y la compuerta de sesión que comparten el
// middleware, el hub de tiempo real y el 2FA.
func wireAuth(w *wiring) {
	w.authSvc = auth.AuthService{Users: w.users, Hasher: w.hasher, Tokens: w.tokens}
	// F15: compuerta de revocación de sesiones (D4). Una consulta por petición
	// autenticada (`SELECT password_changed_at FROM users WHERE id=$1`); un fallo
	// falla CERRADO (401). Los repositorios de restablecer/verificar email guardan
	// SOLO el SHA-256 del token (migración 036).
	w.recoveryRepo = authpostgres.AccountRecovery{Pool: w.pool}
	w.sessionGate = authpostgres.SessionGate{Pool: w.pool}
}

// wireTwoFactor arma el 2FA TOTP opcional (F10b). Depende de la auditoría (un
// enrolamiento escribe `auth.2fa_enabled`) y de la compuerta de sesión.
func wireTwoFactor(w *wiring) {
	// F10b: 2FA TOTP opcional (migración 035). El secreto se guarda cifrado con una
	// clave derivada de JWT_SECRET (rotarlo obliga a re-enrolar el 2FA). Un usuario
	// sin 2FA activo entra por el login de siempre; con 2FA, el login devuelve un
	// desafío (typ:"2fa", 5 min) que NO es una sesión. Enrolar y desactivar
	// escriben `auth.2fa_enabled` / `auth.2fa_disabled` en la auditoría.
	totpBox, err := domain.NewTOTPSecretBox(w.cfg.JWTSecret)
	if err != nil {
		log.Fatalf("2fa: %v", err)
	}
	w.twoFactorSvc = auth.TwoFactorService{
		Repo: authpostgres.TwoFactor{Pool: w.pool}, Users: w.users, Hasher: w.hasher, Tokens: w.tokens,
		Challenges: w.tokens, Box: totpBox, Memberships: w.mems, Audit: w.auditSvc,
		// F15 D4: un desafío emitido antes del último cambio de contraseña no se
		// canjea (el 2FA no se toca, pero el reinicio mata lo que estaba a medias).
		Gate: w.sessionGate,
	}
	w.authSvc.TwoFactor = w.twoFactorSvc
}

// wireAccountRecovery arma F15: recuperar contraseña (D2/D3) y verificar el
// email (D7). Con el mailer nil (sin SMTP_HOST) no se emite token ni se envía.
func wireAccountRecovery(w *wiring) {
	// F15: recuperar contraseña (D2/D3) y verificar el email (D7). TODO el trabajo
	// de un pedido de restablecer corre DESPUÉS de responder, en el runner
	// asíncrono de producción (goroutine con timeout y recover): el tiempo de la
	// respuesta no revela si el email existe. Con el mailer nil (sin SMTP_HOST) no
	// se emite token ni se envía nada.
	w.recoverySvc = auth.AccountRecoveryService{
		Users: w.users, Resets: w.recoveryRepo, Hasher: w.hasher,
		Mailer: w.mailer, AppURL: w.cfg.AppURL,
	}
	w.verifSvc = auth.EmailVerificationService{
		Users: w.users, Verifs: w.recoveryRepo, Mailer: w.mailer, AppURL: w.cfg.AppURL,
	}
}

// wireMe arma «quién soy y en qué salones estoy».
func wireMe(w *wiring) {
	w.meSvc = auth.MeService{Users: w.users, Salons: w.salons, Members: w.mems}
}
