package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// AccountRecovery implementa los dos repositorios de F15 (migración 036):
// pedidos de restablecer contraseña y pedidos de verificar el email.
//
// Las garantías duras viven acá, no en el caso de uso:
//   - el token en claro no aparece en ninguna sentencia: se guarda y se busca su
//     SHA-256 (UNIQUE en la base);
//   - el throttle y la emisión corren bajo un advisory lock POR USUARIO
//     (`password-reset:<id>`): dos pedidos simultáneos del mismo usuario no
//     pueden emitir dos tokens vigentes;
//   - el canje es un UPDATE CONDICIONAL (`used_at IS NULL AND expires_at > now()`)
//     en UNA transacción con UN solo valor del reloj de la base: de 30 canjes
//     paralelos del mismo token gana exactamente uno;
//   - el canje NO toca `user_totp` ni `user_recovery_codes` y no emite sesión.
type AccountRecovery struct {
	Pool *pgxpool.Pool
	// TxTimeout acota las transacciones (emisión y canje). 0 = el tope por
	// defecto de txtimeout.go.
	TxTimeout time.Duration
}

// resetColumns y verificationColumns son la lista única de columnas (used_at se
// escanea en puntero: es NULL hasta el canje).
const (
	resetColumns        = `id,user_id,token_hash,created_at,expires_at,used_at`
	verificationColumns = `id,user_id,email,token_hash,created_at,expires_at,used_at`
)

// resetLockKey es la clave del advisory lock por usuario de los pedidos de
// restablecer: el throttle lee y la emisión escribe bajo el mismo lock.
func resetLockKey(userID string) string { return "password-reset:" + userID }

// verificationLockKey: el mismo patrón para el reenvío de la verificación.
func verificationLockKey(userID string) string { return "email-verification:" + userID }

func scanReset(scan func(...any) error) (domain.PasswordReset, error) {
	var r domain.PasswordReset
	if err := scan(&r.ID, &r.UserID, &r.TokenHash, &r.CreatedAt, &r.ExpiresAt, &r.UsedAt); err != nil {
		return domain.PasswordReset{}, pgstore.NotFound(err)
	}
	r.CreatedAt = r.CreatedAt.UTC()
	r.ExpiresAt = r.ExpiresAt.UTC()
	if r.UsedAt != nil {
		at := r.UsedAt.UTC()
		r.UsedAt = &at
	}
	return r, nil
}

func scanVerification(scan func(...any) error) (domain.EmailVerification, error) {
	var v domain.EmailVerification
	if err := scan(&v.ID, &v.UserID, &v.Email, &v.TokenHash, &v.CreatedAt, &v.ExpiresAt, &v.UsedAt); err != nil {
		return domain.EmailVerification{}, pgstore.NotFound(err)
	}
	v.CreatedAt = v.CreatedAt.UTC()
	v.ExpiresAt = v.ExpiresAt.UTC()
	if v.UsedAt != nil {
		at := v.UsedAt.UTC()
		v.UsedAt = &at
	}
	return v, nil
}

// IssueReset emite el pedido bajo el lock del usuario: aplica el throttle
// (domain.ResetThrottled con lo que ya hay guardado y el `created_at` del pedido
// nuevo como «ahora»), invalida los anteriores sin usar y guarda este. Un pedido
// frenado no escribe NADA.
func (r AccountRecovery) IssueReset(reset domain.PasswordReset) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer pgstore.RollbackTx(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, resetLockKey(reset.UserID)); err != nil {
		return false, err
	}
	// Último pedido del usuario (puede no haber ninguno).
	var last time.Time
	hasLast := true
	if err := tx.QueryRow(ctx, `SELECT created_at FROM password_resets WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, reset.UserID).Scan(&last); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		hasLast = false
	}
	var inLastHour int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM password_resets WHERE user_id=$1 AND created_at > $2`,
		reset.UserID, reset.CreatedAt.Add(-time.Hour)).Scan(&inLastHour); err != nil {
		return false, err
	}
	var lastPtr *time.Time
	if hasLast {
		at := last.UTC()
		lastPtr = &at
	}
	if domain.ResetThrottled(lastPtr, inLastHour, reset.CreatedAt) {
		return false, nil
	}
	// Uno vigente por usuario: el anterior sin usar queda invalidado.
	if _, err := tx.Exec(ctx, `UPDATE password_resets SET used_at=$2 WHERE user_id=$1 AND used_at IS NULL`,
		reset.UserID, reset.CreatedAt); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO password_resets(id,user_id,token_hash,created_at,expires_at) VALUES($1,$2,$3,$4,$5)`,
		reset.ID, reset.UserID, reset.TokenHash, reset.CreatedAt, reset.ExpiresAt); err != nil {
		// 23503 (usuario inexistente) y 23505 (hash repetido) salen como error,
		// nunca como «emitido».
		return false, pgstore.PGError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// FindReset resuelve un pedido por el hash de su token. Solo lectura: no consume
// nada (el caso de uso valida antes de canjear). Desconocido = ErrNotFound.
func (r AccountRecovery) FindReset(tokenHash string) (domain.PasswordReset, error) {
	return scanReset(r.Pool.QueryRow(context.Background(),
		`SELECT `+resetColumns+` FROM password_resets WHERE token_hash=$1`, tokenHash).Scan)
}

// ConsumeReset es el canje completo en UNA transacción con UN reloj:
//
//	UPDATE condicional de used_at -> pass_hash + password_changed_at (el MISMO
//	instante del reloj de la base) + email_verified_at si era NULL -> los demás
//	pedidos del usuario quedan invalidados.
//
// Sin filas en el UPDATE condicional es ErrNotFound (desconocido, usado,
// vencido o perdió la carrera): los 30 canjes paralelos del mismo token dejan
// exactamente uno.
func (r AccountRecovery) ConsumeReset(tokenHash, newPassHash string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx)
	// UN solo valor del reloj de la base para used_at, password_changed_at y
	// email_verified_at: la comparación de D4 (`ia` > password_changed_at) es
	// consistente con el instante en que el enlace se gastó.
	var at time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&at); err != nil {
		return err
	}
	var userID string
	// El UPDATE condicional toma el row lock: el segundo canje ve used_at no
	// NULL y no afecta filas.
	if err := tx.QueryRow(ctx,
		`UPDATE password_resets SET used_at=$2
		 WHERE token_hash=$1 AND used_at IS NULL AND expires_at > $2
		 RETURNING user_id`, tokenHash, at).Scan(&userID); err != nil {
		return pgstore.NotFound(err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET pass_hash=$2, password_changed_at=$3, email_verified_at=COALESCE(email_verified_at,$3)
		 WHERE id=$1`, userID, newPassHash, at); err != nil {
		return err
	}
	// Los otros pedidos vivos del usuario mueren con el mismo instante.
	if _, err := tx.Exec(ctx, `UPDATE password_resets SET used_at=$2 WHERE user_id=$1 AND used_at IS NULL`,
		userID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IssueVerification guarda un pedido de verificar el email bajo el lock del
// usuario. Con minInterval > 0, si ya hay uno creado hace menos de ese intervalo
// devuelve issued=false sin escribir nada (el reenvío: 429). El primero del
// registro (minInterval 0) nunca se frena.
func (r AccountRecovery) IssueVerification(v domain.EmailVerification, minInterval time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer pgstore.RollbackTx(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, verificationLockKey(v.UserID)); err != nil {
		return false, err
	}
	if minInterval > 0 {
		var recent int
		if err := tx.QueryRow(ctx,
			`SELECT count(*)::int FROM email_verifications WHERE user_id=$1 AND created_at > $2`,
			v.UserID, v.CreatedAt.Add(-minInterval)).Scan(&recent); err != nil {
			return false, err
		}
		if recent > 0 {
			return false, nil
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO email_verifications(id,user_id,email,token_hash,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6)`,
		v.ID, v.UserID, v.Email, v.TokenHash, v.CreatedAt, v.ExpiresAt); err != nil {
		return false, pgstore.PGError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// ConsumeVerification canjea el token: marca `email_verified_at` y consume el
// pedido con UN UPDATE condicional, en la misma transacción.
//
// Solo verifica si el email del pedido SIGUE siendo el de la cuenta (un pedido
// viejo a una dirección que ya no es la de la cuenta no verifica nada).
// Idempotente: un token ya usado de una cuenta ya verificada es nil. Cualquier
// otro caso es ErrNotFound (el 400 uniforme del caso de uso).
func (r AccountRecovery) ConsumeVerification(tokenHash string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx)
	var at time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&at); err != nil {
		return err
	}
	var userID string
	err = tx.QueryRow(ctx,
		`UPDATE email_verifications v SET used_at=$2
		 FROM users u
		 WHERE v.token_hash=$1 AND v.used_at IS NULL AND v.expires_at > $2
		   AND u.id = v.user_id AND lower(u.email) = lower(v.email)
		 RETURNING v.user_id`, tokenHash, at).Scan(&userID)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx,
			`UPDATE users SET email_verified_at=COALESCE(email_verified_at,$2) WHERE id=$1`, userID, at); err != nil {
			return err
		}
		return tx.Commit(ctx)
	case errors.Is(err, pgx.ErrNoRows):
		// Idempotencia: el token ya se usó y la cuenta ya está verificada.
		var already bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM email_verifications v JOIN users u ON u.id = v.user_id
			 WHERE v.token_hash=$1 AND v.used_at IS NOT NULL AND u.email_verified_at IS NOT NULL`,
			tokenHash).Scan(&already); err != nil {
			return pgstore.NotFound(err)
		}
		return nil
	default:
		return err
	}
}

// SessionGate es la compuerta de revocación de sesiones (D4): una sola consulta
// por petición autenticada. nil = la contraseña nunca cambió; un error de la
// consulta lo convierte quien llama en un 401 (falla CERRADO).
type SessionGate struct {
	Pool    *pgxpool.Pool
	Timeout time.Duration
}

// PasswordChangedAt devuelve users.password_changed_at (nil si nunca cambió).
// Usuario inexistente = ErrNotFound; cualquier otro fallo se propaga como error
// (nunca como «nunca cambió»).
func (g SessionGate) PasswordChangedAt(userID string) (*time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(g.Timeout))
	defer cancel()
	var at *time.Time
	if err := g.Pool.QueryRow(ctx, `SELECT password_changed_at FROM users WHERE id=$1`, userID).Scan(&at); err != nil {
		return nil, pgstore.NotFound(err)
	}
	if at != nil {
		utc := at.UTC()
		return &utc, nil
	}
	return nil, nil
}

var (
	_ ports.PasswordResetRepository     = AccountRecovery{}
	_ ports.EmailVerificationRepository = AccountRecovery{}
	_ ports.SessionGate                 = SessionGate{}
)
