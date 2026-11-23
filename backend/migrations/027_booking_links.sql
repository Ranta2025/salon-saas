-- 027_booking_links: enlace firmado de un solo uso para cancelar o reprogramar
-- el turno desde el email (F1 del contrato de la fase 6, D1/D2/D3).
--
-- El enlace es una CAPABILITY: quien tiene el token puede cancelar o mover ESE
-- turno, sin sesión. Por eso:
--   * `token_hash` guarda SOLO el SHA-256 (hex de 64) del token: el token en
--     claro no existe en esta tabla, ni en el log, ni en ningún listado. Si la
--     base se filtra, los enlaces no se pueden reconstruir.
--   * un solo uso: `used_at` lo marca el UPDATE condicional del reclamo, dentro
--     de la MISMA transacción del cambio (un horario ocupado lo deshace y el
--     token sigue vivo).
--   * NO hay `expires_at` a propósito: la vigencia la decide el TURNO (está
--     `booked` y faltan 2 horas o más). Si el personal lo cancela, el enlace
--     muere solo; si lo mueve, el enlace vale contra el horario nuevo.
--   * `booking_id` en CASCADE: un turno borrado se lleva su enlace. `salon_id`
--     también: el salón es el tenant.
--
-- Un token NUEVO por cada email enviado (confirmación, recordatorio y aviso de
-- cambio), así que esta tabla crece con los correos y no con los turnos: el
-- índice por `booking_id` es lo que mantiene barato el borrado en cascada.
--
-- SIN backfill: las reservas existentes no tienen enlace hasta su próximo email.
--
-- Idempotente: el runner reaplica todo en cada boot y hay tests que ejecutan el
-- archivo dos veces; las guardas miran `current_schema()` / `pg_indexes` del
-- schema actual, igual que las 015/016/018/019/020/021.

CREATE TABLE IF NOT EXISTS booking_links (
  id         TEXT PRIMARY KEY,
  -- El salón es el tenant: si el salón se va, sus enlaces se van con él.
  salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  booking_id TEXT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  -- SHA-256 en hex del token del enlace. NUNCA el token en claro.
  token_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- NULL = todavía sirve. Lo pone el UPDATE condicional del reclamo.
  used_at    TIMESTAMPTZ,
  -- La DB sostiene lo que el borde ya valida: un hash SHA-256 mide 64 en hex.
  CONSTRAINT booking_links_token_hash_len CHECK (char_length(token_hash) = 64)
);

-- Búsqueda por token: es la ÚNICA query del sistema, junto con la invitación, que
-- no va scopeada por salón (el token es la capability) y tiene que ser única para
-- que el reclamo sea inequívoco.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'booking_links_token_hash_key'
  ) THEN
    CREATE UNIQUE INDEX booking_links_token_hash_key ON booking_links (token_hash);
  END IF;
END $$;

-- Un turno acumula un enlace por email enviado: el índice por turno es el que
-- hace barato el ON DELETE CASCADE del turno.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'booking_links_booking_id_idx'
  ) THEN
    CREATE INDEX booking_links_booking_id_idx ON booking_links (booking_id);
  END IF;
END $$;
