import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError } from '../../../lib/api.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  COVER_MESSAGES,
  absenceForBooking,
  affectedTurnsLabel,
  beginCover,
  candidateOptionLabel,
  cancelBookingForAbsence,
  chooseFor,
  chooseForAll,
  chosenFor,
  coverAllCandidates,
  coverBookingOf,
  coverCandidates,
  coveredNotices,
  fetchCoverAbsences,
  firstEligible,
  forgetChoice,
  ineligibleSummary,
  normalizeCoverAbsences,
  reassignAbsence,
  reassignBooking,
  reassignedNotice,
  rollbackCover,
  type CoverAbsence,
  type CoverBooking,
  type CoverCandidate,
} from './coverAbsence.ts'
import type { Booking } from '../../../types.ts'

const ZONE = 'America/Mexico_City'
const NOW = new Date('2025-12-20T18:00:00.000Z')

// Whole local days of America/Mexico_City: the 24th and the 25th of December.
const DEC_24 = { starts_at: '2025-12-24T06:00:00.000Z', ends_at: '2025-12-26T06:00:00.000Z' }
const TEN_AM = { starts_at: '2025-12-24T16:00:00.000Z', ends_at: '2025-12-24T17:00:00.000Z' }

function candidate(over: Partial<CoverCandidate> & Pick<CoverCandidate, 'id' | 'name'>): CoverCandidate {
  return { available: null, reason: '', does_service: true, bookable: true, absent: false, ...over }
}

function coverBooking(over: Partial<CoverBooking> = {}): CoverBooking {
  return {
    id: 'b1',
    client_name: 'Ana',
    ...TEN_AM,
    service_id: 's1',
    candidates: [
      candidate({ id: 'p2', name: 'Beto' }),
      candidate({ id: 'p3', name: 'Caro', does_service: false }),
      candidate({ id: 'p4', name: 'Dani', absent: true }),
      candidate({ id: 'p5', name: 'Eva', bookable: false }),
    ],
    ...over,
  }
}

function coverAbsence(over: Partial<CoverAbsence> = {}): CoverAbsence {
  return {
    id: 't1',
    user_id: 'p1',
    user_name: 'Ana',
    ...DEC_24,
    reason: 'Vacaciones',
    bookings: [coverBooking()],
    ...over,
  }
}

test('normalizeCoverAbsences reads the contract and drops rows a screen could not use', () => {
  const rows = normalizeCoverAbsences({
    absences: [
      {
        id: 't1',
        user_id: 'p1',
        user_name: ' Ana ',
        ...DEC_24,
        reason: ' Vacaciones ',
        bookings: [
          {
            id: 'b1',
            client_name: ' Ana ',
            ...TEN_AM,
            service_id: 's1',
            candidates: [
              { id: 'p2', name: 'Beto', does_service: true },
              { id: 'p3', name: 'Caro', does_service: false },
              { id: '', name: 'Sin id' },
            ],
          },
          { id: '', client_name: 'Sin id', ...TEN_AM },
        ],
      },
      // No id, no person or unusable range: never rendered.
      { id: '', user_id: 'p1', ...DEC_24 },
      { id: 't2', user_id: '', ...DEC_24 },
      { id: 't3', user_id: 'p2', starts_at: 'nope', ends_at: DEC_24.ends_at },
      { id: 't4', user_id: 'p2', starts_at: DEC_24.ends_at, ends_at: DEC_24.starts_at },
    ],
  })
  assert.equal(rows.length, 1)
  assert.deepEqual(
    { id: rows[0]?.id, user_name: rows[0]?.user_name, reason: rows[0]?.reason },
    { id: 't1', user_name: 'Ana', reason: 'Vacaciones' },
  )
  assert.equal(rows[0]?.bookings.length, 1)
  assert.deepEqual(rows[0]?.bookings[0]?.candidates.map((row) => row.id), ['p2', 'p3'])
  // A missing service id is "the booking has no service": null, never "".
  assert.equal(normalizeCoverAbsences(null).length, 0)
  assert.equal(
    normalizeCoverAbsences({ absences: [{ id: 't9', user_id: 'p1', ...DEC_24, bookings: [] }] })[0]
      ?.bookings.length,
    0,
  )
  assert.equal(
    normalizeCoverAbsences({ absences: [{ id: 't9', user_id: 'p1', ...TEN_AM, bookings: [{ id: 'b1', ...TEN_AM }] }] })[0]
      ?.bookings[0]?.service_id,
    null,
  )
})

test('normalizeCoverAbsences sorts by start and reads the alias of the payload', () => {
  const rows = normalizeCoverAbsences({
    absences: [
      { id: 't2', user_id: 'p1', starts_at: '2025-12-27T06:00:00.000Z', ends_at: '2025-12-28T06:00:00.000Z' },
      { time_off_id: 't1', user_id: 'p1', ...DEC_24, bookings: [{ id: 'b1', ...TEN_AM }] },
    ],
  })
  assert.deepEqual(rows.map((row) => row.id), ['t1', 't2'])
  // A booking nobody names is printed as «Cliente sin nombre», never guessed.
  assert.equal(rows[0]?.bookings[0]?.client_name, '')
})

test('absenceForBooking finds the absence that lists a booking', () => {
  const absence = coverAbsence()
  assert.equal(absenceForBooking([absence], 'b1')?.id, 't1')
  assert.equal(absenceForBooking([absence], 'b9'), null)
  assert.equal(absenceForBooking([], 'b1'), null)
  assert.equal(coverBookingOf(absence, 'b1')?.client_name, 'Ana')
  assert.equal(coverBookingOf(absence, 'b9'), null)
})

test('affectedTurnsLabel uses the right plural', () => {
  assert.equal(affectedTurnsLabel(0), 'Ningún turno afectado')
  assert.equal(affectedTurnsLabel(1), '1 turno afectado')
  assert.equal(affectedTurnsLabel(3), '3 turnos afectados')
  assert.equal(affectedTurnsLabel(Number.NaN), 'Ningún turno afectado')
  assert.equal(affectedTurnsLabel(-2), 'Ningún turno afectado')
})

test('normalizeCoverAbsences reads the candidate shape of the API (available + reason)', () => {
  const rows = normalizeCoverAbsences({
    absences: [
      {
        id: 't1',
        user_id: 'p1',
        user_name: 'Ana',
        ...DEC_24,
        reason: 'Vacaciones',
        bookings: [
          {
            id: 'b1',
            client_name: 'Ana',
            ...TEN_AM,
            service_id: 's1',
            service_name: 'Coloración completa',
            candidates: [
              { professional_id: 'p2', name: 'Beto', available: true, reason: '' },
              { professional_id: 'p3', name: 'Caro', available: false, reason: 'Esa persona no hace ese servicio' },
            ],
          },
        ],
      },
    ],
  })
  const booking = rows[0]?.bookings[0]
  assert.deepEqual(booking?.candidates, [
    { id: 'p2', name: 'Beto', available: true, reason: '', does_service: null, bookable: null, absent: null },
    {
      id: 'p3',
      name: 'Caro',
      available: false,
      reason: 'Esa persona no hace ese servicio',
      does_service: null,
      bookable: null,
      absent: null,
    },
  ])
})

test('coverCandidates prints the reason of the server and trusts its verdict', () => {
  const absence = coverAbsence()
  const booking = coverBooking({
    candidates: [
      candidate({ id: 'p2', name: 'Beto', available: false, reason: 'Esa persona no hace ese servicio' }),
      // The server says she is available although the local list of absences says otherwise.
      candidate({ id: 'p3', name: 'Caro', available: true, absent: true }),
    ],
  })
  const choices = coverCandidates({
    booking,
    absence,
    absences: [{ user_id: 'p3', ...TEN_AM }],
  })
  assert.deepEqual(choices, [
    { id: 'p3', name: 'Caro', eligible: true, reason: '' },
    { id: 'p2', name: 'Beto', eligible: false, reason: 'Esa persona no hace ese servicio' },
  ])
})

test('coverCandidates explains a rejection the server did not word', () => {
  const absence = coverAbsence()
  const booking = coverBooking({
    candidates: [
      candidate({ id: 'p2', name: 'Beto', available: false, does_service: null, bookable: null, absent: null }),
      candidate({ id: 'p3', name: 'Caro', available: false, bookable: false }),
    ],
  })
  const choices = coverCandidates({ booking, absence })
  // Both disabled, in the order the server listed them.
  assert.deepEqual(choices[0], { id: 'p2', name: 'Beto', eligible: false, reason: COVER_MESSAGES.unavailable })
  assert.deepEqual(choices[1], { id: 'p3', name: 'Caro', eligible: false, reason: COVER_MESSAGES.notBookable })
})

test('coveredNotices also reads the aliases of the covered professional', () => {
  const base = {
    id: 'b1',
    professional_id: 'p2',
    client_name: 'Ana',
    ...TEN_AM,
    status: 'booked',
  }
  const notices = coveredNotices(
    [
      { ...base, id: 'b1', reassigned_from: 'p1' } as unknown as Booking,
      { ...base, id: 'b2', previous_professional_id: 'p1' } as unknown as Booking,
    ],
    'p1',
    { timeZone: ZONE, now: NOW },
  )
  assert.deepEqual(notices.map((row) => row.bookingId), ['b1', 'b2'])
})

test('coverCandidates separates who can take the turn from who cannot', () => {
  const absence = coverAbsence()
  const choices = coverCandidates({ booking: coverBooking(), absence })
  assert.deepEqual(choices.map((choice) => choice.id), ['p2', 'p3', 'p4', 'p5'])
  assert.deepEqual(choices[0], { id: 'p2', name: 'Beto', eligible: true, reason: '' })
  // One rejection label per reason, in the order of the contract.
  assert.deepEqual(choices[1], {
    id: 'p3',
    name: 'Caro',
    eligible: false,
    reason: COVER_MESSAGES.noService,
  })
  assert.deepEqual(choices[2], {
    id: 'p4',
    name: 'Dani',
    eligible: false,
    reason: COVER_MESSAGES.absent,
  })
  assert.deepEqual(choices[3], {
    id: 'p5',
    name: 'Eva',
    eligible: false,
    reason: COVER_MESSAGES.notBookable,
  })
  assert.equal(COVER_MESSAGES.notBookable, 'Esa persona no atiende clientes')
  assert.equal(COVER_MESSAGES.absent, 'Esa persona no está de turno ese día')
  assert.equal(COVER_MESSAGES.noService, 'Esa persona no hace ese servicio')
})

test('coverCandidates reads an absence of the local list, not only the server flag', () => {
  const absence = coverAbsence()
  const booking = coverBooking({
    candidates: [
      candidate({ id: 'p2', name: 'Beto', absent: false }),
      candidate({ id: 'p6', name: 'Fabi', absent: null }),
    ],
  })
  const choices = coverCandidates({
    booking,
    absence,
    absences: [{ user_id: 'p2', ...TEN_AM }],
  })
  // Eligible first, so the select opens on somebody who can take the turn.
  assert.deepEqual(choices[0], { id: 'p6', name: 'Fabi', eligible: true, reason: '' })
  assert.deepEqual(choices[1], { id: 'p2', name: 'Beto', eligible: false, reason: COVER_MESSAGES.absent })
})

test('coverCandidates never offers the absent professional as her own cover', () => {
  const absence = coverAbsence()
  const booking = coverBooking({
    candidates: [
      candidate({ id: 'p1', name: 'Ana' }),
      candidate({ id: 'p2', name: 'Beto' }),
    ],
  })
  assert.deepEqual(
    coverCandidates({ booking, absence }).map((choice) => choice.id),
    ['p2'],
  )
  assert.deepEqual(
    coverAllCandidates({ absence: coverAbsence({ bookings: [booking] }) }).map((choice) => choice.id),
    ['p2'],
  )
})

test('coverCandidates only asks for the service when the booking has one', () => {
  const absence = coverAbsence()
  const booking = coverBooking({
    service_id: null,
    candidates: [candidate({ id: 'p3', name: 'Caro', does_service: false })],
  })
  assert.deepEqual(coverCandidates({ booking, absence })[0], {
    id: 'p3',
    name: 'Caro',
    eligible: true,
    reason: '',
  })
})

test('coverCandidates never invents a rejection the server did not state', () => {
  const absence = coverAbsence()
  const booking = coverBooking({
    candidates: [
      candidate({ id: 'p7', name: 'Gil', does_service: null, bookable: null, absent: null }),
    ],
  })
  assert.deepEqual(coverCandidates({ booking, absence })[0], {
    id: 'p7',
    name: 'Gil',
    eligible: true,
    reason: '',
  })
})

test('coverAllCandidates keeps only who can take every turn of the absence', () => {
  const absence = coverAbsence({
    bookings: [
      coverBooking({
        id: 'b1',
        candidates: [candidate({ id: 'p2', name: 'Beto' }), candidate({ id: 'p3', name: 'Caro' })],
      }),
      coverBooking({
        id: 'b2',
        ...{ starts_at: '2025-12-24T18:00:00.000Z', ends_at: '2025-12-24T19:00:00.000Z' },
        candidates: [
          candidate({ id: 'p2', name: 'Beto', does_service: false }),
          candidate({ id: 'p3', name: 'Caro' }),
        ],
      }),
    ],
  })
  const all = coverAllCandidates({ absence })
  assert.deepEqual(all.map((choice) => [choice.id, choice.eligible]), [
    ['p3', true],
    ['p2', false],
  ])
  // The reason is the one of the first turn that person cannot take.
  assert.equal(all[1]?.reason, COVER_MESSAGES.noService)
  assert.equal(firstEligible(all)?.id, 'p3')
  assert.equal(firstEligible(coverAllCandidates({ absence: coverAbsence({ bookings: [] }) })), null)
})

test('candidateOptionLabel and ineligibleSummary name the reason', () => {
  const choices = coverCandidates({ booking: coverBooking(), absence: coverAbsence() })
  assert.equal(candidateOptionLabel(choices[0]!), 'Beto')
  assert.equal(
    candidateOptionLabel(choices[1]!),
    'Caro — no disponible: Esa persona no hace ese servicio',
  )
  assert.equal(
    ineligibleSummary(choices),
    'Caro: Esa persona no hace ese servicio · Dani: Esa persona no está de turno ese día · Eva: Esa persona no atiende clientes',
  )
  assert.equal(ineligibleSummary([]), '')
  assert.equal(ineligibleSummary([choices[0]!]), '')
})

test('reassignedNotice uses the right plural', () => {
  assert.equal(reassignedNotice(1, 'Beto'), '1 turno reasignado a Beto.')
  assert.equal(reassignedNotice(3, 'Beto'), '3 turnos reasignados a Beto.')
  assert.equal(reassignedNotice(0, 'Beto'), 'No se reasignó ningún turno.')
})

test('the optimistic draft shows the choice and rolls back to the saved one', () => {
  const saved = { b1: 'p3' }
  const start = beginCover(saved)
  const chosen = chooseFor(start, 'b1', 'p2')
  assert.equal(chosen.value['b1'], 'p2')
  assert.equal(chosen.saved['b1'], 'p3')

  const all = chooseForAll(chosen, ['b1', 'b2'], 'p4')
  assert.deepEqual(all.value, { b1: 'p4', b2: 'p4' })
  assert.equal(chosenFor(all, 'b1'), 'p4')
  assert.equal(chosenFor(all, 'b9'), '')

  // A rejected reassignment only drops its own choice.
  const dropped = forgetChoice(all, 'b1')
  assert.equal(chosenFor(dropped, 'b1'), 'p3')
  // The other rows keep what was chosen: only the rejected one goes back.
  assert.equal(chosenFor(dropped, 'b2'), 'p4')
  // A row the server never saved simply loses its choice.
  assert.equal(chosenFor(forgetChoice(all, 'b2'), 'b2'), '')
  // The rollback of the whole batch is the saved map, untouched.
  assert.deepEqual(rollbackCover(all).value, saved)
  assert.deepEqual(rollbackCover(all).saved, saved)
  assert.deepEqual(saved, { b1: 'p3' })
})

test('coveredNotices tells my turn covered by somebody else, with its day and hour', () => {
  const mine = (over: Partial<Booking>): Booking => ({
    id: 'b1',
    professional_id: 'p2',
    client_name: 'Ana',
    ...TEN_AM,
    status: 'booked',
    ...over,
  })
  const notices = coveredNotices(
    [
      mine({ id: 'b1', covered_from: 'p1' }),
      // Still mine: nobody covered it.
      mine({ id: 'b2', professional_id: 'p1', covered_from: null }),
      mine({ id: 'b3', covered_from: 'p9' }),
      mine({ id: 'b4', covered_from: 'p1', status: 'cancelled' }),
      // The covered turn already happened: no notice.
      mine({ id: 'b5', covered_from: 'p1', starts_at: '2025-12-18T16:00:00.000Z', ends_at: '2025-12-18T17:00:00.000Z' }),
    ],
    'p1',
    { timeZone: ZONE, now: NOW },
  )
  assert.deepEqual(notices, [
    {
      bookingId: 'b1',
      text: 'Tu turno del 24 de diciembre a las 10:00 lo cubre otra profesional.',
    },
  ])
  assert.deepEqual(coveredNotices([], 'p1', { timeZone: ZONE, now: NOW }), [])
  // The same day reads "de hoy" like the rest of the panel.
  assert.deepEqual(
    coveredNotices([mine({ covered_from: 'p1' })], 'p1', {
      timeZone: ZONE,
      now: new Date('2025-12-24T15:00:00.000Z'),
    }),
    [{ bookingId: 'b1', text: 'Tu turno de hoy a las 10:00 lo cubre otra profesional.' }],
  )
})

// --- API client (contract S-F2d3-2) ---

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

test('fetchCoverAbsences asks the range of the salon and reads the list', async () => {
  setToken('token-1')
  const stub = stubFetch(() =>
    json({
      absences: [
        {
          id: 't1',
          user_id: 'p1',
          user_name: 'Ana',
          ...DEC_24,
          bookings: [{ id: 'b1', client_name: 'Ana', ...TEN_AM, service_id: 's1', candidates: [] }],
        },
      ],
    }),
  )
  try {
    const rows = await fetchCoverAbsences('s1', DEC_24.starts_at, DEC_24.ends_at)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/absences\?from=.+&to=.+/)
    assert.deepEqual(rows.map((row) => row.id), ['t1'])
  } finally {
    stub.restore()
    clearToken()
  }
})

test('reassignAbsence posts the professional and reads how many turned over', async () => {
  const stub = stubFetch(() =>
    json({ reassigned: 2, bookings: [{ id: 'b1', client_name: 'Ana', ...TEN_AM }] }),
  )
  try {
    const outcome = await reassignAbsence('s1', 't1', ' p2 ')
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/absences\/t1\/reassign$/)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { professional_id: 'p2' })
    assert.equal(outcome.reassigned, 2)
    assert.deepEqual(outcome.bookings.map((row) => row.id), ['b1'])
  } finally {
    stub.restore()
  }
})

test('reassignBooking and cancelBookingForAbsence post the two bodies of the contract', async () => {
  const stub = stubFetch(() => json({ reassigned: 1, bookings: [] }))
  try {
    await reassignBooking('s1', 'b1', 'p2')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/bookings\/b1\/reassign$/)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { professional_id: 'p2' })
    await cancelBookingForAbsence('s1', 'b1')
    assert.equal(stub.calls[1]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), { cancel: true })
  } finally {
    stub.restore()
  }
})

test('a rejected reassignment keeps the server sentence verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'Esa persona no está de turno ese día' }, 409))
  try {
    await assert.rejects(
      () => reassignBooking('s1', 'b1', 'p2'),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 409 &&
        error.message === 'Esa persona no está de turno ese día',
    )
  } finally {
    stub.restore()
  }
})
