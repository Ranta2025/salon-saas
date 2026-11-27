import { useEffect, useState, type FormEvent } from 'react'
import {
  MAX_PACKAGE_NOTE_LENGTH,
  PACKAGE_MESSAGES,
  sessionsLabel,
  validatePackageAdjustDraft,
  type ClientPackage,
  type PackageAdjustDraft,
  type PackageAdjustField,
  type PackageAdjustPayload,
} from '../lib/packages'

type PackageAdjustFormProps = {
  /** Balance being corrected; the server's current numbers are shown as a reference. */
  pkg: ClientPackage
  saving: boolean
  /** Sentence of the server (400/409) or the offline/network notice, verbatim. */
  serverError: string | null
  onCancel: () => void
  onSubmit: (payload: PackageAdjustPayload) => void
}

const EMPTY_DRAFT: PackageAdjustDraft = { delta: '', note: '' }

/**
 * «Ajustar saldo» (contract F8, D9, owner and admin): a signed number of sessions
 * and a mandatory note. It corrects a wrong redeem (there is no cancellation of
 * sales); the server decides whether the final balance stays inside 0..total.
 */
export function PackageAdjustForm({
  pkg,
  saving,
  serverError,
  onCancel,
  onSubmit,
}: PackageAdjustFormProps) {
  const [draft, setDraft] = useState<PackageAdjustDraft>(EMPTY_DRAFT)
  const [error, setError] = useState<{ field: PackageAdjustField; message: string } | null>(null)

  useEffect(() => {
    setDraft(EMPTY_DRAFT)
    setError(null)
  }, [pkg.id])

  const idBase = `package-adjust-${pkg.id}`
  const fieldError = (field: PackageAdjustField): string | null =>
    error?.field === field ? error.message : null

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    const check = validatePackageAdjustDraft(draft)
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
          <label htmlFor={`${idBase}-delta`}>Sesiones a sumar o restar</label>
          <input
            id={`${idBase}-delta`}
            className="input"
            type="text"
            inputMode="text"
            autoComplete="off"
            placeholder="+1"
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
              Con signo: +1 devuelve una sesión descontada de más, -1 resta una. Sin decimales.
            </p>
          )}
        </div>

        <div className="field service-form__wide">
          <label htmlFor={`${idBase}-note`}>Motivo del ajuste</label>
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
            required
          />
          {fieldError('note') ? (
            <p className="field__error" id={`${idBase}-note-error`} role="alert">
              {fieldError('note')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-note-hint`}>
              Obligatorio, hasta {MAX_PACKAGE_NOTE_LENGTH} caracteres, en una sola línea. Queda
              en los movimientos del paquete.
            </p>
          )}
        </div>
      </div>

      <p className="field__hint">
        Saldo del servidor al abrir el formulario: {sessionsLabel(pkg)}. Si el ajuste lo deja
        fuera de ese rango, el servidor responde «{PACKAGE_MESSAGES.adjustOutOfRange}» y no
        escribe nada.
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
    </form>
  )
}
