/**
 * Changes to an existing appointment (`PATCH /salons/:id/bookings/:bid`):
 * cancel, mark no-show, mark done or reschedule. Owner/admin (and reception)
 * may change any booking; a professional only their own — that check lives in
 * `permissions.ts` and the panel hides the buttons accordingly.
 */

import { ApiError, NetworkError, request } from '../../../lib/api.ts'
import { addMinutesToIso } from '../../../lib/datetime.ts'
import type { Booking } from '../../../types.ts'

export type BookingAction = 'cancel' | 'no_show' | 'done'

export type BookingPatch = {
  status?: string
  starts_at?: string
  ends_at?: string
  professional_id?: string
}

export const BOOKING_STATUS_LABEL: Record<string, string> = {
  booked: 'Agendada',
  done: 'Atendida',
  cancelled: 'Cancelada',
  no_show: 'No asistió',
}

export const STATUS_BY_ACTION: Record<BookingAction, string> = {
  cancel: 'cancelled',
  no_show: 'no_show',
  done: 'done',
}

export function statusLabel(status: string): string {
  return BOOKING_STATUS_LABEL[status] ?? status
}

/** Duration of the appointment in minutes (0 when the payload is unusable). */
export function bookingDurationMinutes(booking: { starts_at: string; ends_at: string }): number {
  const span = Date.parse(booking.ends_at) - Date.parse(booking.starts_at)
  return Number.isFinite(span) && span > 0 ? Math.round(span / 60_000) : 0
}

/** Rescheduling keeps the duration of the appointment and the professional. */
export function reschedulePayload(
  startsAt: string,
  durationMin: number,
  professionalId?: string,
): BookingPatch {
  const patch: BookingPatch = {
    starts_at: startsAt,
    ends_at: addMinutesToIso(startsAt, durationMin),
  }
  const professional = professionalId?.trim() ?? ''
  if (professional !== '') {
    patch.professional_id = professional
  }
  return patch
}

export function statusPayload(action: BookingAction): BookingPatch {
  return { status: STATUS_BY_ACTION[action] }
}

/** Closing actions the current status still allows (a closed booking has none). */
export function availableActions(status: string): BookingAction[] {
  if (status === 'booked' || status === '') {
    return ['cancel', 'no_show', 'done']
  }
  return []
}

/**
 * Only an open appointment can move. Cancelled, no-show and done ones keep their
 * history: offering "Reprogramar" there would rewrite what already happened.
 */
export function canReschedule(status: string): boolean {
  return status === 'booked' || status === ''
}

/** `1 cita` / `2 citas`: the counter of the calendar column head. */
export function bookingCountLabel(count: number): string {
  const safe = Number.isFinite(count) ? Math.max(0, Math.trunc(count)) : 0
  return `${safe} ${safe === 1 ? 'cita' : 'citas'}`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function toBooking(raw: unknown, fallbackId: string): Booking {
  const source = isRecord(raw) && isRecord(raw['booking']) ? raw['booking'] : isRecord(raw) ? raw : {}
  return {
    id: text(source['id']) || fallbackId,
    professional_id: text(source['professional_id']),
    client_name: text(source['client_name']),
    starts_at: text(source['starts_at']),
    ends_at: text(source['ends_at']),
    status: text(source['status']) || 'booked',
  }
}

export async function patchBooking(
  salonId: string,
  bookingId: string,
  patch: BookingPatch,
): Promise<Booking> {
  const path = `/api/v1/salons/${encodeURIComponent(salonId)}/bookings/${encodeURIComponent(bookingId)}`
  return toBooking(await request<unknown>(path, { method: 'PATCH', body: patch }), bookingId)
}

/** The update endpoint is permissioned, so its messages are user facing. */
export function describeBookingUpdateError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 403) {
      return 'Tu rol no permite cambiar este turno. Pedile a la administración del salón que lo haga.'
    }
    if (error.status === 404) {
      return 'Ese turno ya no existe. Actualizá la agenda para ver el estado actual.'
    }
    if (error.status === 409) {
      return 'Ese horario se cruza con otro turno de la misma persona. Elegí otra hora.'
    }
    return error.message
  }
  if (error instanceof NetworkError) {
    return error.message
  }
  return 'No pudimos actualizar el turno. Intentá de nuevo.'
}
