import { useCallback, useEffect, useState } from 'react'
import { fetchBookings, fetchStaff } from '../lib/api'
import { activeRole, canManageBooking } from '../lib/permissions'
import { timeZoneLabel } from '../lib/publicBooking'
import { fetchSettings } from '../lib/settings'
import {
  TIME_ZONE,
  addDaysToDateValue,
  dayRange,
  fromDateInputValue,
  toDateInputValue,
} from '../lib/datetime'
import type { StaffMember } from '../lib/staff'
import type { Booking } from '../types'
import { useRealtime } from './RealtimeContext'
import { useSalon } from './SalonContext'
import { BookingDetail } from './components/BookingDetail'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { Timeline } from './components/Timeline'

export function CalendarPage() {
  const { salonId, salons, user } = useSalon()
  const { subscribe } = useRealtime()
  const [dateValue, setDateValue] = useState(() => toDateInputValue(new Date()))
  const [bookings, setBookings] = useState<Booking[]>([])
  const [loadedDay, setLoadedDay] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [staffError, setStaffError] = useState<string | null>(null)
  const [timeZone, setTimeZone] = useState<string>(TIME_ZONE)
  const [selectedId, setSelectedId] = useState<string | null>(null)
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

  // Team for the reschedule select and the salon zone for the geometry.
  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    setStaffError(null)
    fetchStaff(salonId)
      .then((list) => {
        if (active) {
          setStaff(list)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setStaffError(err instanceof Error ? err.message : 'No pudimos cargar el equipo del salón.')
        }
      })
    // Settings are readable by any member; a failure keeps the known zone.
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setTimeZone(settings.timezone)
        }
      })
      .catch(() => {
        // keep TIME_ZONE as the fallback
      })
    return () => {
      active = false
    }
  }, [salonId])

  useEffect(() => {
    if (!salonId) {
      return
    }
    const { from, to } = dayRange(new Date(fromDateInputValue(dateValue, timeZone)), timeZone)
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
  }, [salonId, dateValue, timeZone, reloadKey])

  const isToday = dateValue === toDateInputValue(new Date(), timeZone)
  // While another day loads, do not show the appointments of the previous one.
  const agendaReady = loadedDay === dateValue
  const role = activeRole(salons, salonId)
  const selected = bookings.find((booking) => booking.id === selectedId) ?? null

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
        description={`Las citas se consultan con el rango del día en la zona horaria del salón (${timeZoneLabel(
          timeZone,
        )}).`}
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
            onClick={() => setDateValue(toDateInputValue(new Date(), timeZone))}
          >
            Hoy
          </button>
        ) : null}
      </div>

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}
      {loading ? (
        <LoadingState label={agendaReady ? 'Actualizando agenda…' : 'Cargando agenda…'} />
      ) : null}
      {error ? <ErrorNotice message={error} onRetry={reload} /> : null}
      {agendaReady && !error ? (
        <Timeline
          bookings={bookings}
          professionals={staff}
          timeZone={timeZone}
          selectedId={selectedId}
          onSelect={(booking) => {
            setNotice(null)
            setSelectedId(booking.id)
          }}
        />
      ) : null}

      {selected ? (
        <BookingDetail
          booking={selected}
          staff={staff}
          staffError={staffError}
          timeZone={timeZone}
          salonId={salonId}
          canAct={canManageBooking(role, user?.id ?? null, selected.professional_id)}
          onClose={() => setSelectedId(null)}
          onUpdated={(message) => {
            setNotice(message)
            reload()
          }}
        />
      ) : null}
    </>
  )
}
