import { useCallback, useEffect, useState } from 'react'
import { fetchBookings } from '../lib/api'
import {
  addDaysToDateValue,
  dayRange,
  fromDateInputValue,
  toDateInputValue,
} from '../lib/datetime'
import type { Booking } from '../types'
import { useRealtime } from './RealtimeContext'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { Timeline } from './components/Timeline'

export function CalendarPage() {
  const { salonId } = useSalon()
  const { subscribe } = useRealtime()
  const [dateValue, setDateValue] = useState(() => toDateInputValue(new Date()))
  const [bookings, setBookings] = useState<Booking[]>([])
  const [loadedDay, setLoadedDay] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // A booking created or changed anywhere refreshes the visible day.
  useEffect(() => {
    const offCreated = subscribe('booking.created', () => reload())
    const offUpdated = subscribe('booking.updated', () => reload())
    return () => {
      offCreated()
      offUpdated()
    }
  }, [subscribe, reload])

  useEffect(() => {
    if (!salonId) {
      return
    }
    const { from, to } = dayRange(new Date(fromDateInputValue(dateValue)))
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    fetchBookings(salonId, from, to, controller.signal)
      .then((list) => {
        setBookings(list)
        setLoadedDay(dateValue)
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setError(err instanceof Error ? err.message : 'No pudimos cargar la agenda.')
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoading(false)
        }
      })
    return () => controller.abort()
  }, [salonId, dateValue, reloadKey])

  const isToday = dateValue === toDateInputValue(new Date())
  // While another day loads, do not show the appointments of the previous one.
  const agendaReady = loadedDay === dateValue

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Agenda" title="Calendario del día" />
        <SalonRequired />
      </>
    )
  }

  return (
    <>
      <PageHeader
        eyebrow="Agenda"
        title={isToday ? 'Calendario de hoy' : 'Calendario del día'}
        description="Las citas se consultan con el rango del día en la zona horaria del salón."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      <div className="day-nav" role="group" aria-label="Elegir día">
        <button
          type="button"
          className="btn btn--quiet btn--sm"
          onClick={() => setDateValue((value) => addDaysToDateValue(value, -1))}
        >
          ← Día anterior
        </button>
        <label className="day-nav__field">
          <span className="visually-hidden">Fecha</span>
          <input
            className="input"
            type="date"
            value={dateValue}
            onChange={(event) => setDateValue(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="btn btn--quiet btn--sm"
          onClick={() => setDateValue((value) => addDaysToDateValue(value, 1))}
        >
          Día siguiente →
        </button>
        {!isToday ? (
          <button
            type="button"
            className="btn btn--wine btn--sm"
            onClick={() => setDateValue(toDateInputValue(new Date()))}
          >
            Hoy
          </button>
        ) : null}
      </div>

      {loading ? (
        <LoadingState label={agendaReady ? 'Actualizando agenda…' : 'Cargando agenda…'} />
      ) : null}
      {error ? <ErrorNotice message={error} onRetry={reload} /> : null}
      {agendaReady && !error ? <Timeline bookings={bookings} /> : null}
    </>
  )
}
