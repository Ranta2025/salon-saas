import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { describeApiError, fetchBookings, fetchStaff, type ApiErrorNotice } from '@/lib/api'
import { statusLabel } from '../lib/bookingActions'
import { TIME_ZONE, formatTimeOfDay, toDateInputValue } from '@/lib/datetime'
import {
  AGENDA_MARKER_LABEL,
  agendaMarkers,
  bookingLocalDate,
  canOpenMyAgenda,
  formatAgendaDate,
  formatAgendaDateLower,
  nextAgendaEntry,
  selectMyBookings,
  todayAgendaCount,
  todayCountLabel,
  weekDates,
  weekOverview,
  weekRange,
  weekStartOf,
} from '../lib/myAgenda'
import { subscribeOfflineData } from '@/lib/offlineData'
import {
  absenceDays,
  currentAbsence,
  fetchTimeOff,
  timeOffForUser,
  timeOffSentence,
  type TimeOff,
} from '../lib/timeOff'
import { activeRole, canManageBooking } from '@/lib/permissions'
import { coveredNotices } from '../lib/coverAbsence'
import { timeZoneLabel } from '../lib/publicBooking'
import { fetchSettings } from '@/features/settings/lib/settings'
import type { StaffMember } from '@/lib/staff'
import type { Booking } from '@/types'
import { BookingDetail } from '../components/BookingDetail'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PageHeader } from '@/app/components/PageHeader'
import { useOffline } from '@/app/OfflineContext'
import { useRealtime } from '@/app/RealtimeContext'
import { useSalon } from '@/app/SalonContext'

function dayLabel(count: number): string {
  return count === 0 ? 'sin turnos' : `${count} ${count === 1 ? 'turno' : 'turnos'}`
}

/**
 * "Mi agenda" (S-F2b-1..3): my own bookings of the week, the Monday..Sunday
 * strip with their counts and the same actions as the calendar. The data comes
 * from the same `GET /bookings` of the calendar, so the offline cache and the
 * E-10 rule ("saved data is never shown as real") apply unchanged.
 */
export function MyAgendaPage() {
  const { salonId, salons, user, loading: salonLoading } = useSalon()
  const { subscribe } = useRealtime()
  const { online } = useOffline()
  const [timeZone, setTimeZone] = useState<string>(TIME_ZONE)
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [staffLoaded, setStaffLoaded] = useState(false)
  const [staffError, setStaffError] = useState<string | null>(null)
  const [selectedDate, setSelectedDate] = useState<string | null>(null)
  const [bookings, setBookings] = useState<Booking[]>([])
  // Absences of the visible week (F2d2): the notice and the marks of the strip.
  const [absences, setAbsences] = useState<TimeOff[]>([])
  const [loadedWeek, setLoadedWeek] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [savedData, setSavedData] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [now, setNow] = useState(() => new Date())
  // The worker flags a cached answer before the fetch resolves (E-10).
  const cachedAnswer = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // "Ahora"/"Siguiente" move with the clock without a reload.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 60_000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(
    () =>
      subscribeOfflineData((path) => {
        if (path.includes('/bookings')) {
          cachedAnswer.current = true
        }
      }),
    [],
  )

  useEffect(() => {
    const offCreated = subscribe('booking.created', () => reload())
    const offUpdated = subscribe('booking.updated', () => reload())
    return () => {
      offCreated()
      offUpdated()
    }
  }, [subscribe, reload])

  // Team for the reschedule select and to know whether the person is bookable.
  useEffect(() => {
    if (!salonId) {
      setStaff([])
      setStaffLoaded(false)
      return
    }
    let active = true
    setStaff([])
    setStaffLoaded(false)
    setStaffError(null)
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
    // The zone comes from the salon settings; a failure keeps the known one.
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setTimeZone(settings.timezone)
        }
      })
      .catch(() => {
        // TIME_ZONE stays as the fallback
      })
    return () => {
      active = false
    }
  }, [salonId])

  // A salon change starts on today again; the week and the week strip follow.
  useEffect(() => {
    setSelectedDate(null)
    setSelectedId(null)
    setBookings([])
    setLoadedWeek(null)
  }, [salonId])

  const todayValue = toDateInputValue(new Date(now), timeZone)
  const effectiveDate = selectedDate ?? todayValue
  const weekStart = weekStartOf(effectiveDate)
  const isToday = effectiveDate === todayValue

  useEffect(() => {
    if (!salonId) {
      return
    }
    const { from, to } = weekRange(weekStart, timeZone)
    const controller = new AbortController()
    cachedAnswer.current = false
    setLoading(true)
    setError(null)
    fetchBookings(salonId, from, to, controller.signal)
      .then((list) => {
        setBookings(list)
        setLoadedWeek(weekStart)
        setSavedData(cachedAnswer.current)
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setSavedData(false)
          setError(describeApiError(err, 'No pudimos cargar tu agenda.'))
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoading(false)
        }
      })
    // The absence of the day is extra information: a failed request never turns
    // the agenda into an error (§11.6 rule).
    fetchTimeOff(salonId, from, to, controller.signal)
      .then((list) => {
        if (!controller.signal.aborted) {
          setAbsences(list)
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setAbsences([])
        }
      })
    return () => controller.abort()
  }, [salonId, weekStart, timeZone, reloadKey])

  const role = activeRole(salons, salonId)
  const bookable = canOpenMyAgenda({
    staff,
    staffLoaded,
    userId: user?.id ?? null,
    role,
  })
  const mine = selectMyBookings(bookings, user?.id)
  const myId = user?.id ?? ''
  const myAbsences = timeOffForUser(absences, myId)
  const absence = currentAbsence(myAbsences, myId, now)
  // Turns of mine that somebody else covers now (S-F2d3-5): the server marks
  // them with `covered_from`, so a covered turn there shows who kept it.
  const covered = coveredNotices(bookings, myId, { timeZone, now })
  const absentDates = absenceDays(myAbsences, weekDates(weekStart), timeZone)
  const week = weekOverview(mine, weekStart, timeZone, todayValue)
  const todayEntries = agendaMarkers(
    mine.filter((booking) => bookingLocalDate(booking.starts_at, timeZone) === todayValue),
    now,
  )
  const todayCount = todayAgendaCount(mine, todayValue, timeZone)
  const next = nextAgendaEntry(todayEntries)
  const dayEntries = agendaMarkers(
    mine.filter((booking) => bookingLocalDate(booking.starts_at, timeZone) === effectiveDate),
    isToday ? now : null,
  )
  const dayCount = dayEntries.filter((entry) => entry.booking.status !== 'cancelled').length
  const weekReady = loadedWeek === weekStart
  const selected = mine.find((booking) => booking.id === selectedId) ?? null
  // Saved or failed data is never presented as an empty real agenda (E-10).
  const showingSavedData = savedData || !online
  const hasError = error !== null

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Agenda" title="Mi agenda" />
        <SalonRequired />
      </>
    )
  }

  // Only whoever attends has a personal agenda; reception keeps the calendar.
  if (staffLoaded && !bookable && role !== null) {
    return (
      <>
        <PageHeader eyebrow="Agenda" title="Mi agenda" />
        <div className="empty-state">
          <h2>Tu rol no atiende clientes</h2>
          <p>
            «Mi agenda» reúne los turnos de quien atiende. Recepción y administración ven todo el
            salón en el calendario; si también atendés, pedile a la administración que active
            «Atiende clientes» en tu ficha del equipo.
          </p>
          <Link className="btn btn--quiet btn--sm" to="/panel/calendario">
            Ir al calendario del salón
          </Link>
        </div>
      </>
    )
  }

  if (salonLoading && !staffLoaded) {
    return (
      <>
        <PageHeader eyebrow="Agenda" title="Mi agenda" />
        <LoadingState label="Cargando tu acceso…" />
      </>
    )
  }

  return (
    <>
      <PageHeader
        eyebrow="Agenda"
        title="Mi agenda"
        description={`Hoy es ${formatAgendaDateLower(todayValue)} en la zona horaria del salón (${timeZoneLabel(
          timeZone,
        )}).`}
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {!hasError && loadedWeek !== null ? (
        <section className="my-agenda__summary" aria-label="Resumen de mi agenda">
          <p className="my-agenda__count" role="status">
            {todayCountLabel(todayCount)}
          </p>
          <p className="my-agenda__next">
            {next
              ? `Próximo turno: ${formatTimeOfDay(next.booking.starts_at, timeZone)} · ${
                  next.booking.client_name || 'Cliente sin nombre'
                }`
              : 'No queda ningún turno por delante hoy.'}
          </p>
        </section>
      ) : null}

      {!hasError && loadedWeek !== null && absence ? (
        <p className="notice notice--info my-agenda__absence" role="status">
          {`Estás ausente ${timeOffSentence(absence, { timeZone, today: todayValue })}.`}
        </p>
      ) : null}

      {!hasError && loadedWeek !== null && covered.length > 0 ? (
        <div className="notice notice--info my-agenda__covered" role="status">
          <p className="my-agenda__covered-title">
            {covered.length === 1
              ? 'Un turno tuyo lo cubre otra profesional:'
              : 'Turnos tuyos que cubre otra profesional:'}
          </p>
          <ul className="my-agenda__covered-list">
            {covered.map((notice) => (
              <li key={notice.bookingId}>{notice.text}</li>
            ))}
          </ul>
        </div>
      ) : null}

      {!hasError && loadedWeek !== null ? (
        <ol className="week-strip" aria-label={`Semana del ${formatAgendaDate(weekStart)}`}>
          {week.map((day) => (
            <li key={day.date}>
            <button
              type="button"
              className="week-strip__day"
              data-today={day.isToday}
              data-selected={day.date === effectiveDate}
              data-absent={absentDates.includes(day.date)}
              aria-pressed={day.date === effectiveDate}
              aria-current={day.isToday ? 'date' : undefined}
              aria-label={`${day.label} ${day.dayNumber}: ${dayLabel(day.count)}${
                absentDates.includes(day.date) ? ', ausente' : ''
              }`}
              onClick={() => {
                setSelectedDate(day.date)
                setSelectedId(null)
                setNotice(null)
              }}
            >
              <span className="week-strip__name">{day.shortLabel}</span>
              <span className="week-strip__number">{day.dayNumber}</span>
              <span className="week-strip__count">{day.count}</span>
              {absentDates.includes(day.date) ? (
                <span className="week-strip__absent" aria-hidden="true" />
              ) : null}
              </button>
            </li>
          ))}
        </ol>
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
      {loading && !weekReady ? <LoadingState label="Cargando tu agenda…" /> : null}
      {loading && weekReady ? <LoadingState label="Actualizando tu agenda…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {weekReady && !hasError ? (
        <div className={`my-agenda-layout${selected ? ' my-agenda-layout--detail' : ''}`}>
        <section className="my-agenda-day" aria-labelledby="my-agenda-day-title">
          <header className="my-agenda-day__head">
            <h2 id="my-agenda-day-title">
              {isToday ? 'Hoy' : formatAgendaDate(effectiveDate)}
            </h2>
            <span className="panel-card__meta">
              {dayCount === 1 ? '1 turno' : `${dayCount} turnos`}
            </span>
          </header>

          {dayEntries.length === 0 ? (
              <p className="empty-state">
                {showingSavedData
                  ? 'Sin conexión: la copia guardada no tiene turnos para este día. Actualiza cuando vuelva la red.'
                  : isToday
                    ? 'No tenés turnos hoy.'
                    : 'No tenés turnos este día.'}
              </p>
            ) : (
              <ol className="my-agenda__list">
                {dayEntries.map(({ booking, marker }) => (
                  <li key={booking.id} className="my-agenda__item" data-status={booking.status}>
                    <button
                      type="button"
                      className="my-agenda__row"
                      data-selected={booking.id === selectedId}
                      onClick={() => {
                        setNotice(null)
                        setSelectedId(booking.id)
                      }}
                      aria-label={`${formatTimeOfDay(booking.starts_at, timeZone)} a ${formatTimeOfDay(
                        booking.ends_at,
                        timeZone,
                      )}, ${booking.client_name}, ${statusLabel(booking.status)}${
                        marker ? `, ${AGENDA_MARKER_LABEL[marker]}` : ''
                      }. Abrir acciones`}
                    >
                      <span className="my-agenda__time">
                        {formatTimeOfDay(booking.starts_at, timeZone)}–
                        {formatTimeOfDay(booking.ends_at, timeZone)}
                      </span>
                      <span className="my-agenda__client">
                        {booking.client_name || 'Cliente sin nombre'}
                      </span>
                      <span className="my-agenda__flags">
                        {marker ? (
                          <span className="my-agenda__marker" data-marker={marker}>
                            {AGENDA_MARKER_LABEL[marker]}
                          </span>
                        ) : null}
                        <span className="badge" data-status={booking.status}>
                          {statusLabel(booking.status)}
                        </span>
                      </span>
                    </button>
                  </li>
                ))}
              </ol>
            )
          }
        </section>

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
      ) : null}
    </>
  )
}
