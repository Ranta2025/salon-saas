import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError, NetworkError } from '../../../lib/api.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  BOOKING_STATUS_LABEL,
  bookingCountLabel,
  availableActions,
  bookingDurationMinutes,
  canReschedule,
  describeBookingUpdateError,
  patchBooking,
  reschedulePayload,
  statusLabel,
} from './bookingActions.ts'

type Call = { url: string; method: string; headers: Record<string, string>; body: string | undefined }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: typeof init?.body === 'string' ? init.body : undefined,
    }
    calls.push(call)
    return handler(call)
  }) as typeof globalThis.fetch
  return {
    calls,
    restore: () => {
      globalThis.fetch = original
    },
  }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

test('statusLabel names every booking status in Spanish', () => {
  assert.deepEqual({ ...BOOKING_STATUS_LABEL }, {
    booked: 'Agendada',
    done: 'Atendida',
    cancelled: 'Cancelada',
    no_show: 'No asistió',
  })
  assert.equal(statusLabel('booked'), 'Agendada')
  assert.equal(statusLabel('otro'), 'otro')
})

test('availableActions offers the three closing actions only for a booked appointment', () => {
  assert.deepEqual(availableActions('booked'), ['cancel', 'no_show', 'done'])
  assert.deepEqual(availableActions('done'), [])
  assert.deepEqual(availableActions('cancelled'), [])
  assert.deepEqual(availableActions('no_show'), [])
  assert.equal(canReschedule('booked'), true)
  assert.equal(canReschedule(''), true)
  // A closed appointment keeps its history: moving it would rewrite the past.
  assert.equal(canReschedule('cancelled'), false)
  assert.equal(canReschedule('done'), false)
  assert.equal(canReschedule('no_show'), false)
})

test('reschedulePayload keeps the duration of the appointment', () => {
  const booking = {
    starts_at: '2025-03-14T16:00:00Z',
    ends_at: '2025-03-14T17:30:00Z',
  }
  assert.equal(bookingDurationMinutes(booking), 90)
  assert.deepEqual(reschedulePayload('2025-03-14T18:00:00Z', bookingDurationMinutes(booking)), {
    starts_at: '2025-03-14T18:00:00Z',
    ends_at: '2025-03-14T19:30:00.000Z',
  })
  assert.deepEqual(reschedulePayload('2025-03-14T18:00:00Z', 45, 'u2'), {
    starts_at: '2025-03-14T18:00:00Z',
    ends_at: '2025-03-14T18:45:00.000Z',
    professional_id: 'u2',
  })
})

test('patchBooking sends the status and returns the updated booking', async () => {
  const stub = stubFetch(() =>
    json({
      booking: {
        id: 'b-1',
        professional_id: 'u2',
        client_name: 'Ana',
        starts_at: '2025-03-14T16:00:00Z',
        ends_at: '2025-03-14T16:45:00Z',
        status: 'cancelled',
      },
    }),
  )
  try {
    setToken('jwt-booking')
    const booking = await patchBooking('s1', 'b-1', { status: 'cancelled' })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/bookings/b-1`)
    assert.equal(stub.calls[0]?.method, 'PATCH')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { status: 'cancelled' })
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-booking')
    assert.equal(booking.status, 'cancelled')
    assert.equal(booking.client_name, 'Ana')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('patchBooking sends a reschedule body without losing the professional', async () => {
  const stub = stubFetch(() => json({ booking: { id: 'b-1', status: 'booked' } }))
  try {
    setToken('jwt-booking')
    await patchBooking('s1', 'b-1', reschedulePayload('2025-03-14T18:00:00Z', 30, 'u3'))
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      starts_at: '2025-03-14T18:00:00Z',
      ends_at: '2025-03-14T18:30:00.000Z',
      professional_id: 'u3',
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('describeBookingUpdateError explains a 403 instead of showing the raw API text', () => {
  const forbidden = describeBookingUpdateError(new ApiError(403, 'sin acceso al salón'))
  assert.match(forbidden, /rol no permite/)

  const overlap = describeBookingUpdateError(new ApiError(409, 'el turno se cruza con otro'))
  assert.match(overlap, /cruz|otro turno/i)

  const invalid = describeBookingUpdateError(new ApiError(400, 'starts_at en el pasado'))
  assert.equal(invalid, 'starts_at en el pasado')

  const network = describeBookingUpdateError(new NetworkError('No pudimos conectar con el servidor.'))
  assert.equal(network, 'No pudimos conectar con el servidor.')

  assert.match(describeBookingUpdateError('boom'), /No pudimos actualizar el turno/)
})

test('bookingCountLabel agrees the number with cita/citas', () => {
  assert.equal(bookingCountLabel(0), '0 citas')
  assert.equal(bookingCountLabel(1), '1 cita')
  assert.equal(bookingCountLabel(2), '2 citas')
  assert.equal(bookingCountLabel(11), '11 citas')
})
