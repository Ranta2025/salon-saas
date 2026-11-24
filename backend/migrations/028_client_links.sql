-- 028_client_links: alergias de la ficha y vínculo estable por id de turnos y
-- ventas (F5 del contrato de la fase 6, D1/D2/D4).
--
--   * clients.allergies: DATO DE SALUD en su propia columna (no en `notes`),
--     hasta 500 caracteres. SIN relleno desde `notes`: adivinar qué parte del
--     texto libre es una alergia sería inventar un dato de salud.
--   * bookings.client_id / sales.client_id: el vínculo con la ficha es por ID,
--     así un cambio de teléfono no desvincula el historial (D1). FK con
--     ON DELETE SET NULL: borrar una ficha deja el historial con NULL, nunca
--     lo borra.
--   * Relleno ACOTADO de la migración (D2): SOLO turnos, y solo cuando el
--     teléfono normalizado (solo dígitos, no vacío) coincide con EXACTAMENTE UNA
--     ficha del mismo salón. Los ambiguos o sin coincidencia quedan en NULL.
--     Las ventas NO se rellenan: un nombre en texto libre no es confiable, así
--     que el gasto anterior a F5 no se atribuye (la pantalla lo dice).
--
-- Idempotente: las columnas y las FK con guardas de information_schema /
-- pg_constraint miradas en current_schema() (igual que 015–027) y los índices
-- con pg_indexes. Una segunda pasada no hace nada: el relleno solo toca filas
-- con client_id NULL, así que sobre una base ya migrada no cambia ninguna fila.

-- Alergias: la ficha siempre tiene un texto (vacío = sin alergias declaradas).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'clients' AND column_name = 'allergies'
  ) THEN
    ALTER TABLE clients ADD COLUMN allergies TEXT NOT NULL DEFAULT '';
  END IF;
END $$;

-- Mismo tope que domain.MaxTextAllergies (500 CARACTERES, no bytes): última
-- barrera si mañana otro camino de escritura toca la tabla.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'clients_allergies_len' AND conrelid = 'clients'::regclass
  ) THEN
    ALTER TABLE clients
      ADD CONSTRAINT clients_allergies_len CHECK (char_length(allergies) <= 500);
  END IF;
END $$;

-- client_id en turnos (D1). NULL = turno sin ficha vinculada.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'bookings' AND column_name = 'client_id'
  ) THEN
    ALTER TABLE bookings ADD COLUMN client_id TEXT;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'bookings_client_id_fkey' AND conrelid = 'bookings'::regclass
  ) THEN
    ALTER TABLE bookings
      ADD CONSTRAINT bookings_client_id_fkey
      FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE SET NULL;
  END IF;
END $$;

-- client_id en ventas (D1): el POS lo elige a mano; sin selector no hay vínculo.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'sales' AND column_name = 'client_id'
  ) THEN
    ALTER TABLE sales ADD COLUMN client_id TEXT;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sales_client_id_fkey' AND conrelid = 'sales'::regclass
  ) THEN
    ALTER TABLE sales
      ADD CONSTRAINT sales_client_id_fkey
      FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE SET NULL;
  END IF;
END $$;

-- Índice del historial: turnos y ventas de UNA ficha dentro del salón. Parcial:
-- las filas sin vínculo (la enorme mayoría de las ventas viejas) no entran.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'bookings_salon_client_idx'
  ) THEN
    CREATE INDEX bookings_salon_client_idx ON bookings (salon_id, client_id)
      WHERE client_id IS NOT NULL;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'sales_salon_client_idx'
  ) THEN
    CREATE INDEX sales_salon_client_idx ON sales (salon_id, client_id)
      WHERE client_id IS NOT NULL;
  END IF;
END $$;

-- RELLENO ACOTADO (D2). La normalización es la MISMA que clients.phone_norm
-- (migración 013): solo los dígitos ASCII del teléfono del turno. Se exige
-- coincidencia ÚNICA con una ficha del MISMO salón: dos fichas con el mismo
-- teléfono son ambiguas y el turno queda en NULL (mejor sin vínculo que con el
-- vínculo equivocado). Solo toca filas con client_id NULL, así que repetir la
-- migración no cambia nada.
UPDATE bookings b
SET client_id = c.id
FROM clients c
WHERE b.client_id IS NULL
  AND c.salon_id = b.salon_id
  AND c.phone_norm <> ''
  AND c.phone_norm = regexp_replace(b.client_phone, '[^0-9]', '', 'g')
  AND (
    SELECT count(*) FROM clients c2
    WHERE c2.salon_id = b.salon_id AND c2.phone_norm = c.phone_norm
  ) = 1;
