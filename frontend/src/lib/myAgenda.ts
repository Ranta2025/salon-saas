/**
 * "Mi agenda" (contract F2b, S-F2b-1..6). Pure rules: which bookings belong to
 * the signed-in professional, their order, the now/next marker, the weekly
 * counts in the salon zone and who is bookable. The page only renders these.
 */

import {
  addDaysToDateValue,
  fromDateInputValue,
  toDateInputValue,
} from './datetime.ts'
import type { Role } from './permissions.ts'
import type { Booking } from '../types'

export type AgendaBooking = Pick<
  Booking,
  'id' | 'professional_id' | 'client_name' | 'starts_at' | 'ends_at' | 'status'
>

export type AgendaMarker = 'now' | 'next'

export const AGENDA_MARKER_LABEL: Record<AgendaMarker, string> = {
  now: 'Ahora',
  next: 'Siguiente',
}

export const WEEKDAY_LABELS = [
  'Lunes',
  'Martes',
  'Miércoles',
  'Jueves',
  'Viernes',
  'Sábado',
  'Domingo',
] as const

export const WEEKDAY_SHORT_LABELS = ['Lun', 'Mar', 'Mié', 'Jue', 'Vie', 'Sáb', 'Dom'] as const

const MONTHS = [
  'enero',
  'febrero',
  'marzo',
  'abril',
  'mayo',
  'junio',
  'julio',
  'agosto',
  'septiembre',
  'octubre',
  'noviembre',
  'diciembre',
] as const

/** A booking still to happen (a closed one is history, never "Ahora"/"Siguiente"). */
export function isOpenBooking(status: string): boolean {
  return status === '' || status === 'booked'
}

/** My bookings of the loaded list; a missing user id selects nothing. */
export function selectMyBookings<T extends { professional_id: string }>(
  bookings: readonly T[],
  professionalId: string | null | undefined,
): T[] {
  const id = professionalId?.trim() ?? ''
  if (id === '') {
    return []
  }
  return bookings.filter((booking) => booking.professional_id === id)
}

/** Chronological order of the agenda: start, then end, then id as a stable tie-break. */
export function sortAgendaBookings<T extends { starts_at: string; ends_at?: string; id?: string }>(
  bookings: readonly T[],
): T[] {
  return [...bookings].sort(
    (a, b) =>
      Date.parse(a.starts_at) - Date.parse(b.starts_at) ||
      Date.parse(a.ends_at ?? a.starts_at) - Date.parse(b.ends_at ?? b.starts_at) ||
      (a.id ?? '').localeCompare(b.id ?? ''),
  )
}

export type AgendaEntry<T extends AgendaBooking> = {
  booking: T
  marker: AgendaMarker | null
}

/**
 * Sorted rows plus the now/next marker. `now` is `null` for a day that is not
 * today: a past or future day has no appointment "in progress".
 */
export function agendaMarkers<T extends AgendaBooking>(
  bookings: readonly T[],
  now: Date | null,
): AgendaEntry<T>[] {
  const entries: AgendaEntry<T>[] = sortAgendaBookings(bookings).map((booking) => ({
    booking,
    marker: null,
  }))
  if (!now || Number.isNaN(now.getTime())) {
    return entries
  }
  const moment = now.getTime()
  const open = entries
    .map((entry, index) => ({ entry, index }))
    .filter(({ entry }) => isOpenBooking(entry.booking.status))
    .filter(({ entry }) => Date.parse(entry.booking.starts_at) <= moment && moment < Date.parse(entry.booking.ends_at))
  const current = open[0]
  if (current) {
    current.entry.marker = 'now'
  }
  const after = current ? current.index + 1 : 0
  for (let index = after; index < entries.length; index += 1) {
    const entry = entries[index]
    if (!entry || !isOpenBooking(entry.booking.status)) {
      continue
    }
    if (Date.parse(entry.booking.starts_at) > moment) {
      entry.marker = 'next'
      break
    }
  }
  return entries
}

/** The first "Siguiente" of the day, used by the summary. */
export function nextAgendaEntry<T extends AgendaBooking>(
  entries: readonly AgendaEntry<T>[],
): AgendaEntry<T> | null {
  return entries.find((entry) => entry.marker === 'next') ?? null
}

/** Local calendar date (`YYYY-MM-DD`) of an instant in the salon zone. */
export function bookingLocalDate(iso: string, timeZone: string): string {
  return toDateInputValue(new Date(iso), timeZone)
}

/**
 * Count of the day's appointments. Cancelled rows are not shifts anymore, so
 * they do not inflate the strip or the "turnos hoy" summary.
 */
export function todayAgendaCount(
  bookings: readonly { starts_at: string; status: string }[],
  dateValue: string,
  timeZone: string,
): number {
  return bookings.filter(
    (booking) => bookingLocalDate(booking.starts_at, timeZone) === dateValue && booking.status !== 'cancelled',
  ).length
}

/** `Sin turnos hoy` / `1 turno hoy` / `3 turnos hoy`. */
export function todayCountLabel(count: number): string {
  const safe = Number.isFinite(count) ? Math.max(0, Math.trunc(count)) : 0
  if (safe === 0) {
    return 'Sin turnos hoy'
  }
  return `${safe} ${safe === 1 ? 'turno' : 'turnos'} hoy`
}

/** Monday of the week that contains the salon date. */
export function weekStartOf(dateValue: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dateValue)
  if (!match) {
    throw new TypeError(`Fecha inválida: ${dateValue}`)
  }
  const [, year = '1970', month = '01', day = '01'] = match
  const weekday = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day))).getUTCDay()
  return addDaysToDateValue(dateValue, -((weekday + 6) % 7))
}

/** The seven `YYYY-MM-DD` values of a week, Monday first. */
export function weekDates(weekStartValue: string): string[] {
  return Array.from({ length: 7 }, (_, index) => addDaysToDateValue(weekStartValue, index))
}

export type WeekDay<T> = {
  date: string
  weekday: number
  label: string
  shortLabel: string
  dayNumber: number
  count: number
  isToday: boolean
  bookings: T[]
}

/**
 * Monday..Sunday strip with the count of my appointments per salon day. A
 * booking at 23:30 belongs to the salon day, not to the UTC day that may be
 * the next one.
 */
export function weekOverview<T extends { starts_at: string; status: string }>(
  bookings: readonly T[],
  weekStartValue: string,
  timeZone: string,
  todayValue: string,
): WeekDay<T>[] {
  const days = weekDates(weekStartValue)
  return days.map((date, index) => {
    const label = WEEKDAY_LABELS[index] ?? ''
    const dayBookings = sortAgendaBookings(
      bookings.filter(
        (booking) =>
          bookingLocalDate(booking.starts_at, timeZone) === date && booking.status !== 'cancelled',
      ),
    )
    return {
      date,
      weekday: index,
      label,
      shortLabel: WEEKDAY_SHORT_LABELS[index] ?? '',
      dayNumber: Number(date.slice(8, 10)),
      count: dayBookings.length,
      isToday: date === todayValue,
      bookings: dayBookings,
    }
  })
}

/** RFC3339 window of the whole week, in the salon time zone. */
export function weekRange(weekStartValue: string, timeZone: string): { from: string; to: string } {
  return {
    from: fromDateInputValue(weekStartValue, timeZone),
    to: fromDateInputValue(addDaysToDateValue(weekStartValue, 7), timeZone),
  }
}

/** `Viernes 21 de noviembre de 2025`. */
export function formatAgendaDate(dateValue: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dateValue)
  if (!match) {
    return dateValue
  }
  const [, year = '1970', month = '01', day = '01'] = match
  const weekday = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day))).getUTCDay()
  const label = WEEKDAY_LABELS[(weekday + 6) % 7] ?? ''
  return `${label} ${Number(day)} de ${MONTHS[Number(month) - 1] ?? ''} de ${year}`
}

/** The same date as a sentence: `sábado 21 de noviembre de 2026`. */
export function formatAgendaDateLower(dateValue: string): string {
  const formatted = formatAgendaDate(dateValue)
  return formatted.charAt(0).toLowerCase() + formatted.slice(1)
}

/** The `GET /salons/:id/staff` list is already the bookable team (F2a). */
export function isBookableInStaff(
  staff: readonly { id: string }[],
  userId: string | null | undefined,
): boolean {
  const id = userId?.trim() ?? ''
  if (id === '') {
    return false
  }
  return staff.some((member) => member.id === id)
}

export type BookabilityInput = {
  staff: readonly { id: string }[]
  /** False while `/staff` is in flight or when the request failed. */
  staffLoaded: boolean
  userId: string | null | undefined
  role: Role | null | undefined
}

/**
 * Whether the person is bookable in the active salon. `/staff` is the authority
 * (S-F2b-5); only when it could not be loaded does the domain rule of F2a
 * stand in — a professional always attends, an owner/admin might not.
 */
export function canOpenMyAgenda({
  staff,
  staffLoaded,
  userId,
  role,
}: BookabilityInput): boolean {
  if (isBookableInStaff(staff, userId)) {
    return true
  }
  if (staffLoaded) {
    return false
  }
  return role === 'professional' && (userId?.trim() ?? '') !== ''
}

/** Calendar views of S-F2b-6. */
export type CalendarView = 'mine' | 'all'

/** Whoever attends opens on their own agenda; the rest keep the whole salon. */
export function initialCalendarView(canOpenMine: boolean): CalendarView {
  return canOpenMine ? 'mine' : 'all'
}

/** Bookings the calendar draws for the chosen view. */
export function visibleCalendarBookings<T extends { professional_id: string }>(
  bookings: readonly T[],
  view: CalendarView,
  userId: string | null | undefined,
): T[] {
  return view === 'mine' ? selectMyBookings(bookings, userId) : [...bookings]
}
