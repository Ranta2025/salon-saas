-- 008_clients: fichas de clientes del salón y sus fotos.
-- El cliente es del salón (no un usuario del sistema): puede no tener cuenta.
CREATE TABLE IF NOT EXISTS clients (
  id          TEXT PRIMARY KEY,
  salon_id    TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  phone       TEXT NOT NULL DEFAULT '',
  notes       TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Idempotente: la foto referencia (client_id, salon_id) contra este único, así
-- que en una DB fresca tiene que existir ANTES de crear client_photos.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'clients_id_salon_key') THEN
    ALTER TABLE clients ADD CONSTRAINT clients_id_salon_key UNIQUE (id, salon_id);
  END IF;
END $$;

-- El binario vive en el storage (filesystem local hoy, S3 mañana): acá solo la
-- ruta, el tamaño y las dimensiones. Los CHECK son la última barrera si otro
-- camino de escritura toca la tabla: solo imágenes y nada de 8 MB para arriba.
CREATE TABLE IF NOT EXISTS client_photos (
  id           TEXT PRIMARY KEY,
  salon_id     TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  client_id    TEXT NOT NULL,
  path         TEXT NOT NULL,
  width        INTEGER NOT NULL CHECK (width > 0),
  height       INTEGER NOT NULL CHECK (height > 0),
  bytes        INTEGER NOT NULL CHECK (bytes > 0 AND bytes <= 8388608),
  content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- FK compuesta: una foto NUNCA puede apuntar a un cliente de otro salón.
  -- Es la garantía dura del multi-tenancy, además del scope en las queries.
  CONSTRAINT client_photos_client_fkey
    FOREIGN KEY (client_id, salon_id) REFERENCES clients(id, salon_id) ON DELETE CASCADE
);

-- Listado de fichas del salón por nombre.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'clients_salon_name_idx') THEN
    CREATE INDEX clients_salon_name_idx ON clients (salon_id, name);
  END IF;
END $$;

-- Galería de una ficha.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'client_photos_salon_client_idx') THEN
    CREATE INDEX client_photos_salon_client_idx ON client_photos (salon_id, client_id, created_at DESC);
  END IF;
END $$;
