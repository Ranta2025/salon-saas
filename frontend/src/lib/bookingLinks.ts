/**
 * Public `/turno` page: the single-use link of the booking emails (contract F1,
 * section «Contrato F1» of `odd/tasks/fase6-roadmap.md`). The shapes below are
 * the frozen ones the backend implements in parallel:
 *
 * - `POST /api/v1/public/booking-links/lookup {token}` → `{booking:{…}}`
 * - `POST /api/v1/public/booking-links/slots {token, date}` → the same slot
 *   items as `GET /public/salons/:slug/availability` (read with
 *   `normalizeAvailability`, so the shape lives in one place)
 * - `POST /api/v1/public/booking-links/cancel {token}` → `{status:"cancelled"}`
 * - `POST /api/v1/public/booking-links/reschedule {token, starts_at}` →
 *   `{booking:{…}, new_link_sent:bool}`; the new token NEVER travels in the
 *   answer (it only goes out by email).
 *
 * Security rules of the page (D1/D6, non negotiable):
 * - the token travels in the FRAGMENT of the link (`/turno#<token>`), so it never
 *   reaches a log; the page reads it once, wipes it from the address bar with
 *   `history.replaceState` BEFORE any request and keeps it only in memory:
 *   never localStorage, never sessionStorage, never a cookie, never the query
 *   string, never the path, never an error message;
 * - it goes out only in request BODIES (`{token, …}`);
 * - every failure that means "this link is dead" (404, used, expired, moved out
 *   of the window) gets the ONE generic sentence of the page: the UI never says
 *   which cause it was.
 *
 * Everything here is pure and testable; the server stays the authority on the
 * window of two hours, the availability and the change itself.
 */

import { ApiError, request } from './api.ts'
import { toDateInputValue } from './datetime.ts'
import {
  formatSlotRange,
  formatSlotTime,
  normalizeAvailability,
  timeZoneLabel,
  visitorTimeZone,
  type AvailabilitySlot,
} from './publicBooking.ts'

/**
 * Sentences of the contract. `invalid` is the ONE sentence of the page for a
 * link that cannot be used (it deliberately differs from the server's
 * `Enlace inválido o vencido`: the visitor needs to know what to do next, not
 * which of the four causes it was). `tooLate` is both the notice of
 * `can_change:false` and the 409 of the API, so it is written once.
 */
export const BOOKING_LINK_MESSAGES = {
  invalid: 'Este enlace ya se usó o el turno ya no está vigente',
  tooLate: 'Faltan menos de 2 horas para el turno: comunicate con el salón para cambiarlo',
  slotTaken: 'Ese horario ya no está disponible: elegí otro',
  sameTime: 'Elegí un horario distinto al actual',
  newLinkSent: 'Te mandamos un email con el nuevo enlace',
} as const

/** Labels of the two actions of a link that can still change. */
export const CHANGE_ACTION = 'Cambiar horario'
export const CANCEL_ACTION = 'Cancelar turno'

/** Results: only after the server confirmed the change (never optimistic). */
export const CANCEL_DONE = 'Tu turno quedó cancelado'
export const RESCHEDULE_DONE = 'Tu turno nuevo quedó reservado'

/** Confirmation of the cancel: it says out loud that it cannot be undone. */
export const CANCEL_CONFIRM_NOTE =
  'No se puede deshacer: el horario queda libre para otra persona y para recuperarlo hay que reservar de nuevo.'

/** What the page says when the email with the new link did not leave the server. */
export const NEW_LINK_FALLBACK_NOTE =
  'No pudimos enviar el email con el enlace nuevo: si necesitás cambiarlo otra vez, escribile al salón.'

/** Help of a dead link: how to get a new one (the old one cannot come back). */
export const INVALID_LINK_HELP =
  'El enlace sirve una sola vez. Pedile al salón un enlace nuevo para cambiar o cancelar el turno.'

/** The path prefix shared by the four public routes (D7: its own rate bucket). */
const LINKS_PATH = '/api/v1/public/booking-links'

/** `crypto/rand` gives 32 bytes → exactly 43 characters in base64url (D1). */
const TOKEN_PATTERN = /^[A-Za-z0-9_-]{43}$/

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function isInstant(value: string): boolean {
  return value !== '' && !Number.isNaN(Date.parse(value))
}

// --- the token of the fragment --------------------------------------------

/**
 * A token is exactly 43 base64url characters: no padding, no spaces, no
 * percent-encoding. Anything else in the fragment is not a token and is never
 * treated as one (and never requested).
 */
export function isBookingLinkToken(value: string): boolean {
  return TOKEN_PATTERN.test(value)
}

export type HashRead = { token: string | null; url: string }

/**
 * Reads the token of `location.hash` and returns the URL without it, ready for
 * `history.replaceState`. Only the FIRST fragment is the link slot of the
 * contract (`/turno#<token>`).
 *
 * The URL is cleaned in every case: a token that appears in another fragment is
 * removed and IGNORED (never used, never left behind for a later page to read),
 * a fragment without any token is junk and is dropped whole, and the anchors of
 * the rest stay. So a broken link leaves no trace and no secret in the history.
 */
export function readBookingLinkHash(href: string): HashRead {
  const hashIndex = href.indexOf('#')
  if (hashIndex === -1) {
    return { token: null, url: href }
  }
  const base = href.slice(0, hashIndex)
  const segments = href.slice(hashIndex + 1).split('#')
  const slot = segments.findIndex(isBookingLinkToken)
  const token = slot === 0 ? (segments[0] ?? null) : null
  // Never keep something that looks like a token in the address bar.
  const anchors = segments.filter(
    (segment, index) => index !== slot && segment !== '' && !isBookingLinkToken(segment),
  )
  const hash = slot === -1 || anchors.length === 0 ? '' : `#${anchors.join('#')}`
  return { token, url: `${base}${hash}` }
}

/** The two methods of `history` the page needs (a fake one is enough in tests). */
export type HistoryLike = {
  replaceState(data: unknown, unused: string, url?: string | null): void
}

/**
 * The page's first step: read the token and wipe it from the address bar in the
 * same tick, BEFORE any request. `null` means the link is not usable and the
 * page must not call the API at all.
 */
export function takeBookingLinkToken(href: string, history: HistoryLike | null): string | null {
  const read = readBookingLinkHash(href)
  if (read.url !== href && history) {
    history.replaceState(null, '', read.url)
  }
  return read.token
}

// --- the booking of the lookup (frozen shape of the contract) --------------

/**
 * The appointment as the link knows it: no client phone, no client email and no
 * internal ids (D6). `can_change:false` means less than `min_notice_hours` are
 * left: the appointment is still vigente, it just cannot be changed.
 */
export type BookingLinkBooking = {
  salon_name: string
  service_name: string
  professional_name: string
  starts_at: string
  ends_at: string
  /** IANA zone of the salon: every date and time of the page is shown in it. */
  timezone: string
  can_change: boolean
  min_notice_hours: number
}

export type BookingLinkLookup = { booking: BookingLinkBooking }

/** Answer of the reschedule: the new appointment and whether the email left. */
export type BookingLinkRescheduled = { booking: BookingLinkBooking; new_link_sent: boolean }

/** `min_notice_hours` of the contract (domain `LinkMinNoticeHours`). */
export const MIN_NOTICE_HOURS = 2

function isTimeZone(value: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value })
    return true
  } catch {
    return false
  }
}

/**
 * Zone where every date of the page is printed: the one the API sent with the
 * appointment. A missing or unusable zone falls back to the VISITOR zone, never
 * to a hard-coded salon (the page is public and any salon can send the link).
 */
export function salonTimeZone(value: unknown): string {
  const zone = text(value)
  return zone !== '' && isTimeZone(zone) ? zone : visitorTimeZone()
}

/** Hours of notice the API sent, or the constant of the contract. */
export function noticeHours(value: unknown): number {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0
    ? value
    : MIN_NOTICE_HOURS
}

/** Reads one booking of the contract; `null` when nothing usable came back. */
export function normalizeLinkBooking(raw: unknown): BookingLinkBooking | null {
  if (!isRecord(raw)) {
    return null
  }
  const source = isRecord(raw['booking']) ? raw['booking'] : raw
  const salonName = text(source['salon_name'])
  const startsAt = text(source['starts_at'])
  const endsAt = text(source['ends_at'])
  if (salonName === '' || !isInstant(startsAt) || !isInstant(endsAt)) {
    return null
  }
  return {
    salon_name: salonName,
    service_name: text(source['service_name']),
    professional_name: text(source['professional_name']),
    starts_at: startsAt,
    ends_at: endsAt,
    timezone: salonTimeZone(source['timezone']),
    // Only a real `true` opens the window: anything else hides both actions.
    can_change: source['can_change'] === true,
    min_notice_hours: noticeHours(source['min_notice_hours']),
  }
}

/** Reads `{booking:{…}}`; `null` when the answer cannot be shown. */
export function normalizeLinkLookup(raw: unknown): BookingLinkLookup | null {
  const booking = normalizeLinkBooking(raw)
  return booking ? { booking } : null
}

/** Reads `{booking:{…}, new_link_sent:bool}` of the reschedule. */
export function normalizeReschedule(raw: unknown): BookingLinkRescheduled | null {
  if (!isRecord(raw)) {
    return null
  }
  const booking = normalizeLinkBooking(raw['booking'])
  if (!booking) {
    return null
  }
  return { booking, new_link_sent: raw['new_link_sent'] === true }
}

// --- dates and times, always in the zone of the salon ---------------------

const dayFormatters = new Map<string, Intl.DateTimeFormat>()

function dayFormatter(timeZone: string): Intl.DateTimeFormat {
  const cached = dayFormatters.get(timeZone)
  if (cached) {
    return cached
  }
  const created = new Intl.DateTimeFormat('es-MX', {
    timeZone,
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  })
  dayFormatters.set(timeZone, created)
  return created
}

/** `sábado, 14 de marzo de 2026` in `timeZone`; empty when the instant is unusable. */
export function formatLinkDay(iso: string, timeZone: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return ''
  }
  return dayFormatter(timeZone).format(date)
}

/** `sábado, 14 de marzo de 2026, 11:00–12:00`; the start alone without a usable end. */
export function formatLinkWhen(
  booking: { starts_at: string; ends_at: string },
  timeZone: string,
): string {
  const day = formatLinkDay(booking.starts_at, timeZone)
  const endsAt = typeof booking.ends_at === 'string' ? booking.ends_at : ''
  const range = isInstant(endsAt)
    ? formatSlotRange(booking.starts_at, endsAt, timeZone)
    : formatSlotTime(booking.starts_at, timeZone)
  return day === '' ? range : `${day}, ${range}`
}

/** `Bogotá (America/Bogota)`: the zone is printed so nobody guesses it. */
export function formatLinkZone(timeZone: string): string {
  return `${timeZoneLabel(timeZone)} (${timeZone})`
}

const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/

/** `YYYY-MM-DD`, the `date` the slots endpoint expects (one local salon day). */
export function isDateValue(value: string): boolean {
  return DATE_PATTERN.test(value)
}

/** Today in the zone of the SALON (never the fixed constant of `datetime.ts`). */
export function defaultPickDate(now: Date, timeZone: string): string {
  return toDateInputValue(now, timeZone)
}

// --- copy of the flow ----------------------------------------------------

/** Hint of how long the link can be used in the zone of the salon. */
export function cutOffNotice(hours: number): string {
  if (hours <= 0) {
    return 'Podés cambiar o cancelar hasta el momento del turno'
  }
  return `Podés cambiar o cancelar hasta ${hours} ${hours === 1 ? 'hora' : 'horas'} antes del turno`
}

/** «¿Cancelar tu turno del …?»: names the appointment before destroying it. */
export function cancelConfirmQuestion(when: string): string {
  const label = when.trim()
  return label === '' ? '¿Cancelar tu turno?' : `¿Cancelar tu turno del ${label}?`
}

/** «¿Mover tu turno al …?»: names the new time before writing it. */
export function rescheduleConfirmQuestion(when: string): string {
  const label = when.trim()
  return label === '' ? '¿Mover tu turno a otro horario?' : `¿Mover tu turno al ${label}?`
}

/**
 * Sentence about the email with the new link. `false` is not an error of the
 * change (it already happened): it only says the link has to be asked for.
 */
export function newLinkNotice(newLinkSent: boolean): string {
  return newLinkSent ? BOOKING_LINK_MESSAGES.newLinkSent : NEW_LINK_FALLBACK_NOTE
}

/**
 * Copy of a failed request: a 404 is ALWAYS the one generic sentence of the
 * page (never the cause); every other status keeps the server sentence verbatim
 * (409 «Faltan menos de 2 horas…», 409 «Ese horario ya no está disponible…»,
 * 400 «Elegí un horario distinto al actual», the 429 of the rate limit).
 */
export function bookingLinkErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.status === 404) {
    return BOOKING_LINK_MESSAGES.invalid
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return fallback
}

// --- the state of the page: no optimistic change -------------------------

export type LinkStep = 'view' | 'slots' | 'confirm_cancel' | 'done_cancel' | 'done_rescheduled'

/**
 * What the page shows in the change flow. `booking`, `newLinkSent` and
 * `cancelled` are written ONLY when the server confirmed the change: the UI
 * never paints an optimistic outcome, because a 409 means the appointment did
 * not move and the link is still valid.
 */
export type LinkChangeState = {
  step: LinkStep
  /** Day chosen in the picker (`YYYY-MM-DD` of the salon zone). */
  date: string
  error: string | null
  cancelled: boolean
  /** New appointment of a confirmed reschedule. */
  booking: BookingLinkBooking | null
  newLinkSent: boolean
}

export type LinkChangeEvent =
  | { type: 'open_slots' }
  | { type: 'open_cancel' }
  | { type: 'back' }
  | { type: 'date'; date: string }
  | { type: 'failed'; message: string }
  | { type: 'cancelled' }
  | { type: 'rescheduled'; booking: BookingLinkBooking; newLinkSent: boolean }

export function initialLinkChange(): LinkChangeState {
  return {
    step: 'view',
    date: '',
    error: null,
    cancelled: false,
    booking: null,
    newLinkSent: false,
  }
}

/**
 * The token is consumed by a successful change and the link cannot come back
 * (the new one only arrives by email), so both success steps are terminal.
 */
export function isLinkTerminal(state: LinkChangeState): boolean {
  return state.step === 'done_cancel' || state.step === 'done_rescheduled'
}

/** Transitions of the flow; anything out of order is ignored, never guessed. */
export function reduceLinkChange(
  state: LinkChangeState,
  event: LinkChangeEvent,
): LinkChangeState {
  if (isLinkTerminal(state)) {
    return state
  }
  switch (event.type) {
    case 'open_slots':
      return { ...state, step: 'slots', error: null }
    case 'open_cancel':
      return { ...state, step: 'confirm_cancel', error: null }
    case 'back':
      return state.step === 'slots' || state.step === 'confirm_cancel'
        ? { ...state, step: 'view', error: null }
        : state
    case 'date':
      // A new day clears the message of the previous attempt.
      return { ...state, date: event.date, error: null }
    case 'failed':
      // The request failed: the page stays where it was and stays usable.
      return { ...state, error: event.message }
    case 'cancelled':
      return state.step === 'confirm_cancel'
        ? { ...state, step: 'done_cancel', cancelled: true, error: null }
        : state
    case 'rescheduled':
      return state.step === 'slots'
        ? {
            ...state,
            step: 'done_rescheduled',
            booking: event.booking,
            newLinkSent: event.newLinkSent,
            error: null,
          }
        : state
  }
}

export type LinkGuard = { canChange: boolean; busy: boolean }

/** «Cancelar turno» is only available with a changeable link and no request running. */
export function canCancel(guard: LinkGuard): boolean {
  return guard.canChange && !guard.busy
}

/** «Confirmar cambio» needs, besides the above, a chosen slot (a double click cannot repeat it). */
export function canReschedule(guard: LinkGuard & { hasSlot: boolean }): boolean {
  return guard.canChange && !guard.busy && guard.hasSlot
}

// --- API client (all four routes are POST with the token in the BODY) -----

type LinkRequestOptions = { signal?: AbortSignal }

/**
 * `POST /public/booking-links/lookup {token}`. An answer without an appointment
 * is treated as the same generic invalid link: the page never learns why.
 */
export async function lookupBookingLink(
  token: string,
  options: LinkRequestOptions = {},
): Promise<BookingLinkLookup> {
  const payload = await request<unknown>(`${LINKS_PATH}/lookup`, {
    method: 'POST',
    body: { token },
    auth: 'none',
    signal: options.signal,
  })
  const lookup = normalizeLinkLookup(payload)
  if (!lookup) {
    throw new ApiError(200, BOOKING_LINK_MESSAGES.invalid)
  }
  return lookup
}

/**
 * `POST /public/booking-links/slots {token, date}`: the free slots of ONE local
 * day of the salon for the same service and professional, excluding the
 * appointment that moves. Same item shape as the public availability.
 */
export async function fetchLinkSlots(
  token: string,
  date: string,
  options: LinkRequestOptions = {},
): Promise<AvailabilitySlot[]> {
  const payload = await request<unknown>(`${LINKS_PATH}/slots`, {
    method: 'POST',
    body: { token, date },
    auth: 'none',
    signal: options.signal,
  })
  return normalizeAvailability(payload)
}

/** `POST /public/booking-links/cancel {token}` → the `status` of the answer. */
export async function cancelBookingLink(
  token: string,
  options: LinkRequestOptions = {},
): Promise<string> {
  const payload = await request<unknown>(`${LINKS_PATH}/cancel`, {
    method: 'POST',
    body: { token },
    auth: 'none',
    signal: options.signal,
  })
  return isRecord(payload) ? text(payload['status']) : ''
}

/**
 * `POST /public/booking-links/reschedule {token, starts_at}`. The new link is
 * never in the answer: it goes out by email (`new_link_sent`).
 */
export async function rescheduleBookingLink(
  token: string,
  startsAt: string,
  options: LinkRequestOptions = {},
): Promise<BookingLinkRescheduled> {
  const payload = await request<unknown>(`${LINKS_PATH}/reschedule`, {
    method: 'POST',
    body: { token, starts_at: startsAt },
    auth: 'none',
    signal: options.signal,
  })
  const result = normalizeReschedule(payload)
  if (!result) {
    throw new ApiError(
      200,
      'El salón guardó el cambio pero no devolvió los datos del turno nuevo.',
    )
  }
  return result
}
