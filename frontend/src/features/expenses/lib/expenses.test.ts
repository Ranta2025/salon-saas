import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, API_BASE } from '../../../lib/api.ts'
import { todayInTimeZone, addDaysToDateValue } from '../../../lib/datetime.ts'
import {
  canManageExpenses,
  checkExpenseRange,
  createExpense,
  DEFAULT_RANGE_DAYS,
  defaultExpenseRange,
  DELETE_EXPENSE_NOTE,
  DELETE_EXPENSE_QUESTION,
  deleteExpense,
  EXPENSE_CATEGORIES,
  EXPENSE_CATEGORY_LABELS,
  expenseAmountInputFromCents,
  expenseCreateBody,
  expenseUpdateBody,
  fetchExpenses,
  FUTURE_DATE_MESSAGE,
  INVALID_AMOUNT_MESSAGE,
  INVALID_CATEGORY_MESSAGE,
  INVALID_DATE_MESSAGE,
  LONG_DESCRIPTION_MESSAGE,
  MAX_AMOUNT_MESSAGE,
  MAX_DESCRIPTION_LENGTH,
  MISSING_FIELDS_CREATE_MESSAGE,
  MISSING_FIELDS_UPDATE_MESSAGE,
  normalizeExpense,
  normalizeExpenses,
  OLD_DATE_MESSAGE,
  parseExpenseAmountToCents,
  updateExpense,
  validateExpenseDraft,
  ZERO_AMOUNT_MESSAGE,
  type Expense,
} from './expenses.ts'
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

function noContent(): Response {
  return new Response(null, { status: 204 })
}

function expense(over: Partial<Expense> & Pick<Expense, 'id'>): Expense {
  return {
    category: 'supplies',
    amount_cents: 1250,
    spent_on: '2026-11-03',
    description: '',
    created_at: '2026-11-03T18:00:00Z',
    created_by: 'u1',
    ...over,
  }
}

const TODAY = '2026-11-10'

// --- monto: texto → centavos enteros, nunca float ---

test('parseExpenseAmountToCents reads the text an expense is typed in', () => {
  assert.equal(parseExpenseAmountToCents('12'), 1200)
  assert.equal(parseExpenseAmountToCents('12,5'), 1250)
  assert.equal(parseExpenseAmountToCents('12.50'), 1250)
  assert.equal(parseExpenseAmountToCents('12.5'), 1250)
  assert.equal(parseExpenseAmountToCents('  $1250 '), 125000)
  assert.equal(parseExpenseAmountToCents('0.01'), 1)
  assert.equal(parseExpenseAmountToCents('20000000'), 2_000_000_000)
})

test('parseExpenseAmountToCents refuses anything that is not one amount', () => {
  for (const bad of ['', '   ', 'abc', '1,2,3', '1.2.3', '-5', '-5.00', '12,345', '12.345', '1e3', '1 2', '.5', '5.']) {
    assert.equal(parseExpenseAmountToCents(bad), null, `should refuse ${JSON.stringify(bad)}`)
  }
})

test('parseExpenseAmountToCents never passes through a float', () => {
  // 10.10 is the classic float trap: 1010 cents, not 1009 or 1010.0000000000001.
  const parsed = parseExpenseAmountToCents('10.10')
  assert.equal(parsed, 1010)
  assert.ok(Number.isSafeInteger(parsed))
  // Beyond the safe integer range it refuses instead of rounding silently.
  assert.equal(parseExpenseAmountToCents('999999999999999999999'), null)
})

test('expenseAmountInputFromCents prints the amount for the edit form in cents', () => {
  assert.equal(expenseAmountInputFromCents(1250), '12.50')
  assert.equal(expenseAmountInputFromCents(123456), '1234.56')
  assert.equal(expenseAmountInputFromCents(0), '0.00')
  assert.equal(expenseAmountInputFromCents(-30), '0.00')
})

// --- validación del formulario: mensajes del contrato, verbatim ---

test('validateExpenseDraft builds the payload of a valid draft', () => {
  const check = validateExpenseDraft(
    { category: 'rent', amount: '1250,50', spent_on: '2026-11-01', description: '  alquiler  ' },
    TODAY,
  )
  assert.deepEqual(check, {
    ok: true,
    payload: {
      category: 'rent',
      amount_cents: 125050,
      spent_on: '2026-11-01',
      description: 'alquiler',
    },
  })
})

test('validateExpenseDraft refuses zero with the sentence of the API', () => {
  const check = validateExpenseDraft(
    { category: 'other', amount: '0', spent_on: TODAY, description: '' },
    TODAY,
  )
  assert.equal(check.ok, false)
  assert.equal(check.ok === false ? check.message : '', ZERO_AMOUNT_MESSAGE)
  assert.equal(ZERO_AMOUNT_MESSAGE, 'El monto debe ser mayor a cero')
})

test('validateExpenseDraft refuses an unreadable amount instead of guessing', () => {
  const check = validateExpenseDraft(
    { category: 'other', amount: 'abc', spent_on: TODAY, description: '' },
    TODAY,
  )
  assert.equal(check.ok, false)
  assert.equal(check.ok === false ? check.field : '', 'amount')
  assert.equal(check.ok === false ? check.message : '', INVALID_AMOUNT_MESSAGE)
})

test('validateExpenseDraft refuses the cap of the contract', () => {
  const check = validateExpenseDraft(
    { category: 'other', amount: '20000000.01', spent_on: TODAY, description: '' },
    TODAY,
  )
  assert.equal(check.ok, false)
  assert.equal(check.ok === false ? check.message : '', MAX_AMOUNT_MESSAGE)
  assert.equal(MAX_AMOUNT_MESSAGE, 'El monto no puede superar los 2000000000 centavos')
})

test('validateExpenseDraft refuses a missing category, amount or date with the create sentence', () => {
  const noCategory = validateExpenseDraft(
    { category: '', amount: '10', spent_on: TODAY, description: '' },
    TODAY,
  )
  assert.equal(noCategory.ok === false ? noCategory.field : '', 'category')
  assert.equal(noCategory.ok === false ? noCategory.message : '', MISSING_FIELDS_CREATE_MESSAGE)
  const noDate = validateExpenseDraft(
    { category: 'rent', amount: '10', spent_on: '', description: '' },
    TODAY,
  )
  assert.equal(noDate.ok === false ? noDate.field : '', 'spent_on')
  assert.equal(noDate.ok === false ? noDate.message : '', MISSING_FIELDS_CREATE_MESSAGE)
  assert.equal(MISSING_FIELDS_CREATE_MESSAGE, 'Faltan datos del gasto: categoría, monto y fecha')
  assert.equal(
    MISSING_FIELDS_UPDATE_MESSAGE,
    'Faltan datos del gasto: categoría, monto, fecha y descripción',
  )
})

test('validateExpenseDraft refuses an unknown category with the sentence of the API', () => {
  const check = validateExpenseDraft(
    { category: 'impuestos', amount: '10', spent_on: TODAY, description: '' },
    TODAY,
  )
  assert.equal(check.ok === false ? check.message : '', INVALID_CATEGORY_MESSAGE)
  assert.equal(
    INVALID_CATEGORY_MESSAGE,
    'Categoría inválida: usá alquiler, insumos, servicios u otros',
  )
})

test('validateExpenseDraft checks the local date: format, too old and future', () => {
  const bad = validateExpenseDraft(
    { category: 'rent', amount: '10', spent_on: '03-11-2026', description: '' },
    TODAY,
  )
  assert.equal(bad.ok === false ? bad.message : '', INVALID_DATE_MESSAGE)
  const old = validateExpenseDraft(
    { category: 'rent', amount: '10', spent_on: '1999-12-31', description: '' },
    TODAY,
  )
  assert.equal(old.ok === false ? old.message : '', OLD_DATE_MESSAGE)
  const future = validateExpenseDraft(
    { category: 'rent', amount: '10', spent_on: addDaysToDateValue(TODAY, 1), description: '' },
    TODAY,
  )
  assert.equal(future.ok === false ? future.message : '', FUTURE_DATE_MESSAGE)
  // Today itself is allowed: the window is [2000-01-01, today] in the salon zone.
  assert.equal(
    validateExpenseDraft({ category: 'rent', amount: '10', spent_on: TODAY, description: '' }, TODAY)
      .ok,
    true,
  )
})

test('validateExpenseDraft counts characters, not bytes, in the description', () => {
  const accented = 'ñ'.repeat(MAX_DESCRIPTION_LENGTH)
  assert.equal(
    validateExpenseDraft({ category: 'other', amount: '1', spent_on: TODAY, description: accented }, TODAY).ok,
    true,
  )
  const tooLong = validateExpenseDraft(
    { category: 'other', amount: '1', spent_on: TODAY, description: `${accented}ñ` },
    TODAY,
  )
  assert.equal(tooLong.ok === false ? tooLong.message : '', LONG_DESCRIPTION_MESSAGE)
  assert.equal(LONG_DESCRIPTION_MESSAGE, 'La descripción no puede superar los 200 caracteres')
})

// --- cuerpos de los pedidos: PUT manda las CUATRO claves ---

test('expenseCreateBody leaves the description optional', () => {
  const payload = {
    category: 'rent' as const,
    amount_cents: 125050,
    spent_on: '2026-11-01',
    description: '',
  }
  assert.deepEqual(expenseCreateBody(payload), {
    category: 'rent',
    amount_cents: 125050,
    spent_on: '2026-11-01',
  })
  assert.deepEqual(expenseCreateBody({ ...payload, description: 'luz de octubre' }), {
    category: 'rent',
    amount_cents: 125050,
    spent_on: '2026-11-01',
    description: 'luz de octubre',
  })
})

test('expenseUpdateBody always sends the four keys, description included', () => {
  assert.deepEqual(
    expenseUpdateBody({
      category: 'utilities',
      amount_cents: 9900,
      spent_on: '2026-10-30',
      description: '',
    }),
    {
      category: 'utilities',
      amount_cents: 9900,
      spent_on: '2026-10-30',
      description: '',
    },
  )
})

// --- rango de la lista ---

test('defaultExpenseRange covers the last 30 days of the salon calendar', () => {
  const range = defaultExpenseRange('2026-11-10')
  assert.equal(range.to, '2026-11-10')
  assert.equal(range.from, '2026-10-12')
  assert.equal(DEFAULT_RANGE_DAYS, 30)
})

test('checkExpenseRange refuses a reversed or too long range with the sentence of the API', () => {
  assert.deepEqual(checkExpenseRange('2026-11-01', '2026-11-10'), {
    ok: true,
    from: '2026-11-01',
    to: '2026-11-10',
  })
  const reversed = checkExpenseRange('2026-11-10', '2026-11-01')
  assert.equal(reversed.ok, false)
  assert.equal(
    reversed.ok === false ? reversed.message : '',
    'Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días',
  )
  const tooLong = checkExpenseRange('2024-01-01', '2026-11-10')
  assert.equal(tooLong.ok, false)
  const malformed = checkExpenseRange('ayer', '2026-11-10')
  assert.equal(malformed.ok, false)
})

// --- lectura defensiva de la API ---

test('normalizeExpenses reads the list, drops unusable rows and sorts newest first', () => {
  const page = normalizeExpenses({
    expenses: [
      { id: 'e1', category: 'rent', amount_cents: 100000, spent_on: '2026-11-01', description: '', created_at: '2026-11-01T10:00:00Z', created_by: 'u1' },
      { id: 'e2', category: 'supplies', amount_cents: 250, spent_on: '2026-11-05', description: 'tijeras', created_at: '2026-11-05T10:00:00Z', created_by: 'u1' },
      { id: 'e3', category: 'other', amount_cents: 100, spent_on: '2026-11-05', description: '', created_at: '2026-11-06T10:00:00Z', created_by: 'u2' },
      { id: '', category: 'rent', amount_cents: 100, spent_on: '2026-11-05', description: '' },
      { id: 'e5', category: 'rent', amount_cents: 100, spent_on: '2026-02-30', description: '' },
      { id: 'e6', category: 'impuestos', amount_cents: 100, spent_on: '2026-11-02', description: '' },
    ],
    truncated: true,
  })
  assert.deepEqual(
    page.expenses.map((row) => row.id),
    ['e3', 'e2', 'e1'],
  )
  assert.equal(page.truncated, true)
})

test('normalizeExpenses answers an empty list and truncated false instead of throwing', () => {
  assert.deepEqual(normalizeExpenses(null), { expenses: [], truncated: false })
  assert.deepEqual(normalizeExpenses({ expenses: 'nope' }), { expenses: [], truncated: false })
})

test('normalizeExpense refuses an amount that is not a positive integer', () => {
  const row = { id: 'e1', category: 'rent', spent_on: '2026-11-01' }
  assert.equal(normalizeExpense({ ...row, amount_cents: 12.5 })?.amount_cents, 0)
  assert.equal(normalizeExpense({ ...row, amount_cents: -100 })?.amount_cents, 0)
  assert.equal(normalizeExpense({ ...row, amount_cents: '100' })?.amount_cents, 0)
  assert.equal(normalizeExpense({ ...row, amount_cents: 100 })?.created_at, null)
  assert.equal(normalizeExpense({ ...row, amount_cents: 100 })?.description, '')
  assert.equal(normalizeExpense({ id: 'e1', category: 'nope', amount_cents: 100, spent_on: '2026-11-01' }), null)
})

test('the categories of the contract are the four of the list, with their Spanish labels', () => {
  assert.deepEqual(EXPENSE_CATEGORIES, ['rent', 'supplies', 'utilities', 'other'])
  assert.deepEqual(EXPENSE_CATEGORY_LABELS, {
    rent: 'Alquiler',
    supplies: 'Insumos',
    utilities: 'Servicios',
    other: 'Otros',
  })
})

// --- confirmación del borrado ---

test('the confirmation of a delete says it cannot be undone', () => {
  assert.match(DELETE_EXPENSE_QUESTION, /borrar/i)
  assert.match(DELETE_EXPENSE_NOTE, /no se puede deshacer/i)
})

// --- permisos (D5) ---

test('only owner and admin manage expenses', () => {
  assert.equal(canManageExpenses('owner'), true)
  assert.equal(canManageExpenses('admin'), true)
  assert.equal(canManageExpenses('professional'), false)
  assert.equal(canManageExpenses('reception'), false)
  assert.equal(canManageExpenses(null), false)
})

// --- cliente de la API (formas congeladas del contrato F3b) ---

test('fetchExpenses sends the inclusive range and the bearer token', async () => {
  const stub = stubFetch(() => json({ expenses: [], truncated: false }))
  try {
    setToken('jwt-expenses')
    const page = await fetchExpenses('s1', { from: '2026-11-01', to: '2026-11-10' })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/expenses?from=2026-11-01&to=2026-11-10`)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-expenses')
    assert.deepEqual(page, { expenses: [], truncated: false })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('createExpense POSTs the amount in integer cents and returns the saved row', async () => {
  const stub = stubFetch(() => json(expense({ id: 'e9' }), 201))
  try {
    setToken('jwt-expenses')
    const saved = await createExpense('s1', {
      category: 'supplies',
      amount_cents: 1250,
      spent_on: '2026-11-03',
      description: 'tijeras',
    })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/expenses`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      category: 'supplies',
      amount_cents: 1250,
      spent_on: '2026-11-03',
      description: 'tijeras',
    })
    assert.equal(saved.id, 'e9')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('updateExpense PUTs all four keys even with an empty description', async () => {
  const stub = stubFetch(() => json(expense({ id: 'e9', description: '' })))
  try {
    setToken('jwt-expenses')
    await updateExpense('s1', 'e9', {
      category: 'other',
      amount_cents: 500,
      spent_on: '2026-11-04',
      description: '',
    })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/expenses/e9`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      category: 'other',
      amount_cents: 500,
      spent_on: '2026-11-04',
      description: '',
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a 400 of the server reaches the screen verbatim', async () => {
  const stub = stubFetch(() => json({ error: FUTURE_DATE_MESSAGE }, 400))
  try {
    setToken('jwt-expenses')
    await assert.rejects(
      () =>
        createExpense('s1', {
          category: 'rent',
          amount_cents: 100,
          spent_on: '2030-01-01',
          description: '',
        }),
      (error: unknown) => {
        assert.ok(error instanceof ApiError)
        assert.equal(error.status, 400)
        assert.equal(error.message, FUTURE_DATE_MESSAGE)
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a 404 of a delete keeps the sentence of the API', async () => {
  const stub = stubFetch(() => json({ error: 'Gasto no encontrado' }, 404))
  try {
    setToken('jwt-expenses')
    await assert.rejects(
      () => deleteExpense('s1', 'missing'),
      (error: unknown) =>
        error instanceof ApiError && error.status === 404 && error.message === 'Gasto no encontrado',
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('deleteExpense resolves on 204', async () => {
  const stub = stubFetch(() => noContent())
  try {
    setToken('jwt-expenses')
    await deleteExpense('s1', 'e9')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/expenses/e9`)
    assert.equal(stub.calls[0]?.method, 'DELETE')
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- la fecha de hoy sale de la zona del salón, no de una zona fija ---

test('todayInTimeZone follows the zone of the salon, not a fixed one', () => {
  // 2026-11-10T05:30:00Z is still 2026-11-09 in Mexico City (UTC-6) and already
  // 2026-11-10 in Buenos Aires (UTC-3).
  const instant = new Date('2026-11-10T05:30:00Z')
  assert.equal(todayInTimeZone('America/Mexico_City', instant), '2026-11-09')
  assert.equal(todayInTimeZone('America/Argentina/Buenos_Aires', instant), '2026-11-10')
  assert.equal(todayInTimeZone('UTC', instant), '2026-11-10')
  // The salon zone is not the hard-coded one of the calendar helpers.
  assert.equal(todayInTimeZone('America/Tijuana', new Date('2026-11-10T07:30:00Z')), '2026-11-09')
})

test('todayInTimeZone survives an unusable zone without throwing', () => {
  const instant = new Date('2026-11-10T05:30:00Z')
  assert.equal(todayInTimeZone('Marte/Olympus', instant), '2026-11-09')
  assert.equal(todayInTimeZone('', instant), '2026-11-09')
})
