import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, API_BASE } from '../../../lib/api.ts'
import { EXPENSE_CATEGORY_LABELS } from './expenses.ts'
import {
  canViewProfit,
  checkProfitInput,
  fetchProfit,
  formatProfitNet,
  INVALID_PROFIT_PERIOD_MESSAGE,
  normalizeProfit,
  PROFIT_KINDS,
  PROFIT_KIND_LABELS,
  PROFIT_PAYROLL_NOTICE,
  profitCategoryRows,
  profitDayBarLabel,
  profitDayBars,
  profitNetSign,
  profitPeriodLabel,
  type ProfitReport,
} from './profit.ts'
import { clearToken, setToken } from '../../../lib/session.ts'

type Call = { url: string; method: string; body: string | undefined; headers: Record<string, string> }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
      headers: (init?.headers ?? {}) as Record<string, string>,
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

function day(date: string, revenue: number, expenses: number, payroll: number, net: number) {
  return {
    date,
    revenue_cents: revenue,
    expenses_cents: expenses,
    payroll_cents: payroll,
    net_cents: net,
  }
}

const WEEK_REPORT = {
  period: { kind: 'week', from: '2026-11-02', to: '2026-11-08' },
  totals: { revenue_cents: 135000, expenses_cents: 45000, payroll_cents: 90000, net_cents: 0 },
  days: [
    day('2026-11-02', 50000, 0, 0, 50000),
    day('2026-11-03', 0, 45000, 0, -45000),
    day('2026-11-04', 0, 0, 0, 0),
    day('2026-11-05', 85000, 0, 90000, -5000),
    day('2026-11-06', 0, 0, 0, 0),
    day('2026-11-07', 0, 0, 0, 0),
    day('2026-11-08', 0, 0, 0, 0),
  ],
  expenses_by_category: [
    { category: 'rent', total_cents: 40000 },
    { category: 'supplies', total_cents: 5000 },
  ],
}

// --- lectura defensiva de GET /profit ---

test('normalizeProfit reads the frozen shape of the report', () => {
  const report = normalizeProfit(WEEK_REPORT)
  assert.ok(report)
  assert.deepEqual(report.period, { kind: 'week', from: '2026-11-02', to: '2026-11-08' })
  assert.deepEqual(report.totals, {
    revenue_cents: 135000,
    expenses_cents: 45000,
    payroll_cents: 90000,
    net_cents: 0,
  })
  assert.equal(report.days.length, 7)
  assert.deepEqual(report.expenses_by_category, [
    { category: 'rent', total_cents: 40000 },
    { category: 'supplies', total_cents: 5000 },
  ])
})

test('normalizeProfit fills a day the payload left empty with zeros instead of guessing', () => {
  const report = normalizeProfit({
    ...WEEK_REPORT,
    days: [{ date: '2026-11-04' }, { date: '2026-11-05', revenue_cents: 5000 }],
  })
  assert.deepEqual(report?.days, [
    { date: '2026-11-04', revenue_cents: 0, expenses_cents: 0, payroll_cents: 0, net_cents: 0 },
    {
      date: '2026-11-05',
      revenue_cents: 5000,
      expenses_cents: 0,
      payroll_cents: 0,
      net_cents: 5000,
    },
  ])
})

test('normalizeProfit keeps the sign of the net and derives it when it does not add up', () => {
  const report = normalizeProfit({
    ...WEEK_REPORT,
    days: [
      { date: '2026-11-05', revenue_cents: 85000, expenses_cents: 0, payroll_cents: 90000 },
      { date: '2026-11-06', revenue_cents: 85000, expenses_cents: 0, payroll_cents: 90000, net_cents: 0 },
    ],
  })
  assert.equal(report?.days[0]?.net_cents, -5000)
  // A net of exactly zero is a real value, not a missing one.
  assert.equal(report?.days[1]?.net_cents, 0)
})

test('normalizeProfit orders days ascending and drops an impossible date', () => {
  const report = normalizeProfit({
    ...WEEK_REPORT,
    days: [
      day('2026-11-03', 0, 0, 0, 0),
      day('2026-11-02', 0, 0, 0, 0),
      day('2026-02-30', 0, 0, 0, 0),
    ],
  })
  assert.deepEqual(
    report?.days.map((row) => row.date),
    ['2026-11-02', '2026-11-03'],
  )
})

test('normalizeProfit recomputes the totals from the days when they cannot be trusted', () => {
  const report = normalizeProfit({
    ...WEEK_REPORT,
    totals: { revenue_cents: 'mucho', expenses_cents: 0, payroll_cents: 0, net_cents: 0 },
  })
  assert.deepEqual(report?.totals, {
    revenue_cents: 135000,
    expenses_cents: 45000,
    payroll_cents: 90000,
    net_cents: 0,
  })
})

test('normalizeProfit keeps only the categories with a positive total', () => {
  const report = normalizeProfit({
    ...WEEK_REPORT,
    expenses_by_category: [
      { category: 'supplies', total_cents: 5000 },
      { category: 'other', total_cents: 0 },
      { category: 'rent', total_cents: -100 },
      { category: '', total_cents: 900 },
    ],
  })
  assert.deepEqual(report?.expenses_by_category, [{ category: 'supplies', total_cents: 5000 }])
})

test('normalizeProfit answers a usable empty report for a salon without movements', () => {
  const report = normalizeProfit({
    period: { kind: 'day', from: '2026-11-10', to: '2026-11-10' },
    totals: { revenue_cents: 0, expenses_cents: 0, payroll_cents: 0, net_cents: 0 },
    days: [day('2026-11-10', 0, 0, 0, 0)],
    expenses_by_category: [],
  })
  assert.equal(report?.totals.net_cents, 0)
  assert.equal(profitNetSign(report?.totals ?? { revenue_cents: 0, expenses_cents: 0, payroll_cents: 0, net_cents: 0 }), 'zero')
  assert.deepEqual(profitCategoryRows(report!), [])
})

test('normalizeProfit refuses a payload without a usable period', () => {
  assert.equal(normalizeProfit(null), null)
  assert.equal(normalizeProfit({ totals: WEEK_REPORT.totals })?.period, null)
  assert.equal(normalizeProfit({ ...WEEK_REPORT, period: { kind: 'year', from: '2026-01-01', to: '2026-12-31' } })?.period, null)
})

// --- ganancia neta: signo y color ---

test('profitNetSign separates a loss from a win and from zero', () => {
  const base = { revenue_cents: 0, expenses_cents: 0, payroll_cents: 0, net_cents: 0 }
  assert.equal(profitNetSign({ ...base, net_cents: -45000 }), 'negative')
  assert.equal(profitNetSign(base), 'zero')
  assert.equal(profitNetSign({ ...base, net_cents: 85000 }), 'positive')
})

test('formatProfitNet always shows the sign of a loss', () => {
  assert.equal(formatProfitNet(-45000), '-$450.00')
  assert.equal(formatProfitNet(0), '$0.00')
  assert.equal(formatProfitNet(125050), '$1,250.50')
})

// --- selector de período ---

test('checkProfitInput accepts the three kinds of the contract and refuses the rest', () => {
  assert.deepEqual(checkProfitInput('day', '2026-11-10'), { ok: true, kind: 'day', date: '2026-11-10' })
  assert.deepEqual(checkProfitInput('week', '2026-11-10'), { ok: true, kind: 'week', date: '2026-11-10' })
  assert.deepEqual(checkProfitInput('month', '2026-11-10'), { ok: true, kind: 'month', date: '2026-11-10' })
  for (const bad of [
    ['year', '2026-11-10'],
    ['day', '2026-02-30'],
    ['day', ''],
    [null, '2026-11-10'],
  ] as const) {
    const check = checkProfitInput(bad[0], bad[1])
    assert.equal(check.ok, false, `should refuse ${String(bad[0])} ${bad[1]}`)
    assert.equal(check.ok === false ? check.message : '', INVALID_PROFIT_PERIOD_MESSAGE)
  }
  assert.equal(INVALID_PROFIT_PERIOD_MESSAGE, 'Período inválido')
})

test('the selector offers día, semana and mes with their Spanish labels', () => {
  assert.deepEqual(PROFIT_KINDS, ['day', 'week', 'month'])
  assert.deepEqual(PROFIT_KIND_LABELS, { day: 'Día', week: 'Semana', month: 'Mes' })
})

test('profitPeriodLabel names the day and reuses the label of the payroll periods', () => {
  assert.equal(profitPeriodLabel({ kind: 'day', from: '2026-11-10', to: '2026-11-10' }), '10 de noviembre de 2026')
  assert.equal(profitPeriodLabel({ kind: 'week', from: '2026-11-02', to: '2026-11-08' }), 'Semana del 2 al 8 de noviembre')
  assert.equal(profitPeriodLabel({ kind: 'month', from: '2026-11-01', to: '2026-11-30' }), 'Noviembre de 2026')
})

// --- barras por día ---

test('profitDayBars scales every day against the best one and keeps the empty days', () => {
  const report = normalizeProfit({
    period: { kind: 'week', from: '2026-11-02', to: '2026-11-03' },
    totals: { revenue_cents: 150000, expenses_cents: 0, payroll_cents: 0, net_cents: 150000 },
    days: [day('2026-11-02', 100000, 0, 0, 100000), day('2026-11-03', 50000, 0, 0, 50000)],
    expenses_by_category: [],
  })
  const bars = profitDayBars(report!)
  assert.deepEqual(
    bars.map((bar) => [bar.date, bar.height]),
    [
      ['2026-11-02', 100],
      ['2026-11-03', 50],
    ],
  )
  assert.equal(bars[0]?.label, '2 nov')
})

test('profitDayBars gives a zero height to a period without revenue', () => {
  const report = normalizeProfit(WEEK_REPORT)
  const bars = profitDayBars(
    normalizeProfit({
      ...WEEK_REPORT,
      totals: { revenue_cents: 0, expenses_cents: 0, payroll_cents: 0, net_cents: 0 },
      days: [day('2026-11-04', 0, 0, 0, 0), day('2026-11-05', 0, 0, 0, 0)],
    })!,
  )
  assert.deepEqual(
    bars.map((bar) => bar.height),
    [0, 0],
  )
  assert.equal(report?.days.length, 7)
})

test('profitDayBars keeps the negative net of a day', () => {
  const bars = profitDayBars(normalizeProfit(WEEK_REPORT)!)
  assert.equal(bars.find((bar) => bar.date === '2026-11-05')?.net_cents, -5000)
})

test('profitDayBarLabel spells the day and both amounts for the accessible name', () => {
  assert.equal(
    profitDayBarLabel({
      date: '2026-11-05',
      label: '5 nov',
      height: 85,
      revenue_cents: 85000,
      net_cents: -5000,
    }),
    '5 de noviembre: ingresos $850.00, ganancia neta -$50.00',
  )
})

// --- desglose por categoría ---

test('profitCategoryRows labels the categories of the contract and scales the bar', () => {
  const rows = profitCategoryRows(normalizeProfit(WEEK_REPORT)!)
  assert.deepEqual(
    rows.map((row) => [row.category, row.label, row.total_cents, row.share]),
    [
      ['rent', 'Alquiler', 40000, 100],
      ['supplies', 'Insumos', 5000, 12.5],
    ],
  )
  assert.deepEqual(EXPENSE_CATEGORY_LABELS, {
    rent: 'Alquiler',
    supplies: 'Insumos',
    utilities: 'Servicios',
    other: 'Otros',
  })
})

test('profitCategoryRows keeps an unknown code readable instead of blank', () => {
  const report = normalizeProfit({
    ...WEEK_REPORT,
    expenses_by_category: [{ category: 'taxes', total_cents: 900 }],
  })
  assert.deepEqual(profitCategoryRows(report!), [
    { category: 'taxes', label: 'taxes', total_cents: 900, share: 100 },
  ])
})

// --- aviso fijo y permisos ---

test('the fixed notice says what payroll counts and what does not', () => {
  assert.equal(
    PROFIT_PAYROLL_NOTICE,
    'Incluye la nómina ya pagada; las liquidaciones abiertas y las comisiones sin liquidar no cuentan',
  )
})

test('only owner and admin see the profit report', () => {
  assert.equal(canViewProfit('owner'), true)
  assert.equal(canViewProfit('admin'), true)
  assert.equal(canViewProfit('professional'), false)
  assert.equal(canViewProfit('reception'), false)
  assert.equal(canViewProfit(null), false)
})

// --- cliente de la API ---

test('fetchProfit sends kind and date and reads the report', async () => {
  const stub = stubFetch(() => json(WEEK_REPORT))
  try {
    setToken('jwt-profit')
    const report = await fetchProfit('s1', { kind: 'week', date: '2026-11-05' })
    assert.equal(
      stub.calls[0]?.url,
      `${API_BASE}/api/v1/salons/s1/profit?kind=week&date=2026-11-05`,
    )
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-profit')
    assert.equal(report?.totals.revenue_cents, 135000)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a 400 «Período inválido» reaches the screen verbatim', async () => {
  const stub = stubFetch(() => json({ error: INVALID_PROFIT_PERIOD_MESSAGE }, 400))
  try {
    setToken('jwt-profit')
    await assert.rejects(
      () => fetchProfit('s1', { kind: 'month', date: '2026-13-01' }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 400 &&
        error.message === INVALID_PROFIT_PERIOD_MESSAGE,
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchProfit reports a 403 as an ApiError for the page to explain', async () => {
  const stub = stubFetch(() => json({ error: 'sin permiso' }, 403))
  try {
    setToken('jwt-profit')
    await assert.rejects(
      () => fetchProfit('s1', { kind: 'day', date: '2026-11-10' }),
      (error: unknown) => error instanceof ApiError && error.status === 403,
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

// El tipo del reporte normalizado es el que consumen las páginas.
const _reportType: ProfitReport | null = normalizeProfit(WEEK_REPORT)
assert.ok(_reportType)
