-- 034_audit_events: registro de auditoría (F10, parte A del contrato de la fase
-- 6, decisión D1). Quién hizo qué, sobre qué id y cuándo; NADA más: sin valores
-- antes/después ni datos personales.
--
--   * audit_events: `actor_name` es una FOTO del nombre del actor al momento del
--     evento (vacío/NULL si no se pudo resolver); `entity_id` es el id de la ruta
--     de la acción cuando tiene uno (NULL si no). `actor_user_id` NO tiene FK a
--     propósito: el registro conserva quién fue aunque la cuenta cambie.
--   * Índice (salon_id, created_at DESC, id DESC): el orden y el cursor de
--     GET /salons/:id/audit-log.
--   * APPEND-ONLY: un trigger rechaza UPDATE y DELETE. El borrado en cascada de
--     un salón SÍ se permite (si no, un salón no se podría borrar nunca): se
--     reconoce porque corre dentro del trigger de integridad referencial
--     (pg_trigger_depth() > 1) Y la fila del salón ya no existe. Un DELETE
--     directo corre a profundidad 1 y siempre falla; lo mismo un DELETE de un
--     salón que sigue existiendo.
--
-- No toca NINGUNA tabla existente. Idempotente: tabla con guarda de
-- information_schema mirada en current_schema() (igual que 015–033), índice con
-- pg_indexes, función con CREATE OR REPLACE y trigger con pg_trigger. Una
-- segunda pasada no hace nada.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'audit_events'
  ) THEN
    CREATE TABLE audit_events (
      id            TEXT PRIMARY KEY,
      salon_id      TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      actor_user_id TEXT NOT NULL,
      -- FOTO del nombre del actor; no cambia si después se renombra.
      actor_name    TEXT,
      action        TEXT NOT NULL,
      entity_id     TEXT,
      created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
      CONSTRAINT audit_events_action_len CHECK (char_length(action) BETWEEN 1 AND 60),
      CONSTRAINT audit_events_entity_len CHECK (entity_id IS NULL OR char_length(entity_id) <= 100)
    );
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'audit_events_salon_created_idx'
  ) THEN
    CREATE INDEX audit_events_salon_created_idx
      ON audit_events (salon_id, created_at DESC, id DESC);
  END IF;
END $$;

-- Append-only. En un borrado en cascada de salons el trigger de la FK corre a
-- profundidad 1 y este (su consecuencia) a profundidad 2, con el salón ya
-- borrado en el mismo comando.
CREATE OR REPLACE FUNCTION audit_events_append_only() RETURNS trigger AS $fn$
BEGIN
  IF TG_OP = 'DELETE'
     AND pg_trigger_depth() > 1
     AND NOT EXISTS (SELECT 1 FROM salons WHERE id = OLD.salon_id) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'audit_events es de solo escritura (no se permite %)', TG_OP
    USING ERRCODE = 'restrict_violation';
END
$fn$ LANGUAGE plpgsql;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_trigger t
    JOIN pg_class c ON c.oid = t.tgrelid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = current_schema()
      AND c.relname = 'audit_events'
      AND t.tgname = 'audit_events_append_only'
      AND NOT t.tgisinternal
  ) THEN
    CREATE TRIGGER audit_events_append_only
      BEFORE UPDATE OR DELETE ON audit_events
      FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();
  END IF;
END $$;
