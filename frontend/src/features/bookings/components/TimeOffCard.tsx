import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { friendlyApiMessage } from '@/lib/api'
import { formatTimeOfDay, toDateInputValue } from '@/lib/datetime'
import type { CoverAbsence } from '../lib/coverAbsence'
import {
  conflictsNotice,
  createTimeOff,
  deleteTimeOff,
  formatTimeOffRange,
  TIME_OFF_MAX_REASON,
  timeOffForUser,
  upcomingAbsences,
  validateTimeOffDraft,
  type TimeOff,
  type TimeOffConflict,
} from '../lib/timeOff'
import type { SalonMember } from '@/lib/members'
import { AbsenceCover } from './AbsenceCover'

type TimeOffCardProps = {
  salonId: string
  member: SalonMember
  /** Every absence of the salon: the card keeps the ones of this person. */
  entries: readonly TimeOff[]
  /** Absences with their turns (`GET /salons/:id/absences`, S-F2d3-2). */
  coverAbsences: readonly CoverAbsence[]
  /** False when that request failed: the panel never reports "no turns". */
  coverReady: boolean
  /** Zone of the salon: the form converts its local dates with it. */
  timeZone: string
  now: Date
  /** Parent reloads the list of absences after a create or a delete. */
  onChanged: () => void
  /** Parent shows the message of a reassignment and reloads the turns. */
  onCovered: (message: string) => void
}

function conflictTime(conflict: TimeOffConflict, timeZone: string): string {
  return `${formatTimeOfDay(conflict.starts_at, timeZone)}–${formatTimeOfDay(
    conflict.ends_at,
    timeZone,
  )}`
}

/**
 * "Ausencias" block of one bookable member (contract S-F2d2-6): the absences in
 * progress and to come, the form of a new one (two dates, optional partial hours
 * and an optional reason) and the delete with confirmation. Rules live in
 * `src/lib/timeOff.ts`; the server sentences are shown verbatim.
 */
export function TimeOffCard({
  salonId,
  member,
  entries,
  coverAbsences,
  coverReady,
  timeZone,
  now,
  onChanged,
  onCovered,
}: TimeOffCardProps) {
  const [fromDate, setFromDate] = useState('')
  const [toDate, setToDate] = useState('')
  const [fromTime, setFromTime] = useState('')
  const [toTime, setToTime] = useState('')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [conflicts, setConflicts] = useState<TimeOffConflict[]>([])
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  // Absence whose "Cubrir" panel is open (S-F2d3-5).
  const [coveringId, setCoveringId] = useState<string | null>(null)

  const titleId = `member-timeoff-${member.id}`
  const mine = timeOffForUser(entries, member.id)
  const list = upcomingAbsences(mine, now)
  const today = toDateInputValue(now, timeZone)
  const conflictsText = conflictsNotice(conflicts.length)
  const firstConflict = conflicts[0]

  const resetForm = (): void => {
    setFromDate('')
    setToDate('')
    setFromTime('')
    setToTime('')
    setReason('')
  }

  const handleCreate = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (saving) {
      return
    }
    setFormError(null)
    setError(null)
    setNotice(null)
    setConflicts([])
    const check = validateTimeOffDraft({
      draft: { fromDate, toDate, fromTime, toTime, reason },
      timeZone,
      existing: mine,
      now,
    })
    if (!check.ok) {
      setFormError(check.message)
      return
    }
    setSaving(true)
    try {
      const created = await createTimeOff(salonId, member.id, check.value)
      setConflicts(created.conflicts)
      setNotice(
        created.conflicts.length > 0
          ? 'Ausencia cargada. Revisá los turnos que caen en ese período.'
          : 'Ausencia cargada.',
      )
      resetForm()
      onChanged()
    } catch (err: unknown) {
      setError(friendlyApiMessage(err, `No pudimos cargar la ausencia de ${member.name}.`))
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (entry: TimeOff): Promise<void> => {
    setError(null)
    setNotice(null)
    setDeletingId(entry.id)
    try {
      await deleteTimeOff(salonId, entry.id)
      setConfirmId(null)
      setNotice('Ausencia borrada.')
      onChanged()
    } catch (err: unknown) {
      setError(friendlyApiMessage(err, 'No pudimos borrar la ausencia.'))
    } finally {
      setDeletingId(null)
    }
  }

  return (
    <article className="panel-card hours-card timeoff-card" aria-labelledby={titleId}>
      <div className="work-card__head">
        <h3 id={titleId}>{member.name}</h3>
        <span className="pill pill--on">
          {list.length === 0
            ? 'Sin ausencias'
            : list.length === 1
              ? '1 ausencia'
              : `${list.length} ausencias`}
        </span>
      </div>

      {list.length === 0 ? (
        <p className="field__hint">
          No tiene ausencias cargadas de hoy en adelante.
        </p>
      ) : (
        <ul className="timeoff-list">
          {list.map((entry) => {
            const running = Date.parse(entry.starts_at) <= now.getTime()
            // The cover payload is the authority on the turns of this absence; an
            // absence without it has nothing to reassign, never invented turns.
            const cover: CoverAbsence = coverAbsences.find((row) => row.id === entry.id) ?? {
              id: entry.id,
              user_id: member.id,
              user_name: member.name,
              starts_at: entry.starts_at,
              ends_at: entry.ends_at,
              reason: entry.reason,
              bookings: [],
            }
            const covering = coveringId === entry.id
            return (
              <li className="timeoff-item" key={entry.id} data-running={running}>
                <span className="timeoff-item__range">
                  {formatTimeOffRange(entry, { timeZone, today })}
                  {running ? (
                    <>
                      {' '}
                      <span className="timeoff-item__tag">En curso</span>
                    </>
                  ) : null}
                </span>
                {entry.reason ? (
                  <span className="timeoff-item__reason">Motivo: {entry.reason}</span>
                ) : null}
                {confirmId === entry.id ? (
                  <span className="photo-confirm" role="group" aria-label="Confirmar borrado">
                    <span>¿Borrar la ausencia?</span>
                    <button
                      type="button"
                      className="btn btn--wine btn--sm"
                      disabled={deletingId === entry.id}
                      onClick={() => void handleDelete(entry)}
                    >
                      Sí, borrar
                    </button>
                    <button
                      type="button"
                      className="btn btn--quiet btn--sm"
                      onClick={() => setConfirmId(null)}
                    >
                      Volver
                    </button>
                  </span>
                ) : (
                  <span className="timeoff-item__buttons">
                    <button
                      type="button"
                      className="btn btn--quiet btn--sm timeoff-item__action"
                      aria-expanded={covering}
                      aria-controls={`cover-panel-${entry.id}`}
                      onClick={() => setCoveringId(covering ? null : entry.id)}
                    >
                      Cubrir
                    </button>
                    <button
                      type="button"
                      className="btn btn--quiet btn--sm timeoff-item__action"
                      aria-label={`Borrar la ausencia de ${member.name}: ${formatTimeOffRange(entry, {
                        timeZone,
                        today,
                      })}`}
                      onClick={() => setConfirmId(entry.id)}
                    >
                      Borrar
                    </button>
                  </span>
                )}
                {covering ? (
                  <div className="timeoff-item__cover" id={`cover-panel-${entry.id}`}>
                    <AbsenceCover
                      salonId={salonId}
                      absence={cover}
                      absences={entries}
                      ready={coverReady}
                      timeZone={timeZone}
                      onCovered={onCovered}
                    />
                  </div>
                ) : null}
              </li>
            )
          })}
        </ul>
      )}

      <form className="timeoff-form" onSubmit={handleCreate} noValidate>
        <h4 className="timeoff-form__title">Cargar una ausencia</h4>
        <div className="timeoff-form__grid">
          <div className="field">
            <label htmlFor={`${titleId}-from`}>Desde</label>
            <input
              id={`${titleId}-from`}
              className="input"
              type="date"
              value={fromDate}
              onChange={(event) => setFromDate(event.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor={`${titleId}-to`}>Hasta</label>
            <input
              id={`${titleId}-to`}
              className="input"
              type="date"
              value={toDate}
              onChange={(event) => setToDate(event.target.value)}
            />
          </div>
        </div>
        <div className="timeoff-form__grid">
          <div className="field">
            <label htmlFor={`${titleId}-from-time`}>Hora de inicio (opcional)</label>
            <input
              id={`${titleId}-from-time`}
              className="input"
              type="time"
              value={fromTime}
              aria-describedby={`${titleId}-hours-hint`}
              onChange={(event) => setFromTime(event.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor={`${titleId}-to-time`}>Hora de fin (opcional)</label>
            <input
              id={`${titleId}-to-time`}
              className="input"
              type="time"
              value={toTime}
              onChange={(event) => setToTime(event.target.value)}
            />
          </div>
        </div>
        <p className="field__hint" id={`${titleId}-hours-hint`}>
          Sin horas la ausencia cubre los días completos, desde las 00:00 del primer día hasta las
          00:00 del día siguiente al último.
        </p>
        <div className="field">
          <label htmlFor={`${titleId}-reason`}>Motivo (opcional)</label>
          <input
            id={`${titleId}-reason`}
            className="input"
            value={reason}
            maxLength={TIME_OFF_MAX_REASON}
            placeholder="Vacaciones, médico, trámite…"
            onChange={(event) => setReason(event.target.value)}
          />
          <p className="field__hint">
            Lo ve la administración; una profesional solo ve el motivo de sus propias ausencias.
          </p>
        </div>

        {formError ? (
          <p className="field__error" role="alert">
            {formError}
          </p>
        ) : null}
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

        <div className="timeoff-form__actions">
          <button type="submit" className="btn btn--wine btn--sm" disabled={saving}>
            {saving ? 'Cargando…' : 'Cargar ausencia'}
          </button>
        </div>
      </form>

      {conflictsText ? (
        <div className="timeoff-conflicts" role="status">
          <p className="timeoff-conflicts__text">{conflictsText}</p>
          <ul className="timeoff-conflicts__list">
            {conflicts.map((conflict) => (
              <li key={conflict.id}>
                {conflictTime(conflict, timeZone)} · {conflict.client_name || 'Cliente sin nombre'}
              </li>
            ))}
          </ul>
          {firstConflict ? (
            <Link
              className="btn btn--quiet btn--sm"
              to={`/panel/calendario?date=${toDateInputValue(new Date(firstConflict.starts_at), timeZone)}`}
            >
              Ver el calendario de ese día
            </Link>
          ) : null}
        </div>
      ) : null}
    </article>
  )
}
