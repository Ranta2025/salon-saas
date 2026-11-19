-- 004_services: catálogo de servicios del salón (qué vende, cuánto dura, cuánto
-- cuesta). Dinero en centavos enteros: nunca float. Toda fila cuelga de un
-- salón, y el precio/comisión tienen rango cerrado en la DB (no solo en la API):
-- si mañana otro camino de escritura toca la tabla, el CHECK sigue de guardia.
CREATE TABLE IF NOT EXISTS services (
  id             TEXT PRIMARY KEY,
  salon_id       TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  duration_min   INTEGER NOT NULL CHECK (duration_min > 0),
  price_cents    INTEGER NOT NULL CHECK (price_cents >= 0),
  commission_pct INTEGER NOT NULL DEFAULT 0 CHECK (commission_pct BETWEEN 0 AND 100),
  active         BOOLEAN NOT NULL DEFAULT true,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Un nombre activo es único por salón (case-insensitive: "Corte" y "corte" son
-- el mismo servicio). Los inactivos no bloquean: desactivar libera el nombre.
-- Idempotente: migrate() re-ejecuta todo en cada boot.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'services_active_name_idx') THEN
    CREATE UNIQUE INDEX services_active_name_idx ON services (salon_id, lower(name)) WHERE active;
  END IF;
END $$;

-- Listado del catálogo por salón (activos primero, luego nombre).
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'services_salon_active_idx') THEN
    CREATE INDEX services_salon_active_idx ON services (salon_id, active, name);
  END IF;
END $$;
