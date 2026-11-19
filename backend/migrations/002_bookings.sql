-- 002_bookings: reservas con anti-solape GARANTIZADO por la DB.
-- El check-then-insert del caso de uso evita el 99% de choques; este EXCLUDE
-- cierra la carrera que queda entre dos inserts concurrentes (el perdedor recibe
-- error de exclusión y la API responde 409). Solo cuentan las activas: canceladas
-- y finalizadas liberan el hueco.
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE IF NOT EXISTS bookings (
  id          TEXT PRIMARY KEY,
  salon_id    TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  barber_id   TEXT NOT NULL REFERENCES users(id),
  client_name TEXT NOT NULL,
  client_phone TEXT NOT NULL DEFAULT '',
  starts_at   TIMESTAMPTZ NOT NULL,
  ends_at     TIMESTAMPTZ NOT NULL,
  status      TEXT NOT NULL DEFAULT 'booked'
              CHECK (status IN ('booked','cancelled','done','no_show')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at)
);

ALTER TABLE bookings
  DROP CONSTRAINT IF EXISTS bookings_no_overlap;
ALTER TABLE bookings
  ADD CONSTRAINT bookings_no_overlap EXCLUDE USING gist (
    salon_id WITH =,
    barber_id WITH =,
    tstzrange(starts_at, ends_at) WITH &&
  ) WHERE (status = 'booked');

CREATE INDEX IF NOT EXISTS bookings_agenda_idx
  ON bookings (salon_id, barber_id, starts_at);
