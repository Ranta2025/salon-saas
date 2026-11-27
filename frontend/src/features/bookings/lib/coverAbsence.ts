/**
 * Covering an absence by reassigning its turns (contract F2d3, S-F2d3-2 shape and
 * S-F2d3-5). Pure rules plus the thin API client: `/panel/equipo` lists the
 * affected turns of each absence, the calendar offers the same select on a turn
 * of an absent professional, and "Mi agenda" tells the covered professional that
 * somebody else took her turn.
 *
 * The server stays the authority: it decides who may take a turn (S-F2d3-3) and
 * its sentences are shown verbatim. The rules below only render the choice the
 * server describes — a flag it does not send is never turned into a rejection.
 */

import { request } from '../../../lib/api.ts'
import { TIME_ZONE, formatTimeOfDay, toDateInputValue } from '../../../lib/datetime.ts'
import { overlapsRange } from './timeOff.ts'
import { beginSave, rollbackSave, type OptimisticSave } from '../../team/lib/weeklyHours.ts'
import type { Booking } from '../../../types.ts'

/** Sentences of the API (S-F2d3-3), shown verbatim when the server rejects. */
export const COVER_MESSAGES = {
  /** The destination is not bookable (F2a). */
  notBookable: 'Esa persona no atiende clientes',
  /** The destination has an absence that covers the turn (F2d2). */
  absent: 'Esa persona no está de turno ese día',
  /** The destination does not do the service of the turn (F2c). */
  noService: 'Esa persona no hace ese servicio',
  /** The request names nobody to reassign to. */
  noCandidate: 'No hay a quién reasignar',
  /** The destination already has a turn at that time (the EXCLUDE decides). */
  busy: 'Ya tiene un turno a esa hora',
  /** UI only: the server said "no" without saying why (it always words it today). */
  unavailable: 'No puede tomar el turno',
  /** Somebody else moved or cancelled the turn first. */
  alreadyMoved: 'El turno ya no está reservado',
  /** The turn is not one of the turns of that absence. */
  notFromAbsence: 'Ese turno no es de esa ausencia',
} as const

/**
 * What the server says about one possible destination of a turn. The API of
 * S-F2d3-2 already answers `available` with the sentence that explains a "no"
 * (`reason`), so those two are the authority; the older flags (`bookable`,
 * `does_service`, `absent`) are read when the server sends them and `null` means
 * "the server did not say", never a rejection.
 */
export type CoverCandidate = {
  id: string
  name: string
  /** Server verdict: `false` disables the option, `true` is a go-ahead. */
  available: boolean | null
  /** Sentence of the server for a "no", shown verbatim. */
  reason: string
  does_service: boolean | null
  bookable: boolean | null
  absent: boolean | null
}

/** One `booked` turn that falls inside an absence. */
export type CoverBooking = {
  id: string
  client_name: string
  starts_at: string
  ends_at: string
  /** Service of the turn; `null` means it has none and anybody bookable can take it. */
  service_id: string | null
  candidates: CoverCandidate[]
}

/** One absence with the turns it puts in danger (`GET /salons/:id/absences`). */
export type CoverAbsence = {
  id: string
  user_id: string
  /** Name of the absent professional; empty when the payload does not carry it. */
  user_name: string
  starts_at: string
  ends_at: string
  reason: string
  bookings: CoverBooking[]
}

/** One row of the select: who may take the turn and, if not, why. */
export type CoverChoice = {
  id: string
  name: string
  eligible: boolean
  /** Empty when the person is eligible; the "why not" label otherwise. */
  reason: string
}

/** Answer of both reassign endpoints (`{reassigned, bookings}`). */
export type CoverOutcome = {
  reassigned: number
  bookings: { id: string; client_name: string; starts_at: string }[]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Reads a tri-state flag: only a real boolean is an answer. */
function flag(...values: unknown[]): boolean | null {
  for (const value of values) {
    if (typeof value === 'boolean') {
      return value
    }
  }
  return null
}

function candidatesOf(raw: unknown): CoverCandidate[] {
  const rows = Array.isArray(raw) ? raw : []
  const list: CoverCandidate[] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const id = text(row['id']) || text(row['professional_id'])
    if (id === '') {
      continue
    }
    list.push({
      id,
      name: text(row['name']) || text(row['user_name']) || id,
      available: flag(row['available']),
      reason: text(row['reason']),
      does_service: flag(row['does_service'], row['serves_service']),
      bookable: flag(row['bookable'], row['attends']),
      absent: flag(row['absent'], row['on_time_off']),
    })
  }
  return list
}

function bookingsOf(raw: unknown): CoverBooking[] {
  const rows = Array.isArray(raw) ? raw : []
  const list: CoverBooking[] = []
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
    const serviceId = text(row['service_id'])
    list.push({
      id,
      client_name: text(row['client_name']),
      starts_at: startsAt,
      ends_at: endsAt,
      service_id: serviceId === '' ? null : serviceId,
      candidates: candidatesOf(row['candidates'] ?? row['professionals']),
    })
  }
  return list.sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))
}

/** Reads `{absences:[…]}` (or a bare array) and drops rows a screen could not use. */
export function normalizeCoverAbsences(raw: unknown): CoverAbsence[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['absences'])
      ? raw['absences']
      : []
  const list: CoverAbsence[] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const id = text(row['id']) || text(row['time_off_id'])
    const userId = text(row['user_id'])
    const startsAt = text(row['starts_at'])
    const endsAt = text(row['ends_at'])
    const start = Date.parse(startsAt)
    const end = Date.parse(endsAt)
    if (id === '' || userId === '' || Number.isNaN(start) || Number.isNaN(end) || end <= start) {
      continue
    }
    list.push({
      id,
      user_id: userId,
      user_name: text(row['user_name']) || text(row['name']),
      starts_at: startsAt,
      ends_at: endsAt,
      reason: text(row['reason']),
      bookings: bookingsOf(row['bookings']),
    })
  }
  return list.sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))
}

/** The absence that lists a booking, or null when the turn is not affected. */
export function absenceForBooking(
  absences: readonly CoverAbsence[],
  bookingId: string,
): CoverAbsence | null {
  return coverBookingOfAll(absences, bookingId)?.absence ?? null
}

/** The turn as the cover payload describes it, with its absence. */
export function coverBookingOf(
  absence: CoverAbsence,
  bookingId: string,
): CoverBooking | null {
  return absence.bookings.find((booking) => booking.id === bookingId) ?? null
}

function coverBookingOfAll(
  absences: readonly CoverAbsence[],
  bookingId: string,
): { absence: CoverAbsence; booking: CoverBooking } | null {
  const id = bookingId.trim()
  if (id === '') {
    return null
  }
  for (const absence of absences) {
    const booking = absence.bookings.find((row) => row.id === id)
    if (booking) {
      return { absence, booking }
    }
  }
  return null
}

/** «Ningún turno afectado» / «1 turno afectado» / «3 turnos afectados». */
export function affectedTurnsLabel(count: number): string {
  const safe = Number.isFinite(count) ? Math.max(0, Math.trunc(count)) : 0
  if (safe === 0) {
    return 'Ningún turno afectado'
  }
  return safe === 1 ? '1 turno afectado' : `${safe} turnos afectados`
}

type AbsenceLike = { user_id: string; starts_at: string; ends_at: string }

/** Whether somebody else's absence (or the server flag) keeps her away that day. */
function isAway(
  candidate: CoverCandidate,
  window: { starts_at: string; ends_at: string },
  absences: readonly AbsenceLike[],
): boolean {
  if (candidate.absent === true) {
    return true
  }
  return absences.some((entry) => entry.user_id === candidate.id && overlapsRange(entry, window))
}

/**
 * The "why not" label of one candidate. The sentence of the server wins (it is
 * the one the POST would answer); its verdict wins too. Without either, the rules
 * of S-F2d3-3 fill in: not bookable, away that day, and — only when the turn has
 * a service — does not do it.
 */
function rejectReason(booking: CoverBooking, candidate: CoverCandidate, away: boolean): string {
  if (candidate.reason !== '') {
    return candidate.reason
  }
  if (candidate.available === true) {
    return ''
  }
  const local = (): string => {
    if (candidate.bookable === false) {
      return COVER_MESSAGES.notBookable
    }
    if (away) {
      return COVER_MESSAGES.absent
    }
    if (booking.service_id !== null && candidate.does_service === false) {
      return COVER_MESSAGES.noService
    }
    return ''
  }
  const reason = local()
  if (reason !== '') {
    return reason
  }
  return candidate.available === false ? COVER_MESSAGES.unavailable : ''
}

function toChoice(candidate: CoverCandidate, reason: string): CoverChoice {
  return { id: candidate.id, name: candidate.name, eligible: reason === '', reason }
}

/**
 * Every destination of one turn, eligible first, each with the label that
 * explains why it is disabled (S-F2d3-5). The order of the server is kept among
 * the eligible ones; a person the server does not describe keeps her name only.
 */
export function coverCandidates(input: {
  booking: CoverBooking
  absence: CoverAbsence
  /** Absences of the salon (`GET /time-off`), used when the server sends no flag. */
  absences?: readonly AbsenceLike[]
}): CoverChoice[] {
  const window = { starts_at: input.absence.starts_at, ends_at: input.absence.ends_at }
  // The absent professional is never her own cover, even if the server lists her.
  const candidates = input.booking.candidates.filter(
    (candidate) => candidate.id !== input.absence.user_id,
  )
  const choices = candidates.map((candidate) =>
    toChoice(candidate, rejectReason(input.booking, candidate, isAway(candidate, window, input.absences ?? []))),
  )
  return [...choices].sort((a, b) => Number(b.eligible) - Number(a.eligible))
}

/**
 * Destinations of «Reasignar todo»: only who can take every turn of the absence,
 * with the reason of the first turn that person cannot take.
 */
export function coverAllCandidates(input: {
  absence: CoverAbsence
  absences?: readonly AbsenceLike[]
}): CoverChoice[] {
  const first = input.absence.bookings[0]
  if (!first) {
    return []
  }
  const perTurn = input.absence.bookings.map((booking) =>
    coverCandidates({ booking, absence: input.absence, absences: input.absences }),
  )
  const all = perTurn[0] ?? []
  return all
    .map((choice) => {
      const failing = perTurn.find((turn) => turn.find((row) => row.id === choice.id)?.eligible === false)
      const row = failing?.find((candidate) => candidate.id === choice.id)
      return row && row.reason !== '' ? { ...choice, eligible: false, reason: row.reason } : choice
    })
    .sort((a, b) => Number(b.eligible) - Number(a.eligible))
}

/** Label of one option: the name, or the name and the reason when disabled. */
export function candidateOptionLabel(choice: CoverChoice): string {
  return choice.eligible ? choice.name : `${choice.name} — no disponible: ${choice.reason}`
}

/** «Caro: … · Dani: …» for the note under the select; empty when all fit. */
export function ineligibleSummary(choices: readonly CoverChoice[]): string {
  return choices
    .filter((choice) => !choice.eligible)
    .map((choice) => `${choice.name}: ${choice.reason}`)
    .join(' · ')
}

/** First person that can take the turn, used as the default of a select. */
export function firstEligible(choices: readonly CoverChoice[]): CoverChoice | null {
  return choices.find((choice) => choice.eligible) ?? null
}

/** «1 turno reasignado a Beto.» / «3 turnos reasignados a Beto.» */
export function reassignedNotice(count: number, professionalName: string): string {
  const safe = Number.isFinite(count) ? Math.max(0, Math.trunc(count)) : 0
  const name = professionalName.trim()
  if (safe === 0) {
    return 'No se reasignó ningún turno.'
  }
  return safe === 1
    ? `1 turno reasignado a ${name}.`
    : `${safe} turnos reasignados a ${name}.`
}

// --- Optimistic select state ---

/** Chosen professional per booking id; `saved` is what the server has. */
export type CoverDraft = OptimisticSave<Record<string, string>>

/** Starts a batch: nothing chosen yet, and the saved map is today's truth. */
export function beginCover(current: Record<string, string> = {}): CoverDraft {
  return beginSave({ ...current }, { ...current })
}

/** One select: the row shows the choice before the server answers. */
export function chooseFor(draft: CoverDraft, bookingId: string, professionalId: string): CoverDraft {
  const id = bookingId.trim()
  if (id === '') {
    return draft
  }
  return { value: { ...draft.value, [id]: professionalId.trim() }, saved: draft.saved }
}

/** «Reasignar todo»: the whole list of turns flips at once. */
export function chooseForAll(
  draft: CoverDraft,
  bookingIds: readonly string[],
  professionalId: string,
): CoverDraft {
  const value = { ...draft.value }
  for (const id of bookingIds) {
    const key = id.trim()
    if (key !== '') {
      value[key] = professionalId.trim()
    }
  }
  return { value, saved: draft.saved }
}

/** Choice shown in the select of a turn (empty when nobody was chosen). */
export function chosenFor(draft: CoverDraft, bookingId: string): string {
  return draft.value[bookingId.trim()] ?? ''
}

/** Rejected reassignment: only that turn goes back to what the server has. */
export function forgetChoice(draft: CoverDraft, bookingId: string): CoverDraft {
  const value = { ...draft.value }
  const key = bookingId.trim()
  const saved = draft.saved[key]
  if (saved === undefined) {
    delete value[key]
  } else {
    value[key] = saved
  }
  return { value, saved: draft.saved }
}

/** The whole batch went back: exactly the saved map, nobody else touched. */
export function rollbackCover(draft: CoverDraft): CoverDraft {
  return rollbackSave(draft)
}

// --- My agenda (S-F2d3-5) ---

/**
 * Id of the professional the turn was moved away from. The API of S-F2d3-2 does
 * not define the field on the booking yet, so the plausible names are read and a
 * payload without any of them simply yields no notice (never an invention).
 */
function coveredFrom(booking: Booking): string {
  const row = booking as unknown as Record<string, unknown>
  return (
    text(row['covered_from']) || text(row['reassigned_from']) || text(row['previous_professional_id'])
  )
}

/** One covered turn as "Mi agenda" prints it. */
export type CoverNotice = { bookingId: string; text: string }

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

/** «del 24 de diciembre a las 10:00», «de hoy a las 10:00». */
function coveredWhen(startsAt: string, timeZone: string, now: Date): string {
  const date = toDateInputValue(new Date(startsAt), timeZone)
  const hour = formatTimeOfDay(startsAt, timeZone)
  if (date === toDateInputValue(now, timeZone)) {
    return `de hoy a las ${hour}`
  }
  const [, month = '01', day = '01'] = date.split('-')
  return `del ${Number(day)} de ${MONTHS[Number(month) - 1] ?? ''} a las ${hour}`
}

/**
 * Turns of mine that somebody else covers now (S-F2d3-5). The server marks them
 * with `covered_from` (the professional the turn was moved away from); without
 * that field the list is empty, never an invention.
 */
export function coveredNotices(
  bookings: readonly Booking[],
  userId: string,
  options: { timeZone?: string; now: Date },
): CoverNotice[] {
  const id = userId.trim()
  if (id === '') {
    return []
  }
  const timeZone = options.timeZone ?? TIME_ZONE
  return bookings
    .filter((booking) => {
      const from = coveredFrom(booking)
      if (from !== id || booking.professional_id === id) {
        return false
      }
      if (booking.status !== 'booked' && booking.status !== '') {
        return false
      }
      const end = Date.parse(booking.ends_at)
      return !Number.isNaN(end) && end > options.now.getTime()
    })
    .sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))
    .map((booking) => ({
      bookingId: booking.id,
      text: `Tu turno ${coveredWhen(booking.starts_at, timeZone, options.now)} lo cubre otra profesional.`,
    }))
}

// --- API client (contract S-F2d3-2) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function normalizeOutcome(raw: unknown): CoverOutcome {
  if (!isRecord(raw)) {
    return { reassigned: 0, bookings: [] }
  }
  const count = Number(raw['reassigned'])
  const rows = Array.isArray(raw['bookings']) ? raw['bookings'] : []
  const bookings: CoverOutcome['bookings'] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const id = text(row['id'])
    if (id === '') {
      continue
    }
    bookings.push({
      id,
      client_name: text(row['client_name']),
      starts_at: text(row['starts_at']),
    })
  }
  return {
    reassigned: Number.isFinite(count) ? Math.max(0, Math.trunc(count)) : bookings.length,
    bookings,
  }
}

/** `GET /salons/:id/absences?from&to`: absences of the range with their turns. */
export async function fetchCoverAbsences(
  salonId: string,
  from: string,
  to: string,
  signal?: AbortSignal,
): Promise<CoverAbsence[]> {
  const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
  return normalizeCoverAbsences(
    await request<unknown>(salonPath(salonId, `/absences${query}`), { signal }),
  )
}

/** `POST /salons/:id/absences/:tid/reassign`: every turn of the absence at once. */
export async function reassignAbsence(
  salonId: string,
  absenceId: string,
  professionalId: string,
): Promise<CoverOutcome> {
  return normalizeOutcome(
    await request<unknown>(
      salonPath(salonId, `/absences/${encodeURIComponent(absenceId)}/reassign`),
      { method: 'POST', body: { professional_id: professionalId.trim() } },
    ),
  )
}

/** `POST /salons/:id/bookings/:bid/reassign`: one single turn. */
export async function reassignBooking(
  salonId: string,
  bookingId: string,
  professionalId: string,
): Promise<CoverOutcome> {
  return normalizeOutcome(
    await request<unknown>(
      salonPath(salonId, `/bookings/${encodeURIComponent(bookingId)}/reassign`),
      { method: 'POST', body: { professional_id: professionalId.trim() } },
    ),
  )
}

/** `POST …/reassign {"cancel":true}`: the salon cancels and calls the client. */
export async function cancelBookingForAbsence(
  salonId: string,
  bookingId: string,
): Promise<CoverOutcome> {
  return normalizeOutcome(
    await request<unknown>(
      salonPath(salonId, `/bookings/${encodeURIComponent(bookingId)}/reassign`),
      { method: 'POST', body: { cancel: true } },
    ),
  )
}
