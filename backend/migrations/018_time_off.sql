-- 018_time_off: ausencias de cada profesional (F2d2), vacaciones y días libres
-- puntuales.
--
-- Una ausencia es un período [starts_at, ends_at) en UTC de UNA persona
-- reservable (rol professional, o dueño/administración con attends: F2a). La UI
-- convierte desde la zona del salón; un día completo es de 00:00 a 00:00 del día
-- siguiente en esa zona.
--
-- El período es semiabierto igual que los turnos: una ausencia que termina a las
-- 12:00 no bloquea el turno que arranca a las 12:00.
--
-- SIN backfill: una base migrada no tiene ausencias hasta que alguien carga una.
--
-- "Dos ausencias de la misma persona no pueden pisarse" NO es un constraint: el
-- mensaje tiene que ser el del contrato ("Ya hay una ausencia en ese período") y
-- lo resuelve el caso de uso contra las filas existentes. La DB sostiene lo que
-- no necesita texto: fin posterior al inicio, tope del motivo y la membresía que
-- autoriza.
--
-- Idempotente: la tabla se crea con guarda de catálogo y el índice usa IF NOT
-- EXISTS, así el runner puede reaplicar el archivo sin fallar. La guarda mira
-- current_schema(), no cualquier tabla homónima del catálogo.

-- La garantía de que una persona no tenga dos ausencias superpuestas la
-- pone la DB (EXCLUDE), igual que los turnos: el chequeo del caso de uso evita
-- el 99% de los choques, pero dos altas simultáneas pasarían las dos.
-- btree_gist da los operadores de igualdad de texto al índice GiST.
CREATE EXTENSION IF NOT EXISTS btree_gist;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'time_off'
  ) THEN
    CREATE TABLE time_off (
      id         TEXT PRIMARY KEY,
      -- El salón es el tenant de la ausencia: si el salón se va, se va con él.
      salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      user_id    TEXT NOT NULL,
      starts_at  TIMESTAMPTZ NOT NULL,
      ends_at    TIMESTAMPTZ NOT NULL,
      -- Motivo ("Vacaciones", "Médico"). Vacío es válido: es opcional.
      reason     TEXT NOT NULL DEFAULT '',
      -- Quién la cargó (dueño/administración). Sin FK: el dato sirve para el
      -- historial y no tiene que sobrevivir a nada.
      created_by TEXT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- La membresía es la que autoriza: una ausencia solo existe para alguien
      -- del salón, y si la persona sale del equipo se va con ella (los turnos ya
      -- agendados no se tocan: F2d3 los reasigna).
      FOREIGN KEY (user_id, salon_id) REFERENCES memberships(user_id, salon_id) ON DELETE CASCADE,
      -- El período es semiabierto y tiene que durar algo.
      CHECK (starts_at < ends_at),
      -- El tope también vive en la DB (120 caracteres): si mañana otro camino de
      -- escritura toca la tabla, el CHECK sigue de guardia.
      CONSTRAINT time_off_reason_len CHECK (char_length(reason) <= 120)
    );
  END IF;
END $$;

-- Lo que consultan la disponibilidad (todas las ausencias de un salón en una
-- ventana) y la pantalla de equipo (las de una persona).
CREATE INDEX IF NOT EXISTS time_off_member_idx
  ON time_off (salon_id, user_id, starts_at);

-- Dos ausencias de la MISMA persona no pueden pisarse. El período es
-- semiabierto [inicio, fin): una ausencia que termina a las 12 y otra que
-- empieza a las 12 NO se pisan (como los turnos). Guardada por nombre para que
-- reaplicar la migración en una base ya migrada no falle.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'time_off_no_overlap'
  ) THEN
    ALTER TABLE time_off
      ADD CONSTRAINT time_off_no_overlap EXCLUDE USING gist (
        salon_id WITH =,
        user_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
      );
  END IF;
END $$;
