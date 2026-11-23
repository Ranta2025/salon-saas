-- 022_membership_ended_at: dar de baja en vez de borrar (F2g2, S-F2g2-1).
--
-- Hoy `DELETE /salons/:id/members/:uid` borra la fila de memberships: los turnos
-- y las ventas sobreviven (referencian a users), pero professional_hours,
-- time_off y professional_services se van en cascada y se pierde el vínculo de
-- la persona con el salón. Dar de baja conserva la fila con ended_at: el
-- historial, lo que F3 (nómina) necesita para liquidar comisiones de alguien
-- que ya no está, y la posibilidad de reactivarla.
--
-- SIN backfill: ended_at NULL es "activa", así que todo lo existente queda
-- activo y el comportamiento de hoy no cambia.
--
-- NO cambia ninguna FK ni cascada: la fila ya no se borra, así que las tablas
-- hijas (horarios, ausencias, servicios) se conservan aunque la persona esté
-- dada de baja y vuelven enteras al reactivarla.
--
-- Idempotente: la columna se agrega con guarda de information_schema y el
-- índice con guarda de pg_indexes, las dos miradas en current_schema() (igual
-- que 015/016/018/019/020/021). Una segunda pasada no hace nada.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'memberships'
      AND column_name = 'ended_at'
  ) THEN
    ALTER TABLE memberships ADD COLUMN ended_at TIMESTAMPTZ;
  END IF;
END $$;

-- Índice parcial del equipo ACTIVO del salón: las consultas de agenda,
-- disponibilidad y selectores filtran ended_at IS NULL y no tienen por qué
-- recorrer a quien ya no está.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'memberships_active_idx'
  ) THEN
    CREATE INDEX memberships_active_idx ON memberships (salon_id) WHERE ended_at IS NULL;
  END IF;
END $$;
