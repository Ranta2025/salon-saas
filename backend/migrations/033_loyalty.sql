-- 033_loyalty: fidelidad, «cada N visitas, un servicio de regalo» (F9 del
-- contrato de la fase 6, decisiones D1, D2, D3).
--
--   * loyalty_programs: UNA fila por salón (PK salon_id): activado, visitas
--     necesarias (2..50), el servicio de regalo, la vigencia opcional del vale
--     (NULL = no vence) y `counting_from`, el momento en que el programa se
--     guardó por PRIMERA vez: el historial anterior no cuenta, activarlo no
--     regala vales de golpe (D2). Las visitas NO se guardan: se calculan de
--     `sales` + `sale_items` con una consulta de solo lectura.
--   * loyalty_rewards: los vales entregados. El servicio y su nombre son una
--     FOTO del programa al entregar. UNIQUE (client_id, reward_number): UN vale
--     por hito pase lo que pase (la guarda final de la entrega, D6). El
--     `redeemed_at IS NULL` del UPDATE condicional del adaptador es la guarda del
--     uso (D7). Los índices únicos parciales de grant_op_id y redeem_op_id
--     detectan el pedido repetido de la cola sin entregar ni usar dos veces.
--
-- No toca NINGUNA tabla existente (D3): usar un vale no es una venta y no mueve
-- dinero. Idempotente: tablas con guardas de information_schema miradas en
-- current_schema() (igual que 015–032) e índices con pg_indexes. Una segunda
-- pasada no hace nada.

-- --- Programa del salón -----------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'loyalty_programs'
  ) THEN
    CREATE TABLE loyalty_programs (
      salon_id            TEXT PRIMARY KEY REFERENCES salons(id) ON DELETE CASCADE,
      enabled             BOOLEAN NOT NULL,
      visits_required     INTEGER NOT NULL CHECK (visits_required BETWEEN 2 AND 50),
      -- RESTRICT: un servicio de regalo no se borra (la API solo lo desactiva).
      reward_service_id   TEXT NOT NULL REFERENCES services(id) ON DELETE RESTRICT,
      -- NULL = el vale no vence.
      reward_validity_days INTEGER
        CHECK (reward_validity_days IS NULL OR reward_validity_days BETWEEN 1 AND 3650),
      -- Se fija UNA vez, al primer guardado; los siguientes lo conservan.
      counting_from       TIMESTAMPTZ NOT NULL,
      updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
    );
  END IF;
END $$;

-- --- Vales entregados -------------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'loyalty_rewards'
  ) THEN
    CREATE TABLE loyalty_rewards (
      id                     TEXT PRIMARY KEY,
      salon_id               TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      client_id              TEXT NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
      -- El hito: el vale número 1, 2, 3... de esa clienta.
      reward_number          INTEGER NOT NULL CHECK (reward_number >= 1),
      service_id             TEXT NOT NULL REFERENCES services(id) ON DELETE RESTRICT,
      -- FOTO del nombre del servicio al entregar.
      service_name           TEXT NOT NULL,
      granted_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- NULL = no vence. Fin del día LOCAL del salón de hoy + vigencia.
      expires_at             TIMESTAMPTZ,
      redeemed_at            TIMESTAMPTZ,
      redeemed_by            TEXT,
      redeemed_professional_id TEXT,
      redeem_note            TEXT,
      -- Operation id de la cola offline: el repetido no entrega ni usa dos veces.
      grant_op_id            TEXT,
      redeem_op_id           TEXT,
      -- UN vale por hito pase lo que pase (D6): la guarda final de la entrega.
      CONSTRAINT loyalty_rewards_client_number_uniq UNIQUE (client_id, reward_number),
      CONSTRAINT loyalty_rewards_name_len CHECK (char_length(service_name) BETWEEN 1 AND 120),
      CONSTRAINT loyalty_rewards_note_len CHECK (redeem_note IS NULL
        OR (char_length(redeem_note) <= 200 AND redeem_note !~ '[\r\n]')),
      -- Un vale usado dice quién lo usó.
      CONSTRAINT loyalty_rewards_redeemed_by CHECK (redeemed_at IS NULL OR redeemed_by IS NOT NULL)
    );
  END IF;
END $$;

-- Un grant_op_id / redeem_op_id se usa UNA vez por salón.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'loyalty_rewards_grant_op_uniq'
  ) THEN
    CREATE UNIQUE INDEX loyalty_rewards_grant_op_uniq
      ON loyalty_rewards (salon_id, grant_op_id) WHERE grant_op_id IS NOT NULL;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'loyalty_rewards_redeem_op_uniq'
  ) THEN
    CREATE UNIQUE INDEX loyalty_rewards_redeem_op_uniq
      ON loyalty_rewards (salon_id, redeem_op_id) WHERE redeem_op_id IS NOT NULL;
  END IF;
END $$;

-- Vales de una clienta (la ficha), los más nuevos primero.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'loyalty_rewards_client_idx'
  ) THEN
    CREATE INDEX loyalty_rewards_client_idx
      ON loyalty_rewards (salon_id, client_id, reward_number DESC);
  END IF;
END $$;

-- Las visitas se calculan sobre las ventas de UNA clienta (sales_salon_client_idx,
-- 028) y sus líneas (sale_items_sale_idx, 005): no hace falta ningún índice nuevo
-- y ninguna tabla existente se toca.
