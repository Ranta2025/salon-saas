// Recuperar contraseña por email y revocación de sesiones (F15, parte A).
//
// Reglas de seguridad que sostiene este archivo (contrato D2–D5):
//
//   - Anti-enumeración: Forgot valida la FORMA del email y responde; TODO lo demás
//     (buscar al usuario, emitir el token, mandar el correo) corre en el runner
//     asíncrono DESPUÉS de responder. El tiempo de la respuesta no depende de si
//     el email existe, y el resultado visible es el mismo.
//   - El token son 32 bytes aleatorios (base64url); solo se guarda su SHA-256.
//     Nunca se loguea (ni el token, ni el enlace, ni la dirección).
//   - Reset valida primero (forma del token, token vigente, contraseña) y recién
//     entonces consume: una contraseña inválida NO gasta el enlace. El canje y el
//     cambio de contraseña son UNA transacción del repositorio. Reset NO emite
//     sesión: hay que iniciar sesión de nuevo (y las sesiones anteriores mueren
//     por la compuerta D4).
//   - El 2FA no se toca: el login de un usuario con 2FA sigue pidiendo el código.
package app

import (
	"context"
	"errors"
	"log"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// asyncJobTimeout es el techo de un trabajo asíncrono (buscar, emitir, enviar).
const asyncJobTimeout = 15 * time.Second

// AsyncRunner corre un trabajo FUERA del pedido. En producción es GoAsync (una
// goroutine con timeout y recover); los tests inyectan uno que registra y
// difiere, o uno que corre en línea.
type AsyncRunner func(job func(ctx context.Context))

// GoAsync es el runner de producción: una goroutine por trabajo, con su propio
// timeout y con recover (un pánico del adaptador no puede tumbar el proceso desde
// una goroutine suelta). Es el mismo patrón de la confirmación de reserva.
func GoAsync(timeout time.Duration) AsyncRunner {
	if timeout <= 0 {
		timeout = asyncJobTimeout
	}
	return func(job func(ctx context.Context)) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("cuenta: pánico en un trabajo asíncrono: %v", r)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			job(ctx)
		}()
	}
}

// runAsync usa el runner inyectado o, si no hay, el de producción.
func runAsync(run AsyncRunner, job func(ctx context.Context)) {
	if run == nil {
		run = GoAsync(asyncJobTimeout)
	}
	run(job)
}

// errLinkInvalid es la ÚNICA respuesta de un enlace que no sirve (formato
// inválido, desconocido, vencido o usado): 400 con el texto del contrato.
func errLinkInvalid() error { return domain.NewMessage(domain.ErrValidation, domain.MsgLinkInvalid) }

// AccountRecoveryService: pedir y canjear el restablecimiento de contraseña.
type AccountRecoveryService struct {
	Users  ports.UserRepository
	Resets ports.PasswordResetRepository
	Hasher ports.PasswordHasher
	// Mailer nil = SMTP deshabilitado: el trabajo no emite ni envía nada.
	Mailer ports.MailSender
	// AppURL es la base del enlace (APP_URL).
	AppURL string
	// Run es el runner asíncrono; nil = GoAsync.
	Run AsyncRunner
	// Now es el reloj; nil = time.Now.
	Now func() time.Time
	// GenToken genera el token; nil = domain.NewRecoveryToken (los tests inyectan
	// uno fijo).
	GenToken func() (string, error)
}

func (s AccountRecoveryService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s AccountRecoveryService) token() (string, error) {
	if s.GenToken != nil {
		return s.GenToken()
	}
	return domain.NewRecoveryToken()
}

// Forgot recibe el pedido de restablecer. Valida SOLO la forma del email (un
// error de forma se responde: no revela nada de las cuentas) y encola el trabajo.
// Devuelve nil para cualquier email bien formado, exista o no.
func (s AccountRecoveryService) Forgot(email string) error {
	email = domain.NormalizeEmail(email)
	if err := domain.ValidateEmail(email); err != nil {
		return err
	}
	runAsync(s.Run, func(ctx context.Context) { s.sendReset(ctx, email) })
	return nil
}

// sendReset es TODO el trabajo del pedido, ya fuera del pedido HTTP. Cualquier
// resultado (usuario inexistente, throttle, SMTP caído) es silencioso: quien pidió
// ya recibió su 202. El log nunca lleva la dirección, el token ni el enlace.
func (s AccountRecoveryService) sendReset(ctx context.Context, email string) {
	if s.Mailer == nil {
		return // SMTP deshabilitado: no se emite token que nadie va a recibir
	}
	user, err := s.Users.FindByEmail(email)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			log.Printf("recuperación: buscar la cuenta falló (%T)", err)
		}
		return
	}
	token, err := s.token()
	if err != nil {
		log.Printf("recuperación: generar el token falló (%T)", err)
		return
	}
	reset, err := domain.NewPasswordReset(user.ID, domain.HashRecoveryToken(token), s.now())
	if err != nil {
		log.Printf("recuperación: armar el pedido falló (%T)", err)
		return
	}
	issued, err := s.Resets.IssueReset(reset)
	if err != nil {
		log.Printf("recuperación: guardar el pedido de la cuenta %s falló (%T)", user.ID, err)
		return
	}
	if !issued {
		return // throttle por usuario: silencioso
	}
	msg := resetMail(user.Email, recoveryLink(s.AppURL, "/restablecer", token))
	if err := s.Mailer.Send(ctx, msg); err != nil {
		// Sin err.Error(): un error de SMTP suele traer la dirección del
		// destinatario.
		log.Printf("recuperación: enviar el correo de la cuenta %s falló (%T)", user.ID, err)
	}
}

// Reset canjea el token con la contraseña nueva. NO emite sesión.
//
// Orden (D3): forma del token (sin tocar la base) -> el pedido existe, está sin
// usar y vigente -> contraseña válida -> hash -> canje atómico. Una contraseña
// inválida devuelve su mensaje y el token sigue sirviendo.
func (s AccountRecoveryService) Reset(token, password string) error {
	if !domain.ValidRecoveryTokenShape(token) {
		return errLinkInvalid()
	}
	hash := domain.HashRecoveryToken(token)
	reset, err := s.Resets.FindReset(hash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return errLinkInvalid()
		}
		return err
	}
	// Defensa en profundidad: comparación de tiempo constante y vigencia.
	if !domain.RecoveryTokenMatches(reset.TokenHash, token) || !reset.Usable(s.now()) {
		return errLinkInvalid()
	}
	// B-06: la clave se valida antes de gastar bcrypt y ANTES de consumir.
	if err := domain.ValidatePassword(password); err != nil {
		return err
	}
	passHash, err := s.Hasher.Hash(password)
	if err != nil {
		return err
	}
	if err := s.Resets.ConsumeReset(hash, passHash); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return errLinkInvalid() // perdió la carrera, venció o ya se usó
		}
		return err
	}
	return nil
}

// AuthenticateSession es LA autenticación de sesión de AuthMiddleware y del hub
// de tiempo real (D4): firma, vencimiento y tipo (los valida el emisor) más la
// compuerta de revocación.
//
//   - gate nil = comportamiento de siempre: solo tokens.Parse (los routers de los
//     tests unitarios y los entornos sin compuerta no cambian).
//   - Con compuerta: un token vale solo si domain.SessionValid dice que sí. Un
//     emisor sin ParseSession se trata como token sin `ia`.
//   - La consulta de la compuerta FALLA CERRADO: cualquier error (incluido el
//     usuario inexistente) es 401.
func AuthenticateSession(tokens ports.TokenIssuer, gate ports.SessionGate, token string) (string, error) {
	if gate == nil {
		return tokens.Parse(token)
	}
	var claims domain.SessionClaims
	if parser, ok := tokens.(ports.SessionTokenParser); ok {
		c, err := parser.ParseSession(token)
		if err != nil {
			return "", err
		}
		claims = c
	} else {
		uid, err := tokens.Parse(token)
		if err != nil {
			return "", err
		}
		claims = domain.SessionClaims{UserID: uid}
	}
	if claims.UserID == "" {
		return "", domain.ErrUnauthorized
	}
	changedAt, err := gate.PasswordChangedAt(claims.UserID)
	if err != nil {
		log.Printf("sesión: la compuerta de revocación falló (%T): se rechaza el token", err)
		return "", domain.ErrUnauthorized
	}
	if !domain.SessionValid(changedAt, claims.IssuedAtMs, claims.HasIA) {
		return "", domain.ErrUnauthorized
	}
	return claims.UserID, nil
}
