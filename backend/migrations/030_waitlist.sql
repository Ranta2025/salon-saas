-- 030_waitlist: lista de espera del salón (F7 del contrato de la fase 6,
-- decisiones D2, D3 y D6).
--
--   * waitlist_entries: una persona anotada para UN servicio, UN día LOCAL del
--     salón y, opcionalmente, UNA profesional (NULL = «cualquiera»). El estado
--     se DERIVA de las fechas: `notified_at` y `left_at` NULL son la verdad
--     (waiting / notified / left), no hay una columna `status` que pueda
--     contradecirlas.
--   * token_hash: el SHA-256 (64 hex) del token de la baja. Se emite recién
--     cuando hay un aviso que mandar, así una entrada que nunca se avisa no deja
--     un enlace vivo. El token en claro NUNCA se guarda (D5).
--   * notify_claimed_at / notify_attempts: el reclamo atómico del barrido (D6),
--     con la retención blanda de 2 h y la gracia de 5 min; `notified_at` implica
--     `notify_claimed_at`.
--   * El índice único PARCIAL impide dos avisos activos idénticos: mismo email,
--     servicio, profesional (o cualquiera) y día. Una baja libera el lugar.
--   * El índice parcial del barrido es el que deja barato el barrido de cada
--     minuto: solo mira las entradas sin avisar y sin baja.
--
-- Idempotente: las tablas, columnas y CHECKs se crean con guardas de
-- information_schema / pg_constraint miradas en current_schema() (igual que
-- 015–029) y los índices con pg_indexes. Una segunda pasada no hace nada y no
-- reescribe ninguna fila de las tablas existentes.
--
-- Sobre el NUL: Postgres no puede guardar un carácter 0x00 en un TEXT (lo
-- rechaza con 22021), así que la regla «sin NUL» de D2 se hace cumplir en el
-- dominio (400 exacto, nunca un 500) y el CHECK de largo es el que queda en la
-- base.

-- --- Entradas de la lista --------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'waitlist_entries'
  ) THEN
    CREATE TABLE waitlist_entries (
      id             TEXT PRIMARY KEY,
      salon_id       TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      -- El servicio que la persona quiere: si se borra del catálogo, la entrada
      -- ya no tiene sentido (ON DELETE CASCADE).
      service_id     TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
      -- NULL = «cualquiera»: la persona no pidió una profesional concreta.
      professional_id TEXT REFERENCES users(id) ON DELETE SET NULL,
      -- Día LOCAL del salón (la zona horaria la define salons.timezone).
      day            DATE NOT NULL,
      name           TEXT NOT NULL,
      -- El email es OBLIGATORIO: es el canal del aviso.
      email          TEXT NOT NULL,
      phone          TEXT,
      -- SHA-256 del token de la baja (64 hex). NULL hasta que haya un aviso.
      token_hash     TEXT UNIQUE,
      created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- Reclamo del barrido (D6): retención blanda de 2 h.
      notify_claimed_at TIMESTAMPTZ,
      notify_attempts   INTEGER NOT NULL DEFAULT 0,
      notified_at    TIMESTAMPTZ,
      -- Baja: la pidió la persona por el enlace o la quitó el personal (D3/D8).
      left_at        TIMESTAMPTZ
    );
  END IF;
END $$;

-- CHECKs de D2 (nombre 1..120, email 3..254, teléfono NULL o 1..40, intentos
-- 0..3, hash de 64 cuando existe y `notified_at` implica `notify_claimed_at`).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'waitlist_entries_name_len'
      AND conrelid = to_regclass(current_schema() || '.waitlist_entries')
  ) THEN
    ALTER TABLE waitlist_entries
      ADD CONSTRAINT waitlist_entries_name_len CHECK (char_length(name) BETWEEN 1 AND 120);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'waitlist_entries_email_len'
      AND conrelid = to_regclass(current_schema() || '.waitlist_entries')
  ) THEN
    ALTER TABLE waitlist_entries
      ADD CONSTRAINT waitlist_entries_email_len CHECK (char_length(email) BETWEEN 3 AND 254);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'waitlist_entries_phone_len'
      AND conrelid = to_regclass(current_schema() || '.waitlist_entries')
  ) THEN
    ALTER TABLE waitlist_entries
      ADD CONSTRAINT waitlist_entries_phone_len
      CHECK (phone IS NULL OR char_length(phone) BETWEEN 1 AND 40);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'waitlist_entries_attempts'
      AND conrelid = to_regclass(current_schema() || '.waitlist_entries')
  ) THEN
    ALTER TABLE waitlist_entries
      ADD CONSTRAINT waitlist_entries_attempts CHECK (notify_attempts BETWEEN 0 AND 3);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'waitlist_entries_token_hash'
      AND conrelid = to_regclass(current_schema() || '.waitlist_entries')
  ) THEN
    ALTER TABLE waitlist_entries
      ADD CONSTRAINT waitlist_entries_token_hash
      CHECK (token_hash IS NULL OR (char_length(token_hash) = 64 AND token_hash ~ '^[0-9a-f]+$'));
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'waitlist_entries_notified_claimed'
      AND conrelid = to_regclass(current_schema() || '.waitlist_entries')
  ) THEN
    ALTER TABLE waitlist_entries
      ADD CONSTRAINT waitlist_entries_notified_claimed
      CHECK (notified_at IS NULL OR notify_claimed_at IS NOT NULL);
  END IF;
END $$;

-- Un aviso ACTIVO idéntico por (salón, email, servicio, profesional, día): dos
-- pedidos simultáneos no crean dos entradas (la carrera la resuelve el índice).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'waitlist_entries_active_idx'
  ) THEN
    CREATE UNIQUE INDEX waitlist_entries_active_idx
      ON waitlist_entries (salon_id, lower(email), service_id, coalesce(professional_id, ''), day)
      WHERE left_at IS NULL;
  END IF;
END $$;

-- El barrido de cada minuto: solo entradas sin avisar y sin baja, por salón y
-- día (es el WHERE de ListGroups).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'waitlist_entries_pending_idx'
  ) THEN
    CREATE INDEX waitlist_entries_pending_idx
      ON waitlist_entries (salon_id, day, created_at, id)
      WHERE left_at IS NULL AND notified_at IS NULL;
  END IF;
END $$;

-- La pantalla del personal: entradas vigentes del salón por día y alta.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'waitlist_entries_salon_day_idx'
  ) THEN
    CREATE INDEX waitlist_entries_salon_day_idx
      ON waitlist_entries (salon_id, day, created_at);
  END IF;
END $$;
