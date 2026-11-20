-- 007_notifications: avisos persistidos del salón (la bandeja que sobrevive al
-- refresh; el websocket es el "ahora", esto es el "después").
-- user_id NULL = aviso para todo el salón (un cierre de caja le importa a
-- cualquiera que esté en el mostrador).
CREATE TABLE IF NOT EXISTS notifications (
  id          TEXT PRIMARY KEY,
  salon_id    TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL CHECK (kind IN ('info','booking','sale','cash')),
  read_at     TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Bandeja del salón (avisos de todos + los propios), más nuevas primero.
-- Idempotente: migrate() re-ejecuta todos los archivos en cada boot.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'notifications_salon_created_idx') THEN
    CREATE INDEX notifications_salon_created_idx ON notifications (salon_id, created_at DESC);
  END IF;
END $$;

-- Bandeja por destinatario (user_id NULL entra por el índice anterior).
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'notifications_salon_user_created_idx') THEN
    CREATE INDEX notifications_salon_user_created_idx ON notifications (salon_id, user_id, created_at DESC);
  END IF;
END $$;

-- Badge de no leídas: índice parcial, que es justo lo que se cuenta.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'notifications_unread_idx') THEN
    CREATE INDEX notifications_unread_idx ON notifications (salon_id, user_id) WHERE read_at IS NULL;
  END IF;
END $$;
