-- 021_invitations: invitaciones de equipo (F2g1, S-F2g1-1).
--
-- Hoy `POST /members` exige que la persona YA tenga cuenta ("La persona todavía
-- no tiene cuenta"). La invitación resuelve el alta de gente nueva: el salón
-- emite un enlace de un solo uso, la persona crea su cuenta y entra con el rol
-- asignado.
--
-- Seguridad (no negociable):
--   * `token_hash` guarda SOLO el SHA-256 del token. El token en claro no existe
--     en esta tabla, ni en el log, ni en ningún listado: si la DB se filtra, los
--     enlaces no se pueden reconstruir.
--   * un solo uso: `accepted_at` lo marca el UPDATE condicional del canje
--     (accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()).
--   * vence a los 7 días (`expires_at`).
--   * el índice único PARCIAL de la pendiente por email evita dos invitaciones
--     vivas para la misma dirección en el mismo salón (reinvitar revoca la
--     anterior y emite una nueva); una aceptada o revocada no ocupa el lugar.
--     OJO: el predicado NO mira el vencimiento (un índice no puede usar now()),
--     así que una invitación vencida que nadie revocó SIGUE ocupando el lugar.
--     Lo compensa el caso de uso: al reinvitar se revoca toda pendiente de ese
--     email sin mirar la fecha (probado: reinvitar sobre una vencida da 201).
--
-- SIN backfill: una base migrada no tiene invitaciones hasta que alguien emite
-- una, así que el comportamiento de hoy no cambia.
--
-- Idempotente: el runner reaplica todo en cada boot y los tests ejecutan el
-- archivo dos veces. Las guardas miran current_schema() y pg_class/pg_indexes
-- del schema actual, igual que las 015/016/018/019/020.

CREATE TABLE IF NOT EXISTS invitations (
  id          TEXT PRIMARY KEY,
  -- El salón es el tenant: si el salón se va, sus invitaciones se van con él.
  salon_id    TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  -- Email NORMALIZADO (minúsculas, sin espacios): es la dirección a la que va el
  -- enlace y con la que se compara la cuenta que acepta.
  email       TEXT NOT NULL,
  role        TEXT NOT NULL,
  -- La marca de "atiende clientes" que va a tener la membresía (F2a), validada
  -- al emitir con la misma regla del PATCH de equipo.
  attends     BOOLEAN NOT NULL DEFAULT false,
  -- SHA-256 en hex del token del enlace. NUNCA el token en claro.
  token_hash  TEXT NOT NULL,
  expires_at  TIMESTAMPTZ NOT NULL,
  accepted_at TIMESTAMPTZ,
  accepted_by TEXT NOT NULL DEFAULT '',
  revoked_at  TIMESTAMPTZ,
  -- Quién la emitió (owner/admin). Sin FK: el dato sirve para el historial y no
  -- tiene que sobrevivir a un cambio de equipo.
  created_by  TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- El hash es SHA-256 en hex (64) y el email no pasa el límite de una cuenta:
  -- la DB sostiene lo que no necesita texto, aunque el borde ya lo valide.
  -- La vigencia NO se chequea contra created_at a propósito: un entorno de prueba
  -- adelanta expires_at para probar el vencimiento y el chequeo lo impediría; el
  -- que decide es el UPDATE condicional del canje.
  CONSTRAINT invitations_token_hash_len CHECK (char_length(token_hash) = 64),
  CONSTRAINT invitations_email_len CHECK (char_length(email) <= 254)
);

-- Búsqueda por token: es la ÚNICA query del sistema sin salon_id (el token es la
-- capability) y tiene que ser única para que el canje sea inequívoco. Se crea acá
-- (no en el CREATE TABLE) para que reaplicar el archivo sea un no-op.
CREATE UNIQUE INDEX IF NOT EXISTS invitations_token_hash_key ON invitations (token_hash);

-- Una sola PENDIENTE por email y salón. El índice es parcial: las aceptadas,
-- revocadas y vencidas no compiten. `lower(email)` porque el email se normaliza
-- en el borde, pero el índice tampoco confía en eso.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'invitations_pending_email_idx'
  ) THEN
    CREATE UNIQUE INDEX invitations_pending_email_idx
      ON invitations (salon_id, lower(email))
      WHERE accepted_at IS NULL AND revoked_at IS NULL;
  END IF;
END $$;
