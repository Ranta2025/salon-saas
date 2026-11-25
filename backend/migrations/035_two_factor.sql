-- 035_two_factor: 2FA TOTP opcional por usuario (F10, parte B del contrato de
-- la fase 6, decisión D6). Va aparte de la 034 (auditoría): el backend se
-- construyó en dos tramos.
--
--   * user_totp: UNA fila por usuario (PK user_id, ON DELETE CASCADE).
--       - secret_enc: el secreto TOTP CIFRADO con AES-256-GCM (nonce de 12 bytes
--         al principio). La clave sale de JWT_SECRET (HMAC-SHA256 con la etiqueta
--         `salon-saas/totp/v1`): la base nunca guarda el secreto en claro y rotar
--         JWT_SECRET invalida los 2FA ya enrolados (hay que re-enrolar).
--       - enabled: false = enrolamiento PENDIENTE (setup hecho, falta confirmar
--         con un código); true = el login exige el segundo paso.
--       - last_step: último paso TOTP aceptado (anti-replay: solo se acepta un
--         paso mayor; lo garantiza `UPDATE ... WHERE last_step < paso`).
--       - failed_count / locked_until: fuerza bruta por usuario (D9). El
--         contador y el bloqueo (5 fallos -> 15 minutos) los mueve UN solo
--         `UPDATE ... RETURNING` del adaptador.
--   * user_recovery_codes: los 8 códigos de recuperación, guardados como SHA-256
--     (son aleatorios de alta entropía). UNIQUE (user_id, code_hash). Se gasta
--     con `UPDATE ... WHERE used_at IS NULL`: de dos usos simultáneos gana UNO.
--
-- No toca NINGUNA tabla existente: un usuario sin fila en user_totp (todos hoy)
-- entra como siempre. Idempotente: tablas con guarda de information_schema
-- mirada en current_schema() (igual que 015–034). Una segunda pasada no hace
-- nada.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'user_totp'
  ) THEN
    CREATE TABLE user_totp (
      user_id      TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
      secret_enc   BYTEA NOT NULL,
      enabled      BOOLEAN NOT NULL DEFAULT false,
      last_step    BIGINT NOT NULL DEFAULT 0,
      failed_count INTEGER NOT NULL DEFAULT 0,
      locked_until TIMESTAMPTZ,
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      enabled_at   TIMESTAMPTZ,
      CONSTRAINT user_totp_secret_len CHECK (octet_length(secret_enc) > 0),
      CONSTRAINT user_totp_last_step_chk CHECK (last_step >= 0),
      CONSTRAINT user_totp_failed_count_chk CHECK (failed_count >= 0),
      -- Un 2FA activo siempre dice desde cuándo.
      CONSTRAINT user_totp_enabled_at_chk CHECK (NOT enabled OR enabled_at IS NOT NULL)
    );
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'user_recovery_codes'
  ) THEN
    CREATE TABLE user_recovery_codes (
      id         TEXT PRIMARY KEY,
      user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      -- SHA-256 en hex (64 caracteres) del código normalizado.
      code_hash  TEXT NOT NULL,
      used_at    TIMESTAMPTZ,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      CONSTRAINT user_recovery_codes_hash_len CHECK (char_length(code_hash) = 64),
      CONSTRAINT user_recovery_codes_user_hash_key UNIQUE (user_id, code_hash)
    );
  END IF;
END $$;
