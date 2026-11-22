import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { describeApiError, type ApiErrorNotice } from '../lib/api'
import { activeRole, isManager, roleLabel } from '../lib/permissions'
import {
  DEFAULT_SETTINGS,
  SLOT_STEPS,
  TIME_ZONES,
  fetchSettings,
  saveSettings,
  validateSettings,
  type SalonSettings,
  type SettingsField,
} from '../lib/settings'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'

const STEP_LABEL: Record<number, string> = {
  15: 'Cada 15 minutos',
  30: 'Cada 30 minutos',
  60: 'Cada 60 minutos',
}

/** `/panel/ajustes`: time zone, opening window and slot step (owner/admin). */
export function SettingsPage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = isManager(role)

  const [draft, setDraft] = useState<SalonSettings>(DEFAULT_SETTINGS)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [fieldError, setFieldError] = useState<{ field: SettingsField; message: string } | null>(
    null,
  )
  const [saving, setSaving] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setDraft(settings)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar la configuración del salón.'))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, allowed, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Ajustes" title="Configuración del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Ajustes" title="Configuración del salón" />
        <div className="empty-state">
          <h2>Esta sección es de la administración</h2>
          <p>
            Tu rol ({roleLabel(role)}) no puede cambiar los ajustes del salón. Si necesitas acceso,
            pídeselo a la dueña o a la administración.
          </p>
        </div>
      </>
    )
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setNotice(null)
    setFieldError(null)
    const validation = validateSettings(draft)
    if (!validation.ok) {
      setFieldError({ field: validation.field, message: validation.message })
      return
    }
    setSaving(true)
    try {
      const saved = await saveSettings(salonId, validation.settings)
      setDraft(saved)
      setNotice(
        'Ajustes guardados. La agenda y los horarios públicos ya usan este horario y esta zona.',
      )
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos guardar los ajustes del salón.'))
    } finally {
      setSaving(false)
    }
  }

  const fieldMessage = (field: SettingsField): string | undefined =>
    fieldError?.field === field ? fieldError.message : undefined

  return (
    <>
      <PageHeader
        eyebrow="Ajustes"
        title="Configuración del salón"
        description="Horario de atención, zona horaria y cada cuánto se ofrecen turnos en la reserva pública."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Recargar
          </button>
        }
      />

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}
      {loading && !loaded ? <LoadingState label="Cargando configuración…" /> : null}

      {loaded ? (
        <form className="panel-card settings-form" onSubmit={handleSubmit} noValidate>
          <div className="settings-form__grid">
            <div className="field settings-form__wide">
              <label htmlFor="settings-timezone">Zona horaria</label>
              <select
                id="settings-timezone"
                className="select"
                value={draft.timezone}
                onChange={(event) => setDraft({ ...draft, timezone: event.target.value })}
                aria-describedby={fieldMessage('timezone') ? 'settings-timezone-error' : 'settings-timezone-hint'}
                aria-invalid={fieldMessage('timezone') ? true : undefined}
              >
                {TIME_ZONES.every((zone) => zone.value !== draft.timezone) ? (
                  <option value={draft.timezone}>{draft.timezone}</option>
                ) : null}
                {TIME_ZONES.map((zone) => (
                  <option key={zone.value} value={zone.value}>
                    {zone.label}
                  </option>
                ))}
              </select>
              <p className="field__hint" id="settings-timezone-hint">
                La agenda, el cierre de caja y los horarios públicos usan esta zona.
              </p>
              {fieldMessage('timezone') ? (
                <p className="field__error" id="settings-timezone-error" role="alert">
                  {fieldMessage('timezone')}
                </p>
              ) : null}
            </div>

            <div className="field">
              <label htmlFor="settings-open">Hora de apertura</label>
              <input
                id="settings-open"
                className="input"
                type="time"
                value={draft.openTime}
                onChange={(event) => setDraft({ ...draft, openTime: event.target.value })}
                aria-invalid={fieldMessage('openTime') ? true : undefined}
                aria-describedby={fieldMessage('openTime') ? 'settings-open-error' : undefined}
                required
              />
              {fieldMessage('openTime') ? (
                <p className="field__error" id="settings-open-error" role="alert">
                  {fieldMessage('openTime')}
                </p>
              ) : null}
            </div>

            <div className="field">
              <label htmlFor="settings-close">Hora de cierre</label>
              <input
                id="settings-close"
                className="input"
                type="time"
                value={draft.closeTime}
                onChange={(event) => setDraft({ ...draft, closeTime: event.target.value })}
                aria-invalid={fieldMessage('closeTime') ? true : undefined}
                aria-describedby={fieldMessage('closeTime') ? 'settings-close-error' : undefined}
                required
              />
              {fieldMessage('closeTime') ? (
                <p className="field__error" id="settings-close-error" role="alert">
                  {fieldMessage('closeTime')}
                </p>
              ) : null}
            </div>

            <div className="field settings-form__wide">
              <label htmlFor="settings-step">Paso de los turnos</label>
              <select
                id="settings-step"
                className="select"
                value={draft.slotStepMin}
                onChange={(event) =>
                  setDraft({ ...draft, slotStepMin: Number(event.target.value) })
                }
                aria-invalid={fieldMessage('slotStepMin') ? true : undefined}
                aria-describedby={fieldMessage('slotStepMin') ? 'settings-step-error' : 'settings-step-hint'}
              >
                {SLOT_STEPS.map((step) => (
                  <option key={step} value={step}>
                    {STEP_LABEL[step]}
                  </option>
                ))}
              </select>
              <p className="field__hint" id="settings-step-hint">
                Cada cuánto tiempo se ofrece un turno en la reserva pública.
              </p>
              {fieldMessage('slotStepMin') ? (
                <p className="field__error" id="settings-step-error" role="alert">
                  {fieldMessage('slotStepMin')}
                </p>
              ) : null}
            </div>
          </div>

          <div className="settings-form__actions">
            <button type="submit" className="btn btn--wine" disabled={saving}>
              {saving ? 'Guardando…' : 'Guardar ajustes'}
            </button>
            <button
              type="button"
              className="btn btn--quiet"
              onClick={reload}
              disabled={saving}
            >
              Descartar cambios
            </button>
          </div>
        </form>
      ) : null}
    </>
  )
}
