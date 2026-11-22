import { useCallback, useEffect, useState } from 'react'
import { describeApiError } from '../../lib/api'
import { fetchMemberHours, saveMemberHours, type SalonMember } from '../../lib/members'
import {
  beginSave,
  breakLabel,
  commitSave,
  copySegmentsDay,
  copyTargets,
  dayBreaks,
  daySummary,
  formatClock,
  MAX_SEGMENTS_PER_DAY,
  parseClock,
  removeSegment,
  rollbackSave,
  sameSegments,
  salonSegments,
  segmentsForDay,
  setDaySegments,
  validateSegments,
  WEEKDAY_LABELS,
  WEEKDAYS,
  weekSummary,
  type DaySegment,
  type OptimisticSave,
  type SalonDay,
  type Weekday,
} from '../../lib/weeklyHours'

type MemberHoursCardProps = {
  salonId: string
  member: SalonMember
  /** Weekly hours of the salon: the default and the reference of the card. */
  salonDays: readonly SalonDay[]
}

type HoursDraft = { custom: boolean; segments: DaySegment[] }

function sameDraft(a: HoursDraft, b: HoursDraft): boolean {
  return a.custom === b.custom && sameSegments(a.segments, b.segments)
}

/**
 * "Horario" block of one bookable member (contract S-F2d1-6): seven days, up to
 * four segments each, "Igual que el salón" by default, copy of one day to the
 * others, the gaps between segments shown as breaks and the server sentences
 * verbatim. Saving is optimistic with an exact roll-back.
 */
export function MemberHoursCard({ salonId, member, salonDays }: MemberHoursCardProps) {
  const [state, setState] = useState<OptimisticSave<HoursDraft>>({
    value: { custom: false, segments: [] },
    saved: { custom: false, segments: [] },
  })
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [fieldError, setFieldError] = useState<{ weekday: number; message: string } | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    let active = true
    setLoading(true)
    setLoadError(null)
    fetchMemberHours(salonId, member.id)
      .then((hours) => {
        if (active) {
          const loaded: HoursDraft = { custom: hours.custom, segments: hours.segments }
          setState({ value: loaded, saved: loaded })
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setLoadError(
            describeApiError(err, `No pudimos cargar el horario de ${member.name}.`).message,
          )
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
  }, [salonId, member.id, member.name, reloadKey])

  const draft = state.value
  const dirty = !sameDraft(draft, state.saved)
  const titleId = `member-hours-${member.id}`
  const salonSummary = salonDays.length > 0 ? weekSummary(salonDays) : null

  const update = (next: HoursDraft): void => {
    setState((current) => ({ ...current, value: next }))
    setFieldError(null)
    setError(null)
    setNotice(null)
  }

  const setSegments = (segments: DaySegment[]): void => {
    update({ custom: true, segments })
  }

  const switchMode = (custom: boolean): void => {
    if (custom) {
      // Own hours start as a copy of the salon week, never as an empty one.
      const segments = draft.segments.length > 0 ? draft.segments : salonSegments(salonDays)
      update({ custom: true, segments })
      return
    }
    update({ custom: false, segments: [] })
  }

  const editSegment = (
    weekday: Weekday,
    index: number,
    patch: Partial<Pick<DaySegment, 'start' | 'end'>>,
  ): void => {
    const day = segmentsForDay(draft.segments, weekday).map((row, position) =>
      position === index ? { ...row, ...patch } : row,
    )
    setSegments(setDaySegments(draft.segments, weekday, day))
  }

  const addRow = (weekday: Weekday): void => {
    const day = segmentsForDay(draft.segments, weekday)
    const salonDay = salonDays.find((row) => row.weekday === weekday)
    const last = day[day.length - 1]
    const fallback = last
      ? { start: last.end, end: formatClock((parseClock(last.end) ?? 0) + 60) }
      : salonDay
        ? { start: salonDay.open, end: salonDay.close }
        : { start: '09:00', end: '13:00' }
    setSegments(setDaySegments(draft.segments, weekday, [...day, fallback]))
  }

  const removeRow = (weekday: Weekday, index: number): void => {
    setSegments(removeSegment(draft.segments, weekday, index))
  }

  const handleCopy = (weekday: Weekday, value: string): void => {
    const target = copyTargets(weekday).find((option) => option.value === value)
    if (!target) {
      return
    }
    setSegments(copySegmentsDay(draft.segments, weekday, target.weekdays))
    setNotice(
      `${WEEKDAY_LABELS[weekday]} copiado a ${target.weekdays
        .map((day) => WEEKDAY_LABELS[day].toLowerCase())
        .join(', ')}.`,
    )
  }

  const save = async (): Promise<void> => {
    if (saving) {
      return
    }
    setError(null)
    setNotice(null)
    setFieldError(null)
    const validation = validateSegments(draft.custom ? draft.segments : [])
    if (!validation.ok) {
      setFieldError({ weekday: validation.weekday, message: validation.message })
      return
    }
    const started = beginSave(state.saved, draft)
    setState(started)
    setSaving(true)
    try {
      const stored = await saveMemberHours(salonId, member.id, validation.value)
      const saved: HoursDraft = { custom: stored.custom, segments: stored.segments }
      setState(commitSave(started, saved))
      setNotice('Horario guardado.')
    } catch (err: unknown) {
      setState(rollbackSave(started))
      setError(describeApiError(err, `No pudimos guardar el horario de ${member.name}.`).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <article className="panel-card hours-card member-hours" aria-labelledby={titleId}>
      <div className="work-card__head">
        <h3 id={titleId}>{member.name}</h3>
        <span className="pill pill--on">
          {draft.custom ? 'Horario propio' : 'Igual que el salón'}
        </span>
      </div>

      {loadError ? (
        <p className="notice notice--error" role="alert">
          {loadError}
          <button type="button" className="btn btn--quiet btn--sm hours-card__retry" onClick={reload}>
            Reintentar
          </button>
        </p>
      ) : null}
      {loading && loadError === null ? (
        <p className="field__hint" role="status">
          Cargando horario…
        </p>
      ) : null}

      {loadError === null && !loading ? (
        <>
          <fieldset className="hours-mode">
            <legend>Horario de trabajo</legend>
            <label className="hours-mode__option">
              <input
                type="radio"
                name={`hours-mode-${member.id}`}
                checked={!draft.custom}
                onChange={() => switchMode(false)}
              />
              <span>Igual que el salón</span>
            </label>
            <label className="hours-mode__option">
              <input
                type="radio"
                name={`hours-mode-${member.id}`}
                checked={draft.custom}
                onChange={() => switchMode(true)}
              />
              <span>Horario propio</span>
            </label>
          </fieldset>

          {draft.custom ? (
            <div className="hours-days">
              {WEEKDAYS.map((weekday) => {
                const day = segmentsForDay(draft.segments, weekday)
                const breaks = dayBreaks(draft.segments, weekday)
                const label = WEEKDAY_LABELS[weekday]
                const dayError = fieldError?.weekday === weekday ? fieldError.message : null
                return (
                  <section
                    key={weekday}
                    className="hours-day"
                    aria-labelledby={`${titleId}-day-${weekday}`}
                  >
                    <div className="hours-day__head">
                      <h4 id={`${titleId}-day-${weekday}`}>{label}</h4>
                      <span className="hours-day__summary">
                        {daySummary(draft.segments, weekday)}
                      </span>
                    </div>

                    {day.map((row, index) => {
                      const gap = breaks.find((item) => item.start === row.end)
                      return (
                        <div key={`${weekday}-${index}`} className="hours-day__block">
                          <div className="hours-segment">
                            <div className="field">
                              <label htmlFor={`${titleId}-${weekday}-${index}-start`}>
                                Desde
                              </label>
                              <input
                                id={`${titleId}-${weekday}-${index}-start`}
                                className="input"
                                type="time"
                                value={row.start}
                                onChange={(event) =>
                                  editSegment(weekday, index, { start: event.target.value })
                                }
                              />
                            </div>
                            <div className="field">
                              <label htmlFor={`${titleId}-${weekday}-${index}-end`}>Hasta</label>
                              <input
                                id={`${titleId}-${weekday}-${index}-end`}
                                className="input"
                                type="time"
                                value={row.end}
                                onChange={(event) =>
                                  editSegment(weekday, index, { end: event.target.value })
                                }
                              />
                            </div>
                            <button
                              type="button"
                              className="btn btn--quiet btn--sm"
                              aria-label={`Quitar el tramo de ${label.toLowerCase()} de ${row.start} a ${row.end}`}
                              onClick={() => removeRow(weekday, index)}
                            >
                              Quitar
                            </button>
                          </div>
                          {gap ? <p className="hours-break">{breakLabel(gap)}</p> : null}
                        </div>
                      )
                    })}

                    <div className="hours-day__actions">
                      <button
                        type="button"
                        className="btn btn--quiet btn--sm"
                        disabled={day.length >= MAX_SEGMENTS_PER_DAY}
                        onClick={() => addRow(weekday)}
                      >
                        Agregar tramo
                      </button>
                      <span className="field__hint">
                        {day.length === 0
                          ? 'Día libre'
                          : `${day.length} de ${MAX_SEGMENTS_PER_DAY} tramos`}
                      </span>
                      <label className="visually-hidden" htmlFor={`${titleId}-copy-${weekday}`}>
                        Copiar {label} a otros días
                      </label>
                      <select
                        id={`${titleId}-copy-${weekday}`}
                        className="select hours-day__copy"
                        value=""
                        onChange={(event) => handleCopy(weekday, event.target.value)}
                      >
                        <option value="">Copiar este día a…</option>
                        {copyTargets(weekday).map((target) => (
                          <option key={target.value} value={target.value}>
                            {target.label}
                          </option>
                        ))}
                      </select>
                    </div>
                    {dayError ? (
                      <p className="field__error" role="alert">
                        {dayError}
                      </p>
                    ) : null}
                  </section>
                )
              })}
            </div>
          ) : (
            <p className="field__hint">
              {salonSummary === null
                ? 'Sigue el horario del salón de Ajustes.'
                : `Sigue el horario del salón: ${salonSummary}.`}{' '}
              Elegí «Horario propio» para tener días y descansos distintos.
            </p>
          )}

          {error ? (
            <p className="notice notice--error" role="alert">
              {error}
            </p>
          ) : null}
          {notice ? (
            <p className="notice notice--success" role="status">
              {notice}
            </p>
          ) : null}

          <div className="hours-card__actions">
            <button
              type="button"
              className="btn btn--wine"
              disabled={saving || !dirty}
              onClick={() => void save()}
            >
              {saving ? 'Guardando…' : 'Guardar horario'}
            </button>
            <button
              type="button"
              className="btn btn--quiet"
              disabled={saving || !dirty}
              onClick={() => {
                setState((current) => ({ value: current.saved, saved: current.saved }))
                setError(null)
                setNotice(null)
                setFieldError(null)
              }}
            >
              Descartar cambios
            </button>
          </div>
        </>
      ) : null}
    </article>
  )
}
