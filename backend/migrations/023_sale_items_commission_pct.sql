-- 023_sale_items_commission_pct: FOTO del % de comisión en cada línea de venta
-- (F3.1, S-F3.1-1 del contrato F3, decisión D1).
--
-- Hasta hoy `sale_items` solo congela `unit_price_cents`: la comisión de una
-- venta se leería EN VIVO de `services.commission_pct`, así que si el dueño
-- edita un porcentaje, una liquidación ya pagada cambiaría sola. Cada línea
-- guarda desde ahora el porcentaje que estaba vigente cuando se vendió.
--
-- NULL = la línea es anterior a F3.1 y no tiene foto. Ese caso NO se rellena
-- (SIN backfill: no se reescribe ningún dato guardado); F3.2 estima su comisión
-- con la tarifa actual y la marca `estimated`. 0 es un porcentaje real
-- (servicio sin comisión), no "sin foto".
--
-- El CHECK 0..100 es la última barrera, igual que en 004_services: si mañana
-- otro camino de escritura toca la tabla, la DB sigue de guardia. En Postgres
-- un CHECK que da NULL (columna NULL) pasa, así que NULL sigue permitido.
--
-- Idempotente: la columna se agrega con guarda de information_schema y el
-- CHECK con guarda de pg_constraint, las dos miradas en current_schema() (igual
-- que 015/016/018/019/020/021/022). Una segunda pasada no hace nada y agregar
-- una columna nullable SIN DEFAULT no reescribe las filas existentes.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'sale_items'
      AND column_name = 'commission_pct'
  ) THEN
    ALTER TABLE sale_items ADD COLUMN commission_pct SMALLINT;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sale_items_commission_pct_range'
      AND conrelid = to_regclass(current_schema() || '.sale_items')
  ) THEN
    ALTER TABLE sale_items
      ADD CONSTRAINT sale_items_commission_pct_range
      CHECK (commission_pct BETWEEN 0 AND 100);
  END IF;
END $$;
