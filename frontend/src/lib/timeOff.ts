/**
 * Vacations and one-off days off per professional (contract F2d2, S-F2d2-5
 * shape, S-F2d2-6 and S-F2d2-7). Pure rules plus the thin API client:
 * `/panel/equipo`, the calendar band and "Mi agenda" render what these return
 * and the server stays the authority.
 *
 * An absence is a `[starts_at, ends_at)` period in UTC of ONE bookable
 * professional. The UI converts from the salon zone: a whole day is the local
 * 00:00 to the local 00:00 of the next day. The error sentences are the ones of
 * the API, verbatim, so a rejected absence reads exactly like a `400`.
 */

import { request } from './api.ts'
import {
  TIME_ZONE,
  addDaysToDateValue,
  formatTimeOfDay,
  fromDateInputValue,
  toDateInputValue,
  toDateTimeIso,
} from './datetime.ts'
import { isManager, type Role } from './permissions.ts'

/** Domain limit of one absence (S-F2d2-2). */
export const TIME_OFF_MAX_DAYS = 90

/** Domain limit of `time_off.reason` (S-F2d2-1). */
export const TIME_OFF_MAX_REASON = 120

/** Sentences of the API shown verbatim (S-F2d2-5) plus two of the form itself. */
export const TIME_OFF_MESSAGES = {
  order: 'El fin debe ser posterior al inicio',
  tooLong: 'Una ausencia no puede durar más de 90 días',
  alreadyEnded: 'No se puede cargar una ausencia que ya terminó',
  overlap: 'Ya hay una ausencia en ese período',
  noRole: 'Ese rol no tiene ausencias',
  /** Only the form: the two dates are required. */
  dates: 'Elegí la fecha de inicio y la de fin',
  /** Only the form: a partial absence needs both hours. */
  hours: 'Completá las dos horas del horario parcial',
} as const

/** One absence (`GET /salons/:id/time-off`, S-F2d2-5). */
export type TimeOff = {
  id: string
  user_id: string
  starts_at: string
  ends_at: string
  /** Empty when the server hides it: only management and the person itself read it. */
  reason: string
}

/** A `booked` booking that falls inside the new absence (S-F2d2-4). */
export type TimeOffConflict = {
  id: string
  client_name: string
  starts_at: string
  ends_at: string
}

/** Answer of `POST /salons/:id/members/:uid/time-off`, 201. */
export type TimeOffCreated = { time_off: TimeOff | null; conflicts: TimeOffConflict[] }

/** What the "Ausencias" form asks for: two local dates and optional hours. */
export type TimeOffDraft = {
  fromDate: string
  toDate: string
  /** Partial start hour (`HH:MM`); empty means the whole day. */
  fromTime?: string
  toTime?: string
  reason?: string
}

export type TimeOffCheck =
  | { ok: true; value: { startsAt: string; endsAt: string; reason: string } }
  | { ok: false; message: string }

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

const DAY_MS = 86_400_000
const DATE_PATTERN = /^(\d{4})-(\d{2})-(\d{2})$/
const CLOCK_PATTERN = /^([01]\d|2[0-3]):([0-5]\d)$/

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function isDateValue(value: string): boolean {
  return DATE_PATTERN.test(value)
}

function isClockValue(value: string): boolean {
  return CLOCK_PATTERN.test(value)
}

/** Reason as the server stores it: trimmed, one line, at most 120 characters. */
export function normalizeReason(value: unknown): string {
  return text(value)
    .replace(/[\r\n\t]+/g, ' ')
    .replace(/\s+/g, ' ')
    .slice(0, TIME_OFF_MAX_REASON)
    .trimEnd()
}

function normalizeEntry(value: unknown): TimeOff | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  const userId = text(value['user_id'])
  const startsAt = text(value['starts_at'])
  const endsAt = text(value['ends_at'])
  const start = Date.parse(startsAt)
  const end = Date.parse(endsAt)
  if (id === '' || userId === '' || Number.isNaN(start) || Number.isNaN(end) || end <= start) {
    return null
  }
  return {
    id,
    user_id: userId,
    starts_at: startsAt,
    ends_at: endsAt,
    reason: normalizeReason(value['reason']),
  }
}

/** Reads `{time_off:[…]}` (or a bare array) and drops rows a week could not hold. */
export function normalizeTimeOff(raw: unknown): TimeOff[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['time_off'])
      ? raw['time_off']
      : []
  const entries: TimeOff[] = []
  for (const row of rows) {
    const entry = normalizeEntry(row)
    if (entry) {
      entries.push(entry)
    }
  }
  return entries.sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))
}

/** Reads the `conflicts` list of a created absence (S-F2d2-4). */
export function normalizeConflicts(raw: unknown): TimeOffConflict[] {
  const rows = Array.isArray(raw) ? raw : []
  const conflicts: TimeOffConflict[] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const id = text(row['id'])
    const startsAt = text(row['starts_at'])
    const endsAt = text(row['ends_at'])
    if (id === '' || Number.isNaN(Date.parse(startsAt)) || Number.isNaN(Date.parse(endsAt))) {
      continue
    }
    conflicts.push({
      id,
      client_name: text(row['client_name']),
      starts_at: startsAt,
      ends_at: endsAt,
    })
  }
  return conflicts.sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))
}

/** Reads `{time_off, conflicts}` of the POST; a bare absence is also accepted. */
export function normalizeTimeOffCreated(raw: unknown): TimeOffCreated {
  if (!isRecord(raw)) {
    return { time_off: null, conflicts: [] }
  }
  return {
    time_off: normalizeEntry(raw['time_off']),
    conflicts: normalizeConflicts(raw['conflicts']),
  }
}

/**
 * Local draft → UTC range. A whole day is the local 00:00 to the local 00:00 of
 * the next day; with partial hours the given wall clocks are used instead. Throws
 * a `TypeError` on an unusable date instead of sending `Invalid Date` to the API.
 */
export function timeOffRange(
  draft: TimeOffDraft,
  timeZone: string = TIME_ZONE,
): { startsAt: string; endsAt: string } {
  const fromDate = (draft.fromDate ?? '').trim()
  const toDate = (draft.toDate ?? '').trim()
  const fromTime = (draft.fromTime ?? '').trim()
  const toTime = (draft.toTime ?? '').trim()
  const startsAt =
    fromTime === '' ? fromDateInputValue(fromDate, timeZone) : toDateTimeIso(fromDate, fromTime, timeZone)
  const endsAt =
    toTime === ''
      ? fromDateInputValue(addDaysToDateValue(toDate, 1), timeZone)
      : toDateTimeIso(toDate, toTime, timeZone)
  return { startsAt, endsAt }
}

/** `[a, b)` overlap: touching at the edge is not an overlap. */
export function overlapsRange(
  a: { starts_at: string; ends_at: string },
  b: { starts_at: string; ends_at: string },
): boolean {
  const aStart = Date.parse(a.starts_at)
  const aEnd = Date.parse(a.ends_at)
  const bStart = Date.parse(b.starts_at)
  const bEnd = Date.parse(b.ends_at)
  if ([aStart, aEnd, bStart, bEnd].some(Number.isNaN)) {
    return false
  }
  return aStart < bEnd && bStart < aEnd
}

/**
 * Server rules of S-F2d2-2, in the server order: the end is after the start, the
 * absence lasts at most 90 days, it cannot be already over and two absences of
 * the same person cannot overlap. The sentences are the ones of the API.
 */
export function validateTimeOff(input: {
  startsAt: string
  endsAt: string
  reason?: string
  existing?: readonly TimeOff[]
  now: Date
}): TimeOffCheck {
  const start = Date.parse(input.startsAt)
  const end = Date.parse(input.endsAt)
  if (Number.isNaN(start) || Number.isNaN(end) || end <= start) {
    return { ok: false, message: TIME_OFF_MESSAGES.order }
  }
  if (end - start > TIME_OFF_MAX_DAYS * DAY_MS) {
    return { ok: false, message: TIME_OFF_MESSAGES.tooLong }
  }
  if (end <= input.now.getTime()) {
    return { ok: false, message: TIME_OFF_MESSAGES.alreadyEnded }
  }
  const range = { starts_at: input.startsAt, ends_at: input.endsAt }
  if ((input.existing ?? []).some((entry) => overlapsRange(entry, range))) {
    return { ok: false, message: TIME_OFF_MESSAGES.overlap }
  }
  return {
    ok: true,
    value: {
      startsAt: input.startsAt,
      endsAt: input.endsAt,
      reason: normalizeReason(input.reason),
    },
  }
}

/**
 * The same validation read from the form: two local dates plus optional hours.
 * A draft without both dates, or with only one of the two hours, is answered by
 * the form before the API is asked.
 */
export function validateTimeOffDraft(input: {
  draft: TimeOffDraft
  timeZone: string
  existing?: readonly TimeOff[]
  now: Date
}): TimeOffCheck {
  const fromDate = (input.draft.fromDate ?? '').trim()
  const toDate = (input.draft.toDate ?? '').trim()
  const fromTime = (input.draft.fromTime ?? '').trim()
  const toTime = (input.draft.toTime ?? '').trim()
  if (!isDateValue(fromDate) || !isDateValue(toDate)) {
    return { ok: false, message: TIME_OFF_MESSAGES.dates }
  }
  if ((fromTime === '') !== (toTime === '')) {
    return { ok: false, message: TIME_OFF_MESSAGES.hours }
  }
  if (fromTime !== '' && (!isClockValue(fromTime) || !isClockValue(toTime))) {
    return { ok: false, message: TIME_OFF_MESSAGES.hours }
  }
  const { startsAt, endsAt } = timeOffRange({ fromDate, toDate, fromTime, toTime }, input.timeZone)
  return validateTimeOff({
    startsAt,
    endsAt,
    reason: input.draft.reason,
    existing: input.existing,
    now: input.now,
  })
}

/** Absences of one person, sorted by start. */
export function timeOffForUser(entries: readonly TimeOff[], userId: string): TimeOff[] {
  const id = userId.trim()
  if (id === '') {
    return []
  }
  return entries.filter((entry) => entry.user_id === id)
}

/** Local day window `[00:00, next 00:00)` of a `YYYY-MM-DD` value, in UTC. */
function dayWindow(dateValue: string, timeZone: string): { starts_at: string; ends_at: string } {
  return {
    starts_at: fromDateInputValue(dateValue, timeZone),
    ends_at: fromDateInputValue(addDaysToDateValue(dateValue, 1), timeZone),
  }
}

/** Absences that touch the local day: the calendar band and the strip use this. */
export function absencesOnDay(
  entries: readonly TimeOff[],
  dateValue: string,
  timeZone: string = TIME_ZONE,
): TimeOff[] {
  if (!isDateValue(dateValue)) {
    return []
  }
  const window = dayWindow(dateValue, timeZone)
  return entries.filter((entry) => overlapsRange(entry, window))
}

/** The `YYYY-MM-DD` values of a week that carry an absence (S-F2d2-7 strip). */
export function absenceDays(
  entries: readonly TimeOff[],
  dates: readonly string[],
  timeZone: string = TIME_ZONE,
): string[] {
  return dates.filter((date) => absencesOnDay(entries, date, timeZone).length > 0)
}

/** Management reads every reason; anybody else only the own absences (S-F2d2-5). */
export function canSeeReason(
  actorRole: Role | null | undefined,
  viewerId: string | null | undefined,
  userId: string,
): boolean {
  if (isManager(actorRole)) {
    return true
  }
  const viewer = viewerId?.trim() ?? ''
  return viewer !== '' && viewer === userId
}

/**
 * Reason the UI may print. The server already hides it, and this is the second
 * half of the rule: a payload that carried the text never leaks it on screen.
 */
export function visibleReason(
  entry: TimeOff,
  actorRole: Role | null | undefined,
  viewerId: string | null | undefined,
): string {
  return canSeeReason(actorRole, viewerId, entry.user_id) ? entry.reason : ''
}

/** Today in the salon zone, used by the "Hoy" copy of the range. */
function todayValue(timeZone: string, today?: string): string {
  if (today && isDateValue(today)) {
    return today
  }
  return toDateInputValue(new Date(), timeZone)
}

type RangeOptions = { timeZone?: string; today?: string }

function dayLabel(dateValue: string): string {
  const match = DATE_PATTERN.exec(dateValue)
  if (!match) {
    return dateValue
  }
  const [, , month = '01', day = '01'] = match
  return `${Number(day)} de ${MONTHS[Number(month) - 1] ?? ''}`
}

/**
 * Spanish range of one absence: «Del 24 al 26 de diciembre», «El 24 de diciembre»
 * or «Hoy de 10:00 a 12:00» when it starts today with partial hours. The end of a
 * whole-day absence is the midnight of the following day, so the printed last day
 * is the one the absence really covers.
 */
export function formatTimeOffRange(entry: { starts_at: string; ends_at: string }, options: RangeOptions = {}): string {
  const timeZone = options.timeZone ?? TIME_ZONE
  const today = todayValue(timeZone, options.today)
  const start = new Date(entry.starts_at)
  const end = new Date(entry.ends_at)
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) {
    return ''
  }
  const startDate = toDateInputValue(start, timeZone)
  const endsAtMidnight = formatTimeOfDay(entry.ends_at, timeZone) === '00:00'
  // A whole-day absence ends at the midnight of the day after its last day.
  const lastDate = endsAtMidnight
    ? toDateInputValue(new Date(end.getTime() - 1000), timeZone)
    : toDateInputValue(end, timeZone)
  const startTime = formatTimeOfDay(entry.starts_at, timeZone)
  const endTime = formatTimeOfDay(entry.ends_at, timeZone)
  const partial = !(endsAtMidnight && startTime === '00:00')
  const sameDay = startDate === lastDate

  if (partial) {
    if (sameDay) {
      return startDate === today
        ? `Hoy de ${startTime} a ${endTime}`
        : `El ${dayLabel(startDate)} de ${startTime} a ${endTime}`
    }
    return `Del ${dayLabel(startDate)} a las ${startTime} al ${dayLabel(lastDate)} a las ${endTime}`
  }
  if (sameDay) {
    return startDate === today ? 'Hoy' : `El ${dayLabel(startDate)}`
  }
  if (startDate.slice(0, 7) === lastDate.slice(0, 7)) {
    return `Del ${dayLabel(startDate).replace(/ de .*$/, '')} al ${dayLabel(lastDate)}`
  }
  return `Del ${dayLabel(startDate)} al ${dayLabel(lastDate)}`
}

/** The same range as a mid-sentence phrase: «del 24 al 26 de diciembre». */
export function timeOffSentence(
  entry: { starts_at: string; ends_at: string },
  options: RangeOptions = {},
): string {
  const text = formatTimeOffRange(entry, options)
  return text.charAt(0).toLowerCase() + text.slice(1)
}

/** Absences in progress or still to come, sorted by start (S-F2d2-6 list). */
export function upcomingAbsences(entries: readonly TimeOff[], now: Date): TimeOff[] {
  const moment = now.getTime()
  return [...entries]
    .filter((entry) => Date.parse(entry.ends_at) > moment)
    .sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))
}

/** The absence in progress of one person, or the next one; null when none. */
export function currentAbsence(
  entries: readonly TimeOff[],
  userId: string,
  now: Date,
): TimeOff | null {
  const running = upcomingAbsences(timeOffForUser(entries, userId), now).filter(
    (entry) => Date.parse(entry.starts_at) <= now.getTime(),
  )
  if (running[0]) {
    return running[0]
  }
  return upcomingAbsences(timeOffForUser(entries, userId), now)[0] ?? null
}

/** `Tiene 3 turnos en ese período: reasignalos o cancelalos desde el calendario`. */
export function conflictsNotice(count: number): string | null {
  const safe = Number.isFinite(count) ? Math.max(0, Math.trunc(count)) : 0
  if (safe === 0) {
    return null
  }
  if (safe === 1) {
    return 'Tiene 1 turno en ese período: reasignalo o cancelalo desde el calendario'
  }
  return `Tiene ${safe} turnos en ese período: reasignalos o cancelalos desde el calendario`
}

/** One "Ausente" band of the calendar column. */
export type TimeOffBand = {
  userId: string
  /** Name of the person when the caller knows the team, else empty. */
  name: string
  /** Always «Ausente»; the hours go on their own line. */
  label: string
  /** `10:00–12:00` when the absence covers only part of the window, else empty. */
  hours: string
  /** Only when the viewer may read it, else empty. */
  reason: string
}

/**
 * Bands of the visible window (a day of the calendar): one per person, with the
 * hours when the absence does not cover the whole window and the reason only for
 * those who may read it (S-F2d2-7).
 */
export function timeOffBands(
  entries: readonly TimeOff[],
  options: {
    from: string
    to: string
    timeZone?: string
    actorRole?: Role | null
    viewerId?: string | null
    /** Team names, used by the copy of a day without appointments. */
    names?: readonly { id: string; name: string }[]
  },
): TimeOffBand[] {
  const timeZone = options.timeZone ?? TIME_ZONE
  const window = { starts_at: options.from, ends_at: options.to }
  const windowStart = Date.parse(options.from)
  const windowEnd = Date.parse(options.to)
  const overlapping = entries.filter((entry) => overlapsRange(entry, window))
  const users: string[] = []
  for (const entry of overlapping) {
    if (!users.includes(entry.user_id)) {
      users.push(entry.user_id)
    }
  }
  return users.map((userId) => {
    const ofUser = timeOffForUser(overlapping, userId)
    const first = ofUser[0]
    const start = Math.max(Date.parse(first?.starts_at ?? options.from), windowStart)
    const end = Math.min(Date.parse(first?.ends_at ?? options.to), windowEnd)
    const wholeWindow = start <= windowStart && end >= windowEnd
    const hours = wholeWindow
      ? ''
      : `${formatTimeOfDay(new Date(start).toISOString(), timeZone)}–${formatTimeOfDay(
          new Date(end).toISOString(),
          timeZone,
        )}`
    const reason = ofUser.reduce(
      (found, entry) => found || visibleReason(entry, options.actorRole, options.viewerId),
      '',
    )
    const name = options.names?.find((person) => person.id === userId)?.name ?? ''
    return { userId, name, label: 'Ausente', hours, reason }
  })
}

/** Owner/admin add and remove absences, and only of who attends clients. */
export function canEditAbsences(actorRole: Role | null | undefined): boolean {
  return isManager(actorRole)
}

// --- API client (contract S-F2d2-5) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

/** `GET /salons/:id/time-off?from&to`: the absences overlapping the range. */
export async function fetchTimeOff(
  salonId: string,
  from: string,
  to: string,
  signal?: AbortSignal,
): Promise<TimeOff[]> {
  const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
  return normalizeTimeOff(
    await request<unknown>(salonPath(salonId, `/time-off${query}`), { signal }),
  )
}

/**
 * `POST /salons/:id/members/:uid/time-off` → 201 with the absence and the
 * bookings of that person that fall inside it (S-F2d2-4). The reason travels only
 * when it has text, so an empty form sends the same body as always.
 */
export async function createTimeOff(
  salonId: string,
  userId: string,
  input: { startsAt: string; endsAt: string; reason?: string },
): Promise<TimeOffCreated> {
  const body: Record<string, unknown> = {
    starts_at: input.startsAt,
    ends_at: input.endsAt,
  }
  const reason = normalizeReason(input.reason)
  if (reason !== '') {
    body['reason'] = reason
  }
  const payload = await request<unknown>(
    salonPath(salonId, `/members/${encodeURIComponent(userId)}/time-off`),
    { method: 'POST', body },
  )
  return normalizeTimeOffCreated(payload)
}

/** `DELETE /salons/:id/time-off/:tid` → 204. */
export async function deleteTimeOff(salonId: string, timeOffId: string): Promise<void> {
  await request<unknown>(salonPath(salonId, `/time-off/${encodeURIComponent(timeOffId)}`), {
    method: 'DELETE',
  })
}
