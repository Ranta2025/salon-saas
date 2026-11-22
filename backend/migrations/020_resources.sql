-- 020_resources: puestos y equipamiento con capacidad (F2e, S-F2e-1).
--
-- Un servicio puede exigir un TIPO de equipo ("Puesto de peluquería", "Silla de
-- barbería", "Lavacabezas") y cada turno ocupa UNA UNIDAD concreta durante toda
-- su duración. La capacidad NO es un contador: cada unidad es una fila con su
-- propia EXCLUDE (salon_id, resource_unit_id, rango), el mismo patrón que
-- `bookings_no_overlap` (002). Así dos reservas simultáneas nunca se llevan la
-- última unidad: la base deja entrar una y la otra vuelve como conflicto.
--
-- SIN backfill: un servicio sin `resource_type_id` y un turno sin
-- `resource_unit_id` se comportan exactamente como antes de esta migración.
-- Los CHECK de nombre (1..60) repiten la regla del dominio porque otro camino de
-- escritura (importador, script) también tiene que respetarla.
--
-- Idempotente: el runner reaplica todo en cada boot y hay tests que ejecutan el
-- archivo dos veces; las guardas usan information_schema/pg_constraint del
-- schema actual, igual que las 015/016/018/019.

CREATE TABLE IF NOT EXISTS resource_types (
  id         TEXT PRIMARY KEY,
  salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (length(btrim(name)) BETWEEN 1 AND 60)
);

-- Nombre único por salón sin importar mayúsculas: "Puesto" y "puesto" son el
-- mismo equipo. Es la garantía dura que el caso de uso traduce a su mensaje.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'resource_types_salon_name_idx'
  ) THEN
    CREATE UNIQUE INDEX resource_types_salon_name_idx
      ON resource_types (salon_id, lower(name));
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS resource_units (
  id         TEXT PRIMARY KEY,
  salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  -- ON DELETE RESTRICT: un tipo con unidades no se borra a la fuerza; el caso de
  -- uso borra sus unidades en la misma transacción (S-F2e-3).
  type_id    TEXT NOT NULL REFERENCES resource_types(id) ON DELETE RESTRICT,
  name       TEXT NOT NULL,
  active     BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (length(btrim(name)) BETWEEN 1 AND 60)
);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'resource_units_salon_type_name_idx'
  ) THEN
    CREATE UNIQUE INDEX resource_units_salon_type_name_idx
      ON resource_units (salon_id, type_id, lower(name));
  END IF;
END $$;

-- Listado del equipamiento: unidades del tipo, activas primero.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'resource_units_type_idx'
  ) THEN
    CREATE INDEX resource_units_type_idx
      ON resource_units (salon_id, type_id, active, name);
  END IF;
END $$;

-- Qué tipo de equipo usa cada servicio (NULL = no usa equipo).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'services'
      AND column_name = 'resource_type_id'
  ) THEN
    ALTER TABLE services
      ADD COLUMN resource_type_id TEXT REFERENCES resource_types(id) ON DELETE RESTRICT;
  END IF;
END $$;

-- Qué unidad ocupó cada turno (NULL = no ocupa equipo: turno del calendario o
-- servicio sin tipo).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'bookings'
      AND column_name = 'resource_unit_id'
  ) THEN
    ALTER TABLE bookings
      ADD COLUMN resource_unit_id TEXT REFERENCES resource_units(id) ON DELETE RESTRICT;
  END IF;
END $$;

-- Anti-solape POR UNIDAD: el mismo patrón y el mismo tipo de guarda que
-- `bookings_no_overlap` (002). Solo cuentan las reservas activas (`booked`):
-- cancelar libera la unidad y un turno sin unidad nunca choca.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'bookings_resource_no_overlap'
  ) THEN
    ALTER TABLE bookings
      ADD CONSTRAINT bookings_resource_no_overlap EXCLUDE USING gist (
        salon_id WITH =,
        resource_unit_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
      ) WHERE (status = 'booked' AND resource_unit_id IS NOT NULL);
  END IF;
END $$;

-- Ocupación de una unidad en una ventana (disponibilidad y reprogramación).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'bookings_resource_unit_idx'
  ) THEN
    CREATE INDEX bookings_resource_unit_idx
      ON bookings (salon_id, resource_unit_id, starts_at)
      WHERE resource_unit_id IS NOT NULL;
  END IF;
END $$;

-- Servicios por tipo de equipo: "N servicios usan este equipo" y el rechazo del
-- borrado se consultan por (salon_id, resource_type_id).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'services_resource_type_idx'
  ) THEN
    CREATE INDEX services_resource_type_idx
      ON services (salon_id, resource_type_id)
      WHERE resource_type_id IS NOT NULL;
  END IF;
END $$;
