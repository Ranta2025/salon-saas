import { useState, type FormEvent } from 'react'
import {
  MAX_DEPOSIT_PERCENT,
  MAX_HOLD_MINUTES,
  MAX_REFUND_HOURS,
  MIN_DEPOSIT_PERCENT,
  MIN_HOLD_MINUTES,
  MIN_REFUND_HOURS,
  validateDepositPolicy,
  type DepositPolicyDraft,
  type DepositPolicyField,
  type DepositPolicyPayload,
} from '../../lib/deposits'

type DepositPolicyFormProps = {
  draft: DepositPolicyDraft
  onChange: (draft: DepositPolicyDraft) => void
  /** Reception reads the policy; only owner and admin edit it (D3, D13-a). */
  readOnly: boolean
  saving: boolean
  /** The browser is offline: the policy is only saved online (D13-d). */
  offline: boolean
  /** Sentence of the server (400/403/409) or the offline/network notice, verbatim. */
  serverError: string | null
  /** What the client sees with the percentage of the draft, or `null` while it is unusable. */
  preview: string | null
  onSubmit: (payload: DepositPolicyPayload) => void
}

const FOCUS_TARGET: Record<DepositPolicyField, string> = {
  percent: 'deposit-percent',
  hold: 'deposit-hold',
  refund: 'deposit-refund',
}

/**
 * Deposit policy of `/panel/senas` (contract F11, D3, D13-a): activada, porcentaje
 * (10..100), espera en minutos (5..120) y devolución en horas (0..168). The draft is
 * checked with the sentences of the API before sending, and the preview is the
 * sentence the client sees on the landing and on the confirmation screen.
 */
export function DepositPolicyForm({
  draft,
  onChange,
  readOnly,
  saving,
  offline,
  serverError,
  preview,
  onSubmit,
}: DepositPolicyFormProps) {
  const [error, setError] = useState<{ field: DepositPolicyField; message: string } | null>(null)
  const fieldError = (field: DepositPolicyField): string | null =>
    error?.field === field ? error.message : null
  // A message about a field goes away as soon as the person changes the form.
  const change = (patch: Partial<DepositPolicyDraft>): void => {
    onChange({ ...draft, ...patch })
    setError(null)
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (readOnly || saving) {
      return
    }
    const check = validateDepositPolicy(draft)
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      document.getElementById(FOCUS_TARGET[check.field])?.focus()
      return
    }
    setError(null)
    onSubmit(check.payload)
  }

  return (
    <div className="deposit-layout">
      <form className="service-form" onSubmit={handleSubmit} noValidate>
        <div className="field">
          <label className="checkbox" htmlFor="deposit-enabled">
            <input
              id="deposit-enabled"
              type="checkbox"
              checked={draft.enabled}
              disabled={readOnly}
              onChange={(event) => change({ enabled: event.target.checked })}
            />
            <span>Seña activada</span>
          </label>
          <p className="field__hint">
            Activada, la clienta paga una seña al reservar en línea y el turno queda retenido
            mientras dura la espera. Apagada, la reserva y el correo son los de siempre.
          </p>
        </div>

        <div className="service-form__grid deposit-form__grid">
          <div className="field">
            <label htmlFor="deposit-percent">Porcentaje del servicio</label>
            <input
              id="deposit-percent"
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              placeholder="30"
              value={draft.percent}
              disabled={readOnly}
              onChange={(event) => change({ percent: event.target.value })}
              aria-invalid={fieldError('percent') ? true : undefined}
              aria-describedby={fieldError('percent') ? 'deposit-percent-error' : 'deposit-percent-hint'}
            />
            {fieldError('percent') ? (
              <p className="field__error" id="deposit-percent-error" role="alert">
                {fieldError('percent')}
              </p>
            ) : (
              <p className="field__hint" id="deposit-percent-hint">
                De {MIN_DEPOSIT_PERCENT} a {MAX_DEPOSIT_PERCENT}, sin decimales. El servidor calcula
                el monto de cada servicio y la página pública muestra ese monto.
              </p>
            )}
          </div>

          <div className="field">
            <label htmlFor="deposit-hold">Espera para pagar (minutos)</label>
            <input
              id="deposit-hold"
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              placeholder="30"
              value={draft.hold_minutes}
              disabled={readOnly}
              onChange={(event) => change({ hold_minutes: event.target.value })}
              aria-invalid={fieldError('hold') ? true : undefined}
              aria-describedby={fieldError('hold') ? 'deposit-hold-error' : 'deposit-hold-hint'}
            />
            {fieldError('hold') ? (
              <p className="field__error" id="deposit-hold-error" role="alert">
                {fieldError('hold')}
              </p>
            ) : (
              <p className="field__hint" id="deposit-hold-hint">
                De {MIN_HOLD_MINUTES} a {MAX_HOLD_MINUTES} minutos. Si la seña no se paga en ese
                plazo, el turno se cancela solo y el horario vuelve a quedar libre.
              </p>
            )}
          </div>

          <div className="field">
            <label htmlFor="deposit-refund">Devolución (horas antes del turno)</label>
            <input
              id="deposit-refund"
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              placeholder="24"
              value={draft.refund_hours}
              disabled={readOnly}
              onChange={(event) => change({ refund_hours: event.target.value })}
              aria-invalid={fieldError('refund') ? true : undefined}
              aria-describedby={fieldError('refund') ? 'deposit-refund-error' : 'deposit-refund-hint'}
            />
            {fieldError('refund') ? (
              <p className="field__error" id="deposit-refund-error" role="alert">
                {fieldError('refund')}
              </p>
            ) : (
              <p className="field__hint" id="deposit-refund-hint">
                De {MIN_REFUND_HOURS} a {MAX_REFUND_HOURS} horas. Si el turno se cancela con más
                anticipación, la seña se devuelve; con menos, se pierde. Cero es «sin devolución».
              </p>
            )}
          </div>
        </div>

        {serverError ? (
          <p className="notice notice--error" role="alert">
            {serverError}
          </p>
        ) : null}

        {readOnly ? (
          <p className="field__hint">
            Tu rol ve la política y la lista de señas. Cambiarla queda en manos de la dueña o de
            la administración.
          </p>
        ) : (
          <div className="page-head__actions">
            <button type="submit" className="btn btn--wine" disabled={saving || offline}>
              {saving ? 'Guardando…' : 'Guardar política'}
            </button>
            {offline ? (
              <span className="field__hint">Sin conexión: la política no se guarda sin señal.</span>
            ) : null}
          </div>
        )}
      </form>

      <aside className="deposit-preview" aria-labelledby="deposit-preview-title">
        <p className="eyebrow">Vista previa</p>
        <h3 id="deposit-preview-title">Lo que ve la clienta</h3>
        {preview !== null ? (
          <p className="deposit-preview__sentence">{preview}</p>
        ) : (
          <p className="field__hint">
            Completá el porcentaje para ver la frase que se muestra en la página pública y en la
            confirmación de la reserva.
          </p>
        )}
      </aside>
    </div>
  )
}
