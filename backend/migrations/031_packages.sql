-- 031_packages: paquetes y bonos prepagos con saldo por clienta (F8 del
-- contrato de la fase 6, decisiones D1, D3 y D4).
--
--   * service_packages: el catálogo. UN servicio, N sesiones, un precio fijo y
--     una vigencia opcional (NULL = no vence).
--   * client_packages: lo que compró una clienta. El precio y el nombre son una
--     FOTO del catálogo al comprar. `sessions_left` es un CONTADOR y
--     package_movements su LIBRO: en todo momento sessions_left = Σ delta (D4).
--     El CHECK 0..sessions_total y el UPDATE condicional del adaptador son las
--     dos barreras contra un saldo negativo o mayor que lo comprado.
--     `sale_id` UNIQUE: una compra = una venta (el dinero entra por la venta).
--   * package_movements: APPEND-ONLY (compra, consumo, ajuste). El índice único
--     parcial (salon_id, client_op_id) detecta el consumo repetido de la cola
--     offline sin descontar dos veces.
--   * sales.packages_cents: la parte de la venta que es un paquete (0 en toda
--     venta anterior: SIN backfill). La nómina la descuenta de `ventas` (D6).
--
-- Idempotente: tablas, columnas y CHECKs con guardas de information_schema /
-- pg_constraint miradas en current_schema() (igual que 015–030) y los índices
-- con pg_indexes. Una segunda pasada no hace nada: la columna nueva tiene
-- DEFAULT 0 y no reescribe filas, así que el md5 de `sales`, `sale_items`,
-- `clients` y `products` no cambia.

-- --- Catálogo de paquetes --------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'service_packages'
  ) THEN
    CREATE TABLE service_packages (
      id            TEXT PRIMARY KEY,
      salon_id      TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      name          TEXT NOT NULL,
      -- RESTRICT: un servicio con paquetes no se borra (la API solo lo desactiva).
      service_id    TEXT NOT NULL REFERENCES services(id) ON DELETE RESTRICT,
      sessions      INTEGER NOT NULL CHECK (sessions BETWEEN 1 AND 100),
      -- Dinero en centavos enteros, nunca float.
      price_cents   INTEGER NOT NULL CHECK (price_cents BETWEEN 0 AND 10000000),
      -- NULL = sin vencimiento.
      validity_days INTEGER CHECK (validity_days IS NULL OR validity_days BETWEEN 1 AND 3650),
      -- Quitar un paquete es DESACTIVARLO: no hay DELETE.
      active        BOOLEAN NOT NULL DEFAULT true,
      created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
      CONSTRAINT service_packages_name_len CHECK (char_length(name) BETWEEN 1 AND 120),
      CONSTRAINT service_packages_name_text CHECK (name !~ '[\r\n]')
    );
  END IF;
END $$;

-- Un nombre ACTIVO es único por salón, sin importar mayúsculas (como products).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'service_packages_active_name_idx'
  ) THEN
    CREATE UNIQUE INDEX service_packages_active_name_idx
      ON service_packages (salon_id, lower(name)) WHERE active;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'service_packages_salon_name_idx'
  ) THEN
    CREATE INDEX service_packages_salon_name_idx ON service_packages (salon_id, name);
  END IF;
END $$;

-- --- La parte de paquete de la venta -------------------------------------
-- Va ANTES de client_packages: la FK de sale_id apunta a `sales`.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'sales' AND column_name = 'packages_cents'
  ) THEN
    ALTER TABLE sales ADD COLUMN packages_cents INTEGER NOT NULL DEFAULT 0;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sales_packages_cents_range'
      AND conrelid = to_regclass(current_schema() || '.sales')
  ) THEN
    ALTER TABLE sales
      ADD CONSTRAINT sales_packages_cents_range
      CHECK (packages_cents >= 0 AND packages_cents <= total_cents);
  END IF;
END $$;

-- --- Paquetes comprados por una clienta -----------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'client_packages'
  ) THEN
    CREATE TABLE client_packages (
      id             TEXT PRIMARY KEY,
      salon_id       TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      client_id      TEXT NOT NULL REFERENCES clients(id) ON DELETE RESTRICT,
      package_id     TEXT NOT NULL REFERENCES service_packages(id) ON DELETE RESTRICT,
      service_id     TEXT NOT NULL REFERENCES services(id) ON DELETE RESTRICT,
      -- FOTO del catálogo al comprar: editar el catálogo después no la mueve.
      name           TEXT NOT NULL,
      sessions_total INTEGER NOT NULL CHECK (sessions_total BETWEEN 1 AND 100),
      price_cents    INTEGER NOT NULL CHECK (price_cents BETWEEN 0 AND 10000000),
      -- Contador espejo del libro (D4): 0..sessions_total.
      sessions_left  INTEGER NOT NULL,
      purchased_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- NULL = no vence. Fin del día LOCAL del salón de compra + vigencia.
      expires_at     TIMESTAMPTZ,
      -- UNIQUE: una compra = una venta. RESTRICT: las ventas no se borran.
      sale_id        TEXT NOT NULL UNIQUE REFERENCES sales(id) ON DELETE RESTRICT,
      CONSTRAINT client_packages_name_len CHECK (char_length(name) BETWEEN 1 AND 120),
      CONSTRAINT client_packages_name_text CHECK (name !~ '[\r\n]'),
      CONSTRAINT client_packages_left_range
        CHECK (sessions_left >= 0 AND sessions_left <= sessions_total)
    );
  END IF;
END $$;

-- Paquetes de una clienta (la ficha), los más nuevos primero.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'client_packages_client_idx'
  ) THEN
    CREATE INDEX client_packages_client_idx
      ON client_packages (salon_id, client_id, purchased_at DESC, id DESC);
  END IF;
END $$;

-- --- Libro de saldo (append-only) -------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'package_movements'
  ) THEN
    CREATE TABLE package_movements (
      id                TEXT PRIMARY KEY,
      salon_id          TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      client_package_id TEXT NOT NULL REFERENCES client_packages(id) ON DELETE CASCADE,
      -- delta <> 0: un movimiento de cero no es un movimiento.
      delta             INTEGER NOT NULL CHECK (delta <> 0 AND delta BETWEEN -100 AND 100),
      reason            TEXT NOT NULL CHECK (reason IN ('purchase','redeem','adjust')),
      note              TEXT CHECK (note IS NULL
                                    OR (char_length(note) <= 200 AND note !~ '[\r\n]')),
      -- Quién atendió (consumo) o vendió (compra); NULL si no se dijo.
      professional_id   TEXT,
      -- Operation id de la cola offline: el repetido no descuenta dos veces.
      client_op_id      TEXT,
      created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- Quién hizo el movimiento (usuario del panel); NULL si no se conoce.
      actor             TEXT
    );
  END IF;
END $$;

-- Un client_op_id se usa UNA vez por salón (detecta el consumo repetido).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'package_movements_op_uniq'
  ) THEN
    CREATE UNIQUE INDEX package_movements_op_uniq
      ON package_movements (salon_id, client_op_id) WHERE client_op_id IS NOT NULL;
  END IF;
END $$;

-- Movimientos de un paquete, más nuevo primero (ficha de la clienta).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'package_movements_package_idx'
  ) THEN
    CREATE INDEX package_movements_package_idx
      ON package_movements (salon_id, client_package_id, created_at DESC, id DESC);
  END IF;
END $$;
