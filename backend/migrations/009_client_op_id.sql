-- 009_client_op_id: idempotencia real de la cola offline del cliente.
-- Sin esto, una venta o reserva que llegó al servidor pero perdió la respuesta
-- se cobraba o agendaba DOS veces al reintentarse. client_op_id identifica la
-- operación en el cliente; el índice único por salón garantiza que no hay
-- duplicados ni siquiera en carrera (el perdedor recibe 23505 -> 409 y el caso
-- de uso devuelve la entidad original). Vacío = operación en línea, sin dedupe.
-- Idempotente: DO con guardas de catálogo, re-ejecutable en cada boot y válida
-- en DB fresca (006) y en DB ya migrada.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'sales' AND column_name = 'client_op_id'
  ) THEN
    ALTER TABLE sales ADD COLUMN client_op_id TEXT NOT NULL DEFAULT '';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'bookings' AND column_name = 'client_op_id'
  ) THEN
    ALTER TABLE bookings ADD COLUMN client_op_id TEXT NOT NULL DEFAULT '';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'sales_client_op_uniq') THEN
    CREATE UNIQUE INDEX sales_client_op_uniq ON sales (salon_id, client_op_id)
    WHERE client_op_id <> '';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'bookings_client_op_uniq') THEN
    CREATE UNIQUE INDEX bookings_client_op_uniq ON bookings (salon_id, client_op_id)
    WHERE client_op_id <> '';
  END IF;
END $$;
