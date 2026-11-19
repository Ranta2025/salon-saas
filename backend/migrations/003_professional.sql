-- 003_professional: el que atiende no es solo barbero (corte, keratina,
-- planchado, color, uñas). La columna pasa a professional_id; el constraint
-- EXCLUDE y el índice la siguen automáticamente (Postgres actualiza
-- dependencias al renombrar). No se reescribe historia: 002 queda intacta.
-- Idempotente: migrate() re-ejecuta todo en cada boot, así que solo actúa
-- si la columna vieja aún existe.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'bookings' AND column_name = 'barber_id'
  ) THEN
    ALTER TABLE bookings RENAME COLUMN barber_id TO professional_id;
  END IF;
END $$;

-- Nombre cosmético de la FK acorde al nuevo nombre de columna.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'bookings_barber_id_fkey') THEN
    ALTER TABLE bookings RENAME CONSTRAINT bookings_barber_id_fkey TO bookings_professional_id_fkey;
  END IF;
END $$;

-- El índice de agenda se crea acá (post-rename) y solo si falta: en DBs
-- migradas ya existe (siguió al rename), en DBs frescas se crea ahora.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'bookings_agenda_idx') THEN
    CREATE INDEX bookings_agenda_idx ON bookings (salon_id, professional_id, starts_at);
  END IF;
END $$;
