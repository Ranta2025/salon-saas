-- 013_clients_contact: contacto de la ficha del cliente (E-03 de la auditoría).
--
-- email: la ficha acepta un correo opcional. Hasta ahora el POST lo descartaba
--   en silencio y la pantalla mostraba "Sin correo".
-- phone_norm: SOLO los dígitos del teléfono, para ENCONTRAR la ficha desde la
--   reserva pública ("11 5555-1234" y "1155551234" son la misma persona). Es una
--   columna generada: no hay dos fuentes de verdad que se puedan desincronizar.
--   La ficha sigue guardando el teléfono como lo escribió la persona.
--
-- Idempotente: guardas de catálogo / IF NOT EXISTS, válida en base fresca y en
-- base ya migrada (las fichas existentes calculan su phone_norm solas).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'clients' AND column_name = 'email'
  ) THEN
    ALTER TABLE clients ADD COLUMN email TEXT NOT NULL DEFAULT '';
  END IF;
END $$;

-- Mismo tope que el dominio (MaxTextEmail): última barrera si mañana otro
-- camino de escritura toca la tabla.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'clients_email_len') THEN
    ALTER TABLE clients ADD CONSTRAINT clients_email_len CHECK (char_length(email) <= 254);
  END IF;
END $$;

-- Normalización del teléfono: la MISMA regla que domain.NormalizePhone (dejar
-- solo los dígitos ASCII). regexp_replace es IMMUTABLE, así que Postgres acepta
-- la columna generada.
ALTER TABLE clients
  ADD COLUMN IF NOT EXISTS phone_norm TEXT
  GENERATED ALWAYS AS (regexp_replace(phone, '[^0-9]', '', 'g')) STORED;

-- Búsqueda por teléfono dentro del salón (la reserva pública la hace en cada
-- alta). Parcial: las fichas sin teléfono no necesitan entrar al índice.
CREATE INDEX IF NOT EXISTS clients_salon_phone_norm_idx
  ON clients (salon_id, phone_norm) WHERE phone_norm <> '';
