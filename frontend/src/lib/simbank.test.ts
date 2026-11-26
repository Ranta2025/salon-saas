import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from './api.ts'
import {
  BANK_PATHS,
  cardDigits,
  chargeResultText,
  createSimbankGate,
  describeSimbankError,
  formatCardNumber,
  formatExpiry,
  isPaymentPath,
  lookupCharge,
  luhnValid,
  normalizeChargeLookup,
  normalizePayResult,
  payCharge,
  payPath,
  resultPath,
  SIMBANK_MESSAGES,
  SIMULATED_BANNER,
  TEST_CARDS,
  validateCardDraft,
  type CardDraft,
} from './simbank.ts'

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

// ---------- card number: formatting, digits only, Luhn hint ----------

test('cardDigits keeps only digits and caps the number at 19', () => {
  assert.equal(cardDigits('4242 4242 4242 4242'), '4242424242424242')
  assert.equal(cardDigits('4242-4242-4242-4242'), '4242424242424242')
  assert.equal(cardDigits(' 42a42b42c42 '), '42424242')
  assert.equal(cardDigits(''), '')
  assert.equal(cardDigits('nope'), '')
  assert.equal(cardDigits('1'.repeat(26)).length, 19)
})

test('formatCardNumber groups the digits in fours as the person types', () => {
  assert.equal(formatCardNumber('4242424242424242'), '4242 4242 4242 4242')
  assert.equal(formatCardNumber('424242424242'), '4242 4242 4242')
  assert.equal(formatCardNumber('42424'), '4242 4')
  assert.equal(formatCardNumber(' 4242 4242 4242 4242 '), '4242 4242 4242 4242')
  assert.equal(formatCardNumber(''), '')
})

test('formatExpiry inserts the slash and keeps four digits', () => {
  assert.equal(formatExpiry('1227'), '12/27')
  assert.equal(formatExpiry('12/27'), '12/27')
  assert.equal(formatExpiry('1'), '1')
  assert.equal(formatExpiry('122'), '12/2')
  assert.equal(formatExpiry('12/27/99'), '12/27')
  assert.equal(formatExpiry('ab'), '')
})

test('luhnValid is the UX hint of the test cards and nothing more', () => {
  assert.equal(luhnValid('4242424242424242'), true)
  assert.equal(luhnValid('4000000000000002'), true)
  assert.equal(luhnValid('4000000000009995'), true)
  assert.equal(luhnValid('4242424242424241'), false)
  assert.equal(luhnValid(''), false)
  assert.equal(luhnValid('42 42'), false)
})

test('the three test cards of the contract are the buttons that fill the form', () => {
  assert.deepEqual(
    TEST_CARDS.map((card) => [card.number, card.label]),
    [
      ['4242 4242 4242 4242', 'Aprobada'],
      ['4000 0000 0000 0002', 'Rechazada'],
      ['4000 0000 0000 9995', 'Sin fondos'],
    ],
  )
  assert.deepEqual(
    TEST_CARDS.map((card) => cardDigits(card.number)),
    ['4242424242424242', '4000000000000002', '4000000000009995'],
  )
})

const DRAFT: CardDraft = {
  number: '4242 4242 4242 4242',
  exp: '12/27',
  cvc: '123',
  name: 'Ana Ruiz',
}

test('validateCardDraft sends the digits only and never stores what was typed', () => {
  const check = validateCardDraft(DRAFT)
  assert.equal(check.ok, true)
  assert.deepEqual(check.ok ? check.card : null, {
    number: '4242424242424242',
    exp: '12/27',
    cvc: '123',
    name: 'Ana Ruiz',
  })
  assert.equal(check.ok ? check.luhnHint : 'x', null)
})

test('validateCardDraft asks for every field before sending anything', () => {
  const missing = (patch: Partial<CardDraft>) => validateCardDraft({ ...DRAFT, ...patch })
  assert.deepEqual(missing({ number: '  ' }), {
    ok: false,
    field: 'number',
    message: SIMBANK_MESSAGES.numberHint,
  })
  assert.deepEqual(missing({ number: '4242' }), {
    ok: false,
    field: 'number',
    message: SIMBANK_MESSAGES.numberHint,
  })
  assert.deepEqual(missing({ exp: '' }), {
    ok: false,
    field: 'exp',
    message: SIMBANK_MESSAGES.expiryHint,
  })
  assert.deepEqual(missing({ exp: '13/27' }), {
    ok: false,
    field: 'exp',
    message: SIMBANK_MESSAGES.expiryHint,
  })
  assert.deepEqual(missing({ cvc: '12' }), {
    ok: false,
    field: 'cvc',
    message: SIMBANK_MESSAGES.cvcHint,
  })
  assert.deepEqual(missing({ name: ' A ' }), {
    ok: false,
    field: 'name',
    message: SIMBANK_MESSAGES.nameHint,
  })
})

test('a number that fails Luhn is still sent: the hint only warns, the server decides', () => {
  const check = validateCardDraft({ ...DRAFT, number: '4242 4242 4242 4241' })
  assert.equal(check.ok, true)
  assert.equal(check.ok ? check.luhnHint : null, SIMBANK_MESSAGES.luhnHint)
  // A card the contract does not name but with a valid Luhn never gets a hint.
  const other = validateCardDraft({ ...DRAFT, number: '5555 5555 5555 4444' })
  assert.equal(other.ok ? other.luhnHint : 'x', null)
})

test('a second tap on «Pagar» is refused by the gate while the first is in flight', () => {
  const gate = createSimbankGate()
  assert.equal(gate.start(), true)
  assert.equal(gate.start(), false)
  gate.finish()
  assert.equal(gate.start(), true)
})

// ---------- routes of the two public pages ----------

test('payPath, resultPath and isPaymentPath pin the two public routes', () => {
  assert.equal(SIMULATED_BANNER, 'PAGO SIMULADO - no se cobra dinero real')
  assert.equal(payPath('charge_abc'), '/pagar#charge_abc')
  assert.equal(resultPath('charge_abc'), '/pago-resultado#charge_abc')
  assert.deepEqual([...BANK_PATHS], ['/pagar', '/pago-resultado'])
  assert.equal(isPaymentPath('/pagar'), true)
  assert.equal(isPaymentPath('/pagar/'), true)
  assert.equal(isPaymentPath('/pago-resultado'), true)
  assert.equal(isPaymentPath('/panel'), false)
  assert.equal(isPaymentPath('/'), false)
})

// ---------- lookup of the charge (D6) ----------

test('lookupCharge posts the charge id and reads amount, description and status', async () => {
  const stub = stubFetch(() =>
    json({
      amount_cents: 5400,
      description: 'Sena de Corte y peinado',
      status: 'pending',
      expires_at: '2026-11-02T16:30:00Z',
      simulated: true,
    }),
  )
  try {
    const charge = await lookupCharge('charge_abc')
    assert.deepEqual(charge, {
      amountCents: 5400,
      description: 'Sena de Corte y peinado',
      status: 'pending',
      expiresAt: '2026-11-02T16:30:00Z',
      simulated: true,
    })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/simbank/charges/lookup`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { charge_id: 'charge_abc' })
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
  } finally {
    stub.restore()
  }
})

test('normalizeChargeLookup rejects a body the page cannot show', () => {
  const good = { amount_cents: 5400, description: 'Sena', status: 'pending' }
  assert.equal(normalizeChargeLookup(good)?.amountCents, 5400)
  assert.equal(normalizeChargeLookup(good)?.simulated, false)
  for (const bad of [null, 'nope', {}, { ...good, amount_cents: 0 }, { ...good, amount_cents: -5 }, { ...good, status: '' }]) {
    assert.equal(normalizeChargeLookup(bad), null, JSON.stringify(bad))
  }
})

// ---------- paying (D6) ----------

test('payCharge sends the digits only in the body and never in the URL', async () => {
  const stub = stubFetch(() => json({ status: 'approved', message: 'Pago aprobado', amount_cents: 5400, last4: '4242' }))
  try {
    const result = await payCharge('charge_abc', {
      number: '4242424242424242',
      exp: '12/27',
      cvc: '123',
      name: 'Ana Ruiz',
    })
    assert.deepEqual(result, {
      status: 'approved',
      message: 'Pago aprobado',
      amountCents: 5400,
      last4: '4242',
      cardBrand: null,
    })
    const call = stub.calls[0]
    assert.equal(call?.url, `${API_BASE}/api/v1/simbank/charges/pay`)
    assert.equal(call?.method, 'POST')
    assert.deepEqual(JSON.parse(call?.body ?? '{}'), {
      charge_id: 'charge_abc',
      card: { number: '4242424242424242', exp: '12/27', cvc: '123', name: 'Ana Ruiz' },
    })
    // The card number never travels in the URL nor in a header.
    assert.equal(call?.url.includes('4242'), false)
    assert.equal(JSON.stringify(call?.headers).includes('4242'), false)
  } finally {
    stub.restore()
  }
})

test('payCharge reads a result nested in the body and a declined outcome', async () => {
  const stub = stubFetch(() => json({ result: { status: 'declined', message: 'Tarjeta rechazada' } }))
  try {
    const result = await payCharge('charge_abc', {
      number: '4000000000000002',
      exp: '12/27',
      cvc: '123',
      name: 'Ana Ruiz',
    })
    assert.equal(result.status, 'declined')
    assert.equal(result.message, 'Tarjeta rechazada')
  } finally {
    stub.restore()
  }
})

test('normalizePayResult never invents an outcome', () => {
  assert.equal(normalizePayResult(null), null)
  assert.equal(normalizePayResult({ ok: true })?.status, 'unknown')
  assert.equal(normalizePayResult({ status: 'insufficient' })?.status, 'insufficient')
  assert.equal(normalizePayResult({ charge: { status: 'approved', last4: '4242' } })?.last4, '4242')
})

test('the sentences of the bank reach the page verbatim (409 vencido, 409 aprobado, 429, 400)', async () => {
  const cases: { status: number; error: string }[] = [
    { status: 409, error: 'El pago vencio' },
    { status: 409, error: 'Este pago ya se aprobo' },
    { status: 429, error: 'Demasiados intentos' },
    { status: 400, error: 'Datos de tarjeta invalidos' },
  ]
  for (const item of cases) {
    const stub = stubFetch(() => json({ error: item.error }, item.status))
    try {
      const error = await payCharge('charge_abc', {
        number: '4242424242424242',
        exp: '12/27',
        cvc: '123',
        name: 'Ana Ruiz',
      }).then(
        () => null,
        (err: unknown) => err,
      )
      assert.ok(error instanceof ApiError)
      assert.equal(error.status, item.status)
      const failure = describeSimbankError(error)
      assert.equal(failure.message, item.error)
      assert.equal(failure.verbatim, true)
    } finally {
      stub.restore()
    }
  }
  assert.equal(SIMBANK_MESSAGES.expired, 'El pago vencio')
  assert.equal(SIMBANK_MESSAGES.alreadyApproved, 'Este pago ya se aprobo')
  assert.equal(SIMBANK_MESSAGES.invalidCard, 'Datos de tarjeta invalidos')
})

test('describeSimbankError classifies the failures the page reacts to differently', () => {
  assert.equal(describeSimbankError(new ApiError(409, 'El pago vencio')).kind, 'expired')
  assert.equal(describeSimbankError(new ApiError(409, 'Este pago ya se aprobo')).kind, 'already_approved')
  assert.equal(describeSimbankError(new ApiError(409, 'otra cosa')).kind, 'conflict')
  assert.equal(describeSimbankError(new ApiError(429, 'Demasiados intentos')).kind, 'too_many')
  assert.equal(describeSimbankError(new ApiError(400, 'Datos de tarjeta invalidos')).kind, 'invalid_card')
  assert.equal(describeSimbankError(new ApiError(500, 'error interno')).kind, 'server')
  assert.equal(describeSimbankError(new TypeError('fetch failed')).kind, 'network')
  assert.equal(describeSimbankError('boom').kind, 'server')
})

test('chargeResultText says the four outcomes in Spanish', () => {
  assert.equal(chargeResultText('approved'), 'Pago aprobado')
  assert.equal(chargeResultText('declined'), 'Tarjeta rechazada')
  assert.equal(chargeResultText('insufficient'), 'Fondos insuficientes')
  assert.equal(chargeResultText('refunded'), 'Pago devuelto')
  assert.equal(chargeResultText('pending'), 'Pago pendiente')
  assert.equal(chargeResultText('unknown'), 'Sin resultado')
})

test('neither the card number nor the charge id is stored, logged or put in a URL', async () => {
  const stub = stubFetch(() => json({ status: 'approved' }))
  const store = new Map<string, string>()
  const originalStorage = (globalThis as { localStorage?: unknown }).localStorage
  ;(globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
    removeItem: (key: string) => void store.delete(key),
  }
  const logged: string[] = []
  const originalLog = console.log
  const originalError = console.error
  const originalWarn = console.warn
  console.log = (...args: unknown[]) => void logged.push(args.map(String).join(' '))
  console.error = (...args: unknown[]) => void logged.push(args.map(String).join(' '))
  console.warn = (...args: unknown[]) => void logged.push(args.map(String).join(' '))
  try {
    await payCharge('charge_abc', {
      number: '4242424242424242',
      exp: '12/27',
      cvc: '123',
      name: 'Ana Ruiz',
    })
    assert.deepEqual([...store.keys()], [])
    assert.equal(logged.some((line) => line.includes('4242')), false)
    assert.equal(logged.some((line) => line.includes('charge_abc')), false)
    assert.equal(stub.calls[0]?.url.includes('charge_abc'), false)
  } finally {
    console.log = originalLog
    console.error = originalError
    console.warn = originalWarn
    ;(globalThis as { localStorage?: unknown }).localStorage = originalStorage
    stub.restore()
  }
})
