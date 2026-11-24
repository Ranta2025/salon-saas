-- 029_products: productos e inventario del POS (F6 del contrato de la fase 6,
-- decisiones D1 y D2).
--
--   * products: el catálogo de productos que el salón vende en el POS. El
--     stock es un CONTADOR y stock_movements su LIBRO: en todo momento
--     stock = Σ delta (D2). El CHECK `stock >= 0` y el UPDATE condicional del
--     adaptador son las dos barreras que impiden un stock negativo.
--   * stock_movements: APPEND-ONLY. Cada cambio de stock escribe su movimiento
--     en la MISMA transacción (razones initial|restock|loss|correction|sale).
--   * sale_products: las líneas de producto de una venta, con el precio
--     unitario CONGELADO (igual que sale_items). `sale_items` queda intacta: la
--     base de comisión de la nómina sigue saliendo solo de servicios (D6).
--   * sales.products_cents: la parte de producto de la venta (0 en todas las
--     ventas existentes: SIN backfill). `sales.total_cents` pasa a ser
--     servicios + productos para las ventas nuevas; para una venta existente no
--     cambia NINGÚN valor.
--
-- Idempotente: las tablas, columnas y CHECKs se crean con guardas de
-- information_schema / pg_constraint miradas en current_schema() (igual que
-- 015–028) y los índices con pg_indexes. Una segunda pasada no hace nada: la
-- columna nueva tiene DEFAULT 0 y no reescribe filas, así que el md5 de
-- `sales`, `sale_items` y `services` no cambia.

-- --- Productos -------------------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'products'
  ) THEN
    CREATE TABLE products (
      id                 TEXT PRIMARY KEY,
      salon_id           TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      name               TEXT NOT NULL,
      -- Dinero en centavos enteros, nunca float.
      price_cents        INTEGER NOT NULL CHECK (price_cents BETWEEN 0 AND 10000000),
      -- Contador espejo del libro (D2): nunca negativo.
      stock              INTEGER NOT NULL DEFAULT 0 CHECK (stock BETWEEN 0 AND 1000000),
      -- 0 = sin alerta de stock bajo (D8).
      low_stock_threshold INTEGER NOT NULL DEFAULT 0
        CHECK (low_stock_threshold BETWEEN 0 AND 1000000),
      -- Quitar un producto es DESACTIVARLO (D9): no hay DELETE.
      active             BOOLEAN NOT NULL DEFAULT true,
      created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
      CONSTRAINT products_name_len CHECK (char_length(name) BETWEEN 1 AND 120)
    );
  END IF;
END $$;

-- Un nombre ACTIVO es único por salón, sin importar mayúsculas: los inactivos
-- liberan el nombre (mismo criterio que el índice de services).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'products_active_name_idx'
  ) THEN
    CREATE UNIQUE INDEX products_active_name_idx
      ON products (salon_id, lower(name)) WHERE active;
  END IF;
END $$;

-- Listado del inventario (activos e inactivos) por salón.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'products_salon_name_idx'
  ) THEN
    CREATE INDEX products_salon_name_idx ON products (salon_id, name);
  END IF;
END $$;

-- --- Libro de stock (append-only) -----------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'stock_movements'
  ) THEN
    CREATE TABLE stock_movements (
      id         TEXT PRIMARY KEY,
      salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      -- RESTRICT: un producto con historial no se puede borrar (D9).
      product_id TEXT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
      -- delta <> 0: un movimiento de cero no es un movimiento.
      delta      INTEGER NOT NULL CHECK (delta <> 0 AND delta BETWEEN -1000000 AND 1000000),
      reason     TEXT NOT NULL
        CHECK (reason IN ('initial','restock','loss','correction','sale')),
      note       TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 200),
      -- La venta que originó el descuento (NULL en los ajustes manuales).
      -- RESTRICT: el libro no se puede quedar sin una fila que explique el
      -- stock (las ventas no se borran en este sistema).
      sale_id    TEXT REFERENCES sales(id) ON DELETE RESTRICT,
      created_by TEXT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
  END IF;
END $$;

-- Movimientos de un producto, más reciente primero (pantalla Inventario).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'stock_movements_product_idx'
  ) THEN
    CREATE INDEX stock_movements_product_idx
      ON stock_movements (salon_id, product_id, created_at DESC, id DESC);
  END IF;
END $$;

-- --- Líneas de producto de una venta -------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'sale_products'
  ) THEN
    CREATE TABLE sale_products (
      id               TEXT PRIMARY KEY,
      sale_id          TEXT NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
      product_id       TEXT NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
      qty              INTEGER NOT NULL CHECK (qty BETWEEN 1 AND 1000),
      unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents BETWEEN 0 AND 10000000)
    );
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'sale_products_sale_idx'
  ) THEN
    CREATE INDEX sale_products_sale_idx ON sale_products (sale_id);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'sale_products_product_idx'
  ) THEN
    CREATE INDEX sale_products_product_idx ON sale_products (product_id);
  END IF;
END $$;

-- --- La parte de producto de la venta ------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'sales' AND column_name = 'products_cents'
  ) THEN
    ALTER TABLE sales ADD COLUMN products_cents INTEGER NOT NULL DEFAULT 0;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sales_products_cents_range'
      AND conrelid = to_regclass(current_schema() || '.sales')
  ) THEN
    ALTER TABLE sales
      ADD CONSTRAINT sales_products_cents_range
      CHECK (products_cents >= 0 AND products_cents <= 2000000000);
  END IF;
END $$;
