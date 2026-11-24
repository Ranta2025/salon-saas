import { useEffect, useState, type FormEvent } from 'react'
import {
  ADJUST_REASONS,
  ADJUST_REASON_LABELS,
  MAX_ADJUST_NOTE_LENGTH,
  PRODUCT_MESSAGES,
  validateAdjustDraft,
  type AdjustDraft,
  type AdjustField,
  type AdjustPayload,
  type Product,
} from '../../lib/products'

type ProductAdjustFormProps = {
  /** Product whose stock is adjusted; it shows the stock on screen as a reference. */
  product: Product
  saving: boolean
  /** Spanish sentence of the server (400/409), shown verbatim. */
  serverError: string | null
  onCancel: () => void
  onSubmit: (payload: AdjustPayload) => void
}

const EMPTY_DRAFT: AdjustDraft = { delta: '', reason: 'restock', note: '' }

/**
 * Adjustment of the stock of a product (contract F6): reason, signed delta and an
 * optional note. `restock` only takes positive units, `loss` only negative ones and
 * `correction` takes either sign; the sign is checked here with the sentence of the
 * API and the server still decides the final stock (a 409 says it cannot go below
 * zero and nothing is written).
 */
export function ProductAdjustForm({
  product,
  saving,
  serverError,
  onCancel,
  onSubmit,
}: ProductAdjustFormProps) {
  const [draft, setDraft] = useState<AdjustDraft>(EMPTY_DRAFT)
  const [error, setError] = useState<{ field: AdjustField; message: string } | null>(null)

  useEffect(() => {
    setDraft(EMPTY_DRAFT)
    setError(null)
  }, [product.id])

  const idBase = `product-adjust-${product.id}`
  const fieldError = (field: AdjustField): string | null =>
    error?.field === field ? error.message : null

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    const check = validateAdjustDraft(draft)
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      return
    }
    setError(null)
    onSubmit(check.payload)
  }

  return (
    <form className="service-form" onSubmit={handleSubmit} noValidate>
      <div className="service-form__grid">
        <div className="field">
          <label htmlFor={`${idBase}-reason`}>Motivo del ajuste</label>
          <select
            id={`${idBase}-reason`}
            className="select"
            value={draft.reason}
            onChange={(event) => setDraft({ ...draft, reason: event.target.value })}
            aria-invalid={fieldError('reason') ? true : undefined}
            aria-describedby={fieldError('reason') ? `${idBase}-reason-error` : undefined}
          >
            {ADJUST_REASONS.map((reason) => (
              <option key={reason} value={reason}>
                {ADJUST_REASON_LABELS[reason]}
              </option>
            ))}
          </select>
          {fieldError('reason') ? (
            <p className="field__error" id={`${idBase}-reason-error`} role="alert">
              {fieldError('reason')}
            </p>
          ) : null}
        </div>

        <div className="field">
          <label htmlFor={`${idBase}-delta`}>Unidades del ajuste</label>
          <input
            id={`${idBase}-delta`}
            className="input"
            type="text"
            inputMode="text"
            autoComplete="off"
            placeholder="+5"
            value={draft.delta}
            onChange={(event) => setDraft({ ...draft, delta: event.target.value })}
            aria-invalid={fieldError('delta') ? true : undefined}
            aria-describedby={
              fieldError('delta') ? `${idBase}-delta-error` : `${idBase}-delta-hint`
            }
            required
          />
          {fieldError('delta') ? (
            <p className="field__error" id={`${idBase}-delta-error`} role="alert">
              {fieldError('delta')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-delta-hint`}>
              Con signo: la reposición suma (+5), la pérdida resta (-2) y la corrección
              acepta los dos. Sin decimales.
            </p>
          )}
        </div>

        <div className="field service-form__wide">
          <label htmlFor={`${idBase}-note`}>Nota (opcional)</label>
          <textarea
            id={`${idBase}-note`}
            className="textarea"
            rows={2}
            value={draft.note}
            onChange={(event) => setDraft({ ...draft, note: event.target.value })}
            aria-invalid={fieldError('note') ? true : undefined}
            aria-describedby={
              fieldError('note') ? `${idBase}-note-error` : `${idBase}-note-hint`
            }
          />
          {fieldError('note') ? (
            <p className="field__error" id={`${idBase}-note-error`} role="alert">
              {fieldError('note')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-note-hint`}>
              Hasta {MAX_ADJUST_NOTE_LENGTH} caracteres. Queda en los movimientos del
              producto.
            </p>
          )}
        </div>
      </div>

      <p className="field__hint">
        Stock del servidor al abrir el formulario: {product.stock} unidades. El ajuste no
        cambia el precio ni el estado.
      </p>

      {serverError ? (
        <p className="notice notice--error" role="alert">
          {serverError}
        </p>
      ) : null}

      <div className="page-head__actions">
        <button type="submit" className="btn btn--wine" disabled={saving}>
          {saving ? 'Guardando…' : 'Guardar ajuste'}
        </button>
        <button type="button" className="btn btn--quiet" onClick={onCancel} disabled={saving}>
          Cancelar
        </button>
      </div>

      <p className="field__hint">
        Si el ajuste dejaría el stock en negativo, el servidor responde «
        {PRODUCT_MESSAGES.adjustNegativeStock}» y no escribe nada.
      </p>
    </form>
  )
}
