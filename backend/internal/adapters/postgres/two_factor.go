package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// TwoFactor implementa ports.TwoFactorRepository sobre user_totp y
// user_recovery_codes (migración 035, F10 D6/D9). Las garantías de seguridad
// viven en UNA sentencia SQL cada una:
//
//   - BeginAttempt: UN `UPDATE ... RETURNING` cuenta el intento y aplica el
//     bloqueo; si está bloqueado no actualiza nada (así 30 intentos paralelos
//     nunca reservan más de 5 verificaciones).
//   - CompleteStep: `WHERE last_step < paso` (anti-replay, atómico).
//   - UseRecoveryCode: `WHERE used_at IS NULL` (de dos usos gana UNO).
//
// El secreto entra y sale SIEMPRE cifrado: este adaptador nunca lo ve en claro.
type TwoFactor struct {
	Pool *pgxpool.Pool
	// Timeout acota cada sentencia suelta; TxTimeout, cada transacción
	// (0 = default).
	Timeout   time.Duration
	TxTimeout time.Duration
}

const defaultTwoFactorTimeout = 5 * time.Second

func (r TwoFactor) ctx() (context.Context, context.CancelFunc) {
	d := r.Timeout
	if d <= 0 {
		d = defaultTwoFactorTimeout
	}
	return context.WithTimeout(context.Background(), d)
}

// Find lee el estado del usuario. ErrNotFound si nunca hizo setup.
func (r TwoFactor) Find(userID string) (domain.TwoFactorState, error) {
	ctx, cancel := r.ctx()
	defer cancel()
	var st domain.TwoFactorState
	err := r.Pool.QueryRow(ctx,
		`SELECT secret_enc, enabled, last_step FROM user_totp WHERE user_id=$1`, userID).
		Scan(&st.SecretEnc, &st.Enabled, &st.LastStep)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TwoFactorState{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.TwoFactorState{}, pgError(err)
	}
	return st, nil
}

// SavePending guarda o REEMPLAZA el enrolamiento pendiente en un UPSERT que no
// pisa un 2FA activo (el WHERE del DO UPDATE): 0 filas = ya estaba habilitado.
func (r TwoFactor) SavePending(userID string, secretEnc []byte) error {
	ctx, cancel := r.ctx()
	defer cancel()
	tag, err := r.Pool.Exec(ctx,
		`INSERT INTO user_totp(user_id, secret_enc) VALUES($1, $2)
		 ON CONFLICT (user_id) DO UPDATE
		   SET secret_enc = EXCLUDED.secret_enc, last_step = 0, failed_count = 0,
		       locked_until = NULL, created_at = now()
		   WHERE user_totp.enabled = false`, userID, secretEnc)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAlreadyExists
	}
	return nil
}

// Enable activa el 2FA y guarda los hashes de los códigos de recuperación en UNA
// transacción. El UPDATE exige que siga pendiente y con el MISMO secreto con el
// que se verificó el código.
func (r TwoFactor) Enable(userID string, secretEnc []byte, step int64, codeHashes []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	tag, err := tx.Exec(ctx,
		`UPDATE user_totp
		 SET enabled = true, enabled_at = now(), last_step = $3, failed_count = 0, locked_until = NULL
		 WHERE user_id = $1 AND enabled = false AND secret_enc = $2`, userID, secretEnc, step)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT enabled FROM user_totp WHERE user_id=$1`, userID).Scan(&enabled)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return domain.ErrNotFound
		case err != nil:
			return pgError(err)
		case enabled:
			return domain.ErrAlreadyExists
		}
		return domain.ErrNotFound // pendiente, pero con otro secreto
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_recovery_codes(id, user_id, code_hash) VALUES($1, $2, $3)`,
			domain.NewID(), userID, h); err != nil {
			return pgError(err)
		}
	}
	return pgError(tx.Commit(ctx))
}

// attemptSQL es el UPDATE atómico de D9. Lee y escribe la fila bajo su lock de
// fila, así que concurrentes se serializan y cada uno ve el contador del
// anterior. Un bloqueo VENCIDO (locked_until <= now()) reinicia la cuenta; con
// el bloqueo vigente el WHERE no encuentra la fila y NO se actualiza nada.
const attemptSQL = `
UPDATE user_totp SET
  failed_count = (CASE WHEN locked_until IS NOT NULL AND locked_until <= now() THEN 0 ELSE failed_count END) + 1,
  locked_until = CASE
    WHEN (CASE WHEN locked_until IS NOT NULL AND locked_until <= now() THEN 0 ELSE failed_count END) + 1 >= $2
    THEN now() + ($3::int * interval '1 second')
    ELSE NULL END
WHERE user_id = $1 AND enabled = true
  AND (locked_until IS NULL OR locked_until <= now())
RETURNING secret_enc, last_step`

// BeginAttempt reserva un intento de verificar un código (ver el puerto).
func (r TwoFactor) BeginAttempt(userID string) (domain.TwoFactorAttempt, error) {
	ctx, cancel := r.ctx()
	defer cancel()
	var att domain.TwoFactorAttempt
	err := r.Pool.QueryRow(ctx, attemptSQL, userID,
		domain.TwoFactorMaxFailures, int(domain.TwoFactorLockDuration/time.Second)).
		Scan(&att.SecretEnc, &att.LastStep)
	if err == nil {
		return att, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.TwoFactorAttempt{}, pgError(err)
	}
	// Ninguna fila actualizada: o está bloqueado o no hay 2FA activo.
	var locked bool
	if err := r.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_totp WHERE user_id=$1 AND enabled = true AND locked_until > now())`,
		userID).Scan(&locked); err != nil {
		return domain.TwoFactorAttempt{}, pgError(err)
	}
	if locked {
		return domain.TwoFactorAttempt{}, domain.ErrTooManyAttempts
	}
	return domain.TwoFactorAttempt{}, domain.ErrNotFound
}

// CompleteStep marca el acierto de un código TOTP del paso `step`: solo gana
// quien lo sube de last_step (anti-replay) y reinicia contador y bloqueo.
func (r TwoFactor) CompleteStep(userID string, step int64) (bool, error) {
	ctx, cancel := r.ctx()
	defer cancel()
	tag, err := r.Pool.Exec(ctx,
		`UPDATE user_totp SET last_step = $2, failed_count = 0, locked_until = NULL
		 WHERE user_id = $1 AND enabled = true AND last_step < $2`, userID, step)
	if err != nil {
		return false, pgError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// UseRecoveryCode gasta UN código y, en la MISMA sentencia, reinicia el
// contador: el UPDATE de user_totp solo corre si el de los códigos devolvió una
// fila (`used_at IS NULL` decide de dos usos simultáneos).
func (r TwoFactor) UseRecoveryCode(userID, codeHash string) (bool, error) {
	ctx, cancel := r.ctx()
	defer cancel()
	tag, err := r.Pool.Exec(ctx,
		`WITH used AS (
		   UPDATE user_recovery_codes SET used_at = now()
		   WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
		   RETURNING 1)
		 UPDATE user_totp SET failed_count = 0, locked_until = NULL
		 WHERE user_id = $1 AND EXISTS (SELECT 1 FROM used)`, userID, codeHash)
	if err != nil {
		return false, pgError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// Disable borra los códigos y el secreto en UNA transacción.
func (r TwoFactor) Disable(userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return pgError(err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM user_totp WHERE user_id=$1`, userID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return pgError(tx.Commit(ctx))
}

var _ ports.TwoFactorRepository = TwoFactor{}
