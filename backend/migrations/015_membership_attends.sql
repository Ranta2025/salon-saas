-- 015_membership_attends: quién atiende clientes, más allá del rol (F2a).
--
-- Antes, `role IN ('owner','admin','professional')` decidía quién aparecía en la
-- agenda: un dueño que solo administra inflaba la disponibilidad y recibía
-- turnos. Ahora el rol professional atiende siempre, owner/admin solo con
-- attends = true (el dueño-barbero) y recepción/cliente nunca.
--
-- Idempotente: la columna se crea con guarda de catálogo y, solo cuando se crea,
-- se hace el backfill. Una segunda pasada no toca nada: lo que la persona apagó
-- desde la pantalla de equipo sigue apagado.
--
-- Backfill = comportamiento actual: owner/admin/professional quedan atendiendo,
-- el resto (recepción, cliente) no.
DO $$
DECLARE
  recien_creada boolean := false;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'memberships'
      AND column_name = 'attends'
  ) THEN
    ALTER TABLE memberships ADD COLUMN attends BOOLEAN NOT NULL DEFAULT false;
    recien_creada := true;
  END IF;

  IF recien_creada THEN
    UPDATE memberships
       SET attends = true
     WHERE role IN ('owner', 'admin', 'professional');
  END IF;
END $$;
