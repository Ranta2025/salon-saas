import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  BOOKING_STATUS_LABEL,
  availableActions,
  bookingDurationMinutes,
  canReschedule,
  describeBookingUpdateError,
  patchBooking,
  reschedulePayload,
  statusPayload,
  statusLabel,
  type BookingAction,
} from '../../lib/bookingActions'
import { formatTimeOfDay, toDateInputValue, toDateTimeIso } from '../../lib/datetime'
import type { StaffMember } from '../../lib/staff'
import type { Booking } from '../../types'
import { formatSlotRange } from '../../lib/publicBooking'

const ACTION_LABEL: Record<BookingAction, string> = {
  cancel: 'Cancelar turno',
  no_show: 'Marcar que no asistió',
  done: 'Marcar como atendida',
}

type BookingDetailProps = {
  booking: Booking
  staff: readonly StaffMember[]
  /** Message of a failed /staff request, shown next to the professional select. */
  staffError: string | null
  timeZone: string
  /** False for a professional looking at somebody else's booking. */
  canAct: boolean
  salonId: string
  onClose: () => void
  /** Called after a successful PATCH so the page reloads the day. */
  onUpdated: (message: string) => void
}

/**
 * Actions of one appointment: status badge, cancel (with confirmation), no-show,
 * done and a small reschedule form. Owner/admin change any booking; a
 * professional only their own (the page decides with `canAct`).
 */
export function BookingDetail({
  booking,
  staff,
  staffError,
  timeZone,
  canAct,
  salonId,
  onClose,
  onUpdated,
}: BookingDetailProps) {
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [confirmingCancel, setConfirmingCancel] = useState(false)
  const [dateValue, setDateValue] = useState(() => toDateInputValue(new Date(booking.starts_at), timeZone))
  const [timeValue, setTimeValue] = useState(() => formatTimeOfDay(booking.starts_at, timeZone))
  const [professionalId, setProfessionalId] = useState(booking.professional_id)
  const [rescheduleError, setRescheduleError] = useState<string | null>(null)

  // The panel replaces the focus target: announce it and move focus inside.
  useEffect(() => {
    headingRef.current?.focus()
  }, [booking.id])

  useEffect(() => {
    setDateValue(toDateInputValue(new Date(booking.starts_at), timeZone))
    setTimeValue(formatTimeOfDay(booking.starts_at, timeZone))
    setProfessionalId(booking.professional_id)
    setConfirmingCancel(false)
  }, [booking.id, booking.starts_at, booking.professional_id, timeZone])

  const runAction = async (action: BookingAction): Promise<void> => {
    setError(null)
    setPending(true)
    try {
      await patchBooking(salonId, booking.id, statusPayload(action))
      setConfirmingCancel(false)
      onUpdated(
        action === 'cancel'
          ? 'Turno cancelado. El horario vuelve a estar libre.'
          : action === 'done'
            ? 'Turno marcado como atendido.'
            : 'Turno marcado como no asistió.',
      )
    } catch (err: unknown) {
      setError(describeBookingUpdateError(err))
    } finally {
      setPending(false)
    }
  }

  const handleReschedule = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setRescheduleError(null)
    let startsAt: string
    try {
      startsAt = toDateTimeIso(dateValue, timeValue, timeZone)
    } catch (err: unknown) {
      setRescheduleError(
        err instanceof Error ? err.message : 'Elige una fecha y una hora válidas para mover el turno.',
      )
      return
    }
    const duration = bookingDurationMinutes(booking) || 30
    setPending(true)
    try {
      await patchBooking(
        salonId,
        booking.id,
        reschedulePayload(startsAt, duration, professionalId),
      )
      onUpdated('Turno reprogramado. La agenda ya muestra el nuevo horario.')
    } catch (err: unknown) {
      setRescheduleError(describeBookingUpdateError(err))
    } finally {
      setPending(false)
    }
  }

  const actions = availableActions(booking.status)

  return (
    <section className="panel-card booking-detail" aria-labelledby="booking-detail-title">
      <header className="booking-detail__head">
        <div>
          <p className="eyebrow">Turno</p>
          <h2 id="booking-detail-title" tabIndex={-1} ref={headingRef}>
            {booking.client_name || 'Cliente sin nombre'}
          </h2>
          <p className="booking-detail__when">
            {formatSlotRange(booking.starts_at, booking.ends_at, timeZone)}
          </p>
        </div>
        <div className="booking-detail__state">
          <span className="badge" data-status={booking.status}>
            {statusLabel(booking.status)}
          </span>
          <button type="button" className="btn btn--quiet btn--sm" onClick={onClose}>
            Cerrar
          </button>
        </div>
      </header>

      {!canAct ? (
        <p className="notice notice--info">
          Solo la administración del salón o la profesional asignada puede cambiar este turno.
        </p>
      ) : null}

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}

      {canAct && actions.length > 0 ? (
        <div className="booking-detail__actions" role="group" aria-label="Acciones del turno">
          {actions.map((action) =>
            action === 'cancel' ? (
              confirmingCancel ? (
                <div className="booking-detail__confirm" role="group" aria-label="Confirmar cancelación">
                  <p>¿Cancelar la cita de {booking.client_name || 'esta persona'}?</p>
                  <button
                    type="button"
                    className="btn btn--wine btn--sm"
                    disabled={pending}
                    onClick={() => void runAction('cancel')}
                  >
                    Sí, cancelar
                  </button>
                  <button
                    type="button"
                    className="btn btn--quiet btn--sm"
                    onClick={() => setConfirmingCancel(false)}
                  >
                    Volver
                  </button>
                </div>
              ) : (
                <button
                  key={action}
                  type="button"
                  className="btn btn--quiet btn--sm"
                  disabled={pending}
                  onClick={() => setConfirmingCancel(true)}
                >
                  {ACTION_LABEL[action]}
                </button>
              )
            ) : (
              <button
                key={action}
                type="button"
                className="btn btn--quiet btn--sm"
                disabled={pending}
                onClick={() => void runAction(action)}
              >
                {ACTION_LABEL[action]}
              </button>
            ),
          )}
        </div>
      ) : null}

      {canAct && canReschedule(booking.status) ? (
        <form className="booking-detail__reschedule" onSubmit={handleReschedule} noValidate>
          <h3>Reprogramar</h3>
          <p className="booking-detail__hint">
            El turno dura {bookingDurationMinutes(booking) || 30} minutos y se mueve a la zona
            horaria del salón ({timeZone}).
          </p>
          <div className="booking-detail__fields">
            <div className="field">
              <label htmlFor="reschedule-date">Nueva fecha</label>
              <input
                id="reschedule-date"
                className="input"
                type="date"
                value={dateValue}
                min={toDateInputValue(new Date())}
                onChange={(event) => setDateValue(event.target.value)}
                required
              />
            </div>
            <div className="field">
              <label htmlFor="reschedule-time">Nueva hora</label>
              <input
                id="reschedule-time"
                className="input"
                type="time"
                value={timeValue}
                step={300}
                onChange={(event) => setTimeValue(event.target.value)}
                required
              />
            </div>
            <div className="field">
              <label htmlFor="reschedule-professional">Profesional</label>
              <select
                id="reschedule-professional"
                className="select"
                value={professionalId}
                onChange={(event) => setProfessionalId(event.target.value)}
                aria-describedby={staffError ? 'reschedule-staff-error' : undefined}
              >
                {staff.length === 0 && professionalId !== '' ? (
                  <option value={professionalId}>Profesional actual</option>
                ) : null}
                {staff.map((member) => (
                  <option key={member.id} value={member.id}>
                    {member.name}
                  </option>
                ))}
              </select>
              {staffError ? (
                <p className="field__error" id="reschedule-staff-error" role="alert">
                  {staffError}
                </p>
              ) : null}
            </div>
          </div>
          {rescheduleError ? (
            <p className="notice notice--error" role="alert">
              {rescheduleError}
            </p>
          ) : null}
          <button type="submit" className="btn btn--wine btn--sm" disabled={pending}>
            {pending ? 'Guardando…' : 'Reprogramar turno'}
          </button>
        </form>
      ) : null}

      {canAct && !canReschedule(booking.status) ? (
        <p className="booking-detail__hint">
          Los turnos cancelados no se pueden reprogramar: crea una reserva nueva.
        </p>
      ) : null}

      <p className="booking-detail__hint">
        Estado actual: {BOOKING_STATUS_LABEL[booking.status] ?? booking.status}. El calendario se
        actualiza solo cuando el salón recibe un cambio.
      </p>
    </section>
  )
}
