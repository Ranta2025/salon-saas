-- 014_booking_emails: email de contacto de un turno reservado por la web y la
-- cola de recordatorios (T12).
--
-- Tabla aparte a propósito: las SELECT de reservas y scanBooking (y el JSON de
-- un turno) no cambian por un dato de contacto, y un turno sin email no ocupa
-- una fila acá.
--
-- reminder_*: la cola de recordatorios necesita idempotencia y reintentos.
--   reminder_attempts: cuántas veces se reclamó (tope 3, en la query).
--   reminder_claimed_at: cuándo lo tomó un proceso. Un reclamo se considera
--     vivo 5 minutos; si el proceso muere, el recordatorio vuelve solo.
--   reminder_sent_at: se marcará SOLO después de un envío exitoso; mientras
--     sea NULL, el recordatorio sigue vivo para reintentos.
--
-- booking_id es TEXT: el id de bookings es TEXT (001/002), no uuid.
--
-- Idempotente: IF NOT EXISTS, re-ejecutable en cada boot y válida en base
-- fresca y en base ya migrada.
CREATE TABLE IF NOT EXISTS booking_emails (
  booking_id         TEXT PRIMARY KEY REFERENCES bookings(id) ON DELETE CASCADE,
  salon_id           TEXT NOT NULL,
  email              TEXT NOT NULL CHECK (char_length(email) <= 254),
  reminder_sent_at   TIMESTAMPTZ,
  reminder_attempts  INTEGER NOT NULL DEFAULT 0,
  reminder_claimed_at TIMESTAMPTZ,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- La consulta de vencimiento barre SOLO recordatorios vivos (no enviados): el
-- índice parcial la sirve sin cargar los ya enviados, y el orden del índice
-- (reclamo, intentos, id) es el de sus condiciones.
CREATE INDEX IF NOT EXISTS booking_emails_due_idx
  ON booking_emails (reminder_claimed_at, reminder_attempts, booking_id)
  WHERE reminder_sent_at IS NULL;

-- Tope de intentos del lado de la DB: la query ya lo respeta, esto es la última
-- barrera si mañana otro camino de escritura toca la tabla.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'booking_emails_attempts_check') THEN
    ALTER TABLE booking_emails
      ADD CONSTRAINT booking_emails_attempts_check CHECK (reminder_attempts >= 0);
  END IF;
END $$;
