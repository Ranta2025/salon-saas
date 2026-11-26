import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from './api.ts'
import { clearToken, setToken } from './session.ts'
import {
  createDepositGate,
  DEPOSIT_BUSY_MESSAGE,
  DEPOSIT_CASH_NOTE,
  DEPOSIT_MESSAGES,
  DEPOSIT_PAGE_SIZE,
  DEPOSIT_SIMULATED_NOTICE,
  DEPOSIT_STATUS_FILTERS,
  depositPaymentSummary,
  depositPreview,
  depositStatusLabel,
  depositStatusSentence,
  depositStatusTone,
  depositsPath,
  draftFromPolicy,
  fetchDepositPolicy,
  fetchDeposits,
  formatCountdown,
  formatPublicInstant,
  lookupPublicDeposit,
  mergeDepositRows,
  normalizeBookingPayment,
  normalizeDepositPage,
  normalizeDepositPolicy,
  normalizePublicDepositLookup,
  PAYMENT_POLL_INTERVAL_MS,
  PAYMENT_POLL_MAX_ATTEMPTS,
  PAYMENT_POLL_TIMEOUT_MS,
  paymentTarget,
  policyBody,
  pollDecision,
  saveDepositPolicy,
  validateDepositPolicy,
  writeDepositPolicy,
  type DepositPolicy,
  type DepositPolicyDraft,
  type DepositRow,
} from './deposits.ts'

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

// ---------- policy (D3) ----------

const RAW_POLICY = { enabled: true, percent: 30, hold_minutes: 30, refund_hours: 24 }

test('normalizeDepositPolicy reads the four keys and keeps nulls of a never-saved policy', () => {
  assert.deepEqual(normalizeDepositPolicy(RAW_POLICY), {
    enabled: true,
    percent: 30,
    hold_minutes: 30,
    refund_hours: 24,
  })
  assert.deepEqual(
    normalizeDepositPolicy({ enabled: false, percent: null, hold_minutes: null, refund_hours: null }),
    { enabled: false, percent: null, hold_minutes: null, refund_hours: null },
  )
  // A wrapped answer (the pattern of some routes) reads the same.
  assert.deepEqual(normalizeDepositPolicy({ deposit_policy: RAW_POLICY })?.percent, 30)
  for (const bad of [null, undefined, 'nope', [], 5]) {
    assert.equal(normalizeDepositPolicy(bad), null, JSON.stringify(bad))
  }
  // A number the form cannot use is null, never a made-up one.
  assert.equal(normalizeDepositPolicy({ ...RAW_POLICY, percent: '30' })?.percent, null)
  assert.equal(normalizeDepositPolicy({ ...RAW_POLICY, hold_minutes: 2.5 })?.hold_minutes, null)
})

test('draftFromPolicy fills the form and leaves a never-saved policy blank', () => {
  assert.deepEqual(draftFromPolicy(normalizeDepositPolicy(RAW_POLICY)), {
    enabled: true,
    percent: '30',
    hold_minutes: '30',
    refund_hours: '24',
  })
  assert.deepEqual(
    draftFromPolicy(
      normalizeDepositPolicy({ enabled: false, percent: null, hold_minutes: null, refund_hours: null }),
    ),
    { enabled: false, percent: '', hold_minutes: '', refund_hours: '' },
  )
  assert.deepEqual(draftFromPolicy(null), {
    enabled: false,
    percent: '',
    hold_minutes: '',
    refund_hours: '',
  })
})

const DRAFT: DepositPolicyDraft = {
  enabled: true,
  percent: '30',
  hold_minutes: '30',
  refund_hours: '24',
}

test('validateDepositPolicy asks for the four keys with the sentences of the contract', () => {
  const missing = validateDepositPolicy({ ...DRAFT, hold_minutes: '  ' })
  assert.deepEqual(missing, {
    ok: false,
    field: 'hold',
    message: 'Faltan datos de la sena: activada, porcentaje, espera y devolucion',
  })
  assert.equal(
    DEPOSIT_MESSAGES.missingPolicy,
    'Faltan datos de la sena: activada, porcentaje, espera y devolucion',
  )
  // Even switched off the four keys travel: the server asks for all of them.
  const off = validateDepositPolicy({ enabled: false, percent: '', hold_minutes: '', refund_hours: '' })
  assert.equal(off.ok, false)
  assert.equal(off.ok === false && off.field, 'percent')
})

test('validateDepositPolicy pins the ranges and the exact sentences of the contract', () => {
  const percent = (value: string) => validateDepositPolicy({ ...DRAFT, percent: value })
  assert.deepEqual(percent('9'), {
    ok: false,
    field: 'percent',
    message: 'El porcentaje debe ser entre 10 y 100',
  })
  assert.deepEqual(percent('101'), {
    ok: false,
    field: 'percent',
    message: 'El porcentaje debe ser entre 10 y 100',
  })
  const notANumber = percent('diez')
  assert.equal(notANumber.ok === false ? notANumber.field : 'ok', 'percent')
  const hold = (value: string) => validateDepositPolicy({ ...DRAFT, hold_minutes: value })
  assert.deepEqual(hold('4'), { ok: false, field: 'hold', message: 'La espera debe ser entre 5 y 120 minutos' })
  assert.deepEqual(hold('121'), { ok: false, field: 'hold', message: 'La espera debe ser entre 5 y 120 minutos' })
  const refund = (value: string) => validateDepositPolicy({ ...DRAFT, refund_hours: value })
  assert.deepEqual(refund('-1'), {
    ok: false,
    field: 'refund',
    message: 'La devolucion debe ser entre 0 y 168 horas',
  })
  assert.deepEqual(refund('169'), {
    ok: false,
    field: 'refund',
    message: 'La devolucion debe ser entre 0 y 168 horas',
  })
  // The borders of each range are valid.
  assert.deepEqual(validateDepositPolicy({ enabled: true, percent: '10', hold_minutes: '5', refund_hours: '0' }), {
    ok: true,
    payload: { enabled: true, percent: 10, hold_minutes: 5, refund_hours: 0 },
  })
  assert.deepEqual(validateDepositPolicy({ enabled: true, percent: '100', hold_minutes: '120', refund_hours: '168' }), {
    ok: true,
    payload: { enabled: true, percent: 100, hold_minutes: 120, refund_hours: 168 },
  })
})

test('policyBody always sends the four keys, explicit and as integers', () => {
  const payload = { enabled: false, percent: 25, hold_minutes: 15, refund_hours: 0 }
  const body = policyBody(payload)
  assert.deepEqual(Object.keys(body).sort(), ['enabled', 'hold_minutes', 'percent', 'refund_hours'])
  assert.deepEqual(body, payload)
})

test('depositPreview is the sentence the client sees, with the notice of the simulation', () => {
  assert.equal(depositPreview(30), 'Sena del 30% - pago simulado')
  assert.equal(depositPreview(10), 'Sena del 10% - pago simulado')
  assert.equal(depositPreview(9), null)
  assert.equal(depositPreview(101), null)
  assert.equal(depositPreview(30.5), null)
  assert.equal(DEPOSIT_SIMULATED_NOTICE, 'Pago simulado')
})

// ---------- list of deposits (D10) ----------

const ROWS: DepositRow[] = [
  {
    id: 'd1',
    booking_id: 'b1',
    client_name: 'Ana',
    starts_at: '2026-11-02T16:00:00Z',
    amount_cents: 5400,
    status: 'paid',
    paid_at: '2026-10-01T10:00:00Z',
    settled_at: null,
  },
  {
    id: 'd2',
    booking_id: 'b2',
    client_name: 'Bea',
    starts_at: '2026-11-03T16:00:00Z',
    amount_cents: 3250,
    status: 'pending',
    paid_at: null,
    settled_at: null,
  },
]

test('the chore note of the panel is the verbatim sentence of D13-a', () => {
  assert.equal(DEPOSIT_CASH_NOTE, 'Descuenta la sena cobrada al cobrar el turno en caja')
})

test('the status chips say exactly the five words of the contract', () => {
  assert.equal(depositStatusLabel('pending'), 'Pendiente')
  assert.equal(depositStatusLabel('paid'), 'Pagada')
  assert.equal(depositStatusLabel('expired'), 'Vencida')
  assert.equal(depositStatusLabel('refunded'), 'Devuelta')
  assert.equal(depositStatusLabel('forfeited'), 'Perdida')
  // An unknown state is never invented as one of the five.
  assert.equal(depositStatusLabel('otro'), 'otro')
  // The tone of the chip: money in, waiting, closed.
  assert.equal(depositStatusTone('paid'), 'on')
  assert.equal(depositStatusTone('pending'), 'warn')
  for (const status of ['expired', 'refunded', 'forfeited', 'otro']) {
    assert.equal(depositStatusTone(status), 'off', status)
  }
  assert.deepEqual(
    DEPOSIT_STATUS_FILTERS.map((option) => option.value),
    ['', 'pending', 'paid', 'expired', 'refunded', 'forfeited'],
  )
  assert.equal(DEPOSIT_STATUS_FILTERS[0]?.label, 'Todas')
  assert.deepEqual(
    DEPOSIT_STATUS_FILTERS.slice(1).map((option) => option.label),
    ['Pendiente', 'Pagada', 'Vencida', 'Devuelta', 'Perdida'],
  )
})

test('normalizeDepositPage reads the rows and the cursor and drops what the UI cannot use', () => {
  const page = normalizeDepositPage({
    deposits: [
      ...ROWS,
      { id: '', booking_id: 'b3', client_name: 'X', starts_at: '2026-11-04T16:00:00Z', amount_cents: 100, status: 'paid' },
      { id: 'd4', booking_id: 'b4', client_name: 'X', starts_at: 'no-es-fecha', amount_cents: 100, status: 'paid' },
      { id: 'd5', booking_id: 'b5', client_name: 'X', starts_at: '2026-11-04T16:00:00Z', amount_cents: -1, status: 'paid' },
      null,
    ],
    next_cursor: 'abc',
  })
  assert.ok(page)
  assert.deepEqual(page.deposits.map((row) => row.id), ['d1', 'd2'])
  assert.equal(page.next_cursor, 'abc')
  // `rows` is accepted as well, and no cursor means the end of the list.
  assert.equal(normalizeDepositPage({ rows: ROWS })?.deposits.length, 2)
  assert.equal(normalizeDepositPage({ rows: ROWS })?.next_cursor, null)
  // A body that is not the documented shape is NOT «no deposits»: it is an error.
  for (const bad of [null, 'nope', [], {}, { deposits: 'nope' }]) {
    assert.equal(normalizeDepositPage(bad), null, JSON.stringify(bad))
  }
})

test('mergeDepositRows appends without repeating an id and keeps the order of the server', () => {
  const merged = mergeDepositRows(ROWS, [ROWS[1] as DepositRow, { ...ROWS[0], id: 'd3', client_name: 'Cata' } as DepositRow])
  assert.deepEqual(
    merged.map((row) => row.id),
    ['d1', 'd2', 'd3'],
  )
  assert.deepEqual(ROWS.map((row) => row.id), ['d1', 'd2'])
})

test('depositsPath carries limit, the status filter and the opaque cursor', () => {
  assert.equal(depositsPath('s1', {}), `/api/v1/salons/s1/deposits?limit=${DEPOSIT_PAGE_SIZE}`)
  assert.equal(
    depositsPath('s 1', { status: 'paid', before: 'a b+c' }),
    `/api/v1/salons/s%201/deposits?limit=${DEPOSIT_PAGE_SIZE}&before=a%20b%2Bc&status=paid`,
  )
  // An empty filter is «Todas»: it never travels as `status=`.
  assert.equal(depositsPath('s1', { status: '' }).includes('status='), false)
})

test('fetchDeposits reads the page and passes the server sentence verbatim', async () => {
  const stub = stubFetch((call) =>
    call.url.includes('before=')
      ? json({ error: 'sin permiso' }, 403)
      : json({ deposits: ROWS, next_cursor: 'n1' }),
  )
  try {
    setToken('jwt-deposits')
    const page = await fetchDeposits('s1', { status: 'paid' })
    assert.deepEqual(page.deposits.map((row) => row.id), ['d1', 'd2'])
    assert.equal(page.next_cursor, 'n1')
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-deposits')
    assert.match(stub.calls[0]?.url ?? '', /status=paid/)
    const failure = await fetchDeposits('s1', { before: 'n1' }).then(
      () => null,
      (error: unknown) => error,
    )
    assert.ok(failure instanceof ApiError)
    assert.equal(failure.message, 'sin permiso')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchDeposits treats a body of another shape as an error, not as an empty list', async () => {
  const stub = stubFetch(() => json({ ok: true }))
  try {
    setToken('jwt-deposits')
    await assert.rejects(() => fetchDeposits('s1', {}), /no devolvió la lista de senas/i)
  } finally {
    stub.restore()
    clearToken()
  }
})

// ---------- policy client and its write ----------

test('fetchDepositPolicy and saveDepositPolicy use the contract path and the four keys', async () => {
  let sent: unknown = null
  const stub = stubFetch((call) => {
    if (call.method === 'PUT') {
      sent = JSON.parse(call.body ?? '{}')
      return json(RAW_POLICY)
    }
    return json(RAW_POLICY)
  })
  try {
    setToken('jwt-deposits')
    assert.equal((await fetchDepositPolicy('s1'))?.percent, 30)
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/deposit-policy`)
    assert.equal(stub.calls[0]?.method, 'GET')
    const saved = await saveDepositPolicy('s1', {
      enabled: true,
      percent: 30,
      hold_minutes: 30,
      refund_hours: 24,
    })
    assert.equal(saved?.enabled, true)
    assert.equal(stub.calls[1]?.method, 'PUT')
    assert.deepEqual(sent, { enabled: true, percent: 30, hold_minutes: 30, refund_hours: 24 })
    assert.deepEqual(Object.keys(sent as Record<string, unknown>).sort(), [
      'enabled',
      'hold_minutes',
      'percent',
      'refund_hours',
    ])
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writeDepositPolicy sends nothing offline, queues nothing and says so', async () => {
  const stub = stubFetch(() => json(RAW_POLICY))
  const store = new Map<string, string>()
  const original = (globalThis as { localStorage?: unknown }).localStorage
  ;(globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
    removeItem: (key: string) => void store.delete(key),
  }
  try {
    setToken('jwt-deposits')
    const gate = createDepositGate()
    const outcome = await writeDepositPolicy(
      gate,
      's1',
      { enabled: true, percent: 30, hold_minutes: 30, refund_hours: 24 },
      () => false,
    )
    assert.equal(outcome.status, 'offline')
    assert.match(outcome.status === 'offline' ? outcome.message : '', /sin conexión/i)
    assert.match(outcome.status === 'offline' ? outcome.message : '', /ni quedó en cola/)
    assert.equal(stub.calls.length, 0)
    assert.equal(gate.busy(), false)
    assert.equal([...store.keys()].some((key) => key.includes('offline_queue')), false)
  } finally {
    ;(globalThis as { localStorage?: unknown }).localStorage = original
    stub.restore()
    clearToken()
  }
})

test('writeDepositPolicy keeps the server sentence verbatim (409 without bank, 400 ranges)', async () => {
  let status = 409
  let sentence = 'El cobro de senas no esta disponible en este servidor'
  const stub = stubFetch(() => json({ error: sentence }, status))
  try {
    setToken('jwt-deposits')
    assert.deepEqual(
      await writeDepositPolicy(
        createDepositGate(),
        's1',
        { enabled: true, percent: 30, hold_minutes: 30, refund_hours: 24 },
        () => true,
      ),
      { status: 'failed', message: 'El cobro de senas no esta disponible en este servidor', definitive: true },
    )
    status = 400
    sentence = 'El porcentaje debe ser entre 10 y 100'
    assert.deepEqual(
      await writeDepositPolicy(
        createDepositGate(),
        's1',
        { enabled: true, percent: 30, hold_minutes: 30, refund_hours: 24 },
        () => true,
      ),
      { status: 'failed', message: 'El porcentaje debe ser entre 10 y 100', definitive: true },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a second press of «Guardar» does not send a second request', async () => {
  let release: () => void = () => undefined
  const stub = stubFetch(
    () =>
      new Promise<Response>((resolve) => {
        release = () => resolve(json(RAW_POLICY))
      }),
  )
  try {
    setToken('jwt-deposits')
    const gate = createDepositGate()
    const payload = { enabled: true, percent: 30, hold_minutes: 30, refund_hours: 24 }
    const first = writeDepositPolicy(gate, 's1', payload, () => true)
    const second = writeDepositPolicy(gate, 's1', payload, () => true)
    assert.deepEqual(await second, { status: 'busy', message: DEPOSIT_BUSY_MESSAGE })
    release()
    assert.equal((await first).status, 'saved')
    assert.equal(stub.calls.length, 1)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

// ---------- countdown of the hold (D13-b) ----------

test('formatCountdown renders the hold as mm:ss and then as hours and minutes', () => {
  assert.deepEqual(formatCountdown(0), { expired: true, label: 'Vencido' })
  assert.deepEqual(formatCountdown(-5000), { expired: true, label: 'Vencido' })
  assert.deepEqual(formatCountdown(14 * 60_000 + 59_000), { expired: false, label: '14:59' })
  assert.deepEqual(formatCountdown(60_000), { expired: false, label: '01:00' })
  assert.deepEqual(formatCountdown(3_600_000), { expired: false, label: '1 h 00 min' })
  // The minutes are rounded UP too: a hold never under-reports the time it has left.
  assert.deepEqual(formatCountdown(3_725_000), { expired: false, label: '1 h 03 min' })
  // The seconds are rounded UP: a hold never reads «Vencido» while it still lasts.
  assert.deepEqual(formatCountdown(1), { expired: false, label: '00:01' })
})

const PAYMENT = {
  depositId: 'd1',
  amountCents: 5400,
  expiresAt: '2026-11-02T16:30:00Z',
  payUrl: 'https://app.test/pagar#charge_abc',
  simulated: true,
}

test('depositPaymentSummary is the deposit block of the confirmation screen', () => {
  const now = new Date('2026-11-02T16:15:00Z')
  const summary = depositPaymentSummary(PAYMENT, now)
  assert.equal(summary.amountLabel, '$54.00')
  assert.equal(summary.countdown, '15:00')
  assert.equal(summary.expired, false)
  assert.equal(summary.notice, 'Pago simulado')
  // An unusable date is not a countdown: no invented deadline.
  assert.equal(depositPaymentSummary({ ...PAYMENT, expiresAt: 'no-es-fecha' }, now).countdown, null)
  const late = depositPaymentSummary(PAYMENT, new Date('2026-11-02T17:00:00Z'))
  assert.equal(late.expired, true)
  assert.equal(late.countdown, 'Vencido')
})

test('paymentTarget keeps a same-origin pay_url as a path plus fragment', () => {
  assert.deepEqual(paymentTarget('https://app.test/pagar#charge_abc', 'https://app.test'), {
    kind: 'path',
    path: '/pagar#charge_abc',
  })
  assert.deepEqual(paymentTarget('/pagar#charge_abc', 'https://app.test'), {
    kind: 'path',
    path: '/pagar#charge_abc',
  })
  // Another origin is never navigated with the token in the path of this app.
  assert.deepEqual(paymentTarget('https://banco.test/pagar#charge_abc', 'https://app.test'), {
    kind: 'external',
    href: 'https://banco.test/pagar#charge_abc',
  })
  assert.equal(paymentTarget('', 'https://app.test'), null)
  // Same origin without a fragment is not the payment page: the charge id lives there.
  assert.equal(paymentTarget('https://app.test/pagar', 'https://app.test'), null)
  assert.equal(paymentTarget('no es una url', 'https://app.test'), null)
})

test('normalizeBookingPayment reads the additive `payment` of the 201 (D8)', () => {
  const payment = normalizeBookingPayment({
    deposit_id: 'd1',
    amount_cents: 5400,
    expires_at: '2026-11-02T16:30:00Z',
    pay_url: 'https://app.test/pagar#charge_abc',
    simulated: true,
  })
  assert.deepEqual(payment, PAYMENT)
  for (const bad of [null, 'nope', {}, { deposit_id: 'd1' }, { ...PAYMENT }]) {
    assert.equal(normalizeBookingPayment(bad), null, JSON.stringify(bad))
  }
  assert.equal(
    normalizeBookingPayment({
      deposit_id: 'd1',
      amount_cents: 0,
      expires_at: '2026-11-02T16:30:00Z',
      pay_url: '/pagar#x',
      simulated: true,
    }),
    null,
  )
})

// ---------- /pago-resultado (D8, D13-c) ----------

test('the polling policy stops on a decided state, after 60 s and after 20 attempts', () => {
  assert.equal(PAYMENT_POLL_INTERVAL_MS, 3_000)
  assert.equal(PAYMENT_POLL_TIMEOUT_MS, 60_000)
  assert.equal(PAYMENT_POLL_MAX_ATTEMPTS, 20)
  assert.equal(pollDecision({ status: 'pending', elapsedMs: 0, attempt: 1 }), 'poll')
  assert.equal(pollDecision({ status: 'pending', elapsedMs: 57_000, attempt: 19 }), 'poll')
  // 60 s is the limit: no 21st request.
  assert.equal(pollDecision({ status: 'pending', elapsedMs: 60_000, attempt: 20 }), 'stop')
  assert.equal(pollDecision({ status: 'pending', elapsedMs: 0, attempt: 20 }), 'stop')
  for (const status of ['paid', 'expired', 'refunded', 'forfeited', 'otro']) {
    assert.equal(pollDecision({ status, elapsedMs: 0, attempt: 1 }), 'stop', status)
  }
})

test('normalizePublicDepositLookup reads the four keys of the public lookup', () => {
  assert.deepEqual(
    normalizePublicDepositLookup({
      status: 'paid',
      amount_cents: 5400,
      expires_at: '2026-11-02T16:30:00Z',
      starts_at: '2026-11-02T16:00:00Z',
    }),
    {
      status: 'paid',
      amountCents: 5400,
      expiresAt: '2026-11-02T16:30:00Z',
      startsAt: '2026-11-02T16:00:00Z',
    },
  )
  for (const bad of [null, 'nope', {}, { status: '', amount_cents: 100 }, { status: 'paid', amount_cents: -1 }]) {
    assert.equal(normalizePublicDepositLookup(bad), null, JSON.stringify(bad))
  }
})

test('depositStatusSentence says the state in Spanish for the public page', () => {
  assert.equal(depositStatusSentence('pending'), 'Estamos confirmando el pago simulado…')
  assert.equal(
    depositStatusSentence('paid'),
    'Pago confirmado: la seña está pagada y el turno queda reservado.',
  )
  assert.match(depositStatusSentence('expired'), /venció/i)
  assert.match(depositStatusSentence('refunded'), /devolvió/i)
  assert.match(depositStatusSentence('forfeited'), /perdió/i)
  assert.equal(depositStatusSentence('otro'), 'No pudimos leer el estado del pago.')
})

test('lookupPublicDeposit sends the charge id in the body, without a session, and keeps the 404 verbatim', async () => {
  let failing = false
  const stub = stubFetch(() =>
    failing
      ? json({ error: 'Pago no encontrado' }, 404)
      : json({
          status: 'pending',
          amount_cents: 5400,
          expires_at: '2026-11-02T16:30:00Z',
          starts_at: '2026-11-02T16:00:00Z',
        }),
  )
  try {
    setToken('jwt-that-must-not-travel')
    const found = await lookupPublicDeposit('charge_abc')
    assert.equal(found?.status, 'pending')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/public/deposits/lookup`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { charge_id: 'charge_abc' })
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
    // The charge id never travels in the URL (it is the credential).
    assert.equal(stub.calls[0]?.url.includes('charge_abc'), false)
    failing = true
    const error = await lookupPublicDeposit('charge_abc').then(
      () => null,
      (err: unknown) => err,
    )
    assert.ok(error instanceof ApiError)
    assert.equal(error.status, 404)
    assert.equal(error.message, 'Pago no encontrado')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('depositPolicyDraft type keeps the numbers as typed text', () => {
  const draft: DepositPolicyDraft = { enabled: false, percent: '', hold_minutes: '', refund_hours: '' }
  const policy: DepositPolicy | null = normalizeDepositPolicy(null)
  assert.equal(policy, null)
  assert.deepEqual(draftFromPolicy(null), draft)
})

test('formatPublicInstant renders a public date in the visitor zone and never «Invalid Date»', () => {
  assert.equal(formatPublicInstant('2026-11-26T18:40:00Z', 'America/Mexico_City'), '26 nov 2026, 12:40')
  assert.equal(formatPublicInstant('2026-11-26T18:40:00Z', 'UTC'), '26 nov 2026, 18:40')
  // An unusable zone falls back to UTC instead of throwing.
  assert.equal(formatPublicInstant('2026-11-26T18:40:00Z', 'No/Existe'), '26 nov 2026, 18:40')
  assert.equal(formatPublicInstant('no-es-fecha', 'UTC'), '—')
})
