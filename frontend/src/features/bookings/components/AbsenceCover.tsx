import { useState } from 'react'
import { describeApiError } from '@/lib/api'
import {
  affectedTurnsLabel,
  beginCover,
  chooseFor,
  chooseForAll,
  chosenFor,
  coverAllCandidates,
  coverCandidates,
  firstEligible,
  forgetChoice,
  reassignAbsence,
  reassignBooking,
  reassignedNotice,
  type CoverAbsence,
  type CoverBooking,
  type CoverDraft,
} from '../lib/coverAbsence'
import { formatTimeOfDay } from '@/lib/datetime'
import type { TimeOff } from '../lib/timeOff'
import { CoverSelect } from './CoverSelect'

type AbsenceCoverProps = {
  salonId: string
  /** The absence as `GET /salons/:id/absences` describes it (S-F2d3-2). */
  absence: CoverAbsence
  /** Absences of the salon: a person away that day cannot take the turn. */
  absences: readonly TimeOff[]
  /** False when `GET /absences` failed: an unknown count is never printed as 0. */
  ready?: boolean
  timeZone: string
  /** Parent shows the message and reloads the turns of the absence. */
  onCovered: (message: string) => void
}

function bookingTime(booking: CoverBooking, timeZone: string): string {
  return `${formatTimeOfDay(booking.starts_at, timeZone)}–${formatTimeOfDay(booking.ends_at, timeZone)}`
}

/**
 * "Cubrir" of one absence (S-F2d3-5): the turns it leaves without a professional,
 * one selector per turn with who can take it — and why not the rest — plus
 * "Reasignar todo" at the top for the whole absence. The choice is applied to the
 * select before the answer and comes back to the saved one if the server rejects
 * it, showing its sentence verbatim.
 */
export function AbsenceCover({
  salonId,
  absence,
  absences,
  ready = true,
  timeZone,
  onCovered,
}: AbsenceCoverProps) {
  const [draft, setDraft] = useState<CoverDraft>(() => beginCover())
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [bulk, setBulk] = useState(
    () => firstEligible(coverAllCandidates({ absence, absences }))?.id ?? '',
  )

  const allChoices = coverAllCandidates({ absence, absences })
  const choicesFor = (booking: CoverBooking) => coverCandidates({ booking, absence, absences })
  const nameOf = (professionalId: string): string =>
    allChoices.find((choice) => choice.id === professionalId)?.name ??
    absence.bookings
      .flatMap((booking) => booking.candidates)
      .find((row) => row.id === professionalId)?.name ??
    'esa profesional'

  const reassignOne = async (booking: CoverBooking): Promise<void> => {
    const professionalId =
      chosenFor(draft, booking.id) || firstEligible(choicesFor(booking))?.id || ''
    if (professionalId === '' || pending !== null) {
      return
    }
    setPending(booking.id)
    setError(null)
    setDraft(chooseFor(draft, booking.id, professionalId))
    try {
      const outcome = await reassignBooking(salonId, booking.id, professionalId)
      onCovered(reassignedNotice(outcome.reassigned || 1, nameOf(professionalId)))
    } catch (err: unknown) {
      // Only this turn goes back to what the server has.
      setDraft(forgetChoice(draft, booking.id))
      setError(describeApiError(err, 'No pudimos reasignar el turno.').message)
    } finally {
      setPending(null)
    }
  }

  const reassignAll = async (): Promise<void> => {
    if (bulk === '' || pending !== null) {
      return
    }
    setPending('all')
    setError(null)
    try {
      const outcome = await reassignAbsence(salonId, absence.id, bulk)
      onCovered(reassignedNotice(outcome.reassigned, nameOf(bulk)))
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos reasignar los turnos de la ausencia.').message)
    } finally {
      setPending(null)
    }
  }

  return (
    <div className="cover-turns">
      {ready ? (
        <p className="cover-turns__count">{affectedTurnsLabel(absence.bookings.length)}</p>
      ) : (
        <p className="field__hint">No pudimos cargar los turnos de esta ausencia.</p>
      )}

      {!ready ? null : absence.bookings.length === 0 ? (
        <p className="field__hint">Esta ausencia no tiene turnos agendados.</p>
      ) : (
        <>
          <div className="cover-turns__all">
            <CoverSelect
              id={`cover-all-${absence.id}`}
              label="Reasignar todo a"
              choices={allChoices}
              value={bulk}
              disabled={pending !== null}
              onChange={(value) => {
                setBulk(value)
                setDraft(
                  chooseForAll(
                    draft,
                    absence.bookings.map((booking) => booking.id),
                    value,
                  ),
                )
              }}
            />
            <button
              type="button"
              className="btn btn--wine btn--sm"
              disabled={bulk === '' || pending !== null}
              onClick={() => void reassignAll()}
            >
              {pending === 'all' ? 'Reasignando…' : 'Reasignar todo'}
            </button>
          </div>

          <ul className="cover-turns__list">
            {absence.bookings.map((booking) => {
              const choices = choicesFor(booking)
              const value = chosenFor(draft, booking.id) || firstEligible(choices)?.id || ''
              const busy = pending === booking.id
              return (
                <li className="cover-turn" key={booking.id}>
                  <span className="cover-turn__when">{bookingTime(booking, timeZone)}</span>
                  <span className="cover-turn__client">
                    {booking.client_name || 'Cliente sin nombre'}
                  </span>
                  <CoverSelect
                    id={`cover-turn-${absence.id}-${booking.id}`}
                    label={`Quién toma el turno de ${bookingTime(booking, timeZone)}`}
                    choices={choices}
                    value={value}
                    disabled={pending !== null}
                    onChange={(next) => setDraft(chooseFor(draft, booking.id, next))}
                  />
                  <button
                    type="button"
                    className="btn btn--quiet btn--sm cover-turn__action"
                    disabled={value === '' || pending !== null}
                    onClick={() => void reassignOne(booking)}
                  >
                    {busy ? 'Reasignando…' : 'Reasignar'}
                  </button>
                </li>
              )
            })}
          </ul>
        </>
      )}

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  )
}
