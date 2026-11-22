import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, fetchBookings, fetchStaff, type ApiErrorNotice } from '../lib/api'
import { subscribeOfflineData } from '../lib/offlineData'
import {
  canOpenMyAgenda,
  initialCalendarView,
  visibleCalendarBookings,
  type CalendarView,
} from '../lib/myAgenda'
import { activeRole, canManageBooking } from '../lib/permissions'
import { timeZoneLabel } from '../lib/publicBooking'
import { fetchSettings } from '../lib/settings'
import {
  DEFAULT_WINDOW,
  TIME_ZONE,
  addDaysToDateValue,
  dayRange,
  fromDateInputValue,
  timelineWindowFromHours,
  toDateInputValue,
  type TimelineWindow,
} from '../lib/datetime'
import type { StaffMember } from '../lib/staff'
import type { Booking } from '../types'
import { useOffline } from './OfflineContext'
import { useRealtime } from './RealtimeContext'
import { useSalon } from './SalonContext'
import { BookingDetail } from './components/BookingDetail'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { Timeline } from './components/Timeline'

export function CalendarPage() {
  const { salonId, salons, user } = useSalon()
  const { subscribe } = useRealtime()
  const { online } = useOffline()
  const [dateValue, setDateValue] = useState(() => toDateInputValue(new Date()))
  const [bookings, setBookings] = useState<Booking[]>([])
  const [loadedDay, setLoadedDay] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  /** The list on screen is a cached copy, not a live answer of the API. */
  const [savedData, setSavedData] = useState(false)
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [staffLoaded, setStaffLoaded] = useState(false)
  const [staffError, setStaffError] = useState<string | null>(null)
  const [timeZone, setTimeZone] = useState<string>(TIME_ZONE)
  const [gridWindow, setGridWindow] = useState<TimelineWindow>(DEFAULT_WINDOW)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  /** null until the person chooses; whoever attends starts on their agenda. */
  const [view, setView] = useState<CalendarView | null>(null)
  // The service worker flags a cached answer before the fetch resolves, so the
  // load below reads this ref to know whether the data it got is saved data.
  const cachedAnswer = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(
    () =>
      subscribeOfflineData((path) => {
        if (path.includes('/bookings')) {
          cachedAnswer.current = true
        }
      }),
    [],
  )

  // A booking created or changed anywhere refreshes the visible day.
  useEffect(() => {
    const offCreated = subscribe('booking.created', () => reload())
    const offUpdated = subscribe('booking.updated', () => reload())
    return () => {
      offCreated()
      offUpdated()
    }
  }, [subscribe, reload])

  // Team for the reschedule select; settings for the zone and the visible hours.
  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    setStaffError(null)
    setStaffLoaded(false)
    fetchStaff(salonId)
      .then((list) => {
        if (active) {
          setStaff(list)
          setStaffLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setStaffError(err instanceof Error ? err.message : 'No pudimos cargar el equipo del salón.')
        }
      })
    // Settings are readable by any member; a failure keeps the known values.
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setTimeZone(settings.timezone)
          setGridWindow(timelineWindowFromHours(settings.openTime, settings.closeTime))
        }
      })
      .catch(() => {
        // keep TIME_ZONE and the 09:00–20:00 grid as the fallback
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
    cachedAnswer.current = false
    setLoading(true)
    setError(null)
    fetchBookings(salonId, from, to, controller.signal)
      .then((list) => {
        setBookings(list)
        setLoadedDay(dateValue)
        setSavedData(cachedAnswer.current)
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setSavedData(false)
          setError(describeApiError(err, 'No pudimos cargar la agenda.'))
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoading(false)
        }
      })
    return () => controller.abort()
  }, [salonId, dateValue, timeZone, reloadKey])

  // A salon switch recalculates bookability, so the view starts over there.
  useEffect(() => {
    setView(null)
    setSelectedId(null)
  }, [salonId])

  const isToday = dateValue === toDateInputValue(new Date(), timeZone)
  // While another day loads, do not show the appointments of the previous one.
  const agendaReady = loadedDay === dateValue
  const role = activeRole(salons, salonId)
  const bookable = canOpenMyAgenda({
    staff,
    staffLoaded,
    userId: user?.id ?? null,
    role,
  })
  const calendarView = view ?? initialCalendarView(bookable)
  const visibleBookings = visibleCalendarBookings(bookings, calendarView, user?.id)
  // "Mi agenda" draws only my column; "Todo el salón" draws the whole team and
  // still marks my own column (S-F2b-6).
  const visibleProfessionals =
    calendarView === 'mine' ? staff.filter((member) => member.id === user?.id) : staff
  const selected = bookings.find((booking) => booking.id === selectedId) ?? null
  // Saved or failed data is never presented as today's real agenda.
  const showingSavedData = savedData || !online
  const hasError = error !== null

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

      {bookable ? (
        <div className="view-toggle" role="group" aria-label="Vista de la agenda">
          <button
            type="button"
            className="view-toggle__option"
            aria-pressed={calendarView === 'mine'}
            data-active={calendarView === 'mine'}
            onClick={() => {
              setView('mine')
              setSelectedId(null)
            }}
          >
            Mi agenda
          </button>
          <button
            type="button"
            className="view-toggle__option"
            aria-pressed={calendarView === 'all'}
            data-active={calendarView === 'all'}
            onClick={() => {
              setView('all')
              setSelectedId(null)
            }}
          >
            Todo el salón
          </button>
        </div>
      ) : null}

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}
      {showingSavedData ? (
        <p className="notice notice--info" role="status">
          Sin conexión: mostrando datos guardados. Las citas nuevas aparecerán al recuperar la red.
        </p>
      ) : null}
      {loading ? (
        <LoadingState label={agendaReady ? 'Actualizando agenda…' : 'Cargando agenda…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      <div className={`calendar-layout${selected ? ' calendar-layout--detail' : ''}`}>
        <div className="calendar-layout__grid">
          {agendaReady && !hasError ? (
            <Timeline
              bookings={visibleBookings}
              professionals={visibleProfessionals}
              timeZone={timeZone}
              gridWindow={gridWindow}
              savedData={showingSavedData}
              highlightProfessionalId={bookable ? user?.id ?? null : null}
              emptyLabel={
                calendarView === 'mine' ? 'No tenés citas agendadas para este día.' : undefined
              }
              selectedId={selectedId}
              onSelect={(booking) => {
                setNotice(null)
                setSelectedId(booking.id)
              }}
            />
          ) : null}
        </div>

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
      </div>
    </>
  )
}
