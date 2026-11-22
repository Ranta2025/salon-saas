import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError } from './api.ts'
import { clearToken, setToken } from './session.ts'
import {
  absenceDays,
  absencesOnDay,
  canEditAbsences,
  canSeeReason,
  conflictsNotice,
  createTimeOff,
  currentAbsence,
  deleteTimeOff,
  fetchTimeOff,
  formatTimeOffRange,
  normalizeReason,
  normalizeTimeOff,
  overlapsRange,
  timeOffBands,
  timeOffRange,
  timeOffSentence,
  TIME_OFF_MESSAGES,
  TIME_OFF_MAX_REASON,
  upcomingAbsences,
  validateTimeOff,
  validateTimeOffDraft,
  visibleReason,
  type TimeOff,
} from './timeOff.ts'

// America/Mexico_City has no DST since 2022: the whole local year is UTC-6.
const ZONE = 'America/Mexico_City'
const NOW = new Date('2025-12-20T18:00:00.000Z')

function entry(over: Partial<TimeOff> & Pick<TimeOff, 'starts_at' | 'ends_at'>): TimeOff {
  return { id: 't1', user_id: 'p1', reason: '', ...over }
}

// Whole local days of America/Mexico_City.
const DEC_24 = { starts_at: '2025-12-24T06:00:00.000Z', ends_at: '2025-12-27T06:00:00.000Z' }

test('timeOffRange turns whole local days into UTC, midnight to midnight', () => {
  assert.deepEqual(timeOffRange({ fromDate: '2025-12-24', toDate: '2025-12-26' }, ZONE), {
    startsAt: '2025-12-24T06:00:00.000Z',
    endsAt: '2025-12-27T06:00:00.000Z',
  })
  // One single day still ends at the midnight of the next one.
  assert.deepEqual(timeOffRange({ fromDate: '2025-12-24', toDate: '2025-12-24' }, ZONE), {
    startsAt: '2025-12-24T06:00:00.000Z',
    endsAt: '2025-12-25T06:00:00.000Z',
  })
})

test('timeOffRange keeps a range that crosses the year and a date near midnight', () => {
  assert.deepEqual(timeOffRange({ fromDate: '2025-12-31', toDate: '2025-12-31' }, ZONE), {
    startsAt: '2025-12-31T06:00:00.000Z',
    endsAt: '2026-01-01T06:00:00.000Z',
  })
  // 23:30 of the 24th in the salon zone is already the 25th in UTC.
  assert.deepEqual(
    timeOffRange({ fromDate: '2025-12-24', toDate: '2025-12-24', fromTime: '23:30', toTime: '23:45' }, ZONE),
    { startsAt: '2025-12-25T05:30:00.000Z', endsAt: '2025-12-25T05:45:00.000Z' },
  )
})

test('timeOffRange accepts partial hours and survives a DST change', () => {
  assert.deepEqual(
    timeOffRange({ fromDate: '2025-12-24', toDate: '2025-12-24', fromTime: '10:00', toTime: '12:00' }, ZONE),
    { startsAt: '2025-12-24T16:00:00.000Z', endsAt: '2025-12-24T18:00:00.000Z' },
  )
  // 2026-03-08 in New York is a 23 hour day (02:00 jumps to 03:00).
  assert.deepEqual(
    timeOffRange({ fromDate: '2026-03-08', toDate: '2026-03-08' }, 'America/New_York'),
    { startsAt: '2026-03-08T05:00:00.000Z', endsAt: '2026-03-09T04:00:00.000Z' },
  )
})

test('formatTimeOffRange prints whole days as a Spanish range', () => {
  assert.equal(
    formatTimeOffRange(entry(DEC_24), { timeZone: ZONE, today: '2025-12-20' }),
    'Del 24 al 26 de diciembre',
  )
  assert.equal(
    formatTimeOffRange(entry({ starts_at: '2025-12-24T06:00:00.000Z', ends_at: '2026-01-01T06:00:00.000Z' }), {
      timeZone: ZONE,
      today: '2025-12-20',
    }),
    'Del 24 al 31 de diciembre',
  )
  assert.equal(
    formatTimeOffRange(
      entry({ starts_at: '2025-12-24T06:00:00.000Z', ends_at: '2026-01-04T06:00:00.000Z' }),
      { timeZone: ZONE, today: '2025-12-20' },
    ),
    'Del 24 de diciembre al 3 de enero',
  )
  assert.equal(
    formatTimeOffRange(
      entry({ starts_at: '2025-12-24T06:00:00.000Z', ends_at: '2025-12-25T06:00:00.000Z' }),
      { timeZone: ZONE, today: '2025-12-20' },
    ),
    'El 24 de diciembre',
  )
})

test('formatTimeOffRange says "Hoy" when the absence starts today', () => {
  const today = entry({ starts_at: '2025-12-24T16:00:00.000Z', ends_at: '2025-12-24T18:00:00.000Z' })
  assert.equal(formatTimeOffRange(today, { timeZone: ZONE, today: '2025-12-24' }), 'Hoy de 10:00 a 12:00')
  assert.equal(
    formatTimeOffRange(today, { timeZone: ZONE, today: '2025-12-20' }),
    'El 24 de diciembre de 10:00 a 12:00',
  )
  assert.equal(
    formatTimeOffRange(entry(DEC_24), { timeZone: ZONE, today: '2025-12-20' }),
    'Del 24 al 26 de diciembre',
  )
  assert.equal(
    timeOffSentence(entry(DEC_24), { timeZone: ZONE, today: '2025-12-20' }),
    'del 24 al 26 de diciembre',
  )
})

test('validateTimeOff mirrors the server sentences, in the server order', () => {
  const build = (startsAt: string, endsAt: string, reason = '') =>
    validateTimeOff({ startsAt, endsAt, reason, existing: [], now: NOW })

  assert.deepEqual(build('2025-12-26T06:00:00.000Z', '2025-12-24T06:00:00.000Z'), {
    ok: false,
    message: TIME_OFF_MESSAGES.order,
  })
  assert.equal(TIME_OFF_MESSAGES.order, 'El fin debe ser posterior al inicio')
  // More than 90 days: 91 whole days.
  assert.deepEqual(build('2025-12-24T06:00:00.000Z', '2026-03-25T06:00:00.000Z'), {
    ok: false,
    message: TIME_OFF_MESSAGES.tooLong,
  })
  assert.equal(TIME_OFF_MESSAGES.tooLong, 'Una ausencia no puede durar más de 90 días')
  // Already over: it ended before now.
  assert.deepEqual(build('2025-12-10T06:00:00.000Z', '2025-12-12T06:00:00.000Z'), {
    ok: false,
    message: TIME_OFF_MESSAGES.alreadyEnded,
  })
  assert.equal(TIME_OFF_MESSAGES.alreadyEnded, 'No se puede cargar una ausencia que ya terminó')
})

test('validateTimeOff accepts 90 days, a running absence and rejects an overlap', () => {
  const exactly90 = validateTimeOff({
    startsAt: '2025-12-24T06:00:00.000Z',
    endsAt: '2026-03-24T06:00:00.000Z',
    existing: [],
    now: NOW,
  })
  assert.equal(exactly90.ok, true)

  const running = validateTimeOff({
    startsAt: '2025-12-18T06:00:00.000Z',
    endsAt: '2025-12-22T06:00:00.000Z',
    existing: [],
    now: NOW,
  })
  assert.equal(running.ok, true)

  const overlap = validateTimeOff({
    startsAt: '2025-12-26T06:00:00.000Z',
    endsAt: '2025-12-29T06:00:00.000Z',
    existing: [entry(DEC_24)],
    now: NOW,
  })
  assert.deepEqual(overlap, { ok: false, message: TIME_OFF_MESSAGES.overlap })
  assert.equal(TIME_OFF_MESSAGES.overlap, 'Ya hay una ausencia en ese período')

  // Touching at the edge is not an overlap.
  const touching = validateTimeOff({
    startsAt: '2025-12-27T06:00:00.000Z',
    endsAt: '2025-12-29T06:00:00.000Z',
    existing: [entry(DEC_24)],
    now: NOW,
  })
  assert.equal(touching.ok, true)
})

test('validateTimeOff normalises the reason of a valid absence', () => {
  const result = validateTimeOff({
    startsAt: '2025-12-24T06:00:00.000Z',
    endsAt: '2025-12-27T06:00:00.000Z',
    reason: '  Vacaciones\n' + 'de fin de año  ',
    existing: [],
    now: NOW,
  })
  assert.equal(result.ok, true)
  assert.deepEqual(result.ok ? result.value : null, {
    startsAt: '2025-12-24T06:00:00.000Z',
    endsAt: '2025-12-27T06:00:00.000Z',
    reason: 'Vacaciones de fin de año',
  })
  assert.equal(normalizeReason('x'.repeat(200)).length, TIME_OFF_MAX_REASON)
  assert.equal(TIME_OFF_MAX_REASON, 120)
})

test('validateTimeOffDraft asks both dates and then the two hours, then the rules of the API', () => {
  const base = { timeZone: ZONE, existing: [], now: NOW }
  assert.deepEqual(validateTimeOffDraft({ ...base, draft: { fromDate: '', toDate: '' } }), {
    ok: false,
    message: TIME_OFF_MESSAGES.dates,
  })
  assert.deepEqual(
    validateTimeOffDraft({
      ...base,
      draft: { fromDate: '2025-12-24', toDate: '2025-12-26', fromTime: '10:00' },
    }),
    { ok: false, message: TIME_OFF_MESSAGES.hours },
  )
  // Whole local days become the midnight-to-midnight range.
  assert.deepEqual(validateTimeOffDraft({ ...base, draft: { fromDate: '2025-12-24', toDate: '2025-12-26' } }), {
    ok: true,
    value: { startsAt: DEC_24.starts_at, endsAt: DEC_24.ends_at, reason: '' },
  })
  assert.deepEqual(
    validateTimeOffDraft({
      ...base,
      draft: {
        fromDate: '2025-12-24',
        toDate: '2025-12-24',
        fromTime: '10:00',
        toTime: '12:00',
        reason: 'Médico',
      },
    }),
    {
      ok: true,
      value: {
        startsAt: '2025-12-24T16:00:00.000Z',
        endsAt: '2025-12-24T18:00:00.000Z',
        reason: 'Médico',
      },
    },
  )
  // An absence of the past is refused before it reaches the API.
  assert.deepEqual(validateTimeOffDraft({ ...base, draft: { fromDate: '2025-12-01', toDate: '2025-12-02' } }), {
    ok: false,
    message: TIME_OFF_MESSAGES.alreadyEnded,
  })
})

test('only the management adds or removes absences', () => {
  assert.equal(canEditAbsences('owner'), true)
  assert.equal(canEditAbsences('admin'), true)
  assert.equal(canEditAbsences('professional'), false)
  assert.equal(canEditAbsences('reception'), false)
  assert.equal(canEditAbsences(null), false)
})

test('overlapsRange and absencesOnDay use the local day, boundary excluded', () => {
  const absence = entry(DEC_24)
  assert.equal(overlapsRange(absence, absence), true)
  assert.equal(
    overlapsRange(absence, { starts_at: '2025-12-27T06:00:00.000Z', ends_at: '2025-12-28T06:00:00.000Z' }),
    false,
  )
  assert.equal(absencesOnDay([absence], '2025-12-24', ZONE).length, 1)
  assert.equal(absencesOnDay([absence], '2025-12-26', ZONE).length, 1)
  // The absence ends at the midnight of the 27th, so that day is free.
  assert.equal(absencesOnDay([absence], '2025-12-27', ZONE).length, 0)
  assert.equal(absencesOnDay([absence], '2025-12-23', ZONE).length, 0)
})

test('absencesOnDay keeps an absence of 23:30 on its own salon day', () => {
  // 23:30 of the 24th in the salon zone is 05:30 of the 25th in UTC.
  const late = entry({ starts_at: '2025-12-25T05:30:00.000Z', ends_at: '2025-12-25T06:00:00.000Z' })
  assert.equal(absencesOnDay([late], '2025-12-24', ZONE).length, 1)
  assert.equal(absencesOnDay([late], '2025-12-25', ZONE).length, 0)
})

test('absenceDays marks the days of the week strip that carry an absence', () => {
  const week = [
    '2025-12-22',
    '2025-12-23',
    '2025-12-24',
    '2025-12-25',
    '2025-12-26',
    '2025-12-27',
    '2025-12-28',
  ]
  assert.deepEqual(absenceDays([entry(DEC_24)], week, ZONE), [
    '2025-12-24',
    '2025-12-25',
    '2025-12-26',
  ])
  assert.deepEqual(absenceDays([], week, ZONE), [])
})

test('the reason is only visible for the management and for the own absences', () => {
  const absence = entry({ ...DEC_24, reason: 'Vacaciones', user_id: 'p2' })
  assert.equal(canSeeReason('owner', 'u1', 'p2'), true)
  assert.equal(canSeeReason('admin', 'u1', 'p2'), true)
  assert.equal(canSeeReason('professional', 'p2', 'p2'), true)
  assert.equal(canSeeReason('professional', 'p1', 'p2'), false)
  assert.equal(canSeeReason('reception', 'u1', 'p2'), false)
  assert.equal(canSeeReason(null, 'p1', 'p2'), false)
  assert.equal(visibleReason(absence, 'professional', 'p1'), '')
  assert.equal(visibleReason(absence, 'professional', 'p2'), 'Vacaciones')
  assert.equal(visibleReason(absence, 'owner', 'u1'), 'Vacaciones')
})

test('timeOffBands builds one band per person and hides the reason it may not show', () => {
  const whole = entry({ ...DEC_24, id: 't1', user_id: 'p1', reason: 'Vacaciones' })
  const partial = entry({
    id: 't2',
    user_id: 'p2',
    starts_at: '2025-12-24T16:00:00.000Z',
    ends_at: '2025-12-24T18:00:00.000Z',
    reason: 'Médico',
  })
  const window = {
    from: '2025-12-24T06:00:00.000Z',
    to: '2025-12-25T06:00:00.000Z',
    timeZone: ZONE,
    names: [
      { id: 'p1', name: 'Ana' },
      { id: 'p2', name: 'Beto' },
    ],
  }

  // The management sees every reason; a professional only the own one.
  assert.deepEqual(timeOffBands([whole, partial], { ...window, actorRole: 'owner', viewerId: 'p9' }), [
    { userId: 'p1', name: 'Ana', label: 'Ausente', hours: '', reason: 'Vacaciones' },
    { userId: 'p2', name: 'Beto', label: 'Ausente', hours: '10:00–12:00', reason: 'Médico' },
  ])
  assert.deepEqual(
    timeOffBands([whole, partial], { ...window, actorRole: 'professional', viewerId: 'p1' })[0],
    { userId: 'p1', name: 'Ana', label: 'Ausente', hours: '', reason: 'Vacaciones' },
  )
  assert.deepEqual(
    timeOffBands([whole, partial], { ...window, actorRole: 'professional', viewerId: 'p1' })[1],
    { userId: 'p2', name: 'Beto', label: 'Ausente', hours: '10:00–12:00', reason: '' },
  )
  // An absence that does not touch the window has no band.
  assert.deepEqual(
    timeOffBands([whole], {
      ...window,
      from: '2025-12-27T06:00:00.000Z',
      to: '2025-12-28T06:00:00.000Z',
      actorRole: 'owner',
      viewerId: 'p9',
    }),
    [],
  )
})

test('conflictsNotice uses the right plural', () => {
  assert.equal(conflictsNotice(0), null)
  assert.equal(
    conflictsNotice(1),
    'Tiene 1 turno en ese período: reasignalo o cancelalo desde el calendario',
  )
  assert.equal(
    conflictsNotice(3),
    'Tiene 3 turnos en ese período: reasignalos o cancelalos desde el calendario',
  )
})

test('currentAbsence picks the running one and upcomingAbsences the future ones', () => {
  const past = entry({ id: 'past', starts_at: '2025-12-01T06:00:00.000Z', ends_at: '2025-12-03T06:00:00.000Z' })
  const running = entry({ id: 'running', starts_at: '2025-12-19T06:00:00.000Z', ends_at: '2025-12-22T06:00:00.000Z' })
  const future = entry({ id: 'future', ...DEC_24 })
  assert.equal(currentAbsence([future, past, running], 'p1', NOW)?.id, 'running')
  assert.equal(currentAbsence([future], 'p1', NOW)?.id, 'future')
  assert.equal(currentAbsence([past], 'p1', NOW), null)
  assert.equal(currentAbsence([future], 'p2', NOW), null)
  assert.deepEqual(upcomingAbsences([future, past, running], NOW).map((row) => row.id), [
    'running',
    'future',
  ])
})

test('normalizeTimeOff reads the contract and drops unusable rows', () => {
  const rows = normalizeTimeOff({
    time_off: [
      { id: 't1', user_id: 'p1', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at },
      { id: 't2', user_id: 'p1', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at, reason: ' Vacaciones ' },
      { id: '', user_id: 'p1', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at },
      { id: 't3', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at },
      { id: 't4', user_id: 'p1', starts_at: 'nope', ends_at: DEC_24.ends_at },
      { id: 't5', user_id: 'p1', starts_at: DEC_24.ends_at, ends_at: DEC_24.starts_at },
    ],
  })
  assert.deepEqual(rows, [
    { id: 't1', user_id: 'p1', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at, reason: '' },
    { id: 't2', user_id: 'p1', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at, reason: 'Vacaciones' },
  ])
  assert.deepEqual(normalizeTimeOff(null), [])
})

// --- API client (contract S-F2d2-5) ---

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

test('fetchTimeOff asks the range of the salon and reads the list', async () => {
  setToken('token-1')
  const stub = stubFetch(() =>
    json({ time_off: [{ id: 't1', user_id: 'p1', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at }] }),
  )
  try {
    const rows = await fetchTimeOff('s1', DEC_24.starts_at, DEC_24.ends_at)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/time-off\?from=.+&to=.+/)
    assert.deepEqual(rows.map((row) => row.id), ['t1'])
  } finally {
    stub.restore()
    clearToken()
  }
})

test('createTimeOff posts the range and returns the conflicts of the server', async () => {
  const stub = stubFetch(() =>
    json(
      {
        time_off: { id: 't9', user_id: 'p1', starts_at: DEC_24.starts_at, ends_at: DEC_24.ends_at },
        conflicts: [
          {
            id: 'b1',
            client_name: 'Ana',
            starts_at: '2025-12-24T16:00:00.000Z',
            ends_at: '2025-12-24T17:00:00.000Z',
          },
        ],
      },
      201,
    ),
  )
  try {
    const created = await createTimeOff('s1', 'p1', {
      startsAt: DEC_24.starts_at,
      endsAt: DEC_24.ends_at,
    })
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/members\/p1\/time-off$/)
    // No reason means no key at all, so an older server sees the same body.
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      starts_at: DEC_24.starts_at,
      ends_at: DEC_24.ends_at,
    })
    assert.equal(created.time_off?.id, 't9')
    assert.deepEqual(created.conflicts.map((row) => row.client_name), ['Ana'])
  } finally {
    stub.restore()
  }
})

test('createTimeOff sends the reason and keeps the server sentence verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'Ya hay una ausencia en ese período' }, 400))
  try {
    await assert.rejects(
      () =>
        createTimeOff('s1', 'p1', {
          startsAt: DEC_24.starts_at,
          endsAt: DEC_24.ends_at,
          reason: '  Vacaciones  ',
        }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 400 &&
        error.message === 'Ya hay una ausencia en ese período',
    )
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      starts_at: DEC_24.starts_at,
      ends_at: DEC_24.ends_at,
      reason: 'Vacaciones',
    })
  } finally {
    stub.restore()
  }
})

test('deleteTimeOff removes the absence with a 204', async () => {
  const stub = stubFetch(() => new Response(null, { status: 204 }))
  try {
    await deleteTimeOff('s1', 't9')
    assert.equal(stub.calls[0]?.method, 'DELETE')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/time-off\/t9$/)
  } finally {
    stub.restore()
  }
})
