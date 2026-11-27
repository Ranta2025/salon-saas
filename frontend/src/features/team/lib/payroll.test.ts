import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError } from '../../../lib/api.ts'
import {
  amountInputFromCents,
  applyPayModel,
  canManagePayroll,
  canViewOwnCommissions,
  checkPayrollInput,
  ESTIMATED_NOTICE,
  fetchMyCommissions,
  fetchPayModels,
  isPayPeriodKind,
  isValidPayrollDate,
  normalizeMyCommissions,
  normalizePayModel,
  normalizePayModels,
  normalizePayPeriod,
  normalizePayrollPreview,
  OPEN_PERIOD_NOTICE,
  PAY_MODEL_EXPLANATION,
  PAY_PERIOD_KINDS,
  PAY_PERIOD_KIND_LABELS,
  payModelSummary,
  payPeriodLabel,
  payrollSalesTotals,
  payrollTotals,
  previewPayroll,
  restorePayModel,
  setMemberPayModel,
  type PayModel,
  type PayPeriodKind,
} from './payroll.ts'

type Call = { url: string; method: string; body: string | undefined }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
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

function model(over: Partial<PayModel> & Pick<PayModel, 'user_id'>): PayModel {
  return {
    name: over.user_id,
    role: 'professional',
    active: true,
    fixed_cents: 0,
    fixed_period: 'month',
    commission_enabled: true,
    ...over,
  }
}

// --- lectura de los modelos de pago (GET/PUT /pay-models) ---

test('normalizePayModels reads every member and drops the values it cannot trust', () => {
  const models = normalizePayModels({
    pay_models: [
      {
        user_id: 'u1',
        name: ' Ana ',
        role: 'owner',
        active: true,
        fixed_cents: 150000,
        fixed_period: 'week',
        commission_enabled: false,
      },
      {
        user_id: 'u2',
        name: '',
        role: 'professional',
        active: false,
        fixed_cents: -100,
        fixed_period: 'anio',
        commission_enabled: 'sí',
      },
      {
        user_id: 'u3',
        fixed_cents: 12.5,
        fixed_period: 'fortnight',
      },
      { name: 'sin id' },
    ],
  })
  assert.deepEqual(models, [
    {
      user_id: 'u1',
      name: 'Ana',
      role: 'owner',
      active: true,
      fixed_cents: 150000,
      fixed_period: 'week',
      commission_enabled: false,
    },
    {
      user_id: 'u2',
      name: 'u2',
      role: 'professional',
      active: false,
      fixed_cents: 0,
      fixed_period: 'month',
      commission_enabled: true,
    },
    {
      user_id: 'u3',
      name: 'u3',
      role: 'professional',
      active: true,
      fixed_cents: 0,
      fixed_period: 'fortnight',
      commission_enabled: true,
    },
  ])
  assert.deepEqual(normalizePayModels(null), [])
  assert.deepEqual(normalizePayModels({ pay_models: 'nope' }), [])
  // A bare array is accepted too.
  assert.equal(normalizePayModels([{ user_id: 'u9' }]).length, 1)
})

test('normalizePayModel reads the object of the PUT, bare or wrapped', () => {
  const bare = normalizePayModel({
    user_id: 'u1',
    name: 'Ana',
    role: 'admin',
    active: true,
    fixed_cents: 0,
    fixed_period: 'month',
    commission_enabled: true,
  })
  assert.equal(bare?.fixed_period, 'month')
  const wrapped = normalizePayModel({ pay_model: { user_id: 'u2', fixed_cents: 900 } })
  assert.equal(wrapped?.user_id, 'u2')
  assert.equal(wrapped?.fixed_cents, 900)
  assert.equal(normalizePayModel(null), null)
  assert.equal(normalizePayModel({ name: 'sin id' }), null)
})

// --- el período (D5) ---

test('normalizePayPeriod reads kind, from, to and closed defensively', () => {
  assert.deepEqual(
    normalizePayPeriod({
      kind: 'week',
      from: '2026-11-03',
      to: '2026-11-09',
      closed: false,
    }),
    { kind: 'week', from: '2026-11-03', to: '2026-11-09', closed: false },
  )
  // A missing or garbage flag is an open period: the screen warns instead of lying.
  assert.deepEqual(
    normalizePayPeriod({ kind: 'month', from: '2026-11-01', to: '2026-11-30', closed: 'no' }),
    { kind: 'month', from: '2026-11-01', to: '2026-11-30', closed: false },
  )
  assert.equal(normalizePayPeriod({ kind: 'year', from: '2026-01-01', to: '2026-12-31' }), null)
  assert.equal(normalizePayPeriod({ kind: 'week', from: '2026-02-30', to: '2026-03-01' }), null)
  assert.equal(normalizePayPeriod({ kind: 'week', from: '2026-11-03' }), null)
  assert.equal(normalizePayPeriod(null), null)
})

test('payPeriodLabel writes the three periods of the contract in Spanish', () => {
  assert.equal(
    payPeriodLabel({ kind: 'week', from: '2026-11-03', to: '2026-11-09' }),
    'Semana del 3 al 9 de noviembre',
  )
  assert.equal(
    payPeriodLabel({ kind: 'fortnight', from: '2026-11-01', to: '2026-11-15' }),
    'Quincena del 1 al 15 de noviembre',
  )
  assert.equal(
    payPeriodLabel({ kind: 'month', from: '2026-11-01', to: '2026-11-30' }),
    'Noviembre de 2026',
  )
})

test('payPeriodLabel handles a week across months and years', () => {
  assert.equal(
    payPeriodLabel({ kind: 'week', from: '2026-11-30', to: '2026-12-06' }),
    'Semana del 30 de noviembre al 6 de diciembre',
  )
  assert.equal(
    payPeriodLabel({ kind: 'fortnight', from: '2026-12-16', to: '2026-12-31' }),
    'Quincena del 16 al 31 de diciembre',
  )
  assert.equal(
    payPeriodLabel({ kind: 'week', from: '2025-12-29', to: '2026-01-04' }),
    'Semana del 29 de diciembre de 2025 al 4 de enero de 2026',
  )
  // A "month" whose edges are not the same month still prints its real range.
  assert.equal(
    payPeriodLabel({ kind: 'month', from: '2026-11-01', to: '2026-12-01' }),
    'Mes del 1 de noviembre al 1 de diciembre',
  )
  // An unusable range never invents a day.
  assert.equal(payPeriodLabel({ kind: 'week', from: 'nope', to: '2026-11-09' }), 'Período')
})

test('kind and date validation keeps the sentence of the API', () => {
  assert.deepEqual(PAY_PERIOD_KINDS, ['week', 'fortnight', 'month'])
  assert.equal(isPayPeriodKind('week'), true)
  assert.equal(isPayPeriodKind('quincena'), false)

  assert.equal(isValidPayrollDate('2026-11-03'), true)
  assert.equal(isValidPayrollDate('2028-02-29'), true)
  assert.equal(isValidPayrollDate('2026-02-29'), false)
  assert.equal(isValidPayrollDate('2026-02-30'), false)
  assert.equal(isValidPayrollDate('2026-13-01'), false)
  assert.equal(isValidPayrollDate('2026-1-1'), false)
  assert.equal(isValidPayrollDate(''), false)

  assert.deepEqual(checkPayrollInput('week', '2026-11-03'), {
    ok: true,
    kind: 'week',
    date: '2026-11-03',
  })
  assert.deepEqual(checkPayrollInput('year', '2026-11-03'), {
    ok: false,
    message: 'Período inválido',
  })
  assert.deepEqual(checkPayrollInput('week', '2026-02-30'), {
    ok: false,
    message: 'Período inválido',
  })
})

// --- vista previa (POST /payroll/preview) ---

test('normalizePayrollPreview reads lines, the estimated flag and the period', () => {
  const preview = normalizePayrollPreview({
    period: { kind: 'fortnight', from: '2026-11-01', to: '2026-11-15', closed: true },
    lines: [
      {
        user_id: 'u1',
        name: 'Ana',
        active: true,
        sales_count: 3,
        sales_total_cents: 90000,
        commission_cents: 9000,
        fixed_cents: 50000,
        total_cents: 59000,
        estimated: true,
      },
      {
        user_id: 'u2',
        name: 'Bea',
        active: false,
        sales_count: -2,
        sales_total_cents: 12.5,
        commission_cents: '900',
        fixed_cents: -1,
        estimated: 'sí',
      },
      { name: 'sin id' },
    ],
    totals: { commission_cents: 9000, fixed_cents: 50000, total_cents: 59000 },
  })
  assert.equal(preview.period?.kind, 'fortnight')
  assert.equal(preview.period?.closed, true)
  assert.deepEqual(preview.lines, [
    {
      user_id: 'u1',
      name: 'Ana',
      active: true,
      sales_count: 3,
      sales_total_cents: 90000,
      commission_cents: 9000,
      fixed_cents: 50000,
      total_cents: 59000,
      estimated: true,
    },
    {
      user_id: 'u2',
      name: 'Bea',
      active: false,
      sales_count: 0,
      sales_total_cents: 0,
      commission_cents: 0,
      fixed_cents: 0,
      // A missing total is the integer sum of the two concepts, never a guess.
      total_cents: 0,
      estimated: false,
    },
  ])
  assert.deepEqual(preview.totals, { commission_cents: 9000, fixed_cents: 50000, total_cents: 59000 })
})

test('normalizePayrollPreview recomputes totals it cannot trust and keeps integer cents', () => {
  const preview = normalizePayrollPreview({
    period: { kind: 'week', from: '2026-11-03', to: '2026-11-09' },
    lines: [
      { user_id: 'u1', commission_cents: 1234, fixed_cents: 5000 },
      { user_id: 'u2', commission_cents: 0, fixed_cents: 2500, total_cents: 2500 },
    ],
    totals: { commission_cents: -5, fixed_cents: 1.5, total_cents: 'x' },
  })
  assert.deepEqual(preview.totals, {
    commission_cents: 1234,
    fixed_cents: 7500,
    total_cents: 8734,
  })
  assert.equal(preview.period?.closed, false)
  assert.deepEqual(normalizePayrollPreview(null), {
    period: null,
    lines: [],
    totals: { commission_cents: 0, fixed_cents: 0, total_cents: 0 },
  })
  assert.deepEqual(payrollTotals([]), {
    commission_cents: 0,
    fixed_cents: 0,
    total_cents: 0,
  })
})

test('amountInputFromCents writes the field of the fixed amount without dividing', () => {
  assert.equal(amountInputFromCents(0), '0.00')
  assert.equal(amountInputFromCents(150000), '1500.00')
  assert.equal(amountInputFromCents(1234), '12.34')
  assert.equal(amountInputFromCents(5), '0.05')
  assert.equal(amountInputFromCents(-1), '0.00')
  assert.equal(amountInputFromCents(1.5), '0.00')
})

test('payrollSalesTotals adds the count and the sold amount of the period', () => {
  const lines = normalizePayrollPreview({
    lines: [
      { user_id: 'u1', sales_count: 3, sales_total_cents: 90000 },
      { user_id: 'u2', sales_count: 1, sales_total_cents: 15050 },
      { user_id: 'u3', sales_count: -1, sales_total_cents: 12.5 },
    ],
  }).lines
  assert.deepEqual(payrollSalesTotals(lines), { sales_count: 4, sales_total_cents: 105050 })
  assert.deepEqual(payrollSalesTotals([]), { sales_count: 0, sales_total_cents: 0 })
})

test('payrollTotals sums integer cents without passing through a float', () => {
  const lines = normalizePayrollPreview({
    lines: [
      { user_id: 'u1', commission_cents: 10, fixed_cents: 20, total_cents: 30 },
      { user_id: 'u2', commission_cents: 900719, fixed_cents: 0, total_cents: 900719 },
    ],
  }).lines
  assert.deepEqual(payrollTotals(lines), {
    commission_cents: 900729,
    fixed_cents: 20,
    total_cents: 900749,
  })
})

// --- mis comisiones (GET /payroll/me) ---

test('normalizeMyCommissions reads only the caller figures', () => {
  const mine = normalizeMyCommissions({
    period: { kind: 'week', from: '2026-11-03', to: '2026-11-09', closed: false },
    sales_count: 4,
    sales_total_cents: 120000,
    commission_cents: 12000,
    estimated: true,
  })
  assert.deepEqual(mine, {
    period: { kind: 'week', from: '2026-11-03', to: '2026-11-09', closed: false },
    sales_count: 4,
    sales_total_cents: 120000,
    commission_cents: 12000,
    estimated: true,
  })
  assert.deepEqual(normalizeMyCommissions(null), {
    period: null,
    sales_count: 0,
    sales_total_cents: 0,
    commission_cents: 0,
    estimated: false,
  })
  assert.deepEqual(normalizeMyCommissions({ sales_count: -1, commission_cents: 1.25 }), {
    period: null,
    sales_count: 0,
    sales_total_cents: 0,
    commission_cents: 0,
    estimated: false,
  })
})

// --- quién ve qué (D7) ---

test('role gates: payroll for owner/admin, own commissions for owner/admin/professional', () => {
  assert.equal(canManagePayroll('owner'), true)
  assert.equal(canManagePayroll('admin'), true)
  assert.equal(canManagePayroll('professional'), false)
  assert.equal(canManagePayroll('reception'), false)
  assert.equal(canManagePayroll(null), false)

  assert.equal(canViewOwnCommissions('owner'), true)
  assert.equal(canViewOwnCommissions('admin'), true)
  assert.equal(canViewOwnCommissions('professional'), true)
  assert.equal(canViewOwnCommissions('reception'), false)
  assert.equal(canViewOwnCommissions(null), false)
})

// --- modelo de pago: guardado optimista y vuelta atrás exacta ---

test('applyPayModel changes only that person and never mutates the list', () => {
  const list = [model({ user_id: 'u1' }), model({ user_id: 'u2', name: 'Bea' })]
  const next = applyPayModel(list, 'u2', {
    fixed_cents: 500000,
    fixed_period: 'fortnight',
    commission_enabled: false,
  })
  assert.deepEqual(next[1], {
    user_id: 'u2',
    name: 'Bea',
    role: 'professional',
    active: true,
    fixed_cents: 500000,
    fixed_period: 'fortnight',
    commission_enabled: false,
  })
  assert.deepEqual(list[1], model({ user_id: 'u2', name: 'Bea' }))
  assert.notEqual(next, list)
  // Unknown person: same content, still a new list.
  assert.deepEqual(applyPayModel(list, 'nope', { fixed_cents: 1 }), list)
})

test('restorePayModel puts back the exact previous model', () => {
  const previous = model({ user_id: 'u2', fixed_cents: 900, fixed_period: 'week' })
  const optimistic = applyPayModel([model({ user_id: 'u1' }), previous], 'u2', {
    fixed_cents: 0,
    commission_enabled: false,
  })
  const rolled = restorePayModel(optimistic, previous)
  assert.deepEqual(rolled[1], previous)
  // The row goes back to the snapshot, other rows untouched.
  assert.deepEqual(rolled[0], model({ user_id: 'u1' }))
})

// --- copia del modelo y del estado ---

test('the explanation of the model, the badge and the notices are the contract copy', () => {
  assert.match(PAY_MODEL_EXPLANATION, /fijo \+ comisión/i)
  assert.match(PAY_MODEL_EXPLANATION, /período/i)
  assert.equal(
    ESTIMATED_NOTICE,
    'Incluye ventas anteriores a la nómina: su comisión se calcula con el porcentaje actual del servicio',
  )
  assert.equal(OPEN_PERIOD_NOTICE, 'Período en curso: las cifras pueden cambiar')
  assert.deepEqual(PAY_PERIOD_KIND_LABELS, {
    week: 'Semana',
    fortnight: 'Quincena',
    month: 'Mes',
  })
})

test('payModelSummary writes the model in Spanish with cents formatted', () => {
  assert.equal(
    payModelSummary(model({ user_id: 'u1', fixed_cents: 150000, fixed_period: 'month' })),
    'Fijo $1,500.00 por mes + comisión',
  )
  assert.equal(
    payModelSummary(
      model({ user_id: 'u2', fixed_cents: 50000, fixed_period: 'week', commission_enabled: false }),
    ),
    'Fijo $500.00 por semana, sin comisión',
  )
  assert.equal(payModelSummary(model({ user_id: 'u3' })), 'Solo comisión')
  assert.equal(
    payModelSummary(model({ user_id: 'u4', commission_enabled: false })),
    'Sin pago fijo ni comisión',
  )
})

// --- cliente de la API ---

test('fetchPayModels reads GET /pay-models', async () => {
  const stub = stubFetch(() =>
    json({ pay_models: [{ user_id: 'u1', fixed_cents: 100, fixed_period: 'week' }] }),
  )
  try {
    const models = await fetchPayModels('s1')
    assert.equal(models[0]?.user_id, 'u1')
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/pay-models')
    assert.equal(stub.calls[0]?.method, 'GET')
  } finally {
    stub.restore()
  }
})

test('setMemberPayModel sends the three fields to the PUT and reads the answer', async () => {
  const stub = stubFetch((call) => {
    assert.equal(call.method, 'PUT')
    return json({
      user_id: 'u1',
      name: 'Ana',
      role: 'professional',
      active: true,
      fixed_cents: 150000,
      fixed_period: 'month',
      commission_enabled: true,
    })
  })
  try {
    const saved = await setMemberPayModel('s1', 'u1', {
      fixed_cents: 150000,
      fixed_period: 'month',
      commission_enabled: true,
    })
    assert.equal(saved.fixed_cents, 150000)
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/members/u1/pay-model')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      fixed_cents: 150000,
      fixed_period: 'month',
      commission_enabled: true,
    })
  } finally {
    stub.restore()
  }
})

test('the API errors of the model keep the Spanish sentence verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'El monto fijo no puede ser negativo' }, 400))
  try {
    await assert.rejects(
      () => setMemberPayModel('s1', 'u1', { fixed_cents: 0 }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 400 &&
        error.message === 'El monto fijo no puede ser negativo',
    )
  } finally {
    stub.restore()
  }
})

test('previewPayroll posts kind and date and reads the preview', async () => {
  const stub = stubFetch(() =>
    json({
      period: { kind: 'week', from: '2026-11-03', to: '2026-11-09', closed: false },
      lines: [{ user_id: 'u1', commission_cents: 500 }],
      totals: { commission_cents: 500, fixed_cents: 0, total_cents: 500 },
    }),
  )
  try {
    const preview = await previewPayroll('s1', { kind: 'week', date: '2026-11-03' })
    assert.equal(preview.lines[0]?.commission_cents, 500)
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/payroll/preview')
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      kind: 'week',
      date: '2026-11-03',
    })
  } finally {
    stub.restore()
  }
})

test('fetchMyCommissions reads GET /payroll/me with kind and date', async () => {
  const stub = stubFetch(() =>
    json({ period: { kind: 'month', from: '2026-11-01', to: '2026-11-30' }, commission_cents: 900 }),
  )
  try {
    const mine = await fetchMyCommissions('s1', { kind: 'month', date: '2026-11-20' })
    assert.equal(mine.commission_cents, 900)
    assert.equal(
      stub.calls[0]?.url,
      '/api/v1/salons/s1/payroll/me?kind=month&date=2026-11-20',
    )
    assert.equal(stub.calls[0]?.method, 'GET')
  } finally {
    stub.restore()
  }
})

test('a 400 «Período inválido» of the preview is shown verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'Período inválido' }, 400))
  try {
    await assert.rejects(
      () => previewPayroll('s1', { kind: 'week' as PayPeriodKind, date: '2026-02-30' }),
      (error: unknown) =>
        error instanceof ApiError && error.status === 400 && error.message === 'Período inválido',
    )
  } finally {
    stub.restore()
  }
})
