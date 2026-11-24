-- 032_salon_profile: perfil público del salón, descripción de servicio y galería
-- de fotos del salón (F16 del contrato de la fase 6, decisiones D1, D2 y D3).
--
--   * salons: description (<=500), address (<=200), phone (<=40), email
--     (<=254) e instagram (<=60, solo el usuario: sin @ ni enlaces). Todas NULL
--     («sin dato»): ningún salón existente cambia. Los CHECK repiten los topes
--     del dominio y prohíben el salto de línea; un NUL no se puede guardar en un
--     TEXT de Postgres, así que no hace falta un CHECK para él.
--   * services.description: NULL, <=300, una línea.
--   * salon_photos: la galería PÚBLICA del salón (tope de 12 por salón, que
--     garantiza el adaptador bajo un lock por salón). Es una tabla aparte de
--     client_photos a propósito: la ruta pública nunca puede servir una foto de
--     clienta porque ni siquiera consulta esa tabla.
--
-- Idempotente: columnas, tablas y CHECKs con guardas de information_schema /
-- pg_constraint miradas en current_schema() (igual que 015–031) y los índices con
-- pg_indexes. Una segunda pasada no hace nada y sobre datos previos las columnas
-- nuevas nacen NULL sin reescribir filas: el md5 de `salons`, `services` y
-- `client_photos` no cambia.

-- --- salons: perfil ----------------------------------------------------------

DO $$
DECLARE
  col RECORD;
BEGIN
  FOR col IN
    SELECT * FROM (VALUES
      ('description', 500),
      ('address',     200),
      ('phone',        40),
      ('email',       254),
      ('instagram',    60)
    ) AS t(name, max_len)
  LOOP
    IF NOT EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = 'salons' AND column_name = col.name
    ) THEN
      EXECUTE format('ALTER TABLE salons ADD COLUMN %I TEXT', col.name);
    END IF;

    IF NOT EXISTS (
      SELECT 1 FROM pg_constraint
      WHERE conname = 'salons_' || col.name || '_text'
        AND conrelid = to_regclass(current_schema() || '.salons')
    ) THEN
      EXECUTE format(
        'ALTER TABLE salons ADD CONSTRAINT %I CHECK (%I IS NULL OR (char_length(%I) BETWEEN 1 AND %s AND %I !~ ''[\r\n]''))',
        'salons_' || col.name || '_text', col.name, col.name, col.max_len, col.name);
    END IF;
  END LOOP;
END $$;

-- El usuario de Instagram: solo letras, números, punto y guion bajo.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'salons_instagram_user'
      AND conrelid = to_regclass(current_schema() || '.salons')
  ) THEN
    ALTER TABLE salons
      ADD CONSTRAINT salons_instagram_user
      CHECK (instagram IS NULL OR instagram ~ '^[A-Za-z0-9._]+$');
  END IF;
END $$;

-- --- services.description ------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'services' AND column_name = 'description'
  ) THEN
    ALTER TABLE services ADD COLUMN description TEXT;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'services_description_text'
      AND conrelid = to_regclass(current_schema() || '.services')
  ) THEN
    ALTER TABLE services
      ADD CONSTRAINT services_description_text
      CHECK (description IS NULL OR (char_length(description) BETWEEN 1 AND 300 AND description !~ '[\r\n]'));
  END IF;
END $$;

-- --- Galería pública del salón -------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'salon_photos'
  ) THEN
    CREATE TABLE salon_photos (
      id           TEXT PRIMARY KEY,
      -- CASCADE: borrar el salón borra sus filas (el adaptador de storage no se
      -- entera: lo mismo que client_photos).
      salon_id     TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      -- La ruta estable que devolvió el storage (filesystem local o S3).
      storage_key  TEXT NOT NULL CHECK (char_length(storage_key) BETWEEN 1 AND 500),
      content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
      -- NULL = sin leyenda.
      caption      TEXT,
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      CONSTRAINT salon_photos_caption_text
        CHECK (caption IS NULL OR (char_length(caption) BETWEEN 1 AND 120 AND caption !~ '[\r\n]'))
    );
  END IF;
END $$;

-- Galería del salón en orden de subida.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'salon_photos_salon_idx'
  ) THEN
    CREATE INDEX salon_photos_salon_idx ON salon_photos (salon_id, created_at, id);
  END IF;
END $$;
