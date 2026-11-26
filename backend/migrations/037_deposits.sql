-- 037_deposits: sena online con banco simulado (F11 del contrato de la fase 6,
-- decision D2).
--
--   * deposit_policies: UNA fila por salón (PK salon_id): activada, porcentaje
--     (10..100), espera del pago (5..120 min) y ventana de devolución (0..168 h).
--     Sin fila = la sena nunca se configuró (la API responde enabled:false y el
--     resto null). No hay borrado: guardar es un upsert.
--   * booking_deposits: UNA sena por turno (booking_id UNIQUE) con el monto y el
--     porcentaje FOTO del momento (cambiar la política no la mueve) y el
--     `charge_id` del banco, que es la credencial pública de la página de pago.
--     `client_op_id` (índice único parcial por salón) es la idempotencia del
--     navegador: el reintento no crea otro cargo.
--   * deposit_events: el libro mayor de la sena. APPEND-ONLY con el MISMO trigger
--     de 034 (audit_events): rechaza UPDATE y DELETE, y permite el borrado en
--     cascada con la sena/salón. `bank_event_id` (índice único parcial) es la
--     guarda de «un evento del banco se aplica UNA sola vez»: 30 callbacks
--     simultáneos del mismo evento dejan un solo `paid` y un solo correo.
--   * simbank_charges / simbank_refunds: el banco simulado, en SUS tablas. El
--     número de tarjeta no está acá (solo last4 y marca) y un cargo se reembolsa
--     UNA vez (UNIQUE charge_id: la idempotencia del puerto Refund).
--
-- No toca NINGUNA tabla existente (D1): la sena no mueve ventas, caja, nómina ni
-- ganancia. Idempotente: tablas con guardas de information_schema miradas en
-- current_schema() (igual que 015-036), índices con pg_indexes, función con
-- CREATE OR REPLACE y trigger con pg_trigger. Una segunda pasada no hace nada.

-- --- Política de señas del salón --------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'deposit_policies'
  ) THEN
    CREATE TABLE deposit_policies (
      salon_id     TEXT PRIMARY KEY REFERENCES salons(id) ON DELETE CASCADE,
      enabled      BOOLEAN NOT NULL,
      percent      INTEGER NOT NULL CHECK (percent BETWEEN 10 AND 100),
      hold_minutes INTEGER NOT NULL CHECK (hold_minutes BETWEEN 5 AND 120),
      refund_hours INTEGER NOT NULL CHECK (refund_hours BETWEEN 0 AND 168),
      updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
    );
  END IF;
END $$;

-- --- Señas de las reservas ---------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'booking_deposits'
  ) THEN
    CREATE TABLE booking_deposits (
      id           TEXT PRIMARY KEY,
      salon_id     TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      -- UNA sena por turno: el reintento de la reserva la encuentra por acá.
      booking_id   TEXT NOT NULL UNIQUE REFERENCES bookings(id) ON DELETE CASCADE,
      amount_cents INTEGER NOT NULL CHECK (amount_cents BETWEEN 1 AND 2000000000),
      -- FOTO del porcentaje con el que se calculó el monto.
      percent      INTEGER NOT NULL CHECK (percent BETWEEN 10 AND 100),
      status       TEXT NOT NULL
        CHECK (status IN ('pending','paid','expired','refunded','forfeited')),
      -- El id del cargo del banco (43 caracteres base64url, D8) es UNIQUE: la
      -- credencial pública y la clave del callback.
      charge_id    TEXT NOT NULL UNIQUE,
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      expires_at   TIMESTAMPTZ NOT NULL,
      paid_at      TIMESTAMPTZ NULL,
      settled_at   TIMESTAMPTZ NULL,
      client_op_id TEXT NULL,
      CONSTRAINT booking_deposits_expires_after_created CHECK (expires_at > created_at),
      CONSTRAINT booking_deposits_charge_len CHECK (char_length(charge_id) BETWEEN 1 AND 100),
      CONSTRAINT booking_deposits_client_op_len
        CHECK (client_op_id IS NULL OR char_length(client_op_id) BETWEEN 1 AND 100),
      -- Una sena pagada dice cuándo; la que se devuelve sin haberse pagado (pago
      -- tardío) no tiene paid_at.
      CONSTRAINT booking_deposits_paid_at CHECK (status <> 'paid' OR paid_at IS NOT NULL),
      -- refunded y forfeited son finales y siempre tienen settled_at.
      CONSTRAINT booking_deposits_settled_at
        CHECK ((status IN ('refunded','forfeited')) = (settled_at IS NOT NULL))
    );
  END IF;
END $$;

-- Un client_op_id se usa UNA vez por salón (idempotencia de D8).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'booking_deposits_client_op_uniq'
  ) THEN
    CREATE UNIQUE INDEX booking_deposits_client_op_uniq
      ON booking_deposits (salon_id, client_op_id) WHERE client_op_id IS NOT NULL;
  END IF;
END $$;

-- La pantalla «Señas» (D10): orden (created_at DESC, id DESC) y su cursor.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'booking_deposits_salon_created_idx'
  ) THEN
    CREATE INDEX booking_deposits_salon_created_idx
      ON booking_deposits (salon_id, created_at DESC, id DESC);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'booking_deposits_salon_status_idx'
  ) THEN
    CREATE INDEX booking_deposits_salon_status_idx
      ON booking_deposits (salon_id, status, created_at DESC, id DESC);
  END IF;
END $$;

-- El barrido (D9): las pendientes que vencen y las pagadas por cerrar.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'booking_deposits_pending_expires_idx'
  ) THEN
    CREATE INDEX booking_deposits_pending_expires_idx
      ON booking_deposits (expires_at) WHERE status = 'pending';
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'booking_deposits_paid_idx'
  ) THEN
    CREATE INDEX booking_deposits_paid_idx
      ON booking_deposits (created_at) WHERE status = 'paid';
  END IF;
END $$;

-- --- Libro mayor de la sena --------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'deposit_events'
  ) THEN
    CREATE TABLE deposit_events (
      id            TEXT PRIMARY KEY,
      deposit_id    TEXT NOT NULL REFERENCES booking_deposits(id) ON DELETE CASCADE,
      kind          TEXT NOT NULL
        CHECK (kind IN ('created','attempt_declined','paid','expired','refunded',
                        'forfeited','late_payment_refunded')),
      amount_cents  INTEGER NOT NULL CHECK (amount_cents BETWEEN 0 AND 2000000000),
      detail        TEXT NULL CHECK (detail IS NULL OR char_length(detail) <= 200),
      -- Id del evento del banco; NULL para los eventos nuestros (created,
      -- expired, refunded/forfeited del barrido).
      bank_event_id TEXT NULL
        CHECK (bank_event_id IS NULL OR char_length(bank_event_id) BETWEEN 1 AND 200),
      created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
    );
  END IF;
END $$;

-- UN evento del banco se aplica UNA vez, pase lo que pase (D7).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'deposit_events_bank_event_uniq'
  ) THEN
    CREATE UNIQUE INDEX deposit_events_bank_event_uniq
      ON deposit_events (bank_event_id) WHERE bank_event_id IS NOT NULL;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'deposit_events_deposit_idx'
  ) THEN
    CREATE INDEX deposit_events_deposit_idx
      ON deposit_events (deposit_id, created_at, id);
  END IF;
END $$;

-- Append-only, igual que audit_events (034). El borrado en cascada de la sena (o
-- del salón) SÍ se permite: corre dentro de un trigger de integridad
-- referencial (pg_trigger_depth() > 1) y la sena ya no existe. Un DELETE directo
-- corre a profundidad 1 y siempre falla; lo mismo el de una sena que sigue viva.
CREATE OR REPLACE FUNCTION deposit_events_append_only() RETURNS trigger AS $fn$
BEGIN
  IF TG_OP = 'DELETE'
     AND pg_trigger_depth() > 1
     AND NOT EXISTS (SELECT 1 FROM booking_deposits WHERE id = OLD.deposit_id) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'deposit_events es de solo escritura (no se permite %)', TG_OP
    USING ERRCODE = 'restrict_violation';
END
$fn$ LANGUAGE plpgsql;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_trigger t
    JOIN pg_class c ON c.oid = t.tgrelid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = current_schema()
      AND c.relname = 'deposit_events'
      AND t.tgname = 'deposit_events_append_only'
      AND NOT t.tgisinternal
  ) THEN
    CREATE TRIGGER deposit_events_append_only
      BEFORE UPDATE OR DELETE ON deposit_events
      FOR EACH ROW EXECUTE FUNCTION deposit_events_append_only();
  END IF;
END $$;

-- --- Banco simulado: cargos y reembolsos ------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'simbank_charges'
  ) THEN
    CREATE TABLE simbank_charges (
      id           TEXT PRIMARY KEY,
      amount_cents INTEGER NOT NULL CHECK (amount_cents BETWEEN 1 AND 2000000000),
      reference    TEXT NOT NULL,
      description  TEXT NULL,
      status       TEXT NOT NULL
        CHECK (status IN ('pending','approved','declined','insufficient','refunded')),
      attempts     INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 1000),
      -- Lo ÚNICO que se guarda de la tarjeta: el final y la marca.
      last4        TEXT NULL CHECK (last4 IS NULL OR last4 ~ '^[0-9]{4}$'),
      brand        TEXT NULL
        CHECK (brand IS NULL OR brand IN ('visa','mastercard','amex','otra')),
      expires_at   TIMESTAMPTZ NOT NULL,
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      decided_at   TIMESTAMPTZ NULL,
      CONSTRAINT simbank_charges_reference_len
        CHECK (char_length(reference) BETWEEN 1 AND 100),
      CONSTRAINT simbank_charges_description_len
        CHECK (description IS NULL OR char_length(description) <= 200),
      -- Un cargo decidido dice cuándo (y solo entonces tiene tarjeta).
      CONSTRAINT simbank_charges_decided_at
        CHECK ((status <> 'pending') = (decided_at IS NOT NULL))
    );
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'simbank_charges_pending_idx'
  ) THEN
    CREATE INDEX simbank_charges_pending_idx
      ON simbank_charges (expires_at) WHERE status = 'pending';
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'simbank_refunds'
  ) THEN
    CREATE TABLE simbank_refunds (
      id           TEXT PRIMARY KEY,
      charge_id    TEXT NOT NULL REFERENCES simbank_charges(id) ON DELETE CASCADE,
      amount_cents INTEGER NOT NULL CHECK (amount_cents BETWEEN 1 AND 2000000000),
      reason       TEXT NOT NULL DEFAULT '' CHECK (char_length(reason) <= 200),
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- Un cargo se reembolsa UNA vez: es la idempotencia del puerto Refund
      -- (reembolsar dos veces devuelve nil sin duplicar).
      CONSTRAINT simbank_refunds_charge_uniq UNIQUE (charge_id)
    );
  END IF;
END $$;
