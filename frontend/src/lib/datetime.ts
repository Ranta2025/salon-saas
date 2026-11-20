/** Date/time helpers for the day calendar. Everything is computed in the salon timezone. */

/** Timezone of the salon. Single place to change it when multi-timezone lands. */
export const TIME_ZONE = 'America/Mexico_City'

export type Booking = {
  id: string
  professional_id: string
  client_name: string
  starts_at: string
  ends_at: string
  status: string
}

export type BookingLike = {
  professional_id: string
  starts_at: string
  ends_at: string
}

export type ProfessionalRef = { id: string; name: string }

export type ProfessionalGroup<T extends BookingLike> = {
  id: string
  name: string
  bookings: T[]
}

export type TimelineWindow = { startHour: number; endHour: number }

/** Minutes of a service day that the timeline renders. */
export const DEFAULT_WINDOW: TimelineWindow = { startHour: 9, endHour: 20 }

const partsFormatter = new Map<string, Intl.DateTimeFormat>()

function formatter(timeZone: string): Intl.DateTimeFormat {
  const cached = partsFormatter.get(timeZone)
  if (cached) {
    return cached
  }
  const created = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
  partsFormatter.set(timeZone, created)
  return created
}

type WallClock = { year: number; month: number; day: number; hour: number; minute: number; second: number }

function wallClock(date: Date, timeZone: string): WallClock {
  const parts = formatter(timeZone).formatToParts(date)
  const read = (type: Intl.DateTimeFormatPartTypes): number => {
    const part = parts.find((p) => p.type === type)
    return part ? Number(part.value) : 0
  }
  const hour = read('hour')
  return {
    year: read('year'),
    month: read('month'),
    day: read('day'),
    hour: hour === 24 ? 0 : hour,
    minute: read('minute'),
    second: read('second'),
  }
}

function offsetMs(date: Date, timeZone: string): number {
  const wall = wallClock(date, timeZone)
  const asUtc = Date.UTC(wall.year, wall.month - 1, wall.day, wall.hour, wall.minute, wall.second)
  return asUtc - date.getTime()
}

/** UTC instant of a local midnight, DST safe (re-reads the offset once). */
function localMidnightUtc(year: number, month: number, day: number, timeZone: string): Date {
  const guess = new Date(Date.UTC(year, month - 1, day, 0, 0, 0))
  const firstOffset = offsetMs(guess, timeZone)
  const candidate = new Date(guess.getTime() - firstOffset)
  const secondOffset = offsetMs(candidate, timeZone)
  return secondOffset === firstOffset ? candidate : new Date(guess.getTime() - secondOffset)
}

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

/** RFC3339 window covering the requested local calendar day. */
export function dayRange(date: Date, timeZone: string = TIME_ZONE): { from: string; to: string } {
  const wall = wallClock(date, timeZone)
  const start = localMidnightUtc(wall.year, wall.month, wall.day, timeZone)
  // Calendar arithmetic on the local day; localMidnightUtc normalises month overflow.
  const end = localMidnightUtc(wall.year, wall.month, wall.day + 1, timeZone)
  return { from: start.toISOString(), to: end.toISOString() }
}

/** `2025-03-14` value for `<input type="date">` in the salon timezone. */
export function toDateInputValue(date: Date, timeZone: string = TIME_ZONE): string {
  const wall = wallClock(date, timeZone)
  return `${wall.year}-${pad(wall.month)}-${pad(wall.day)}`
}

/** Moves a `YYYY-MM-DD` value by whole days (calendar arithmetic, DST safe). */
export function addDaysToDateValue(value: string, days: number): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) {
    throw new TypeError(`Fecha inválida: ${value}`)
  }
  const [, year = '1970', month = '01', day = '01'] = match
  const shifted = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day) + days))
  return `${shifted.getUTCFullYear()}-${pad(shifted.getUTCMonth() + 1)}-${pad(shifted.getUTCDate())}`
}

/** RFC3339 instant of a local calendar day coming from `<input type="date">`. */
export function fromDateInputValue(value: string, timeZone: string = TIME_ZONE): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) {
    throw new TypeError(`Fecha inválida: ${value}`)
  }
  const [, year = '1970', month = '01', day = '01'] = match
  return localMidnightUtc(Number(year), Number(month), Number(day), timeZone).toISOString()
}

/** Minutes elapsed since local midnight (used for timeline geometry). */
export function minutesFromMidnight(iso: string, timeZone: string = TIME_ZONE): number {
  const wall = wallClock(new Date(iso), timeZone)
  return wall.hour * 60 + wall.minute
}

/** `09:30` in salon local time. */
export function formatTimeOfDay(iso: string, timeZone: string = TIME_ZONE): string {
  const minutes = minutesFromMidnight(iso, timeZone)
  return `${pad(Math.floor(minutes / 60))}:${pad(minutes % 60)}`
}

/** `9` → `09:00` for timeline rulers. */
export function formatHour(hour: number): string {
  return `${pad(hour)}:00`
}

/**
 * Fractional position of `[startMinutes, endMinutes)` inside the visible window.
 * Values outside the window are clamped instead of rendering out of bounds.
 */
export function positionInWindow(
  startMinutes: number,
  endMinutes: number,
  window: TimelineWindow = DEFAULT_WINDOW,
): { offset: number; duration: number } {
  const windowStart = window.startHour * 60
  const windowEnd = window.endHour * 60
  const span = windowEnd - windowStart
  if (span <= 0) {
    return { offset: 0, duration: 0 }
  }
  const clampedStart = Math.min(Math.max(startMinutes, windowStart), windowEnd)
  const clampedEnd = Math.min(Math.max(endMinutes, clampedStart), windowEnd)
  return {
    offset: (clampedStart - windowStart) / span,
    duration: (clampedEnd - clampedStart) / span,
  }
}

/**
 * Groups appointments by professional, keeping the configured order and adding
 * professionals that only appear in the bookings (server knows more staff than us).
 */
export function groupByProfessional<T extends BookingLike>(
  bookings: readonly T[],
  professionals: readonly ProfessionalRef[],
): ProfessionalGroup<T>[] {
  const groups: ProfessionalGroup<T>[] = professionals.map((p) => ({ id: p.id, name: p.name, bookings: [] }))
  const byId = new Map(groups.map((g) => [g.id, g]))
  for (const booking of bookings) {
    let group = byId.get(booking.professional_id)
    if (!group) {
      group = { id: booking.professional_id, name: `Profesional ${booking.professional_id}`, bookings: [] }
      byId.set(group.id, group)
      groups.push(group)
    }
    group.bookings.push(booking)
  }
  for (const group of groups) {
    group.bookings.sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))
  }
  return groups
}

export type PositionedBlock<T> = T & { column: number; columns: number }

/**
 * Lays out overlapping blocks in columns so the day calendar never hides an
 * appointment behind another one.
 */
export function overlapLayout<T extends { id: string; start: number; end: number }>(
  blocks: readonly T[],
): PositionedBlock<T>[] {
  const sorted = [...blocks].sort((a, b) => a.start - b.start || a.end - b.end)
  const result: PositionedBlock<T>[] = []
  let cluster: PositionedBlock<T>[] = []
  let clusterEnd = Number.NEGATIVE_INFINITY

  const flush = (): void => {
    const columns = cluster.reduce((max, block) => Math.max(max, block.column + 1), 0)
    for (const block of cluster) {
      block.columns = columns
      result.push(block)
    }
    cluster = []
  }

  for (const block of sorted) {
    if (block.start >= clusterEnd) {
      flush()
      clusterEnd = block.end
    } else {
      clusterEnd = Math.max(clusterEnd, block.end)
    }
    const used = new Set(cluster.filter((b) => b.end > block.start).map((b) => b.column))
    let column = 0
    while (used.has(column)) {
      column += 1
    }
    const positioned: PositionedBlock<T> = { ...block, column, columns: 1 }
    cluster.push(positioned)
  }
  flush()
  return result
}
