-- 010_limits: los límites de dinero de la API, también en la DB (B-03).
-- El dominio ya rechaza cantidades y precios absurdos; estos CHECK son la
-- última barrera si mañana otro camino de escritura toca las tablas (importador,
-- script de migración de datos): una venta de 2.000.000.000 centavos hacía que
-- SUM() de Postgres devolviera 22003 y el dashboard/cierre quedaran rotos.
--
-- Idempotente: DO con guardas de catálogo, re-ejecutable en cada boot y válida
-- en DB fresca y en DB ya migrada (los datos existentes respetan los topes).
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sales_total_cents_max') THEN
    ALTER TABLE sales
      ADD CONSTRAINT sales_total_cents_max
      CHECK (total_cents >= 0 AND total_cents <= 2000000000);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sale_items_qty_max') THEN
    ALTER TABLE sale_items
      ADD CONSTRAINT sale_items_qty_max CHECK (qty >= 1 AND qty <= 1000);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sale_items_unit_price_max') THEN
    ALTER TABLE sale_items
      ADD CONSTRAINT sale_items_unit_price_max
      CHECK (unit_price_cents >= 0 AND unit_price_cents <= 10000000);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'cash_closes_amounts_max') THEN
    ALTER TABLE cash_closes
      ADD CONSTRAINT cash_closes_amounts_max
      CHECK (system_cash_cents BETWEEN 0 AND 2000000000
         AND counted_cents BETWEEN 0 AND 2000000000);
  END IF;
END $$;
