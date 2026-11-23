-- 026_expenses: gastos operativos del salón (F3b, D3 del contrato Fase 6).
--
-- Un gasto es dinero operativo: categoría fija (rent|supplies|utilities|other),
-- monto entero en CENTAVOS y un día LOCAL (`spent_on DATE`, no un instante): el
-- gasto no depende de la zona horaria para agruparse, y el panel de ganancias lo
-- lee tal cual. `description` es texto libre recortado, hasta 200 caracteres.
--
-- El techo del monto (1..2000000000) es el mismo que el del resto del dinero y
-- cabe en INTEGER: otro camino de escritura (importador, script) no puede
-- guardar un gasto imposible. La fecha mínima (2000-01-01) es del contrato; la
-- fecha futura la valida el dominio con el día LOCAL del salón.
--
-- Idempotente: la tabla se crea con guarda de information_schema mirada en
-- current_schema() (igual que 015–025) y el índice con guarda de pg_indexes. Una
-- segunda pasada no hace nada y, sobre una base con datos, no cambia ninguna
-- fila.
--
-- SIN backfill: una base migrada no tiene gastos hasta que alguien carga uno, y
-- nada existente cambia.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'expenses'
  ) THEN
    CREATE TABLE expenses (
      id           TEXT PRIMARY KEY,
      -- El salón es el tenant: si el salón se va, sus gastos se van.
      salon_id     TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      category     TEXT NOT NULL,
      -- Centavos enteros: nunca un float (D3).
      amount_cents INTEGER NOT NULL,
      -- DÍA LOCAL del gasto (no un instante): no depende de la zona horaria.
      spent_on     DATE NOT NULL,
      description  TEXT NOT NULL DEFAULT '',
      -- Quién lo cargó: FK a la cuenta. Sin ON DELETE: una cuenta con gastos no
      -- se borra (el historial del dinero no se huerfaniza).
      created_by   TEXT REFERENCES users(id),
      created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
      -- Categorías fijas del contrato: no hay categorías personalizadas.
      CONSTRAINT expenses_category_check
        CHECK (category IN ('rent','supplies','utilities','other')),
      -- Mismo techo que el resto del dinero (010_limits, domain.MaxExpenseCents).
      CONSTRAINT expenses_amount_check
        CHECK (amount_cents BETWEEN 1 AND 2000000000),
      -- La más vieja que acepta el contrato; el futuro lo frena el dominio con
      -- el día local del salón (un CHECK con CURRENT_DATE no sería local).
      CONSTRAINT expenses_spent_on_check
        CHECK (spent_on >= DATE '2000-01-01'),
      -- 200 CARACTERES (Postgres cuenta caracteres, no bytes): el mismo tope que
      -- domain.MaxExpenseDescription.
      CONSTRAINT expenses_description_len
        CHECK (char_length(description) <= 200)
    );
  END IF;
END $$;

-- Listado por rango de fechas y orden del contrato (spent_on y created_at
-- descendentes), ya scopeado por salón.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'expenses_salon_spent_idx'
  ) THEN
    CREATE INDEX expenses_salon_spent_idx
      ON expenses (salon_id, spent_on DESC, created_at DESC);
  END IF;
END $$;
