import { useState, type FormEvent } from 'react'
import {
  LOYALTY_HELP,
  MAX_LOYALTY_VISITS,
  MAX_REWARD_VALIDITY_DAYS,
  MIN_LOYALTY_VISITS,
  previewSentence,
  validateLoyaltyDraft,
  type LoyaltyDraft,
  type LoyaltyField,
  type LoyaltyProgramPayload,
  type LoyaltyServiceOption,
} from '../lib/loyalty'

type LoyaltyProgramFormProps = {
  draft: LoyaltyDraft
  onChange: (draft: LoyaltyDraft) => void
  /** Services the reward can be: the active ones (plus the saved one, marked). */
  options: readonly LoyaltyServiceOption[]
  /** Reception reads the program; only owner and admin edit it (D4). */
  readOnly: boolean
  saving: boolean
  /** The browser is offline: the program is only saved online (D9-d). */
  offline: boolean
  /** Sentence of the server (400/403) or the offline/network notice, verbatim. */
  serverError: string | null
  /** Day the visits started to count (local day of the salon), when the program was saved. */
  countingFrom: string | null
  onSubmit: (payload: LoyaltyProgramPayload) => void
}

/**
 * Program form of `/panel/fidelidad` (contract F9, D4/D9-a): activado, visitas
 * necesarias (2..50), servicio de regalo (the active services) and vigencia in days
 * or «Sin vencimiento». The draft is checked with the sentences of the API before
 * sending; the preview is the sentence the public page will print.
 */
export function LoyaltyProgramForm({
  draft,
  onChange,
  options,
  readOnly,
  saving,
  offline,
  serverError,
  countingFrom,
  onSubmit,
}: LoyaltyProgramFormProps) {
  const [error, setError] = useState<{ field: LoyaltyField; message: string } | null>(null)
  const fieldError = (field: LoyaltyField): string | null =>
    error?.field === field ? error.message : null
  // A message about a field goes away as soon as the person changes the form.
  const change = (patch: Partial<LoyaltyDraft>): void => {
    onChange({ ...draft, ...patch })
    setError(null)
  }
  const sentence = previewSentence(draft, options)

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (readOnly || saving) {
      return
    }
    const check = validateLoyaltyDraft(draft)
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      const target = { visits: 'loyalty-visits', service: 'loyalty-service', validity: 'loyalty-validity' }
      document.getElementById(target[check.field])?.focus()
      return
    }
    setError(null)
    onSubmit(check.payload)
  }

  return (
    <div className="loyalty-layout">
      <form className="service-form" onSubmit={handleSubmit} noValidate>
        <div className="field">
          <label className="checkbox" htmlFor="loyalty-enabled">
            <input
              id="loyalty-enabled"
              type="checkbox"
              checked={draft.enabled}
              disabled={readOnly}
              onChange={(event) => change({ enabled: event.target.checked })}
            />
            <span>Programa activado</span>
          </label>
          <p className="field__hint">
            Apagado, no se entregan ni se usan premios, pero los que ya se entregaron se
            conservan.
          </p>
        </div>

        <div className="service-form__grid loyalty-form__grid">
          <div className="field">
            <label htmlFor="loyalty-visits">Visitas necesarias</label>
            <input
              id="loyalty-visits"
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              placeholder="10"
              value={draft.visits_required}
              disabled={readOnly}
              onChange={(event) => change({ visits_required: event.target.value })}
              aria-invalid={fieldError('visits') ? true : undefined}
              aria-describedby={fieldError('visits') ? 'loyalty-visits-error' : 'loyalty-visits-hint'}
            />
            {fieldError('visits') ? (
              <p className="field__error" id="loyalty-visits-error" role="alert">
                {fieldError('visits')}
              </p>
            ) : (
              <p className="field__hint" id="loyalty-visits-hint">
                De {MIN_LOYALTY_VISITS} a {MAX_LOYALTY_VISITS}, sin decimales. Cada ese número de
                visitas, la clienta gana un premio.
              </p>
            )}
          </div>

          <div className="field">
            <label htmlFor="loyalty-service">Servicio de regalo</label>
            <select
              id="loyalty-service"
              className="select"
              value={draft.reward_service_id}
              disabled={readOnly}
              onChange={(event) => change({ reward_service_id: event.target.value })}
              aria-invalid={fieldError('service') ? true : undefined}
              aria-describedby={fieldError('service') ? 'loyalty-service-error' : undefined}
            >
              <option value="">Elegí un servicio</option>
              {options.map((option) => (
                <option key={option.id} value={option.id}>
                  {option.label}
                </option>
              ))}
            </select>
            {fieldError('service') ? (
              <p className="field__error" id="loyalty-service-error" role="alert">
                {fieldError('service')}
              </p>
            ) : null}
          </div>

          <div className="field">
            <label htmlFor="loyalty-validity">Vigencia del premio en días</label>
            <input
              id="loyalty-validity"
              className="input"
              type="text"
              inputMode="numeric"
              autoComplete="off"
              placeholder="90"
              value={draft.validity_days}
              disabled={readOnly || draft.no_expiry}
              onChange={(event) => change({ validity_days: event.target.value })}
              aria-invalid={fieldError('validity') ? true : undefined}
              aria-describedby={
                fieldError('validity') ? 'loyalty-validity-error' : 'loyalty-validity-hint'
              }
            />
            <label className="checkbox" htmlFor="loyalty-no-expiry">
              <input
                id="loyalty-no-expiry"
                type="checkbox"
                checked={draft.no_expiry}
                disabled={readOnly}
                onChange={(event) => change({ no_expiry: event.target.checked })}
              />
              <span>Sin vencimiento</span>
            </label>
            {fieldError('validity') ? (
              <p className="field__error" id="loyalty-validity-error" role="alert">
                {fieldError('validity')}
              </p>
            ) : (
              <p className="field__hint" id="loyalty-validity-hint">
                De 1 a {MAX_REWARD_VALIDITY_DAYS} días desde que se entrega. Vence al final de ese
                día.
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
            Tu rol ve el programa y entrega o usa premios desde la ficha de la clienta. Cambiar
            el programa queda en manos de la dueña o de la administración.
          </p>
        ) : (
          <div className="page-head__actions">
            <button type="submit" className="btn btn--wine" disabled={saving || offline}>
              {saving ? 'Guardando…' : 'Guardar programa'}
            </button>
          </div>
        )}

        <p className="field__hint">
          Se cuenta una visita por cada venta de la clienta que lleva al menos un servicio.
          {countingFrom !== null
            ? ` Las visitas se cuentan desde el ${countingFrom}, cuando se guardó el programa por primera vez: las ventas anteriores no suman.`
            : ' Las ventas anteriores a la primera vez que guardes el programa no suman.'}{' '}
          Si en el punto de venta no se elige a la clienta, esa venta no cuenta.
        </p>
      </form>

      <aside className="loyalty-preview" aria-labelledby="loyalty-preview-title">
        <h2 id="loyalty-preview-title" className="loyalty-preview__title">
          {LOYALTY_HELP}
        </h2>
        {sentence !== null ? (
          <p className="loyalty-preview__sentence">{sentence}</p>
        ) : (
          <p className="loyalty-preview__empty">
            Completá las visitas y el servicio para ver la frase.
          </p>
        )}
        {sentence !== null && !draft.enabled ? (
          <p className="field__hint">
            Con el programa apagado esta frase no se muestra en tu página pública.
          </p>
        ) : null}
        {sentence !== null && draft.enabled ? (
          <p className="field__hint">Se publica al guardar. Si el servicio se desactiva, deja de mostrarse.</p>
        ) : null}
      </aside>
    </div>
  )
}
