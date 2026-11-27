-- 038_reviews: reseñas posvisita (F13 del contrato de la fase 6, decisión D2).
--
--   * review_settings: UNA fila por salón (PK salon_id): activadas y la demora en
--     horas (1..72) que se espera desde que termina el turno. Sin fila = nunca se
--     configuraron: la API responde enabled:false y delay_hours:24.
--   * review_requests: UN pedido de reseña por turno (booking_id UNIQUE): el
--     turno atendido del que salió el enlace del correo. Guarda SOLO el SHA-256
--     (hex, 64) del token, nunca el token en claro. `sent_at` es el RECLAMO del
--     envío: NULL = todavía no salió y el barrido lo reintenta; `used_at` se
--     llena al canjear el enlace, que sirve UNA sola vez; vence a los 30 días.
--     El token_hash es UNIQUE: el canje es un UPDATE condicional por ese índice.
--   * reviews: UNA reseña por turno (booking_id UNIQUE), 1..5 estrellas, un
--     comentario de hasta 500 CARACTERES (char_length cuenta runas, no bytes), el
--     nombre público (D5: «Lía G.»), el nombre del servicio y `hidden`: el salón
--     puede OCULTARLA, nunca editarla ni borrarla.
--
-- No toca NINGUNA tabla existente (D1): F13 solo LEE bookings, y lo hace por su
-- propio adaptador de solo lectura. Idempotente: tablas con guardas de
-- information_schema miradas en current_schema() (igual que 015-037) e índices
-- con pg_indexes. Una segunda pasada no hace nada.

-- --- Configuración de reseñas del salón -------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'review_settings'
  ) THEN
    CREATE TABLE review_settings (
      salon_id    TEXT PRIMARY KEY REFERENCES salons(id) ON DELETE CASCADE,
      enabled     BOOLEAN NOT NULL,
      delay_hours INTEGER NOT NULL CHECK (delay_hours BETWEEN 1 AND 72),
      updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
    );
  END IF;
END $$;

-- --- Pedidos de reseña (el enlace de un solo uso) ---------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'review_requests'
  ) THEN
    CREATE TABLE review_requests (
      id         TEXT PRIMARY KEY,
      salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      -- UN pedido por turno: es la idempotencia del barrido (dos barredores no
      -- mandan dos correos) y la guarda de «una reseña por turno».
      booking_id TEXT NOT NULL UNIQUE REFERENCES bookings(id) ON DELETE CASCADE,
      token_hash TEXT NOT NULL UNIQUE,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      sent_at    TIMESTAMPTZ NULL,
      expires_at TIMESTAMPTZ NOT NULL,
      used_at    TIMESTAMPTZ NULL,
      CONSTRAINT review_requests_token_hash_len CHECK (char_length(token_hash) = 64),
      CONSTRAINT review_requests_expires_after_created CHECK (expires_at > created_at),
      -- Solo se canjea lo que se envió: un pedido sin correo no tiene enlace.
      CONSTRAINT review_requests_used_after_sent
        CHECK (used_at IS NULL OR sent_at IS NOT NULL)
    );
  END IF;
END $$;

-- El barrido pregunta por los pedidos pendientes de envío (reintento) y por los
-- turnos atendidos sin pedido; este índice sirve la parte «pendiente».
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'review_requests_unsent_idx'
  ) THEN
    CREATE INDEX review_requests_unsent_idx
      ON review_requests (salon_id, sent_at) WHERE sent_at IS NULL;
  END IF;
END $$;

-- --- Reseñas publicadas -------------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'reviews'
  ) THEN
    CREATE TABLE reviews (
      id           TEXT PRIMARY KEY,
      salon_id     TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      booking_id   TEXT NOT NULL UNIQUE REFERENCES bookings(id) ON DELETE CASCADE,
      rating       INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
      -- char_length cuenta RUNAS (un emoji cuenta uno): 500 caracteres del
      -- contrato, no 500 bytes.
      comment      TEXT NOT NULL DEFAULT '' CHECK (char_length(comment) <= 500),
      -- D5: nombre de pila + inicial del apellido. Nunca correo, teléfono ni turno.
      author_name  TEXT NOT NULL DEFAULT '' CHECK (char_length(author_name) <= 120),
      service_name TEXT NOT NULL DEFAULT '' CHECK (char_length(service_name) <= 120),
      hidden       BOOLEAN NOT NULL DEFAULT false,
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
    );
  END IF;
END $$;

-- Panel: (salon_id) del más nuevo al más viejo, con su cursor (created_at, id).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'reviews_salon_created_idx'
  ) THEN
    CREATE INDEX reviews_salon_created_idx
      ON reviews (salon_id, created_at DESC, id DESC);
  END IF;
END $$;

-- Web pública: solo las VISIBLES (una reseña oculta no cuenta ni se muestra) y
-- las 6 más recientes de cada salón.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'reviews_salon_visible_idx'
  ) THEN
    CREATE INDEX reviews_salon_visible_idx
      ON reviews (salon_id, created_at DESC, id DESC) WHERE NOT hidden;
  END IF;
END $$;
