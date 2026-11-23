-- 024_member_pay: modelo de pago de cada miembro del salón (F3.2, D3 del
-- contrato F3).
--
-- Hasta hoy el único pago posible era la comisión del catálogo. Ahora cada
-- persona puede tener sueldo fijo por período, comisión o mixto: esto guarda el
-- fijo (monto y cada cuánto) y si cobra comisión.
--
-- SIN fila = solo comisión (fijo 0, mes, comisión encendida): lo de hoy sigue
-- igual y no hace falta backfill.
--
-- La FK es COMPUESTA a memberships(user_id, salon_id): un modelo de pago solo
-- existe para alguien del salón, así que el aislamiento por tenant no depende de
-- que el caso de uso se acuerde de filtrar. Incluye a quien fue dado de baja
-- (la fila de memberships sigue existiendo con ended_at): el dueño conserva su
-- modelo y puede ajustarlo, y la liquidación le paga solo la comisión de lo que
-- vendió (D4).
--
-- Idempotente: la tabla se crea con guarda de information_schema mirada en
-- current_schema() (igual que 015/016/018/019/020/021/022/023) y los índices con
-- IF NOT EXISTS. Una segunda pasada no hace nada y, sobre una base con datos, no
-- cambia ninguna fila.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema()
      AND table_name = 'member_pay'
  ) THEN
    CREATE TABLE member_pay (
      -- El salón es el tenant: si el salón se va, el modelo se va con él.
      salon_id           TEXT NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
      user_id            TEXT NOT NULL,
      -- Sueldo fijo en centavos por período. El tope es el del resto del dinero
      -- (INTEGER, 010_limits): un valor mayor no se puede guardar.
      fixed_cents        INTEGER NOT NULL DEFAULT 0,
      -- Cada cuánto se paga el fijo: se paga UNA vez cuando el período liquidado
      -- es de este tipo (semana, quincena o mes).
      fixed_period       TEXT NOT NULL DEFAULT 'month',
      -- Apagada = fijo puro: su comisión se informa 0. Las ventas siguen contando.
      commission_enabled BOOLEAN NOT NULL DEFAULT true,
      PRIMARY KEY (salon_id, user_id),
      -- La membresía es la que autoriza: sin ella no hay modelo de pago.
      CONSTRAINT member_pay_membership_fkey
        FOREIGN KEY (user_id, salon_id) REFERENCES memberships(user_id, salon_id) ON DELETE CASCADE,
      -- La última barrera si mañana otro camino de escritura toca la tabla.
      CONSTRAINT member_pay_fixed_cents_range
        CHECK (fixed_cents >= 0 AND fixed_cents <= 2000000000),
      CONSTRAINT member_pay_fixed_period_check
        CHECK (fixed_period IN ('week','fortnight','month'))
    );
  END IF;
END $$;
