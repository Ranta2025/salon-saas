import {
  DEFAULT_WINDOW,
  formatHour,
  formatTimeOfDay,
  groupByProfessional,
  minutesFromMidnight,
  overlapLayout,
  positionInWindow,
  TIME_ZONE,
  type ProfessionalRef,
} from '../../lib/datetime'
import { statusLabel } from '../../lib/bookingActions'
import type { Booking } from '../../types'

const HOUR_HEIGHT = 56

type TimelineProps = {
  bookings: readonly Booking[]
  /** Team of the salon: the column headings use their names. */
  professionals?: readonly ProfessionalRef[]
  /** Zone the calendar is drawn in (the salon time zone). */
  timeZone?: string
  /** Opens the actions panel of a booking. */
  onSelect?: (booking: Booking) => void
  /** Id of the booking whose actions panel is open. */
  selectedId?: string | null
}

type Block = {
  booking: Booking
  start: number
  end: number
  column: number
  columns: number
}

function layoutColumn(bookings: readonly Booking[], timeZone: string): Block[] {
  const measured = bookings.map((booking) => ({
    id: booking.id,
    booking,
    start: minutesFromMidnight(booking.starts_at, timeZone),
    end: Math.max(
      minutesFromMidnight(booking.ends_at, timeZone),
      minutesFromMidnight(booking.starts_at, timeZone) + 15,
    ),
  }))
  const positioned = overlapLayout(measured)
  return positioned.map((block) => ({
    booking: block.booking,
    start: block.start,
    end: block.end,
    column: block.column,
    columns: block.columns,
  }))
}

/**
 * Day calendar: one column per professional with absolutely positioned
 * appointments on desktop, plus a plain agenda list for small screens. Every
 * appointment is a button that opens its actions (status, cancel, no-show,
 * done, reschedule).
 */
export function Timeline({
  bookings,
  professionals = [],
  timeZone = TIME_ZONE,
  onSelect,
  selectedId = null,
}: TimelineProps) {
  const groups = groupByProfessional(bookings, professionals)
  const hours = Array.from(
    { length: DEFAULT_WINDOW.endHour - DEFAULT_WINDOW.startHour + 1 },
    (_, index) => DEFAULT_WINDOW.startHour + index,
  )
  const bodyHeight = (DEFAULT_WINDOW.endHour - DEFAULT_WINDOW.startHour) * HOUR_HEIGHT

  if (bookings.length === 0) {
    return (
      <p className="empty-state">
        No hay citas agendadas para este día. Las reservas nuevas aparecen aquí.
      </p>
    )
  }

  const select = (booking: Booking) => (): void => onSelect?.(booking)

  return (
    <>
      <div className="timeline" role="group" aria-label="Agenda del día por profesional">
        <div className="timeline__ruler" aria-hidden="true">
          <span className="timeline__ruler-spacer" />
          {hours.map((hour) => (
            <span key={hour} className="timeline__ruler-hour" style={{ height: HOUR_HEIGHT }}>
              {formatHour(hour)}
            </span>
          ))}
        </div>

        <div className="timeline__columns">
          {groups.map((group) => {
            const blocks = layoutColumn(group.bookings, timeZone)
            return (
              <section className="timeline__column" key={group.id}>
                <header className="timeline__column-head">
                  <h2>{group.name}</h2>
                  <span>{group.bookings.length} citas</span>
                </header>
                <div className="timeline__body" style={{ height: bodyHeight }}>
                  {hours.map((hour) => (
                    <div
                      key={hour}
                      className="timeline__line"
                      style={{ top: (hour - DEFAULT_WINDOW.startHour) * HOUR_HEIGHT }}
                      aria-hidden="true"
                    />
                  ))}
                  {blocks.map((block) => {
                    const { offset, duration } = positionInWindow(block.start, block.end)
                    const left = (block.column / block.columns) * 100
                    const startsAt = formatTimeOfDay(block.booking.starts_at, timeZone)
                    const endsAt = formatTimeOfDay(block.booking.ends_at, timeZone)
                    return (
                      <button
                        key={block.booking.id}
                        type="button"
                        className="timeline__event"
                        data-status={block.booking.status}
                        data-selected={block.booking.id === selectedId}
                        style={{
                          top: offset * bodyHeight,
                          height: Math.max(duration * bodyHeight, 44),
                          left: `calc(${left}% + 2px)`,
                          width: `calc(${100 / block.columns}% - 6px)`,
                        }}
                        onClick={select(block.booking)}
                        aria-label={`${startsAt} a ${endsAt}, ${block.booking.client_name}, ${statusLabel(
                          block.booking.status,
                        )}. Abrir acciones`}
                      >
                        <span className="timeline__event-head">
                          <span className="timeline__event-time">
                            {startsAt}–{endsAt}
                          </span>
                          <span className="timeline__badge" data-status={block.booking.status}>
                            {statusLabel(block.booking.status)}
                          </span>
                        </span>
                        <span className="timeline__event-client">{block.booking.client_name}</span>
                      </button>
                    )
                  })}
                </div>
              </section>
            )
          })}
        </div>
      </div>

      <div className="agenda">
        {groups.filter((group) => group.bookings.length > 0).map((group) => (
          <section className="agenda__group" key={`agenda-${group.id}`}>
            <h2 className="agenda__title">{group.name}</h2>
            <ul>
              {group.bookings.map((booking) => (
                <li key={booking.id} className="agenda__row" data-status={booking.status}>
                  <span className="agenda__time">
                    {formatTimeOfDay(booking.starts_at, timeZone)}–
                    {formatTimeOfDay(booking.ends_at, timeZone)}
                  </span>
                  <span className="agenda__client">{booking.client_name}</span>
                  <span className="agenda__status">
                    <span className="agenda__badge" data-status={booking.status}>
                      {statusLabel(booking.status)}
                    </span>
                    {onSelect ? (
                      <button
                        type="button"
                        className="btn btn--quiet btn--sm agenda__actions"
                        onClick={select(booking)}
                        aria-label={`Acciones del turno de ${booking.client_name}`}
                      >
                        Acciones
                      </button>
                    ) : null}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </>
  )
}
