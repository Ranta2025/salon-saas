import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError, NetworkError } from './api.ts'
import { clearToken, setToken } from './session.ts'
import {
  BookingAttempt,
  availabilityQuery,
  bookingPayload,
  createPublicBooking,
  describeBookingError,
  fetchAvailability,
  formatSlotRange,
  formatSlotTime,
  groupSlotsByTime,
  normalizeAvailability,
  normalizePublicBooking,
  timeZoneLabel,
  visitorTimeZone,
} from './publicBooking.ts'

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

// --- availability payload -----------------------------------------------

test('normalizeAvailability reads UTC slots and drops rows without a professional or dates', () => {
  const slots = normalizeAvailability({
    date: '2025-03-14',
    slots: [
      { professional_id: 'u2', starts_at: '2025-03-14T16:00:00Z', ends_at: '2025-03-14T16:45:00Z' },
      { professional_id: 'u3', starts_at: '2025-03-14T17:00:00Z', ends_at: '2025-03-14T17:45:00Z' },
      { professional_id: '', starts_at: '2025-03-14T18:00:00Z', ends_at: '2025-03-14T18:45:00Z' },
      { professional_id: 'u4', starts_at: 'no-es-fecha', ends_at: '2025-03-14T19:45:00Z' },
      null,
    ],
  })
  assert.deepEqual(slots, [
    { professionalId: 'u2', startsAt: '2025-03-14T16:00:00Z', endsAt: '2025-03-14T16:45:00Z' },
    { professionalId: 'u3', startsAt: '2025-03-14T17:00:00Z', endsAt: '2025-03-14T17:45:00Z' },
  ])
})

test('normalizeAvailability never throws on a bad payload', () => {
  assert.deepEqual(normalizeAvailability(null), [])
  assert.deepEqual(normalizeAvailability({ slots: 'nope' }), [])
})

// --- slot grouping and local time --------------------------------------

test('formatSlotTime renders the slot in the requested zone, not in UTC', () => {
  assert.equal(formatSlotTime('2025-03-14T16:00:00Z', 'America/Mexico_City'), '10:00')
  assert.equal(formatSlotTime('2025-03-14T16:00:00Z', 'UTC'), '16:00')
  assert.equal(formatSlotRange('2025-03-14T16:00:00Z', '2025-03-14T17:30:00Z', 'America/Mexico_City'), '10:00–11:30')
})

test('groupSlotsByTime groups slots of the same local time and keeps a stable order', () => {
  const slots = normalizeAvailability({
    slots: [
      { professional_id: 'u3', starts_at: '2025-03-14T17:00:00Z', ends_at: '2025-03-14T17:45:00Z' },
      { professional_id: 'u2', starts_at: '2025-03-14T16:00:00Z', ends_at: '2025-03-14T16:45:00Z' },
      { professional_id: 'u1', starts_at: '2025-03-14T16:00:00Z', ends_at: '2025-03-14T16:45:00Z' },
    ],
  })
  const groups = groupSlotsByTime(slots, 'America/Mexico_City')
  assert.deepEqual(
    groups.map((group) => ({ time: group.time, ids: group.slots.map((slot) => slot.professionalId) })),
    [
      { time: '10:00', ids: ['u2', 'u1'] },
      { time: '11:00', ids: ['u3'] },
    ],
  )
  assert.equal(groups[0]?.startsAt, '2025-03-14T16:00:00Z')
})

test('groupSlotsByTime groups the same instant even when the strings differ', () => {
  const groups = groupSlotsByTime(
    [
      { professionalId: 'u1', startsAt: '2025-03-14T16:00:00Z', endsAt: '2025-03-14T16:45:00Z' },
      { professionalId: 'u2', startsAt: '2025-03-14T16:00:00.000Z', endsAt: '2025-03-14T16:45:00.000Z' },
    ],
    'UTC',
  )
  assert.equal(groups.length, 1)
  assert.equal(groups[0]?.slots.length, 2)
})

test('timeZoneLabel names the zone shown to the visitor and falls back to the raw id', () => {
  assert.equal(timeZoneLabel('America/Mexico_City'), 'Ciudad de México')
  assert.equal(timeZoneLabel('UTC'), 'UTC')
  assert.equal(timeZoneLabel('Pacific/Chatham'), 'Pacific/Chatham')
})

// --- availability query and client --------------------------------------

test('availabilityQuery encodes the day, service and optional professional', () => {
  assert.equal(
    availabilityQuery({ date: '2025-03-14', serviceId: 'svc-1' }),
    '?date=2025-03-14&service_id=svc-1',
  )
  assert.equal(
    availabilityQuery({ date: '2025-03-14', serviceId: 'svc-1', professionalId: 'u2' }),
    '?date=2025-03-14&service_id=svc-1&professional_id=u2',
  )
})

test('fetchAvailability calls the anonymous endpoint with the injected fetch', async () => {
  const stub = stubFetch(() =>
    json({
      date: '2025-03-14',
      slots: [{ professional_id: 'u2', starts_at: '2025-03-14T16:00:00Z', ends_at: '2025-03-14T16:45:00Z' }],
    }),
  )
  try {
    setToken('jwt-should-not-travel')
    const slots = await fetchAvailability('aurelia', { date: '2025-03-14', serviceId: 'svc-1' })
    assert.equal(
      stub.calls[0]?.url,
      `${API_BASE}/api/v1/public/salons/aurelia/availability?date=2025-03-14&service_id=svc-1`,
    )
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
    assert.equal(slots.length, 1)
    assert.equal(slots[0]?.professionalId, 'u2')
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- public booking client ---------------------------------------------

const bookingInput = {
  serviceId: 'svc-1',
  clientName: 'Ana Ruiz',
  clientPhone: '5512345678',
  startsAt: '2025-03-14T16:00:00Z',
}

test('bookingPayload sends the snake_case body of the contract with the client_op_id', () => {
  const payload = bookingPayload(bookingInput, 'op-1')
  assert.deepEqual(payload, {
    service_id: 'svc-1',
    client_name: 'Ana Ruiz',
    client_phone: '5512345678',
    starts_at: '2025-03-14T16:00:00Z',
    client_op_id: 'op-1',
  })
  assert.deepEqual(bookingPayload({ ...bookingInput, professionalId: 'u2' }, 'op-2').professional_id, 'u2')
})

test('bookingPayload only sends client_email when the visitor typed one', () => {
  const anonymous = bookingPayload(bookingInput, 'op-1')
  assert.equal('client_email' in anonymous, false)
  assert.equal(typeof anonymous.client_phone, 'string')

  const withEmail = bookingPayload({ ...bookingInput, clientEmail: ' Lia@Mail.com ' }, 'op-2')
  assert.equal(withEmail.client_email, 'lia@mail.com')
  assert.equal(withEmail.client_phone, '5512345678')

  assert.equal('client_email' in bookingPayload({ ...bookingInput, clientEmail: '   ' }, 'op-3'), false)
})

test('BookingAttempt keeps the same client_op_id across retries and renews it after a confirmation', () => {
  const attempt = new BookingAttempt(bookingInput, 'op-fixed')
  assert.equal(attempt.payload().client_op_id, 'op-fixed')
  // A failed request is retried with the same id: the server deduplicates it.
  assert.equal(attempt.payload().client_op_id, 'op-fixed')
  // Editing the form (another slot) is still the same attempt until it succeeds.
  attempt.update({ ...bookingInput, startsAt: '2025-03-14T17:00:00Z' })
  assert.equal(attempt.payload().client_op_id, 'op-fixed')
  assert.equal(attempt.payload().starts_at, '2025-03-14T17:00:00Z')
  attempt.renew()
  assert.notEqual(attempt.payload().client_op_id, 'op-fixed')
})

test('createPublicBooking returns the created booking from the 201 body', async () => {
  const stub = stubFetch(() =>
    json(
      {
        booking: {
          id: 'b-1',
          professional_id: 'u2',
          starts_at: '2025-03-14T16:00:00Z',
          ends_at: '2025-03-14T16:45:00Z',
          status: 'booked',
        },
      },
      201,
    ),
  )
  try {
    const created = await createPublicBooking('aurelia', bookingPayload(bookingInput, 'op-3'))
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/public/salons/aurelia/bookings`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      service_id: 'svc-1',
      client_name: 'Ana Ruiz',
      client_phone: '5512345678',
      starts_at: '2025-03-14T16:00:00Z',
      client_op_id: 'op-3',
    })
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
    assert.deepEqual(created, {
      id: 'b-1',
      professionalId: 'u2',
      startsAt: '2025-03-14T16:00:00Z',
      endsAt: '2025-03-14T16:45:00Z',
      status: 'booked',
    })
  } finally {
    stub.restore()
  }
})

test('a 409 means the slot was just taken and the visitor can pick another one', async () => {
  const stub = stubFetch(() => json({ error: 'ese horario ya no está disponible' }, 409))
  try {
    const error = await createPublicBooking('aurelia', bookingPayload(bookingInput, 'op-4')).then(
      () => null,
      (err: unknown) => err,
    )
    assert.ok(error instanceof ApiError)
    assert.equal((error as ApiError).status, 409)
    const failure = describeBookingError(error)
    assert.equal(failure.kind, 'slot_taken')
    assert.match(failure.message, /acaba de ocupar/)
  } finally {
    stub.restore()
  }
})

test('a 429 is reported as a rate limit with its own message', async () => {
  const stub = stubFetch(() => json({ error: 'demasiadas solicitudes' }, 429))
  try {
    const error = await createPublicBooking('aurelia', bookingPayload(bookingInput, 'op-5')).then(
      () => null,
      (err: unknown) => err,
    )
    const failure = describeBookingError(error)
    assert.equal(failure.kind, 'rate_limited')
    assert.match(failure.message, /demasiadas solicitudes/)
  } finally {
    stub.restore()
  }
})

test('describeBookingError keeps the validation message of a 400 and maps network failures', () => {
  const validation = describeBookingError(new ApiError(400, 'falta el teléfono del cliente'))
  assert.deepEqual(validation, { kind: 'validation', message: 'falta el teléfono del cliente' })

  const offline = describeBookingError(new NetworkError('No pudimos conectar con el servidor.'))
  assert.equal(offline.kind, 'network')
  assert.equal(offline.message, 'No pudimos conectar con el servidor.')

  const other = describeBookingError(new ApiError(500, 'error interno'))
  assert.equal(other.kind, 'unknown')
  assert.equal(other.message, 'error interno')

  const unknown = describeBookingError('boom')
  assert.equal(unknown.kind, 'unknown')
  assert.match(unknown.message, /No pudimos completar la reserva/)
})

test('normalizePublicBooking returns null when the body has no usable booking', () => {
  assert.equal(normalizePublicBooking(null), null)
  assert.equal(normalizePublicBooking({ booking: { id: '' } }), null)
})

test('timeZoneLabel explains the generic Etc/GMT ids of minimal environments', () => {
  assert.equal(timeZoneLabel('Etc/GMT+4'), 'UTC-4')
  assert.equal(timeZoneLabel('Etc/GMT-3'), 'UTC+3')
  assert.equal(timeZoneLabel('Etc/UTC'), 'UTC')
})

test('visitorTimeZone always returns a usable IANA id', () => {
  const zone = visitorTimeZone()
  assert.equal(typeof zone, 'string')
  assert.ok(zone.length > 0)
})

// ---------- F11 (D8, D13-b): the additive `payment` of the 201 ----------

const PAYMENT_RAW = {
  deposit_id: 'd1',
  amount_cents: 5400,
  expires_at: '2026-11-02T16:30:00Z',
  pay_url: 'https://app.test/pagar#charge_abc',
  simulated: true,
}

test('createPublicBooking keeps the `payment` of the 201 when the deposit policy is on', async () => {
  const stub = stubFetch(() =>
    json(
      {
        booking: {
          id: 'b-1',
          professional_id: 'u2',
          starts_at: '2025-03-14T16:00:00Z',
          ends_at: '2025-03-14T16:45:00Z',
          status: 'booked',
        },
        payment: PAYMENT_RAW,
      },
      201,
    ),
  )
  try {
    const created = await createPublicBooking('aurelia', bookingPayload(bookingInput, 'op-1'))
    assert.deepEqual(created.payment, {
      depositId: 'd1',
      amountCents: 5400,
      expiresAt: '2026-11-02T16:30:00Z',
      payUrl: 'https://app.test/pagar#charge_abc',
      simulated: true,
    })
    // The confirmation screen of today is untouched: the booking fields are the same.
    assert.equal(created.id, 'b-1')
    assert.equal(created.status, 'booked')
  } finally {
    stub.restore()
  }
})

test('without `payment` the confirmation screen is exactly as today', async () => {
  const stub = stubFetch(() =>
    json({ booking: { id: 'b-1', starts_at: '2025-03-14T16:00:00Z', ends_at: '2025-03-14T16:45:00Z' } }, 201),
  )
  try {
    const created = await createPublicBooking('aurelia', bookingPayload(bookingInput, 'op-2'))
    assert.equal('payment' in created, false)
  } finally {
    stub.restore()
  }
})

test('a `payment` the page cannot use is dropped instead of painted', async () => {
  const stub = stubFetch(() =>
    json(
      {
        booking: { id: 'b-1', starts_at: '2025-03-14T16:00:00Z', ends_at: '2025-03-14T16:45:00Z' },
        payment: { deposit_id: '', amount_cents: 0 },
      },
      201,
    ),
  )
  try {
    const created = await createPublicBooking('aurelia', bookingPayload(bookingInput, 'op-3'))
    assert.equal('payment' in created, false)
  } finally {
    stub.restore()
  }
})
