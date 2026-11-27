import { useEffect, useState } from 'react'
import { describeApiError } from '@/lib/api'
import { parseAmountToCents } from '@/lib/money'
import {
  amountInputFromCents,
  PAY_MODEL_EXPLANATION,
  PAY_PERIOD_KINDS,
  PAY_PERIOD_KIND_LABELS,
  type PayModel,
  type PayPeriodKind,
} from '../lib/payroll'
import { roleLabel } from '@/lib/permissions'

export type PayModelDraft = Pick<
  PayModel,
  'fixed_cents' | 'fixed_period' | 'commission_enabled'
>

type PayModelCardProps = {
  model: PayModel
  /** Saves through the parent, which paints the change optimistically. */
  onSave: (next: PayModelDraft) => Promise<void>
  /** Marks the row of the person reading the page. */
  isSelf?: boolean
}

const INVALID_AMOUNT = 'Escribe un monto válido en dólares, por ejemplo 1500.00'

/**
 * Model of payment of one member (S-F3.2-5): fixed amount, the kind of period it
 * is paid on and whether the commissions are added. Every field saves on its own
 * with the optimistic update of the parent and the exact rollback it keeps.
 */
export function PayModelCard({ model, onSave, isSelf = false }: PayModelCardProps) {
  const [amount, setAmount] = useState(() => amountInputFromCents(model.fixed_cents))
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  // The server answer (or the rollback) is the truth of the field.
  useEffect(() => {
    setAmount(amountInputFromCents(model.fixed_cents))
  }, [model.fixed_cents])

  const fieldId = `pay-model-${model.user_id}`

  const commit = async (next: PayModelDraft): Promise<void> => {
    if (pending) {
      return
    }
    setSaved(false)
    setError(null)
    setPending(true)
    try {
      await onSave(next)
      setSaved(true)
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos guardar el modelo de pago.').message)
    } finally {
      setPending(false)
    }
  }

  const commitAmount = (): void => {
    const cents = parseAmountToCents(amount)
    if (cents === null || !Number.isSafeInteger(cents)) {
      setSaved(false)
      setError(INVALID_AMOUNT)
      return
    }
    if (cents === model.fixed_cents) {
      setAmount(amountInputFromCents(cents))
      setError(null)
      return
    }
    setAmount(amountInputFromCents(cents))
    void commit({
      fixed_cents: cents,
      fixed_period: model.fixed_period,
      commission_enabled: model.commission_enabled,
    })
  }

  const commitPeriod = (kind: PayPeriodKind): void => {
    void commit({
      fixed_cents: model.fixed_cents,
      fixed_period: kind,
      commission_enabled: model.commission_enabled,
    })
  }

  const commitCommission = (enabled: boolean): void => {
    void commit({
      fixed_cents: model.fixed_cents,
      fixed_period: model.fixed_period,
      commission_enabled: enabled,
    })
  }

  return (
    <article className="panel-card pay-model" aria-labelledby={`${fieldId}-title`}>
      <div className="pay-model__head">
        <h3 id={`${fieldId}-title`}>
          {model.name}
          {isSelf ? <span className="pay-model__self">tú</span> : null}
        </h3>
        <span className="pill pill--on">{roleLabel(model.role)}</span>
      </div>

      <fieldset className="pay-model__fields" aria-busy={pending}>
        <legend className="visually-hidden">Modelo de pago de {model.name}</legend>
        <div className="field">
          <label htmlFor={`${fieldId}-amount`}>Monto fijo</label>
          <input
            id={`${fieldId}-amount`}
            className="input"
            inputMode="decimal"
            autoComplete="off"
            placeholder="0.00"
            value={amount}
            aria-describedby={`${fieldId}-hint`}
            aria-invalid={error !== null ? true : undefined}
            onChange={(event) => {
              setAmount(event.target.value)
              setSaved(false)
            }}
            onBlur={commitAmount}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                event.currentTarget.blur()
              }
              if (event.key === 'Escape') {
                setAmount(amountInputFromCents(model.fixed_cents))
                setError(null)
                event.currentTarget.blur()
              }
            }}
          />
          <p id={`${fieldId}-hint`} className="field__hint">
            En dólares. Se guarda al salir del campo o con Enter. 0 = sin monto fijo.
          </p>
        </div>

        <div className="field">
          <label htmlFor={`${fieldId}-period`}>Período del monto fijo</label>
          <select
            id={`${fieldId}-period`}
            className="select"
            value={model.fixed_period}
            onChange={(event) => commitPeriod(event.target.value as PayPeriodKind)}
          >
            {PAY_PERIOD_KINDS.map((kind) => (
              <option key={kind} value={kind}>
                {PAY_PERIOD_KIND_LABELS[kind]}
              </option>
            ))}
          </select>
        </div>

        <label className="checkbox pay-model__commission">
          <input
            type="checkbox"
            checked={model.commission_enabled}
            aria-label={`Comisión sobre sus ventas: ${model.name}`}
            onChange={(event) => commitCommission(event.target.checked)}
          />
          <span>También gana comisión sobre sus ventas</span>
        </label>
      </fieldset>

      <p className="field__hint">{PAY_MODEL_EXPLANATION}</p>

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}
      {saved ? (
        <p className="notice notice--success" role="status">
          Modelo de pago guardado.
        </p>
      ) : null}
    </article>
  )
}
