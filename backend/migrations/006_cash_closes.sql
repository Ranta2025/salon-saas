-- 006_cash_closes: cierres de caja por turno. `system_cash_cents` es lo que el
-- sistema espera (efectivo de las ventas de la ventana) y `counted_cents` lo que
-- la persona contó; `difference_cents` = contado - sistema (negativo = faltante).
-- La diferencia se persiste: es el dato contable que después se audita.
CREATE TABLE IF NOT EXISTS cash_closes (
  id                TEXT PRIMARY KEY,
  salon_id          TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  opened_at         TIMESTAMPTZ NOT NULL,
  closed_at         TIMESTAMPTZ NOT NULL,
  system_cash_cents INTEGER NOT NULL CHECK (system_cash_cents >= 0),
  counted_cents     INTEGER NOT NULL CHECK (counted_cents >= 0),
  difference_cents  INTEGER NOT NULL,
  note              TEXT NOT NULL DEFAULT '',
  CHECK (closed_at >= opened_at),
  CHECK (difference_cents = counted_cents - system_cash_cents)
);

-- El cierre más reciente marca la apertura del turno siguiente.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'cash_closes_salon_closed_idx') THEN
    CREATE INDEX cash_closes_salon_closed_idx ON cash_closes (salon_id, closed_at DESC);
  END IF;
END $$;
