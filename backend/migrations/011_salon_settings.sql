-- 011_salon_settings: configuración operativa del salón (T6 ronda 2).
--
-- timezone: nombre IANA. No es presentación: define el DÍA del salón para la
--   ventana del cierre de caja y el horario de la reserva pública.
-- open_time / close_time: "HH:MM" local, ventana donde se ofrecen turnos.
-- slot_step_min: cada cuántos minutos arranca un turno ofrecido.
--
-- Idempotente: DO con guardas de catálogo, re-ejecutable en cada boot y válida
-- en DB fresca y en DB ya migrada (los salones existentes toman el default).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'salons' AND column_name = 'timezone'
  ) THEN
    ALTER TABLE salons ADD COLUMN timezone TEXT NOT NULL DEFAULT 'America/Mexico_City';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'salons' AND column_name = 'open_time'
  ) THEN
    ALTER TABLE salons ADD COLUMN open_time TEXT NOT NULL DEFAULT '09:00';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'salons' AND column_name = 'close_time'
  ) THEN
    ALTER TABLE salons ADD COLUMN close_time TEXT NOT NULL DEFAULT '19:00';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'salons' AND column_name = 'slot_step_min'
  ) THEN
    ALTER TABLE salons ADD COLUMN slot_step_min INTEGER NOT NULL DEFAULT 30;
  END IF;

  -- Los mismos topes del dominio, como última barrera si mañana otro camino de
  -- escritura toca la tabla.
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'salons_slot_step_check') THEN
    ALTER TABLE salons
      ADD CONSTRAINT salons_slot_step_check CHECK (slot_step_min BETWEEN 5 AND 240);
  END IF;
END $$;
