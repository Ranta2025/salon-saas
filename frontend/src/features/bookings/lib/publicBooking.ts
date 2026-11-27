/**
 * Public booking: `GET /public/salons/:slug/availability` and
 * `POST /public/salons/:slug/bookings` (no auth). Slots travel in UTC; the UI
 * shows them in the visitor's local time and says which zone it is showing.
 *
 * The `BookingAttempt` keeps the `client_op_id` while a request is retried, so
 * the server deduplicates a booking whose answer was lost by the network.
 */

import { ApiError, NetworkError, request } from '../../../lib/api.ts'
import { normalizeEmail } from '../../../lib/email.ts'
import { newClientOpId } from '../../../lib/offlineQueue.ts'
import { normalizeBookingPayment, type BookingPayment } from './deposits.ts'

export type AvailabilitySlot = {
  professionalId: string
  startsAt: string
  endsAt: string
}

export type SlotGroup = {
  /** Local time of the group, e.g. `10:00`. */
  time: string
  startsAt: string
  slots: AvailabilitySlot[]
}

export type AvailabilityQuery = {
  date: string
  serviceId: string
  professionalId?: string
}

export type PublicBookingInput = {
  serviceId: string
  professionalId?: string
  clientName: string
  clientPhone: string
  /** Optional: only sent when the visitor filled it (reminder/confirmation mail). */
  clientEmail?: string
  startsAt: string
}

export type PublicBookingPayload = {
  service_id: string
  professional_id?: string
  client_name: string
  client_phone: string
  client_email?: string
  starts_at: string
  client_op_id: string
}

export type PublicBookingCreated = {
  id: string
  professionalId: string
  startsAt: string
  endsAt: string
  status: string
  /**
   * `payment` of the 201 (F11, D8): present ONLY when the deposit policy is on and the
   * amount is greater than zero. Without it the confirmation screen is exactly as it
   * was before F11, so the field is optional.
   */
  payment?: BookingPayment
}

export type BookingFailureKind = 'slot_taken' | 'rate_limited' | 'validation' | 'network' | 'unknown'

export type BookingFailure = { kind: BookingFailureKind; message: string }

export const SLOT_TAKEN_MESSAGE =
  'Ese horario se acaba de ocupar. Actualizamos los horarios disponibles: elige otro y confirmamos de nuevo.'

export const RATE_LIMIT_MESSAGE =
  'Recibimos demasiadas solicitudes seguidas. Espera un momento y vuelve a intentar.'

const GENERIC_FAILURE_MESSAGE = 'No pudimos completar la reserva. Intenta de nuevo.'

/** Zones named in Spanish; any other IANA id is shown as it comes. */
const TIME_ZONE_LABELS: Record<string, string> = {
  UTC: 'UTC',
  'America/Mexico_City': 'Ciudad de México',
  'America/Monterrey': 'Monterrey',
  'America/Cancun': 'Cancún',
  'America/Tijuana': 'Tijuana',
  'America/Guatemala': 'Guatemala',
  'America/Costa_Rica': 'Costa Rica',
  'America/El_Salvador': 'El Salvador',
  'America/Panama': 'Panamá',
  'America/Bogota': 'Bogotá',
  'America/Lima': 'Lima',
  'America/Caracas': 'Caracas',
  'America/Santiago': 'Santiago de Chile',
  'America/Asuncion': 'Asunción',
  'America/Montevideo': 'Montevideo',
  'America/Argentina/Buenos_Aires': 'Buenos Aires',
  'America/Sao_Paulo': 'São Paulo',
  'America/La_Paz': 'La Paz',
  'America/Santo_Domingo': 'Santo Domingo',
  'America/Havana': 'La Habana',
  'America/Puerto_Rico': 'San Juan',
  'Europe/Madrid': 'Madrid',
  'Atlantic/Canary': 'Islas Canarias',
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function isInstant(value: string): boolean {
  return value !== '' && !Number.isNaN(Date.parse(value))
}

function normalizeSlot(value: unknown): AvailabilitySlot | null {
  if (!isRecord(value)) {
    return null
  }
  const professionalId = text(value['professional_id'])
  const startsAt = text(value['starts_at'])
  const endsAt = text(value['ends_at'])
  if (professionalId === '' || !isInstant(startsAt) || !isInstant(endsAt)) {
    return null
  }
  return { professionalId, startsAt, endsAt }
}

/** Reads `{date, slots:[…]}` and drops rows the UI cannot use. */
export function normalizeAvailability(raw: unknown): AvailabilitySlot[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['slots'])
      ? raw['slots']
      : []
  const slots: AvailabilitySlot[] = []
  for (const row of rows) {
    const slot = normalizeSlot(row)
    if (slot) {
      slots.push(slot)
    }
  }
  return slots
}

/** Reads `{booking:{…}}` into the shape the confirmation screen needs. */
export function normalizePublicBooking(raw: unknown): PublicBookingCreated | null {
  if (!isRecord(raw)) {
    return null
  }
  const source = isRecord(raw['booking']) ? raw['booking'] : raw
  const id = text(source['id'])
  if (id === '') {
    return null
  }
  const booking: PublicBookingCreated = {
    id,
    professionalId: text(source['professional_id']),
    startsAt: text(source['starts_at']),
    endsAt: text(source['ends_at']),
    status: text(source['status']) || 'booked',
  }
  // F11 (D8): additive, and only when the server sent a payment the page can show.
  const payment = normalizeBookingPayment(raw['payment'])
  if (payment) {
    booking.payment = payment
  }
  return booking
}

/** The visitor's own zone: the slots are shown in local time, not in UTC. */
export function visitorTimeZone(): string {
  try {
    const zone = new Intl.DateTimeFormat().resolvedOptions().timeZone
    return typeof zone === 'string' && zone !== '' ? zone : 'UTC'
  } catch {
    return 'UTC'
  }
}

export function timeZoneLabel(timeZone: string): string {
  const known = TIME_ZONE_LABELS[timeZone]
  if (known) {
    return known
  }
  // Bare environments resolve to `Etc/GMT+4`; IANA inverts the sign of those ids.
  const etc = /^Etc\/GMT([+-])(\d{1,2})$/.exec(timeZone)
  if (etc) {
    return `UTC${etc[1] === '+' ? '-' : '+'}${etc[2]}`
  }
  if (timeZone === 'Etc/UTC' || timeZone === 'Etc/GMT' || timeZone === 'GMT') {
    return 'UTC'
  }
  return timeZone
}

const formatters = new Map<string, Intl.DateTimeFormat>()

function timeFormatter(timeZone: string): Intl.DateTimeFormat {
  const cached = formatters.get(timeZone)
  if (cached) {
    return cached
  }
  const created = new Intl.DateTimeFormat('es-MX', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  formatters.set(timeZone, created)
  return created
}

/** `16:00Z` in `America/Mexico_City` → `10:00`. */
export function formatSlotTime(iso: string, timeZone: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return '--:--'
  }
  return timeFormatter(timeZone).format(date)
}

/** `10:00–11:30`, using the en dash of the rest of the UI. */
export function formatSlotRange(startsAt: string, endsAt: string, timeZone: string): string {
  return `${formatSlotTime(startsAt, timeZone)}–${formatSlotTime(endsAt, timeZone)}`
}

/**
 * Groups the free slots by their local start time: one selectable button per
 * time window, the professionals behind it stay in `slots`. The API returns the
 * same instant with different string spellings, so grouping compares instants.
 */
export function groupSlotsByTime(
  slots: readonly AvailabilitySlot[],
  timeZone: string,
): SlotGroup[] {
  const groups = new Map<string, SlotGroup>()
  const sorted = [...slots].sort((a, b) => Date.parse(a.startsAt) - Date.parse(b.startsAt))
  for (const slot of sorted) {
    const time = formatSlotTime(slot.startsAt, timeZone)
    const existing = groups.get(time)
    if (existing) {
      existing.slots.push(slot)
      continue
    }
    groups.set(time, { time, startsAt: slot.startsAt, slots: [slot] })
  }
  return [...groups.values()]
}

export function availabilityQuery(query: AvailabilityQuery): string {
  const params = [
    `date=${encodeURIComponent(query.date)}`,
    `service_id=${encodeURIComponent(query.serviceId)}`,
  ]
  if (query.professionalId) {
    params.push(`professional_id=${encodeURIComponent(query.professionalId)}`)
  }
  return `?${params.join('&')}`
}

function availabilityPath(slug: string, query: AvailabilityQuery): string {
  return `/api/v1/public/salons/${encodeURIComponent(slug)}/availability${availabilityQuery(query)}`
}

function bookingsPath(slug: string): string {
  return `/api/v1/public/salons/${encodeURIComponent(slug)}/bookings`
}

/** Free slots of one day of the public catalogue (anonymous request). */
export async function fetchAvailability(
  slug: string,
  query: AvailabilityQuery,
  options: { fetchImpl?: typeof fetch; signal?: AbortSignal } = {},
): Promise<AvailabilitySlot[]> {
  const payload = await request<unknown>(availabilityPath(slug, query), {
    auth: 'none',
    fetchImpl: options.fetchImpl,
    signal: options.signal,
  })
  return normalizeAvailability(payload)
}

export function bookingPayload(input: PublicBookingInput, clientOpId: string): PublicBookingPayload {
  const payload: PublicBookingPayload = {
    service_id: input.serviceId,
    client_name: input.clientName.trim(),
    client_phone: input.clientPhone.trim(),
    starts_at: input.startsAt,
    client_op_id: clientOpId,
  }
  const professionalId = input.professionalId?.trim() ?? ''
  if (professionalId !== '') {
    payload.professional_id = professionalId
  }
  // An anonymous booking sends no client_email at all (the API treats it as
  // "no mail"); the phone stays a plain string, as the offline queue expects.
  const email = normalizeEmail(input.clientEmail ?? '')
  if (email !== '') {
    payload.client_email = email
  }
  return payload
}

/** Creates the booking; a 409 means somebody else took the slot first. */
export async function createPublicBooking(
  slug: string,
  payload: PublicBookingPayload,
  fetchImpl?: typeof fetch,
): Promise<PublicBookingCreated> {
  const body = await request<unknown>(bookingsPath(slug), {
    method: 'POST',
    body: payload,
    auth: 'none',
    fetchImpl,
  })
  const booking = normalizePublicBooking(body)
  if (!booking) {
    throw new ApiError(200, 'El salón confirmó la reserva pero no devolvió los datos del turno.')
  }
  return booking
}

/**
 * A booking attempt: the form of the landing keeps one instance while the person
 * fixes a phone number or retries after a network failure, so the `client_op_id`
 * (and therefore the server-side deduplication) survives every retry.
 */
export class BookingAttempt {
  private id: string
  private current: PublicBookingInput

  constructor(input: PublicBookingInput, clientOpId: string = newClientOpId()) {
    this.current = input
    this.id = clientOpId
  }

  get clientOpId(): string {
    return this.id
  }

  payload(): PublicBookingPayload {
    return bookingPayload(this.current, this.id)
  }

  /** Another slot or service: still the same attempt and the same op id. */
  update(input: PublicBookingInput): void {
    this.current = input
  }

  /** The booking was created: the next one must use a fresh op id. */
  renew(): string {
    this.id = newClientOpId()
    return this.id
  }
}

/** Maps a failed booking request to the copy the landing shows. */
export function describeBookingError(error: unknown): BookingFailure {
  if (error instanceof ApiError) {
    if (error.status === 409) {
      return { kind: 'slot_taken', message: SLOT_TAKEN_MESSAGE }
    }
    if (error.status === 429) {
      return { kind: 'rate_limited', message: RATE_LIMIT_MESSAGE }
    }
    if (error.status === 400 || error.status === 422) {
      return { kind: 'validation', message: error.message }
    }
    return { kind: 'unknown', message: error.message }
  }
  if (error instanceof NetworkError) {
    return { kind: 'network', message: error.message }
  }
  return { kind: 'unknown', message: GENERIC_FAILURE_MESSAGE }
}
