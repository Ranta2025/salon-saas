import { useState, type FormEvent } from 'react'
import {
  MAX_REVIEW_DELAY_HOURS,
  MIN_REVIEW_DELAY_HOURS,
  reviewDelayLabel,
  validateReviewSettingsDraft,
  type ReviewSettings,
  type ReviewSettingsDraft,
} from '../lib/reviews'

type ReviewSettingsFormProps = {
  draft: ReviewSettingsDraft
  onChange: (draft: ReviewSettingsDraft) => void
  /** Reception reads the settings; only owner and admin write them (D6). */
  readOnly: boolean
  saving: boolean
  /** The browser is offline: nothing is sent, nothing is queued. */
  offline: boolean
  /** Sentence of the server (400/403) or the offline notice, verbatim. */
  serverError: string | null
  onSubmit: (payload: ReviewSettings) => void
}

/**
 * Settings of `/panel/resenas` (contract F13, D6): the switch and the demora in hours
 * (1..72). The same client-side rule as the server — a whole number of hours — with
 * the sentence the API would answer; the preview says when the request goes out.
 */
export function ReviewSettingsForm({
  draft,
  onChange,
  readOnly,
  saving,
  offline,
  serverError,
  onSubmit,
}: ReviewSettingsFormProps) {
  const [error, setError] = useState<string | null>(null)
  const checked = validateReviewSettingsDraft(draft)

  const change = (patch: Partial<ReviewSettingsDraft>): void => {
    onChange({ ...draft, ...patch })
    setError(null)
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (readOnly || saving) {
      return
    }
    const result = validateReviewSettingsDraft(draft)
    if (!result.ok) {
      setError(result.message)
      document.getElementById('review-settings-delay')?.focus()
      return
    }
    setError(null)
    onSubmit(result.payload)
  }

  return (
    <form className="service-form review-settings" onSubmit={handleSubmit} noValidate>
      <div className="field">
        <label className="checkbox" htmlFor="review-settings-enabled">
          <input
            id="review-settings-enabled"
            type="checkbox"
            role="switch"
            checked={draft.enabled}
            disabled={readOnly}
            onChange={(event) => change({ enabled: event.target.checked })}
          />
          <span>Pedir reseña después de cada turno atendido</span>
        </label>
        <p className="field__hint">
          Apagado, no se piden reseñas nuevas; las que ya llegaron se conservan y se siguen viendo
          en tu página.
        </p>
      </div>

      <div className="field review-settings__delay">
        <label htmlFor="review-settings-delay">Demora del pedido (horas)</label>
        <input
          id="review-settings-delay"
          className="input"
          type="text"
          inputMode="numeric"
          autoComplete="off"
          placeholder="24"
          value={draft.delayHours}
          disabled={readOnly}
          onChange={(event) => change({ delayHours: event.target.value })}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? 'review-settings-delay-error' : 'review-settings-delay-hint'}
        />
        {error ? (
          <p className="field__error" id="review-settings-delay-error" role="alert">
            {error}
          </p>
        ) : (
          <p className="field__hint" id="review-settings-delay-hint">
            De {MIN_REVIEW_DELAY_HOURS} a {MAX_REVIEW_DELAY_HOURS}, sin decimales: el correo sale ese
            tiempo después del turno.
          </p>
        )}
      </div>

      <p className="review-settings__preview" role="status">
        {checked.ok
          ? `El pedido de reseña sale ${reviewDelayLabel(checked.payload.delay_hours)}.`
          : 'Elegí la demora para ver cuándo sale el pedido.'}
      </p>

      {serverError ? (
        <p className="notice notice--error" role="alert">
          {serverError}
        </p>
      ) : null}

      {readOnly ? (
        <p className="field__hint">Solo la dueña y la administración pueden cambiar esta configuración.</p>
      ) : (
        <div className="settings-form__actions">
          <button type="submit" className="btn btn--wine" disabled={saving || offline}>
            {saving ? 'Guardando…' : 'Guardar configuración'}
          </button>
          {offline ? <p className="field__hint">Sin conexión: no se envía nada hasta que vuelva la red.</p> : null}
        </div>
      )}
    </form>
  )
}
