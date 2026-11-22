-- 019_booking_covered_from: quién tenía el turno antes de que lo cubriera otra
-- persona (F2d3, S-F2d3-6).
--
-- Cubrir una ausencia mueve el turno a otra profesional, pero la clienta y "Mi
-- agenda" necesitan saber de quién era: `covered_from` guarda a la profesional
-- ANTERIOR. Se escribe solo la primera vez (Ana -> Cata -> Bea sigue diciendo
-- Ana) y se limpia cuando el turno vuelve a esa persona; esa regla vive en el
-- dominio (Booking.Reassign), no en la DB.
--
-- SIN FK a propósito: la persona puede dejar el equipo y el turno tiene que
-- seguir sabiendo de quién era. Es historia, no una referencia viva.
-- SIN backfill: ningún turno que ya existe estaba cubierto por otra persona.
--
-- Idempotente: la guarda mira current_schema(), igual que las 015/016/018, así
-- el runner puede reaplicar el archivo sin fallar y una tabla bookings
-- homónima de otro esquema no confunde la migración.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'bookings'
      AND column_name = 'covered_from'
  ) THEN
    ALTER TABLE bookings ADD COLUMN covered_from TEXT;
  END IF;
END $$;
