-- 038_reviews: reseñas posvisita (F13 del contrato de la fase 6, decisión D2).
--
--   * review_settings: UNA fila por salón (PK salon_id): activadas y la demora en
--     horas (1..72) que se espera desde que termina el turno. Sin fila = nunca se
--     configuraron: la API responde enabled:false y delay_hours:24.
--   * review_requests: UN pedido de reseña por turno (booking_id UNIQUE): el
--     turno atendido del que salió el enlace del correo. Guarda SOLO el SHA-256
--     (hex, 64) del token, nunca el token en claro. `sent_at` es el ARRIENDO del
--     envío (el último reclamo, no la entrega) y `delivered_at` se sella DESPUÉS
--     de que el correo salió bien: si el proceso muere entre el reclamo y el
--     envío, el arriendo vence (15 min) y el barrido reintenta; un pedido
--     entregado no vuelve a salir nunca. `used_at` se llena al canjear el enlace,
--     que sirve UNA sola vez; el enlace vence a los 30 días. El token_hash es
--     UNIQUE: el canje es un UPDATE condicional por ese índice.
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
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- ARRIENDO del envío: cuándo lo reclamó (por última vez) un barredor. No
      -- significa que el correo haya salido.
      sent_at      TIMESTAMPTZ NULL,
      -- ENTREGA: se sella después de que el correo salió bien. Un pedido con
      -- delivered_at NUNCA vuelve a salir.
      delivered_at TIMESTAMPTZ NULL,
      expires_at   TIMESTAMPTZ NOT NULL,
      used_at      TIMESTAMPTZ NULL,
      CONSTRAINT review_requests_token_hash_len CHECK (char_length(token_hash) = 64),
      CONSTRAINT review_requests_expires_after_created CHECK (expires_at > created_at),
      -- Solo se canjea lo que se ENTREGÓ: un pedido sin correo no tiene enlace
      -- (el arriendo no cuenta: se puede tener el arriendo y no haber mandado nada).
      CONSTRAINT review_requests_used_after_delivered
        CHECK (used_at IS NULL OR delivered_at IS NOT NULL)
    );
  END IF;
END $$;

-- Una base donde ya corrió una versión anterior de ESTA migración (sin publicar)
-- tiene la tabla sin `delivered_at` y con el CHECK viejo: se actualiza en el
-- lugar, sin perder filas.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'review_requests' AND column_name = 'delivered_at'
  ) THEN
    ALTER TABLE review_requests ADD COLUMN delivered_at TIMESTAMPTZ NULL;
    -- Relleno de una sola vez: la versión anterior sellaba sent_at al reclamar y
    -- mandaba el correo enseguida, así que lo reclamado se da por entregado.
    UPDATE review_requests SET delivered_at = sent_at WHERE sent_at IS NOT NULL;
  END IF;
END $$;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'review_requests_used_after_sent' AND conrelid = 'review_requests'::regclass
  ) THEN
    ALTER TABLE review_requests DROP CONSTRAINT review_requests_used_after_sent;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'review_requests_used_after_delivered' AND conrelid = 'review_requests'::regclass
  ) THEN
    ALTER TABLE review_requests ADD CONSTRAINT review_requests_used_after_delivered
      CHECK (used_at IS NULL OR delivered_at IS NOT NULL);
  END IF;
END $$;

-- El barrido pregunta por los pedidos SIN ENTREGAR (los que hay que mandar o
-- reintentar); este índice parcial sirve esa parte. El nombre cambió con el
-- arriendo: una base con la versión anterior se migra en el lugar.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'review_requests_undelivered_idx'
  ) THEN
    DROP INDEX IF EXISTS review_requests_unsent_idx;
    CREATE INDEX review_requests_undelivered_idx
      ON review_requests (salon_id, sent_at) WHERE delivered_at IS NULL;
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
