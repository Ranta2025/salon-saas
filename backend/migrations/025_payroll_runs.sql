-- 025_payroll_runs: liquidaciones de nómina (F3.3, D6 del contrato F3).
--
-- Una liquidación CONGELA un período ya terminado: la cabecera (`payroll_runs`)
-- guarda el tipo, las fechas locales INCLUSIVAS [from_date, to_date], el estado
-- y quién la creó y la pagó; cada línea (`payroll_lines`) guarda la foto del
-- nombre, las ventas, la comisión, el fijo y el ajuste de UNA persona. Desde que
-- se crea, editar el catálogo, cargar ventas del período o cambiar el modelo de
-- pago NO mueve los montos: lo que se paga es lo que está escrito acá.
--
-- Una liquidación PAGADA es inmutable: la API lo rechaza y las queries de
-- escritura (ajuste, pago y borrado) llevan la condición `status='open'`, así
-- que ni un camino nuevo puede tocar una pagada.
--
-- La EXCLUDE por salón sobre `daterange(from_date, to_date + 1, '[)')` impide
-- que dos liquidaciones (de cualquier tipo, abiertas o pagadas) se pisen: es la
-- red de seguridad contra pagar dos veces las mismas ventas. El caso de uso
-- toma un advisory lock por salón y la EXCLUDE es la garantía dura de la DB
-- (23P01 -> 409, nunca un 500).
--
-- SIN backfill: una base migrada no tiene liquidaciones hasta que alguien crea
-- una, y nada existente cambia.
--
-- Idempotente: las tablas se crean con guardas de information_schema miradas en
-- current_schema() (igual que 015/016/018/019/020/021/022/023/024), los índices
-- con IF NOT EXISTS y la EXCLUDE con guarda de pg_constraint. Una segunda pasada
-- no hace nada, y sobre una base con datos no cambia ninguna fila.

-- btree_gist da los operadores de igualdad de texto al índice GiST de la
-- EXCLUDE (mismo patrón que 002, 018 y 020).
CREATE EXTENSION IF NOT EXISTS btree_gist;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'payroll_runs'
  ) THEN
    CREATE TABLE payroll_runs (
      id         TEXT PRIMARY KEY,
      -- El salón es el tenant: si el salón se va, sus liquidaciones se van.
      salon_id   TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      -- Tipo de período liquidado (D5): semana lunes-domingo, quincena 1-15 o
      -- 16-fin de mes, mes calendario.
      kind       TEXT NOT NULL,
      -- Fechas LOCALES del salón, INCLUSIVAS: el [from, to] que se muestra. La
      -- EXCLUDE las convierte a [from, to+1) para comparar días completos.
      from_date  DATE NOT NULL,
      to_date    DATE NOT NULL,
      -- 'open' se puede editar (ajuste por línea) y borrar; 'paid' no admite
      -- NINGÚN cambio (ni líneas, ni borrado, ni un segundo pago).
      status     TEXT NOT NULL DEFAULT 'open',
      -- Quién la creó y quién la pagó. Sin FK: el dato es para el historial y no
      -- tiene que sobrevivir a nada (igual que time_off.created_by).
      created_by TEXT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      paid_at    TIMESTAMPTZ,
      paid_by    TEXT,
      CONSTRAINT payroll_runs_kind_check
        CHECK (kind IN ('week','fortnight','month')),
      CONSTRAINT payroll_runs_status_check
        CHECK (status IN ('open','paid')),
      CONSTRAINT payroll_runs_dates_check
        CHECK (from_date <= to_date),
      -- Estado y pago van juntos: una pagada siempre tiene paid_at (y paid_by);
      -- una abierta nunca.
      CONSTRAINT payroll_runs_paid_check
        CHECK ((status = 'open' AND paid_at IS NULL)
            OR (status = 'paid' AND paid_at IS NOT NULL))
    );
  END IF;
END $$;

-- Listado de liquidaciones del salón, la más nueva primero.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'payroll_runs_salon_from_idx'
  ) THEN
    CREATE INDEX payroll_runs_salon_from_idx
      ON payroll_runs (salon_id, from_date DESC, created_at DESC);
  END IF;
END $$;

-- Dos liquidaciones de CUALQUIER tipo (abiertas o pagadas) no pueden pisar los
-- mismos días: el rango es semiabierto [from, to+1) y una que termina el 15 no
-- choca con otra que empieza el 16. Guardada por nombre para que reaplicar la
-- migración en una base ya migrada no falle.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'payroll_runs_no_overlap'
      AND conrelid = to_regclass(current_schema() || '.payroll_runs')
  ) THEN
    ALTER TABLE payroll_runs
      ADD CONSTRAINT payroll_runs_no_overlap EXCLUDE USING gist (
        salon_id WITH =,
        daterange(from_date, to_date + 1, '[)') WITH &&
      );
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'payroll_lines'
  ) THEN
    CREATE TABLE payroll_lines (
      run_id             TEXT NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
      user_id            TEXT NOT NULL REFERENCES users(id),
      -- FOTO del nombre: la liquidación pagada se lee igual aunque la persona
      -- cambie su nombre después (el rol, el estado y la foto del pago no se
      -- reescriben).
      name_snapshot      TEXT NOT NULL,
      sales_count        INTEGER NOT NULL DEFAULT 0,
      sales_total_cents  INTEGER NOT NULL DEFAULT 0,
      commission_cents   INTEGER NOT NULL DEFAULT 0,
      fixed_cents        INTEGER NOT NULL DEFAULT 0,
      -- Ajuste manual con signo (entero): el dueño corrige una venta mal
      -- cargada sin tocar la caja (el ajuste es por línea de la liquidación).
      adjustment_cents   INTEGER NOT NULL DEFAULT 0,
      -- Obligatoria cuando el ajuste no es 0 (la regla vive en el dominio).
      adjustment_note    TEXT NOT NULL DEFAULT '',
      -- total = comisión + fijo + ajuste, congelado acá para que el listado y
      -- el detalle nunca tengan que recalcular.
      total_cents        INTEGER NOT NULL,
      estimated          BOOLEAN NOT NULL DEFAULT false,
      PRIMARY KEY (run_id, user_id),
      CONSTRAINT payroll_lines_name_len
        CHECK (char_length(name_snapshot) BETWEEN 1 AND 120),
      CONSTRAINT payroll_lines_counts
        CHECK (sales_count >= 0),
      -- Mismos topes que el resto del dinero (010_limits, domain.MaxPayCents):
      -- otro camino de escritura (importador, script) también los respeta.
      CONSTRAINT payroll_lines_sales_total_range
        CHECK (sales_total_cents BETWEEN 0 AND 2000000000),
      CONSTRAINT payroll_lines_commission_range
        CHECK (commission_cents BETWEEN 0 AND 2000000000),
      CONSTRAINT payroll_lines_fixed_range
        CHECK (fixed_cents BETWEEN 0 AND 2000000000),
      CONSTRAINT payroll_lines_adjustment_range
        CHECK (adjustment_cents BETWEEN -2000000000 AND 2000000000),
      CONSTRAINT payroll_lines_note_len
        CHECK (char_length(adjustment_note) <= 200),
      -- El total NUNCA es negativo: comisión y fijo no lo son, así que el
      -- ajuste no puede hundir la línea por debajo de 0.
      CONSTRAINT payroll_lines_total_range
        CHECK (total_cents BETWEEN 0 AND 2000000000)
    );
  END IF;
END $$;

-- Detalle de una liquidación: las líneas en el orden de la vista previa.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname = current_schema() AND indexname = 'payroll_lines_run_total_idx'
  ) THEN
    CREATE INDEX payroll_lines_run_total_idx
      ON payroll_lines (run_id, total_cents DESC, name_snapshot);
  END IF;
END $$;
