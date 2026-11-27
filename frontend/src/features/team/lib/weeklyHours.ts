/**
 * Weekly hours of the salon and of each professional (contract F2d1, S-F2d1-4
 * shape and S-F2d1-6). Pure helpers: `/panel/ajustes`, `/panel/equipo` and the
 * landing render what these return and the server stays the authority.
 *
 * Weekdays are ISO 1..7 (1 = Monday) computed in the zone of the salon; times
 * are 24 h `HH:MM`. The error sentences are the ones of the API, verbatim, so a
 * rejected draft reads exactly like a `400` from the server.
 */

export const WEEKDAYS = [1, 2, 3, 4, 5, 6, 7] as const

export type Weekday = (typeof WEEKDAYS)[number]

export const WEEKDAY_LABELS: Readonly<Record<Weekday, string>> = {
  1: 'Lunes',
  2: 'Martes',
  3: 'Miércoles',
  4: 'Jueves',
  5: 'Viernes',
  6: 'Sábado',
  7: 'Domingo',
}

/** Short labels of the week summary, lowercase as in the contract example. */
export const WEEKDAY_SHORT: Readonly<Record<Weekday, string>> = {
  1: 'Lun',
  2: 'Mar',
  3: 'Mié',
  4: 'Jue',
  5: 'Vie',
  6: 'Sáb',
  7: 'Dom',
}

/** Maximum segments of one professional on one day (S-F2d1-2). */
export const MAX_SEGMENTS_PER_DAY = 4

/** Sentences of the API shown verbatim (S-F2d1-4). */
export const HOURS_MESSAGES = {
  order: 'Horario inválido: el inicio debe ser anterior al fin',
  overlap: 'Los tramos de un mismo día no pueden solaparse',
  tooMany: 'Máximo 4 tramos por día',
  invalidDay: 'Día inválido',
  format: 'Escribe la hora como HH:MM (por ejemplo 09:30)',
} as const

/** One open day of the salon (`GET/PUT /salons/:id/hours`). */
export type SalonDay = { weekday: number; open: string; close: string }

/** One working segment of a professional (`GET/PUT /members/:uid/hours`). */
export type DaySegment = { weekday: number; start: string; end: string }

export type SalonHours = { days: SalonDay[]; custom: boolean }

export type MemberHours = { segments: DaySegment[]; custom: boolean }

/** Editable row of the salon editor: seven rows, closed days keep their times. */
export type DraftDay = { weekday: Weekday; open: string; close: string; closed: boolean }

export type HourCheck<T> =
  | { ok: true; value: T }
  | { ok: false; weekday: number; message: string }

const CLOCK_PATTERN = /^([01]\d|2[0-3]):([0-5]\d)$/

export function isClock(value: unknown): value is string {
  return typeof value === 'string' && CLOCK_PATTERN.test(value)
}

/** Whole minutes since midnight of an `HH:MM` value, or null when unusable. */
export function parseClock(value: string): number | null {
  const match = CLOCK_PATTERN.exec(value)
  if (!match) {
    return null
  }
  return Number(match[1]) * 60 + Number(match[2])
}

function clampMinutes(minutes: number): number {
  if (!Number.isFinite(minutes)) {
    return 0
  }
  return Math.min(Math.max(Math.round(minutes), 0), 23 * 60 + 59)
}

/** `540` → `09:00` (the shape of the API and of `input type="time"`). */
export function formatClock(minutes: number): string {
  const total = clampMinutes(minutes)
  return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`
}

/** `540` → `9:00` (how the week summary prints the hours). */
export function formatClockShort(minutes: number): string {
  const total = clampMinutes(minutes)
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

export function isWeekday(value: unknown): value is Weekday {
  return typeof value === 'number' && WEEKDAYS.includes(value as Weekday)
}

function compareDays(a: SalonDay, b: SalonDay): number {
  return a.weekday - b.weekday
}

function compareSegments(a: DaySegment, b: DaySegment): number {
  return (
    a.weekday - b.weekday ||
    (parseClock(a.start) ?? 0) - (parseClock(b.start) ?? 0) ||
    (parseClock(a.end) ?? 0) - (parseClock(b.end) ?? 0)
  )
}

function sortedSegments(segments: readonly DaySegment[]): DaySegment[] {
  return [...segments].sort(compareSegments)
}

/** Reads `days`, dropping rows with a bad weekday, a bad time or an inverted range. */
export function normalizeDays(raw: unknown): SalonDay[] {
  const rows = Array.isArray(raw) ? raw : []
  const days: SalonDay[] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const weekday = row['weekday']
    const open = text(row['open'])
    const close = text(row['close'])
    if (!isWeekday(weekday) || !isClock(open) || !isClock(close)) {
      continue
    }
    if ((parseClock(close) ?? 0) <= (parseClock(open) ?? 0)) {
      continue
    }
    if (days.some((day) => day.weekday === weekday)) {
      continue
    }
    days.push({ weekday, open, close })
  }
  return days.sort(compareDays)
}

/** Reads `segments`, dropping rows that a readable week could not contain. */
export function normalizeSegments(raw: unknown): DaySegment[] {
  const rows = Array.isArray(raw) ? raw : []
  const segments: DaySegment[] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const weekday = row['weekday']
    const start = text(row['start'])
    const end = text(row['end'])
    if (!isWeekday(weekday) || !isClock(start) || !isClock(end)) {
      continue
    }
    if ((parseClock(end) ?? 0) <= (parseClock(start) ?? 0)) {
      continue
    }
    segments.push({ weekday, start, end })
  }
  return sortedSegments(segments)
}

/**
 * `GET /salons/:id/hours`. `custom` is the flag of the server: `false` means the
 * salon uses the single opening window of `/settings` every day.
 */
export function normalizeSalonHours(raw: unknown): SalonHours {
  if (!isRecord(raw)) {
    return { days: [], custom: false }
  }
  return { days: normalizeDays(raw['days']), custom: raw['custom'] === true }
}

/**
 * `GET /salons/:id/members/:uid/hours`. No rows means the professional follows
 * the salon, so the rows decide `custom`: an empty list can never be "custom"
 * (S-F2d1-2).
 */
export function normalizeMemberHours(raw: unknown): MemberHours {
  if (!isRecord(raw)) {
    return { segments: [], custom: false }
  }
  const segments = normalizeSegments(raw['segments'])
  return { segments, custom: segments.length > 0 }
}

/** Seven editable rows; a closed day keeps the opening window as its draft. */
export function weekDraft(
  days: readonly SalonDay[],
  defaults: { open: string; close: string },
): DraftDay[] {
  const open = isClock(defaults.open) ? defaults.open : '09:00'
  const close = isClock(defaults.close) ? defaults.close : '19:00'
  return WEEKDAYS.map((weekday) => {
    const row = days.find((day) => day.weekday === weekday)
    if (row) {
      return { weekday, open: row.open, close: row.close, closed: false }
    }
    return { weekday, open, close, closed: true }
  })
}

/** Open days of the draft, ready for `PUT /salons/:id/hours` (sorted by day). */
export function draftToDays(draft: readonly DraftDay[]): SalonDay[] {
  return draft
    .filter((day) => !day.closed)
    .map((day) => ({ weekday: day.weekday, open: day.open, close: day.close }))
    .sort(compareDays)
}

export function setDraftDay(
  draft: readonly DraftDay[],
  weekday: Weekday,
  patch: Partial<Pick<DraftDay, 'open' | 'close' | 'closed'>>,
): DraftDay[] {
  return draft.map((day) => (day.weekday === weekday ? { ...day, ...patch } : day))
}

/** Copies one day (open/closed and its times) to the chosen weekdays. */
export function copyDraftDay(
  draft: readonly DraftDay[],
  from: Weekday,
  targets: readonly Weekday[],
): DraftDay[] {
  const source = draft.find((day) => day.weekday === from)
  if (!source) {
    return [...draft]
  }
  return draft.map((day) =>
    targets.includes(day.weekday)
      ? { weekday: day.weekday, open: source.open, close: source.close, closed: source.closed }
      : day,
  )
}

/** Validates the salon draft against the server rules before sending it. */
export function validateDraft(draft: readonly DraftDay[]): HourCheck<SalonDay[]> {
  const days: SalonDay[] = []
  for (const day of draft) {
    if (!isWeekday(day.weekday)) {
      return { ok: false, weekday: day.weekday, message: HOURS_MESSAGES.invalidDay }
    }
    if (day.closed) {
      continue
    }
    if (!isClock(day.open) || !isClock(day.close)) {
      return { ok: false, weekday: day.weekday, message: HOURS_MESSAGES.format }
    }
    if ((parseClock(day.close) ?? 0) <= (parseClock(day.open) ?? 0)) {
      return { ok: false, weekday: day.weekday, message: HOURS_MESSAGES.order }
    }
    days.push({ weekday: day.weekday, open: day.open, close: day.close })
  }
  return { ok: true, value: days.sort(compareDays) }
}

function groupWeek(labels: readonly string[]): string {
  const parts: string[] = []
  let start = 0
  for (let index = 1; index <= labels.length; index += 1) {
    if (index < labels.length && labels[index] === labels[start]) {
      continue
    }
    const first = WEEKDAY_SHORT[(start + 1) as Weekday]
    const last = WEEKDAY_SHORT[index as Weekday]
    const name = index - start === 1 ? first : `${first} a ${last.toLowerCase()}`
    parts.push(`${name} ${labels[start] ?? 'cerrado'}`)
    start = index
  }
  return parts.join(' · ')
}

function clockRange(open: string, close: string): string {
  return `${formatClockShort(parseClock(open) ?? 0)}–${formatClockShort(parseClock(close) ?? 0)}`
}

/**
 * Text of a week: `Lun a vie 9:00–19:00 · Sáb 9:00–14:00 · Dom cerrado`.
 * Equal consecutive days are grouped; a day without rows is closed.
 */
export function weekSummary(days: readonly SalonDay[]): string {
  const labels = WEEKDAYS.map((weekday) => {
    const row = days.find((day) => day.weekday === weekday)
    return row ? clockRange(row.open, row.close) : 'cerrado'
  })
  return groupWeek(labels)
}

/** Segments of one weekday, sorted by start time. */
export function segmentsForDay(
  segments: readonly DaySegment[],
  weekday: number,
): DaySegment[] {
  return sortedSegments(segments.filter((segment) => segment.weekday === weekday))
}

/** Seven lists, Monday first. */
export function segmentsByDay(segments: readonly DaySegment[]): DaySegment[][] {
  return WEEKDAYS.map((weekday) => segmentsForDay(segments, weekday))
}

/** Replaces every segment of one day, keeping the rest of the week untouched. */
export function setDaySegments(
  segments: readonly DaySegment[],
  weekday: Weekday,
  rows: readonly { start: string; end: string }[],
): DaySegment[] {
  const rest = segments.filter((segment) => segment.weekday !== weekday)
  const next = rows.map((row) => ({ weekday, start: row.start, end: row.end }))
  return sortedSegments([...rest, ...next])
}

/** Adds a segment to one day; a full day (4 segments) is left untouched. */
export function addSegment(
  segments: readonly DaySegment[],
  weekday: Weekday,
  fallback: { start: string; end: string },
): DaySegment[] {
  const day = segmentsForDay(segments, weekday)
  if (day.length >= MAX_SEGMENTS_PER_DAY) {
    return sortedSegments(segments)
  }
  return setDaySegments(segments, weekday, [
    ...day.map((row) => ({ start: row.start, end: row.end })),
    fallback,
  ])
}

export function removeSegment(
  segments: readonly DaySegment[],
  weekday: Weekday,
  index: number,
): DaySegment[] {
  const day = segmentsForDay(segments, weekday)
  if (index < 0 || index >= day.length) {
    return sortedSegments(segments)
  }
  return setDaySegments(
    segments,
    weekday,
    day.filter((_, position) => position !== index).map((row) => ({
      start: row.start,
      end: row.end,
    })),
  )
}

/** Copies the segments of one day to other days (an empty source clears them). */
export function copySegmentsDay(
  segments: readonly DaySegment[],
  from: Weekday,
  targets: readonly Weekday[],
): DaySegment[] {
  const source = segmentsForDay(segments, from).map((row) => ({
    start: row.start,
    end: row.end,
  }))
  const rest = segments.filter((segment) => !targets.includes(segment.weekday as Weekday))
  const copies = targets.flatMap((weekday) =>
    source.map((row) => ({ weekday, start: row.start, end: row.end })),
  )
  return sortedSegments([...rest, ...copies])
}

export type CopyTarget = { value: string; label: string; weekdays: Weekday[] }

/** Options of "copiar este día a…": whole week, workweek, weekend or one day. */
export function copyTargets(weekday: Weekday): CopyTarget[] {
  const others = WEEKDAYS.filter((day) => day !== weekday)
  const candidates: CopyTarget[] = [
    { value: 'all', label: 'Todos los días', weekdays: [...others] },
    {
      value: 'weekdays',
      label: 'Lunes a viernes',
      weekdays: others.filter((day) => day <= 5),
    },
    {
      value: 'weekend',
      label: 'Sábado y domingo',
      weekdays: others.filter((day) => day >= 6),
    },
    ...others.map((day) => ({
      value: String(day),
      label: WEEKDAY_LABELS[day],
      weekdays: [day],
    })),
  ]
  return candidates.filter((target) => target.weekdays.length > 0)
}

/** Validates the segments of a member against the server rules. */
export function validateSegments(segments: readonly DaySegment[]): HourCheck<DaySegment[]> {
  for (const segment of segments) {
    if (!isWeekday(segment.weekday)) {
      return { ok: false, weekday: segment.weekday, message: HOURS_MESSAGES.invalidDay }
    }
  }
  for (const weekday of WEEKDAYS) {
    const day = segmentsForDay(segments, weekday)
    if (day.length > MAX_SEGMENTS_PER_DAY) {
      return { ok: false, weekday, message: HOURS_MESSAGES.tooMany }
    }
    for (let index = 0; index < day.length; index += 1) {
      const row = day[index]
      if (!row) {
        continue
      }
      if (!isClock(row.start) || !isClock(row.end)) {
        return { ok: false, weekday, message: HOURS_MESSAGES.format }
      }
      if ((parseClock(row.end) ?? 0) <= (parseClock(row.start) ?? 0)) {
        return { ok: false, weekday, message: HOURS_MESSAGES.order }
      }
      const previous = day[index - 1]
      if (previous && (parseClock(row.start) ?? 0) < (parseClock(previous.end) ?? 0)) {
        return { ok: false, weekday, message: HOURS_MESSAGES.overlap }
      }
    }
  }
  return { ok: true, value: sortedSegments(segments) }
}

/** Gaps between the segments of one day: the visible breaks. */
export function dayBreaks(
  segments: readonly DaySegment[],
  weekday: number,
): { start: string; end: string }[] {
  const day = segmentsForDay(segments, weekday)
  const breaks: { start: string; end: string }[] = []
  for (let index = 1; index < day.length; index += 1) {
    const previous = day[index - 1]
    const current = day[index]
    if (!previous || !current) {
      continue
    }
    if ((parseClock(current.start) ?? 0) > (parseClock(previous.end) ?? 0)) {
      breaks.push({ start: previous.end, end: current.start })
    }
  }
  return breaks
}

export function breakLabel(gap: { start: string; end: string }): string {
  return `Descanso ${clockRange(gap.start, gap.end)}`
}

/** `9:00–13:00, 14:00–19:00` of one day, or «Día libre». */
export function daySummary(segments: readonly DaySegment[], weekday: number): string {
  const day = segmentsForDay(segments, weekday)
  if (day.length === 0) {
    return 'Día libre'
  }
  return day.map((row) => clockRange(row.start, row.end)).join(', ')
}

/** Text of a professional week: equal consecutive days grouped, free days «libre». */
export function segmentsSummary(segments: readonly DaySegment[]): string {
  const labels = WEEKDAYS.map((weekday) => {
    const day = segmentsForDay(segments, weekday)
    if (day.length === 0) {
      return 'libre'
    }
    return day.map((row) => clockRange(row.start, row.end)).join(', ')
  })
  return groupWeek(labels)
}

/** No rows means the professional follows the salon schedule (S-F2d1-2). */
export function followsSalon(segments: readonly DaySegment[]): boolean {
  return segments.length === 0
}

/** The salon days as the starting point of a custom professional week. */
export function salonSegments(days: readonly SalonDay[]): DaySegment[] {
  return days
    .map((day) => ({ weekday: day.weekday, start: day.open, end: day.close }))
    .sort(compareSegments)
}

export function sameSegments(
  a: readonly DaySegment[],
  b: readonly DaySegment[],
): boolean {
  const left = sortedSegments(a)
  const right = sortedSegments(b)
  return (
    left.length === right.length &&
    left.every((row, index) => {
      const other = right[index]
      return (
        other !== undefined &&
        row.weekday === other.weekday &&
        row.start === other.start &&
        row.end === other.end
      )
    })
  )
}

/**
 * ISO weekday (1..7) of an instant in the zone of the salon: a moment near
 * midnight belongs to a different day in UTC than in the salon zone.
 */
export function todayWeekday(now: Date, timeZone: string): Weekday {
  const names = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
  try {
    const name = new Intl.DateTimeFormat('en-US', { timeZone, weekday: 'short' }).format(now)
    const index = names.indexOf(name)
    return (index === -1 ? 1 : index + 1) as Weekday
  } catch {
    const name = new Intl.DateTimeFormat('en-US', { timeZone: 'UTC', weekday: 'short' }).format(
      now,
    )
    const index = names.indexOf(name)
    return (index === -1 ? 1 : index + 1) as Weekday
  }
}

export type PublicHoursRow = {
  weekday: Weekday
  label: string
  /** `9:00 – 19:00` or «Cerrado». */
  time: string
  closed: boolean
  /** The day of today in the zone used to read the week. */
  isToday: boolean
}

/** Landing rows of the public `hours`: a day without rows is «Cerrado». */
export function publicHoursRows(days: readonly SalonDay[], today: Weekday): PublicHoursRow[] {
  return WEEKDAYS.map((weekday) => {
    const row = days.find((day) => day.weekday === weekday)
    return {
      weekday,
      label: WEEKDAY_LABELS[weekday],
      time: row
        ? `${formatClockShort(parseClock(row.open) ?? 0)} – ${formatClockShort(
            parseClock(row.close) ?? 0,
          )}`
        : 'Cerrado',
      closed: row === undefined,
      isToday: weekday === today,
    }
  })
}

/**
 * Optimistic save and its roll-back. The page shows `value` at once and keeps
 * `saved` so `rollbackSave` restores exactly what the server had before.
 */
export type OptimisticSave<T> = { readonly value: T; readonly saved: T }

export function beginSave<T>(saved: T, value: T): OptimisticSave<T> {
  return { value, saved }
}

export function commitSave<T>(_state: OptimisticSave<T>, saved: T): OptimisticSave<T> {
  return { value: saved, saved }
}

export function rollbackSave<T>(state: OptimisticSave<T>): OptimisticSave<T> {
  return { value: state.saved, saved: state.saved }
}

/** Stable order used by the editor when it lists the days of a draft. */
export function sortDraft(draft: readonly DraftDay[]): DraftDay[] {
  return [...draft].sort((a, b) => a.weekday - b.weekday)
}
