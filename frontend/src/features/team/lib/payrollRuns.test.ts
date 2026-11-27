import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, NetworkError } from '../../../lib/api.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  applyRunLineAdjustment,
  canDeleteRun,
  canEditRunLines,
  canManagePayrollRuns,
  canPayRun,
  createPayrollRun,
  csvDownloadFilename,
  deletePayrollRun,
  EXPORT_ERROR_MESSAGE,
  fetchPayrollRun,
  fetchPayrollRunCsv,
  fetchPayrollRuns,
  formatSignedCents,
  liquidateBlockReason,
  lineTotalCents,
  normalizePayrollRun,
  normalizePayrollRuns,
  normalizePayrollRunSummary,
  parseSignedAmountToCents,
  payPayrollRun,
  PERIOD_NOT_CLOSED_MESSAGE,
  restorePayrollRun,
  runCsvFilename,
  runKindLabel,
  runLabel,
  runStatusLabel,
  runTotalsFromLines,
  signedAmountInputFromCents,
  updateRunLineAdjustment,
  type PayrollRun,
  type PayrollRunLine,
} from './payrollRuns.ts'

type Call = { url: string; method: string; body: string | undefined; headers: Record<string, string> }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers: Record<string, string> = {}
    const raw = init?.headers
    if (raw && !Array.isArray(raw) && !(raw instanceof Headers)) {
      for (const [key, value] of Object.entries(raw)) {
        headers[key] = String(value)
      }
    } else if (raw instanceof Headers) {
      raw.forEach((value, key) => {
        headers[key] = value
      })
    }
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
      headers,
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

function csv(body: string, headers: Record<string, string> = {}): Response {
  return new Response(body, {
    status: 200,
    headers: { 'content-type': 'text/csv; charset=utf-8', ...headers },
  })
}

function line(over: Partial<PayrollRunLine> & Pick<PayrollRunLine, 'user_id'>): PayrollRunLine {
  return {
    name: over.user_id,
    active: true,
    sales_count: 0,
    sales_total_cents: 0,
    commission_cents: 0,
    fixed_cents: 0,
    adjustment_cents: 0,
    adjustment_note: '',
    total_cents: 0,
    estimated: false,
    ...over,
  }
}

function run(over: Partial<PayrollRun> = {}): PayrollRun {
  const lines = over.lines ?? [
    line({ user_id: 'u1', name: 'Ana', commission_cents: 1000, total_cents: 1000 }),
    line({ user_id: 'u2', name: 'Bea', fixed_cents: 500, total_cents: 500 }),
  ]
  return {
    id: 'r1',
    kind: 'week',
    from: '2026-11-03',
    to: '2026-11-09',
    status: 'open',
    created_at: '2026-11-11T10:00:00Z',
    paid_at: null,
    totals: runTotalsFromLines(lines),
    lines,
    ...over,
  }
}

// --- lectura defensiva del detalle (GET /payroll/runs/:rid) ---

test('normalizePayrollRun reads the frozen shape and drops the values it cannot trust', () => {
  const parsed = normalizePayrollRun({
    id: 'r1',
    kind: 'week',
    from: '2026-11-03',
    to: '2026-11-09',
    status: 'open',
    created_at: ' 2026-11-11T10:00:00Z ',
    paid_at: null,
    totals: {
      commission_cents: 1000,
      fixed_cents: 500,
      adjustment_cents: -200,
      total_cents: 1300,
    },
    lines: [
      {
        user_id: 'u1',
        name: ' Ana ',
        active: true,
        sales_count: 3,
        sales_total_cents: 120000,
        commission_cents: 1000,
        fixed_cents: 0,
        adjustment_cents: -200,
        adjustment_note: ' Descuento ',
        total_cents: 800,
        estimated: true,
      },
      {
        user_id: 'u2',
        name: '',
        active: false,
        sales_count: 2.5,
        sales_total_cents: -100,
        commission_cents: -500,
        fixed_cents: 12.5,
        adjustment_cents: 1.5,
        adjustment_note: 7,
        total_cents: -1,
        estimated: 'sí',
      },
      { name: 'sin id' },
    ],
  })
  assert.ok(parsed)
  assert.equal(parsed.id, 'r1')
  assert.equal(parsed.kind, 'week')
  assert.equal(parsed.status, 'open')
  assert.equal(parsed.created_at, '2026-11-11T10:00:00Z')
  assert.equal(parsed.paid_at, null)
  assert.deepEqual(parsed.totals, {
    commission_cents: 1000,
    fixed_cents: 500,
    adjustment_cents: -200,
    total_cents: 1300,
  })
  assert.equal(parsed.lines.length, 2)
  assert.deepEqual(parsed.lines[0], {
    user_id: 'u1',
    name: 'Ana',
    active: true,
    sales_count: 3,
    sales_total_cents: 120000,
    commission_cents: 1000,
    fixed_cents: 0,
    adjustment_cents: -200,
    adjustment_note: 'Descuento',
    total_cents: 800,
    estimated: true,
  })
  // Second line: every unusable number falls to 0, the name falls back to the id
  // and its total is recomputed from its concepts (0 + 0 + 0).
  assert.deepEqual(parsed.lines[1], {
    user_id: 'u2',
    name: 'u2',
    active: false,
    sales_count: 0,
    sales_total_cents: 0,
    commission_cents: 0,
    fixed_cents: 0,
    adjustment_cents: 0,
    adjustment_note: '',
    total_cents: 0,
    estimated: false,
  })
})

test('normalizePayrollRun recomputes invalid totals and refuses a run it cannot label', () => {
  const parsed = normalizePayrollRun({
    id: 'r1',
    kind: 'month',
    from: '2026-11-01',
    to: '2026-11-30',
    status: 'cerrada',
    totals: { commission_cents: 1.5, fixed_cents: 0, adjustment_cents: 0, total_cents: 500 },
    lines: [
      line({ user_id: 'u1', commission_cents: 700, fixed_cents: 300, total_cents: 1000 }),
      line({ user_id: 'u2', commission_cents: 100, total_cents: 100 }),
    ],
  })
  assert.ok(parsed)
  // An unknown status is read as paid: read-only is the safe side.
  assert.equal(parsed.status, 'paid')
  assert.deepEqual(parsed.totals, {
    commission_cents: 800,
    fixed_cents: 300,
    adjustment_cents: 0,
    total_cents: 1100,
  })
  assert.equal(parsed.lines[0]?.total_cents, 1000)

  assert.equal(normalizePayrollRun({ kind: 'week', from: '2026-11-03', to: '2026-11-09' }), null)
  assert.equal(
    normalizePayrollRun({ id: 'r1', kind: 'year', from: '2026-11-03', to: '2026-11-09' }),
    null,
  )
  assert.equal(
    normalizePayrollRun({ id: 'r1', kind: 'week', from: '2026-02-30', to: '2026-11-09' }),
    null,
  )
})

// --- lectura defensiva de la lista (GET /payroll/runs) ---

test('normalizePayrollRuns reads the list newest first and drops rows without id', () => {
  const runs = normalizePayrollRuns({
    runs: [
      {
        id: 'old',
        kind: 'month',
        from: '2026-10-01',
        to: '2026-10-31',
        status: 'paid',
        created_at: '2026-11-02T10:00:00Z',
        paid_at: '2026-11-03T10:00:00Z',
        lines_count: 2,
        totals: {
          commission_cents: 500,
          fixed_cents: 0,
          adjustment_cents: 0,
          total_cents: 500,
        },
      },
      {
        id: 'new',
        kind: 'week',
        from: '2026-11-03',
        to: '2026-11-09',
        status: 'open',
        created_at: '2026-11-11T10:00:00Z',
        paid_at: null,
        lines_count: 1.5,
        totals: { commission_cents: 1.5, fixed_cents: -2, adjustment_cents: 0, total_cents: 0 },
      },
      { kind: 'week' },
    ],
  })
  assert.deepEqual(
    runs.map((row) => row.id),
    ['new', 'old'],
  )
  assert.equal(runs[0]?.lines_count, 0)
  // A summary without trustworthy totals shows zeros instead of a made-up number.
  assert.deepEqual(runs[0]?.totals, {
    commission_cents: 0,
    fixed_cents: 0,
    adjustment_cents: 0,
    total_cents: 0,
  })
  assert.equal(runs[1]?.paid_at, '2026-11-03T10:00:00Z')

  const bare = normalizePayrollRuns([
    { id: 'a', kind: 'week', from: '2026-11-03', to: '2026-11-09', status: 'open' },
  ])
  assert.equal(bare.length, 1)
  assert.equal(bare[0]?.lines_count, 0)
  assert.equal(normalizePayrollRunSummary({}), null)
  assert.deepEqual(normalizePayrollRuns(null), [])
})

// --- montos con signo, siempre centavos enteros ---

test('parseSignedAmountToCents reads a signed amount with the amount parser', () => {
  assert.equal(parseSignedAmountToCents('0'), 0)
  assert.equal(parseSignedAmountToCents('1,234.55'), 123455)
  assert.equal(parseSignedAmountToCents('-500'), -50000)
  assert.equal(parseSignedAmountToCents('+12.5'), 1250)
  assert.equal(parseSignedAmountToCents('$-1,000.25'), -100025)
  assert.equal(parseSignedAmountToCents('-0.50'), -50)
  assert.equal(parseSignedAmountToCents('1.234'), null)
  assert.equal(parseSignedAmountToCents('abc'), null)
  assert.equal(parseSignedAmountToCents(''), null)
  assert.equal(parseSignedAmountToCents('--5'), null)
})

test('signedAmountInputFromCents writes the field with integer arithmetic', () => {
  assert.equal(formatSignedCents(-500), '-$5.00')
  assert.equal(formatSignedCents(1250), '$12.50')
  assert.equal(signedAmountInputFromCents(0), '0.00')
  assert.equal(signedAmountInputFromCents(-1250), '-12.50')
  assert.equal(signedAmountInputFromCents(150000), '1500.00')
  assert.equal(signedAmountInputFromCents(12.5), '0.00')
  assert.equal(signedAmountInputFromCents(Number.NaN), '0.00')
})

test('the line total is commission + fixed + signed adjustment in integer cents', () => {
  assert.equal(
    lineTotalCents(
      line({ user_id: 'u1', commission_cents: 1000, fixed_cents: 500, adjustment_cents: -200 }),
    ),
    1300,
  )
  assert.equal(
    lineTotalCents(line({ user_id: 'u1', commission_cents: 100, adjustment_cents: -100 })),
    0,
  )
})

test('runTotalsFromLines sums every concept with the sign of the adjustment', () => {
  const totals = runTotalsFromLines([
    line({ user_id: 'u1', commission_cents: 1000, fixed_cents: 0, adjustment_cents: -500 }),
    line({ user_id: 'u2', commission_cents: 250, fixed_cents: 5000, adjustment_cents: 300 }),
  ])
  assert.deepEqual(totals, {
    commission_cents: 1250,
    fixed_cents: 5000,
    adjustment_cents: -200,
    total_cents: 6050,
  })
  assert.deepEqual(runTotalsFromLines([]), {
    commission_cents: 0,
    fixed_cents: 0,
    adjustment_cents: 0,
    total_cents: 0,
  })
})

// --- guardado optimista del ajuste con vuelta atrás exacta ---

test('applyRunLineAdjustment paints the line and the totals without mutating the run', () => {
  const before = run()
  const after = applyRunLineAdjustment(before, 'u1', {
    adjustment_cents: -500,
    adjustment_note: 'Descuento acordado',
  })
  const changed = after.lines.find((row) => row.user_id === 'u1')
  assert.equal(changed?.adjustment_cents, -500)
  assert.equal(changed?.adjustment_note, 'Descuento acordado')
  assert.equal(changed?.total_cents, 500)
  assert.deepEqual(after.totals, {
    commission_cents: 1000,
    fixed_cents: 500,
    adjustment_cents: -500,
    total_cents: 1000,
  })
  // The snapshot the caller keeps is untouched and comes back identically.
  assert.equal(before.lines[0]?.adjustment_cents, 0)
  assert.equal(before.lines[0]?.total_cents, 1000)
  assert.equal(before.totals.adjustment_cents, 0)
  assert.equal(restorePayrollRun(before), before)
  assert.notEqual(after, before)
})

test('applyRunLineAdjustment leaves the other lines alone and ignores an unknown person', () => {
  const before = run()
  const after = applyRunLineAdjustment(before, 'u2', {
    adjustment_cents: 250,
    adjustment_note: 'Bono',
  })
  assert.equal(after.lines[0]?.adjustment_cents, 0)
  assert.equal(after.lines[1]?.adjustment_cents, 250)
  assert.equal(after.lines[1]?.total_cents, 750)
  const untouched = applyRunLineAdjustment(before, 'nadie', {
    adjustment_cents: 100,
    adjustment_note: 'x',
  })
  assert.deepEqual(untouched.lines, before.lines)
  assert.deepEqual(untouched.totals, before.totals)
})

// --- compuertas de la corrida y del botón de liquidar ---

test('only an open run can be edited, paid or deleted', () => {
  const open = run()
  const paid = run({ status: 'paid', paid_at: '2026-11-12T10:00:00Z' })
  assert.equal(canEditRunLines(open), true)
  assert.equal(canDeleteRun(open), true)
  assert.equal(canPayRun(open), true)
  assert.equal(canEditRunLines(paid), false)
  assert.equal(canDeleteRun(paid), false)
  assert.equal(canPayRun(paid), false)
})

test('the liquidate button needs a closed period and says why it cannot', () => {
  assert.equal(
    liquidateBlockReason({ kind: 'week', from: '2026-11-03', to: '2026-11-09', closed: true }),
    null,
  )
  assert.equal(
    liquidateBlockReason({ kind: 'week', from: '2026-11-03', to: '2026-11-09', closed: false }),
    PERIOD_NOT_CLOSED_MESSAGE,
  )
  assert.equal(liquidateBlockReason(null), 'Elegí un período para liquidar')
})

test('only owner and administration manage payroll runs', () => {
  assert.equal(canManagePayrollRuns('owner'), true)
  assert.equal(canManagePayrollRuns('admin'), true)
  assert.equal(canManagePayrollRuns('professional'), false)
  assert.equal(canManagePayrollRuns('reception'), false)
  assert.equal(canManagePayrollRuns(null), false)
})

// --- rótulos en español ---

test('runs are labelled in Spanish with their kind, dates and status', () => {
  assert.equal(runKindLabel('week'), 'Semana')
  assert.equal(runKindLabel('fortnight'), 'Quincena')
  assert.equal(runKindLabel('month'), 'Mes')
  assert.equal(runStatusLabel('open'), 'Abierta')
  assert.equal(runStatusLabel('paid'), 'Pagada')
  assert.equal(runLabel(run()), 'Semana del 3 al 9 de noviembre')
})

// --- nombre del archivo del CSV ---

test('the CSV filename is taken from Content-Disposition and sanitized', () => {
  assert.equal(
    csvDownloadFilename(
      'attachment; filename="nomina-week-2026-11-03-2026-11-09.csv"',
      'nomina.csv',
    ),
    'nomina-week-2026-11-03-2026-11-09.csv',
  )
  assert.equal(
    csvDownloadFilename("attachment; filename*=UTF-8''nomina%2Dweek.csv", 'nomina.csv'),
    'nomina-week.csv',
  )
  assert.equal(csvDownloadFilename('attachment; filename="../../etc/passwd"', 'nomina.csv'), 'etc-passwd.csv')
  assert.equal(csvDownloadFilename('attachment; filename=""', 'nomina.csv'), 'nomina.csv')
  assert.equal(csvDownloadFilename(null, 'nomina-week-2026-11-03-2026-11-09.csv'), 'nomina-week-2026-11-03-2026-11-09.csv')
  assert.equal(csvDownloadFilename('attachment; filename=" =cmd.csv"', 'nomina.csv'), 'cmd.csv')
  assert.equal(runCsvFilename(run()), 'nomina-week-2026-11-03-2026-11-09.csv')
})

// --- cliente de la API (formas congeladas de F3.3) ---

test('createPayrollRun posts kind and date and reads the created run', async () => {
  const stub = stubFetch(() => json({ ...run(), status: 'open' }, 201))
  try {
    const created = await createPayrollRun('s1', { kind: 'week', date: '2026-11-03' })
    assert.equal(created.id, 'r1')
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/payroll/runs')
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      kind: 'week',
      date: '2026-11-03',
    })
  } finally {
    stub.restore()
  }
})

test('fetchPayrollRuns and fetchPayrollRun read the two GETs', async () => {
  const stub = stubFetch((call) => {
    if (call.url.endsWith('/payroll/runs')) {
      return json({ runs: [{ id: 'r1', kind: 'week', from: '2026-11-03', to: '2026-11-09', status: 'open' }] })
    }
    return json(run())
  })
  try {
    const runs = await fetchPayrollRuns('s1')
    assert.equal(runs[0]?.id, 'r1')
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/payroll/runs')

    const detail = await fetchPayrollRun('s1', 'r1')
    assert.equal(detail.lines.length, 2)
    assert.equal(stub.calls[1]?.url, '/api/v1/salons/s1/payroll/runs/r1')
    assert.equal(stub.calls[1]?.method, 'GET')
  } finally {
    stub.restore()
  }
})

test('updateRunLineAdjustment sends the frozen body to the PATCH', async () => {
  const stub = stubFetch(() => json(run()))
  try {
    await updateRunLineAdjustment('s1', 'r1', 'u 1', {
      adjustment_cents: -500,
      adjustment_note: 'Descuento',
    })
    assert.equal(stub.calls[0]?.method, 'PATCH')
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/payroll/runs/r1/lines/u%201')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      adjustment_cents: -500,
      adjustment_note: 'Descuento',
    })
  } finally {
    stub.restore()
  }
})

test('payPayrollRun and deletePayrollRun use their verbs', async () => {
  const stub = stubFetch((call) =>
    call.method === 'DELETE' ? new Response(null, { status: 204 }) : json(run({ status: 'paid' })),
  )
  try {
    const paid = await payPayrollRun('s1', 'r1')
    assert.equal(paid.status, 'paid')
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/payroll/runs/r1/pay')
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.equal(stub.calls[0]?.body, undefined)

    await deletePayrollRun('s1', 'r1')
    assert.equal(stub.calls[1]?.url, '/api/v1/salons/s1/payroll/runs/r1')
    assert.equal(stub.calls[1]?.method, 'DELETE')
  } finally {
    stub.restore()
  }
})

test('a 409 of a paid run keeps the Spanish sentence verbatim', async () => {
  const stub = stubFetch(() =>
    json({ error: 'La liquidación ya está pagada: no se puede modificar' }, 409),
  )
  try {
    await assert.rejects(
      () =>
        updateRunLineAdjustment('s1', 'r1', 'u1', {
          adjustment_cents: 0,
          adjustment_note: '',
        }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 409 &&
        error.message === 'La liquidación ya está pagada: no se puede modificar',
    )
  } finally {
    stub.restore()
  }
})

// --- exportación autenticada (D8) ---

test('fetchPayrollRunCsv downloads the file with the bearer token and reads its name', async () => {
  setToken('tok-123')
  const stub = stubFetch(() =>
    csv('\ufeffProfesional,Total\r\nAna,10.00\r\n', {
      'content-disposition': 'attachment; filename="nomina-week-2026-11-03-2026-11-09.csv"',
    }),
  )
  try {
    const file = await fetchPayrollRunCsv('s1', 'r1')
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/payroll/runs/r1/export.csv')
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer tok-123')
    assert.equal(file.filename, 'nomina-week-2026-11-03-2026-11-09.csv')
    // `Response.text()` drops the UTF-8 BOM of the body; the bytes travel as-is.
    assert.equal((await file.blob.text()).trim(), 'Profesional,Total\r\nAna,10.00')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchPayrollRunCsv falls back to the safe run filename', async () => {
  const stub = stubFetch(() => csv('Profesional\r\n'))
  try {
    const file = await fetchPayrollRunCsv('s1', 'r1')
    assert.equal(file.filename, 'nomina.csv')
  } finally {
    stub.restore()
  }
})

test('the CSV export reports a server error verbatim and a network failure politely', async () => {
  const stub = stubFetch(() => json({ error: 'La liquidación ya está pagada' }, 409))
  try {
    await assert.rejects(
      () => fetchPayrollRunCsv('s1', 'r1'),
      (error: unknown) =>
        error instanceof ApiError && error.status === 409 && error.message === 'La liquidación ya está pagada',
    )
  } finally {
    stub.restore()
  }

  const offline = stubFetch(() => {
    throw new TypeError('fetch failed')
  })
  try {
    await assert.rejects(
      () => fetchPayrollRunCsv('s1', 'r1'),
      (error: unknown) => error instanceof NetworkError && error.message === EXPORT_ERROR_MESSAGE,
    )
  } finally {
    offline.restore()
  }
})
