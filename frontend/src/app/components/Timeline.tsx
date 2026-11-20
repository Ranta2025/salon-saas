import {
  DEFAULT_WINDOW,
  formatHour,
  formatTimeOfDay,
  groupByProfessional,
  minutesFromMidnight,
  overlapLayout,
  positionInWindow,
} from '../../lib/datetime'
import type { Booking } from '../../types'

const HOUR_HEIGHT = 56

const STATUS_LABEL: Record<string, string> = {
  booked: 'Agendada',
  done: 'Atendida',
  cancelled: 'Cancelada',
  no_show: 'No asistió',
}

function statusLabel(status: string): string {
  return STATUS_LABEL[status] ?? status
}

type TimelineProps = {
  bookings: readonly Booking[]
}

type Block = {
  booking: Booking
  start: number
  end: number
  column: number
  columns: number
}

function layoutColumn(bookings: readonly Booking[]): Block[] {
  const measured = bookings.map((booking) => ({
    id: booking.id,
    booking,
    start: minutesFromMidnight(booking.starts_at),
    end: Math.max(minutesFromMidnight(booking.ends_at), minutesFromMidnight(booking.starts_at) + 15),
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
 * appointments on desktop, plus a plain agenda list for small screens.
 */
export function Timeline({ bookings }: TimelineProps) {
  const groups = groupByProfessional(bookings, [])
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
            const blocks = layoutColumn(group.bookings)
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
                    return (
                      <article
                        key={block.booking.id}
                        className="timeline__event"
                        data-status={block.booking.status}
                        style={{
                          top: offset * bodyHeight,
                          height: Math.max(duration * bodyHeight, 30),
                          left: `calc(${left}% + 2px)`,
                          width: `calc(${100 / block.columns}% - 6px)`,
                        }}
                        aria-label={`${formatTimeOfDay(block.booking.starts_at)} a ${formatTimeOfDay(
                          block.booking.ends_at,
                        )}, ${block.booking.client_name}, ${statusLabel(block.booking.status)}`}
                      >
                        <span className="timeline__event-time">
                          {formatTimeOfDay(block.booking.starts_at)}–
                          {formatTimeOfDay(block.booking.ends_at)}
                        </span>
                        <span className="timeline__event-client">{block.booking.client_name}</span>
                        <span className="timeline__event-status">
                          {statusLabel(block.booking.status)}
                        </span>
                      </article>
                    )
                  })}
                </div>
              </section>
            )
          })}
        </div>
      </div>

      <div className="agenda">
        {groups.map((group) => (
          <section className="agenda__group" key={`agenda-${group.id}`}>
            <h2 className="agenda__title">{group.name}</h2>
            <ul>
              {group.bookings.map((booking) => (
                <li key={booking.id} className="agenda__row" data-status={booking.status}>
                  <span className="agenda__time">
                    {formatTimeOfDay(booking.starts_at)}–{formatTimeOfDay(booking.ends_at)}
                  </span>
                  <span className="agenda__client">{booking.client_name}</span>
                  <span className="agenda__status">{statusLabel(booking.status)}</span>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </>
  )
}
