-- 017_weekly_hours: horario semanal del salón y de cada profesional (F2d1).
--
-- Hasta la fase 6 el salón abría y cerraba con UNA ventana (open_time /
-- close_time) todos los días y cada profesional trabajaba todo lo que el salón
-- abría. Ahora:
--   * salon_weekly_hours    = la ventana de CADA día (ISO 1..7, "HH:MM" local).
--   * professional_hours    = los tramos de cada persona (hasta 4 por día; los
--                             huecos entre tramos son los descansos).
--
-- SIN backfill a propósito: la ausencia de filas es el comportamiento actual
-- (la ventana única de /settings todos los días y la profesional sigue al
-- salón). Así una base migrada se comporta exactamente igual que antes hasta
-- que alguien configure el horario desde Ajustes o Equipo.
--
-- Idempotente: cada tabla se crea con guarda de catálogo, así el runner puede
-- reaplicar el archivo sin fallar. Los días son smallint con CHECK 1..7 y las
-- horas TEXT "HH:MM", cuya comparación lexicográfica es la del reloj: por eso
-- el CHECK (inicio < fin) vale tal cual.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'salon_weekly_hours'
  ) THEN
    CREATE TABLE salon_weekly_hours (
      salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      -- ISO: 1 = lunes ... 7 = domingo, calculado en la zona del salón.
      weekday    SMALLINT NOT NULL CHECK (weekday BETWEEN 1 AND 7),
      open_time  TEXT NOT NULL,
      close_time TEXT NOT NULL,
      PRIMARY KEY (salon_id, weekday),
      CHECK (open_time < close_time),
      -- El mismo formato del dominio: una fila escrita por otro camino no puede
      -- dejar el horario ilegible.
      CONSTRAINT salon_weekly_hours_format_check CHECK (
        open_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'
        AND close_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'
      )
    );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'professional_hours'
  ) THEN
    CREATE TABLE professional_hours (
      salon_id   TEXT NOT NULL,
      user_id    TEXT NOT NULL,
      weekday    SMALLINT NOT NULL CHECK (weekday BETWEEN 1 AND 7),
      start_time TEXT NOT NULL,
      end_time   TEXT NOT NULL,
      -- Un tramo se identifica por día y hora de inicio: cargar dos veces el
      -- mismo tramo no duplica filas.
      PRIMARY KEY (salon_id, user_id, weekday, start_time),
      -- La membresía es la que autoriza: si la persona sale del salón, su
      -- horario se va con ella (los turnos ya agendados no se tocan).
      FOREIGN KEY (user_id, salon_id) REFERENCES memberships(user_id, salon_id) ON DELETE CASCADE,
      CHECK (start_time < end_time),
      CONSTRAINT professional_hours_format_check CHECK (
        start_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'
        AND end_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'
      )
    );
  END IF;
END $$;

-- La disponibilidad pregunta los tramos de TODO el equipo de un salón.
CREATE INDEX IF NOT EXISTS professional_hours_member_idx
  ON professional_hours (salon_id, user_id, weekday);
