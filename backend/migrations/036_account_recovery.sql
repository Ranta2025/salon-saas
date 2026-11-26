-- 036_account_recovery: recuperar contraseña por email, revocación de sesiones y
-- verificación de email (F15, decisión D1 del contrato de la fase 6).
--
--   * users.password_changed_at: cuándo cambió por última vez la contraseña por
--     el restablecimiento (o por la toma de un cascarón, D8). NULL = nunca. Una
--     sesión (JWT) emitida ANTES de esa marca ya no vale (claim `ia`, D4).
--   * users.email_verified_at: cuándo se demostró que la persona lee ese correo.
--     NULL = sin verificar. RELLENO: todos los usuarios que ya existen quedan
--     con email_verified_at = created_at (no se puede demostrar otra cosa y no
--     hay que romper cuentas reales). El relleno corre SOLO en la pasada que crea
--     la columna: una segunda pasada no re-verifica a nadie.
--   * password_resets: un pedido de restablecer por fila; token_hash es el
--     SHA-256 (hex, 64) del token, el token en claro nunca se guarda; used_at se
--     llena al canjear o al invalidar por un pedido más nuevo.
--   * email_verifications: igual, con el email al que se mandó el enlace.
--
-- No borra ni reescribe NADA de las tablas existentes (md5 de users salvo las dos
-- columnas nuevas, memberships, sales, audit_events y user_totp idéntico).
-- Idempotente: guardas de information_schema mirando current_schema() (igual que
-- 015–035). Una segunda pasada no hace nada.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users' AND column_name = 'password_changed_at'
  ) THEN
    ALTER TABLE users ADD COLUMN password_changed_at TIMESTAMPTZ NULL;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'users' AND column_name = 'email_verified_at'
  ) THEN
    ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ NULL;
    -- Relleno de una sola vez, junto con la columna.
    UPDATE users SET email_verified_at = created_at;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'password_resets'
  ) THEN
    CREATE TABLE password_resets (
      id         TEXT PRIMARY KEY,
      user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      token_hash TEXT NOT NULL,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      expires_at TIMESTAMPTZ NOT NULL,
      used_at    TIMESTAMPTZ NULL,
      CONSTRAINT password_resets_token_hash_len CHECK (char_length(token_hash) = 64),
      CONSTRAINT password_resets_token_hash_key UNIQUE (token_hash)
    );
    CREATE INDEX password_resets_user_idx ON password_resets (user_id, created_at DESC);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'email_verifications'
  ) THEN
    CREATE TABLE email_verifications (
      id         TEXT PRIMARY KEY,
      user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      email      TEXT NOT NULL,
      token_hash TEXT NOT NULL,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      expires_at TIMESTAMPTZ NOT NULL,
      used_at    TIMESTAMPTZ NULL,
      CONSTRAINT email_verifications_token_hash_len CHECK (char_length(token_hash) = 64),
      CONSTRAINT email_verifications_token_hash_key UNIQUE (token_hash)
    );
    CREATE INDEX email_verifications_user_idx ON email_verifications (user_id, created_at DESC);
  END IF;
END $$;
