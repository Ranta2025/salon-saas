import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { describeApiError } from '@/lib/api'
import { fetchSalonHours, saveSalonHours } from '../lib/settings'
import {
  copyDraftDay,
  copyTargets,
  setDraftDay,
  validateDraft,
  WEEKDAY_LABELS,
  weekDraft,
  type DraftDay,
  type Weekday,
} from '@/features/team/lib/weeklyHours'
import { LoadingState } from '@/app/components/Feedback'

type SalonHoursCardProps = {
  salonId: string
  /** Single opening window of `/settings`, used for the days still closed. */
  defaults: { open: string; close: string }
}

/**
 * Weekly hours of the salon (`GET/PUT /salons/:id/hours`, contract S-F2d1-6).
 * Seven rows with an open/closed switch and the opening and closing time; an
 * empty list goes back to the single window of the settings form.
 */
export function SalonHoursCard({ salonId, defaults }: SalonHoursCardProps) {
  const [draft, setDraft] = useState<DraftDay[] | null>(null)
  const [custom, setCustom] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [fieldError, setFieldError] = useState<{ weekday: number; message: string } | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [confirmReset, setConfirmReset] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    let active = true
    setLoading(true)
    setError(null)
    fetchSalonHours(salonId)
      .then((hours) => {
        if (active) {
          setDraft(weekDraft(hours.days, defaults))
          setCustom(hours.custom)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar el horario del salón.').message)
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
    // `defaults` only seeds the days that are closed; it must not refetch.
  }, [salonId, reloadKey])

  const patchDay = (weekday: Weekday, patch: Partial<Pick<DraftDay, 'open' | 'close' | 'closed'>>) => {
    setDraft((current) => (current === null ? current : setDraftDay(current, weekday, patch)))
    setFieldError(null)
    setNotice(null)
    setError(null)
  }

  const handleCopy = (weekday: Weekday, value: string) => {
    const target = copyTargets(weekday).find((option) => option.value === value)
    if (!target) {
      return
    }
    setDraft((current) =>
      current === null ? current : copyDraftDay(current, weekday, target.weekdays),
    )
    setFieldError(null)
    setNotice(
      `${WEEKDAY_LABELS[weekday]} copiado a ${target.weekdays
        .map((day) => WEEKDAY_LABELS[day].toLowerCase())
        .join(', ')}. Guardá para aplicar el cambio.`,
    )
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (draft === null || saving) {
      return
    }
    setNotice(null)
    setError(null)
    setFieldError(null)
    const validation = validateDraft(draft)
    if (!validation.ok) {
      setFieldError({ weekday: validation.weekday, message: validation.message })
      return
    }
    setSaving(true)
    try {
      const saved = await saveSalonHours(salonId, validation.value)
      setDraft(weekDraft(saved.days, defaults))
      setCustom(saved.custom)
      setNotice(
        'Horario guardado. La agenda y la reserva pública ya usan estos días y estos horarios.',
      )
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos guardar el horario del salón.').message)
    } finally {
      setSaving(false)
    }
  }

  const handleReset = async (): Promise<void> => {
    if (saving) {
      return
    }
    setSaving(true)
    setError(null)
    setNotice(null)
    setFieldError(null)
    try {
      const saved = await saveSalonHours(salonId, [])
      setDraft(weekDraft(saved.days, defaults))
      setCustom(saved.custom)
      setConfirmReset(false)
      setNotice(
        'El salón volvió al horario único: abre todos los días con la apertura y el cierre de arriba.',
      )
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos volver al horario único.').message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="panel-card hours-card" aria-labelledby="salon-hours-title">
      <div className="panel-card__head">
        <h2 id="salon-hours-title">Horario semanal</h2>
        <span className="panel-card__meta">
          {custom ? 'Días propios' : 'Horario único para todos los días'}
        </span>
      </div>
      <p className="page-head__desc">
        Elegí qué días abre el salón y a qué hora. Un día apagado queda cerrado y no ofrece turnos
        en la reserva pública.
      </p>
      {!custom && draft !== null ? (
        <p className="notice notice--info" role="status">
          Hoy el salón abre todos los días con el horario único de arriba. Guardá para abrir y
          cerrar por día; «Volver al horario único» deshace los días propios.
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}
      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
          <button type="button" className="btn btn--quiet btn--sm hours-card__retry" onClick={reload}>
            Reintentar
          </button>
        </p>
      ) : null}
      {loading && draft === null ? <LoadingState label="Cargando horario…" /> : null}

      {draft !== null ? (
        <form className="hours-form" onSubmit={handleSubmit} noValidate>
          <div className="hours-list">
            {draft.map((day) => {
              const label = WEEKDAY_LABELS[day.weekday]
              const dayError = fieldError?.weekday === day.weekday ? fieldError.message : null
              return (
                <fieldset
                  key={day.weekday}
                  className="hours-row"
                  data-closed={day.closed ? 'true' : undefined}
                >
                  <legend className="hours-row__day">{label}</legend>
                  <div className="hours-row__grid">
                    <label className="attends">
                      <input
                        type="checkbox"
                        role="switch"
                        className="attends__input"
                        checked={!day.closed}
                        aria-checked={!day.closed}
                        aria-label={`Abierto: ${label}`}
                        onChange={(event) =>
                          patchDay(day.weekday, { closed: !event.target.checked })
                        }
                      />
                      <span className="attends__track" aria-hidden="true">
                        <span className="attends__thumb" />
                      </span>
                      <span className="attends__text">
                        {day.closed ? 'Cerrado' : 'Abierto'}
                      </span>
                    </label>
                    <div className="hours-row__times">
                      <div className="field">
                        <label htmlFor={`salon-open-${day.weekday}`}>Apertura</label>
                        <input
                          id={`salon-open-${day.weekday}`}
                          className="input"
                          type="time"
                          value={day.open}
                          disabled={day.closed}
                          onChange={(event) => patchDay(day.weekday, { open: event.target.value })}
                        />
                      </div>
                      <div className="field">
                        <label htmlFor={`salon-close-${day.weekday}`}>Cierre</label>
                        <input
                          id={`salon-close-${day.weekday}`}
                          className="input"
                          type="time"
                          value={day.close}
                          disabled={day.closed}
                          onChange={(event) => patchDay(day.weekday, { close: event.target.value })}
                        />
                      </div>
                    </div>
                    <div className="hours-row__copy">
                      <label className="visually-hidden" htmlFor={`salon-copy-${day.weekday}`}>
                        Copiar {label} a otros días
                      </label>
                      <select
                        id={`salon-copy-${day.weekday}`}
                        className="select"
                        value=""
                        onChange={(event) => handleCopy(day.weekday, event.target.value)}
                      >
                        <option value="">Copiar este día a…</option>
                        {copyTargets(day.weekday).map((target) => (
                          <option key={target.value} value={target.value}>
                            {target.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    {dayError ? (
                      <p className="field__error hours-row__error" role="alert">
                        {dayError}
                      </p>
                    ) : null}
                  </div>
                </fieldset>
              )
            })}
          </div>

          <div className="hours-card__actions">
            <button type="submit" className="btn btn--wine" disabled={saving}>
              {saving ? 'Guardando…' : 'Guardar horario'}
            </button>
            {custom ? (
              confirmReset ? (
                <span className="photo-confirm" role="group" aria-label="Confirmar volver al horario único">
                  <span>¿Volver al horario único? Se borran los días propios.</span>
                  <button
                    type="button"
                    className="btn btn--wine btn--sm"
                    disabled={saving}
                    onClick={() => void handleReset()}
                  >
                    Sí, volver
                  </button>
                  <button
                    type="button"
                    className="btn btn--quiet btn--sm"
                    disabled={saving}
                    onClick={() => setConfirmReset(false)}
                  >
                    Cancelar
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  className="btn btn--quiet"
                  disabled={saving}
                  onClick={() => setConfirmReset(true)}
                >
                  Volver al horario único
                </button>
              )
            ) : null}
          </div>
          <p className="field__hint">
            Los días cerrados se dibujan en la agenda y en la landing como «Cerrado». Las horas se
            leen en la zona horaria del salón; la semana empieza el lunes.
          </p>
        </form>
      ) : null}
    </section>
  )
}
