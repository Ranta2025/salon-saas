-- 016_professional_services: oficio y servicios por profesional (F2c).
--
-- Hasta la fase 5 se asumía que todo el equipo hacía todos los servicios, así
-- que la disponibilidad y la asignación automática ofrecían a cualquiera que
-- atendiera. Ahora:
--   * memberships.trade   = el oficio visible ("Barbero", "Colorista"): texto,
--                           no un permiso. Solo tiene sentido en quien atiende.
--   * professional_services = qué hace cada quien (PK por salón + persona +
--                           servicio, FK a memberships y a services con CASCADE).
--   * bookings.service_id = el servicio del turno que entró por la web.
--
-- Idempotente: cada paso se guarda por catálogo (information_schema /
-- pg_constraint) y el BACKFILL corre SOLO cuando la tabla se crea, así una
-- segunda pasada no duplica filas ni pisa lo que se asignó desde la pantalla de
-- equipo. Backfill = comportamiento actual: cada miembro reservable hace todos
-- los servicios que ya existen en su salón.
--
-- Todas las queries del runner corren sin esquema fijo: las guardas miran
-- current_schema(), igual que la 015 (una tabla memberships de otro esquema no
-- confunde a la migración).

-- 1) El oficio de quien atiende.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'memberships'
      AND column_name = 'trade'
  ) THEN
    ALTER TABLE memberships ADD COLUMN trade TEXT NOT NULL DEFAULT '';
  END IF;

  -- El tope también vive en la DB (40 caracteres): si mañana otro camino de
  -- escritura toca la tabla, el CHECK sigue de guardia.
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint c
    JOIN pg_class t ON t.oid = c.conrelid
    JOIN pg_namespace n ON n.oid = t.relnamespace
    WHERE c.conname = 'memberships_trade_len' AND n.nspname = current_schema()
  ) THEN
    ALTER TABLE memberships
      ADD CONSTRAINT memberships_trade_len CHECK (char_length(trade) <= 40);
  END IF;
END $$;

-- 2) El servicio del turno. Opcional: la agenda de staff agenda sin servicio y
--    ese turno queda en NULL. Borrar un servicio no borra la historia del
--    turno: queda el turno sin servicio (SET NULL).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'bookings'
      AND column_name = 'service_id'
  ) THEN
    ALTER TABLE bookings ADD COLUMN service_id TEXT;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint c
    JOIN pg_class t ON t.oid = c.conrelid
    JOIN pg_namespace n ON n.oid = t.relnamespace
    WHERE c.conname = 'bookings_service_id_fkey' AND n.nspname = current_schema()
  ) THEN
    ALTER TABLE bookings
      ADD CONSTRAINT bookings_service_id_fkey
      FOREIGN KEY (service_id) REFERENCES services(id) ON DELETE SET NULL;
  END IF;
END $$;

-- 3) Qué servicios hace cada profesional, y el backfill que conserva lo de hoy.
DO $$
DECLARE
  recien_creada boolean := false;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'professional_services'
  ) THEN
    CREATE TABLE professional_services (
      salon_id   TEXT NOT NULL,
      user_id    TEXT NOT NULL,
      service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
      PRIMARY KEY (salon_id, user_id, service_id),
      -- La membresía es la que autoriza: si la persona sale del salón, sus
      -- servicios se van con ella (los turnos ya agendados no se tocan).
      FOREIGN KEY (user_id, salon_id) REFERENCES memberships(user_id, salon_id) ON DELETE CASCADE
    );
    recien_creada := true;
  END IF;

  IF recien_creada THEN
    INSERT INTO professional_services(salon_id, user_id, service_id)
    SELECT m.salon_id, m.user_id, s.id
      FROM memberships m
      JOIN services s ON s.salon_id = m.salon_id
     WHERE m.role = 'professional'
        OR (m.attends AND m.role IN ('owner', 'admin'))
    ON CONFLICT DO NOTHING;
  END IF;
END $$;

-- La consulta de la disponibilidad pública y la del alta de un servicio buscan
-- por (salón, servicio): quién lo hace.
CREATE INDEX IF NOT EXISTS professional_services_service_idx
  ON professional_services (salon_id, service_id);
