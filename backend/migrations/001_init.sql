-- 001_init: tenants + auth. Todo cuelga de salons (multi-tenancy).
CREATE TABLE IF NOT EXISTS users (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  email       TEXT NOT NULL UNIQUE,
  pass_hash   TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS salons (
  id          TEXT PRIMARY KEY,
  slug        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memberships (
  user_id     TEXT NOT NULL REFERENCES users(id),
  salon_id    TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  role        TEXT NOT NULL,
  PRIMARY KEY (user_id, salon_id)
);
