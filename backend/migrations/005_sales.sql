-- 005_sales: ventas del POS y sus líneas. La venta guarda el total ya calculado
-- por el servidor (suma de las líneas) y el método de pago: el cierre de caja
-- necesita distinguir efectivo de tarjeta/transferencia.
CREATE TABLE IF NOT EXISTS sales (
  id              TEXT PRIMARY KEY,
  salon_id        TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  professional_id TEXT NOT NULL REFERENCES users(id),
  client_name     TEXT NOT NULL,
  total_cents     INTEGER NOT NULL CHECK (total_cents >= 0),
  method          TEXT NOT NULL CHECK (method IN ('cash','card','transfer')),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cada línea congela el precio unitario del momento de la venta: si mañana el
-- catálogo sube de precio, la venta histórica no se mueve.
CREATE TABLE IF NOT EXISTS sale_items (
  id               TEXT PRIMARY KEY,
  sale_id          TEXT NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
  -- ON DELETE CASCADE: la API nunca borra un servicio (solo lo desactiva), pero
  -- borrar un salón tiene que poder arrastrar todo su historial. Con RESTRICT,
  -- DELETE FROM salons fallaría y el tenant quedaría imposible de limpiar.
  service_id       TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  qty              INTEGER NOT NULL CHECK (qty >= 1),
  unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents >= 0)
);

-- Idempotente: si la tabla ya existía con la FK restrictiva, se re-crea con
-- CASCADE. Sin esto, la DB ya migrada y una fresca quedarían distintas.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sale_items_service_id_fkey' AND confdeltype <> 'c'
  ) THEN
    ALTER TABLE sale_items DROP CONSTRAINT sale_items_service_id_fkey;
    ALTER TABLE sale_items
      ADD CONSTRAINT sale_items_service_id_fkey
      FOREIGN KEY (service_id) REFERENCES services(id) ON DELETE CASCADE;
  END IF;
END $$;

-- Ventas por día/salón: alimenta el listado con ?from=&to= y el dashboard.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'sales_salon_created_idx') THEN
    CREATE INDEX sales_salon_created_idx ON sales (salon_id, created_at DESC);
  END IF;
END $$;

-- Cierre de caja: suma de efectivo por ventana y ranking de servicios.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'sales_salon_method_created_idx') THEN
    CREATE INDEX sales_salon_method_created_idx ON sales (salon_id, method, created_at);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'sale_items_sale_idx') THEN
    CREATE INDEX sale_items_sale_idx ON sale_items (sale_id);
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'sale_items_service_idx') THEN
    CREATE INDEX sale_items_service_idx ON sale_items (service_id);
  END IF;
END $$;
