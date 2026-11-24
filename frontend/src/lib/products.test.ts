import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, API_BASE } from './api.ts'
import { canManageInventory, canViewInventory } from './permissions.ts'
import { clearToken, setToken } from './session.ts'
import {
  ADJUST_REASONS,
  ADJUST_REASON_LABELS,
  adjustBody,
  adjustProductStock,
  createInventoryGate,
  createProduct,
  fetchProductMovements,
  fetchProducts,
  INVENTORY_BUSY_MESSAGE,
  insufficientStockMessage,
  isInsufficientStockMessage,
  LOW_STOCK_LABEL,
  lowStockMark,
  MAX_ADJUST_DELTA,
  MAX_ADJUST_NOTE_LENGTH,
  MAX_PRODUCT_PRICE_CENTS,
  MAX_PRODUCT_STOCK,
  MOVEMENTS_DEFAULT_LIMIT,
  MOVEMENTS_MAX_LIMIT,
  movementReasonLabel,
  normalizeMovements,
  normalizeProduct,
  normalizeProducts,
  parseAdjustDelta,
  parseProductPriceToCents,
  parseStockText,
  productApiMessage,
  productCreateBody,
  productPriceInputFromCents,
  PRODUCT_MESSAGES,
  productUpdateBody,
  updateProduct,
  validateAdjustDraft,
  validateProductDraft,
  writeProduct,
  type Product,
  type ProductDraft,
} from './products.ts'

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

const RAW_PRODUCT = {
  id: 'p1',
  name: 'Tinte rubio',
  price_cents: 1250,
  stock: 4,
  low_stock_threshold: 5,
  active: true,
  low: true,
  created_at: '2026-11-03T18:00:00Z',
}

function product(over: Partial<Product> & Pick<Product, 'id'>): Product {
  return {
    name: 'Tinte rubio',
    price_cents: 1250,
    stock: 4,
    low_stock_threshold: 5,
    active: true,
    low: false,
    created_at: null,
    ...over,
  }
}

const EMPTY_DRAFT: ProductDraft = {
  name: '',
  price: '',
  stock: '',
  low_stock_threshold: '',
  active: true,
}

// --- precio: texto → centavos enteros, nunca float ---

test('parseProductPriceToCents reads the price exactly as expenses do', () => {
  assert.equal(parseProductPriceToCents('12'), 1200)
  assert.equal(parseProductPriceToCents('12,5'), 1250)
  assert.equal(parseProductPriceToCents('12.50'), 1250)
  assert.equal(parseProductPriceToCents('12.5'), 1250)
  assert.equal(parseProductPriceToCents(' $1250 '), 125000)
  assert.equal(parseProductPriceToCents('0.01'), 1)
  assert.equal(parseProductPriceToCents('100000'), MAX_PRODUCT_PRICE_CENTS)
})

test('parseProductPriceToCents refuses anything that is not one amount', () => {
  for (const bad of ['', '   ', 'abc', '1,2,3', '1.2.3', '-5', '-5.00', '12,345', '12.345', '1e3', '.5', '5.']) {
    assert.equal(parseProductPriceToCents(bad), null, `should refuse ${JSON.stringify(bad)}`)
  }
})

test('the price never passes through a float', () => {
  const cents = parseProductPriceToCents('10.10')
  assert.equal(cents, 1010)
  assert.ok(Number.isSafeInteger(cents))
  assert.equal(parseProductPriceToCents('999999999999999999999'), null)
  assert.equal(productPriceInputFromCents(1250), '12.50')
  assert.equal(productPriceInputFromCents(0), '0.00')
})

test('parseStockText reads whole units only', () => {
  assert.equal(parseStockText('0'), 0)
  assert.equal(parseStockText(' 12 '), 12)
  assert.equal(parseStockText('007'), 7)
  // A negative number is read here and refused later by the range of the contract.
  assert.equal(parseStockText('-1'), -1)
  for (const bad of ['', '1.5', '1,5', 'abc', '1e3']) {
    assert.equal(parseStockText(bad), null, `should refuse ${JSON.stringify(bad)}`)
  }
})

// --- validación del formulario: mensajes del contrato, verbatim ---

test('validateProductDraft builds a create payload without the optional numbers', () => {
  const check = validateProductDraft(
    { ...EMPTY_DRAFT, name: '  Tinte rubio  ', price: '12,50' },
    'create',
  )
  assert.deepEqual(check, {
    ok: true,
    mode: 'create',
    payload: { name: 'Tinte rubio', price_cents: 1250 },
  })
  const body = productCreateBody({ name: 'Tinte rubio', price_cents: 1250 })
  assert.equal('stock' in body, false)
  assert.equal('low_stock_threshold' in body, false)
})

test('validateProductDraft sends the stock and the threshold of a create when typed', () => {
  const check = validateProductDraft(
    { ...EMPTY_DRAFT, name: 'Tinte', price: '12.50', stock: '5', low_stock_threshold: '2' },
    'create',
  )
  assert.equal(check.ok, true)
  assert.deepEqual(check.ok ? check.payload : null, {
    name: 'Tinte',
    price_cents: 1250,
    stock: 5,
    low_stock_threshold: 2,
  })
  assert.deepEqual(
    productCreateBody({ name: 'Tinte', price_cents: 1250, stock: 5, low_stock_threshold: 2 }),
    { name: 'Tinte', price_cents: 1250, stock: 5, low_stock_threshold: 2 },
  )
})

test('a create rejects an empty name with the sentence of the API', () => {
  const check = validateProductDraft({ ...EMPTY_DRAFT, price: '10' }, 'create')
  assert.equal(check.ok, false)
  assert.equal(check.ok === false ? check.field : '', 'name')
  assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.nameRequired)
  assert.equal(PRODUCT_MESSAGES.nameRequired, 'El nombre del producto es obligatorio')
})

test('a name of 120 characters fits and 121 does not', () => {
  const long = 'ñ'.repeat(120)
  assert.equal(validateProductDraft({ ...EMPTY_DRAFT, name: long, price: '1' }, 'create').ok, true)
  const tooLong = validateProductDraft({ ...EMPTY_DRAFT, name: `${long}ñ`, price: '1' }, 'create')
  assert.equal(tooLong.ok === false ? tooLong.field : '', 'name')
  assert.equal(tooLong.ok === false ? tooLong.message : '', PRODUCT_MESSAGES.nameTooLong)
  assert.equal(
    PRODUCT_MESSAGES.nameTooLong,
    'El nombre del producto no puede superar los 120 caracteres',
  )
})

test('a NUL in the name is refused before the request, never a 500', () => {
  const check = validateProductDraft({ ...EMPTY_DRAFT, name: 'Tinte\u0000', price: '1' }, 'create')
  assert.equal(check.ok === false ? check.field : '', 'name')
  assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.invalidText)
  assert.equal(PRODUCT_MESSAGES.invalidText, 'El texto tiene caracteres no válidos')
})

test('the price must be an amount inside the range of the contract', () => {
  const unreadable = validateProductDraft({ ...EMPTY_DRAFT, name: 'Tinte', price: 'abc' }, 'create')
  assert.equal(unreadable.ok === false ? unreadable.field : '', 'price')
  assert.equal(unreadable.ok === false ? unreadable.message : '', PRODUCT_MESSAGES.priceUnreadable)
  const negative = validateProductDraft({ ...EMPTY_DRAFT, name: 'Tinte', price: '-1' }, 'create')
  assert.equal(negative.ok === false ? negative.field : '', 'price')
  const tooBig = validateProductDraft(
    { ...EMPTY_DRAFT, name: 'Tinte', price: '10000000.01' },
    'create',
  )
  assert.equal(tooBig.ok === false ? tooBig.message : '', PRODUCT_MESSAGES.priceRange)
  assert.equal(PRODUCT_MESSAGES.priceRange, 'El precio debe estar entre 0 y 10000000 centavos')
  // The contract lets a product cost zero.
  assert.equal(validateProductDraft({ ...EMPTY_DRAFT, name: 'Tinte', price: '0' }, 'create').ok, true)
})

test('the stock and the threshold are whole units inside the range of the contract', () => {
  // Not a whole number of units at all: the copy of the field.
  for (const bad of ['1.5', '1,5', 'abc']) {
    const check = validateProductDraft(
      { ...EMPTY_DRAFT, name: 'Tinte', price: '10', stock: bad },
      'create',
    )
    assert.equal(check.ok === false ? check.field : '', 'stock', `stock ${bad}`)
    assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.stockUnreadable)
    const threshold = validateProductDraft(
      { ...EMPTY_DRAFT, name: 'Tinte', price: '10', low_stock_threshold: bad },
      'create',
    )
    assert.equal(threshold.ok === false ? threshold.message : '', PRODUCT_MESSAGES.stockUnreadable)
  }
  // A whole number outside the range: the sentence of the API.
  for (const bad of ['-1', '9999999999']) {
    const check = validateProductDraft(
      { ...EMPTY_DRAFT, name: 'Tinte', price: '10', stock: bad },
      'create',
    )
    assert.equal(check.ok === false ? check.field : '', 'stock', `stock ${bad}`)
    assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.stockRange)
    const threshold = validateProductDraft(
      { ...EMPTY_DRAFT, name: 'Tinte', price: '10', low_stock_threshold: bad },
      'create',
    )
    assert.equal(threshold.ok === false ? threshold.message : '', PRODUCT_MESSAGES.stockRange)
  }
  assert.equal(PRODUCT_MESSAGES.stockRange, 'El stock y el umbral deben estar entre 0 y 1000000')
  assert.equal(
    validateProductDraft(
      { ...EMPTY_DRAFT, name: 'Tinte', price: '10', stock: String(MAX_PRODUCT_STOCK) },
      'create',
    ).ok,
    true,
  )
})

test('an update sends the four keys of the contract and never the stock', () => {
  const check = validateProductDraft(
    {
      name: 'Tinte rubio',
      price: '12.50',
      stock: '99',
      low_stock_threshold: '3',
      active: false,
    },
    'update',
  )
  assert.equal(check.ok, true)
  const payload = check.ok ? check.payload : {}
  assert.deepEqual(payload, {
    name: 'Tinte rubio',
    price_cents: 1250,
    low_stock_threshold: 3,
    active: false,
  })
  assert.equal('stock' in payload, false)
  assert.deepEqual(productUpdateBody({
    name: 'Tinte rubio',
    price_cents: 1250,
    low_stock_threshold: 3,
    active: false,
  }), {
    name: 'Tinte rubio',
    price_cents: 1250,
    low_stock_threshold: 3,
    active: false,
  })
})

test('an update without a price keeps the sentence of the missing keys', () => {
  const check = validateProductDraft(
    { name: 'Tinte', price: '', stock: '', low_stock_threshold: '', active: true },
    'update',
  )
  assert.equal(check.ok === false ? check.field : '', 'price')
  assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.missingFields)
  assert.equal(
    PRODUCT_MESSAGES.missingFields,
    'Faltan datos del producto: nombre, precio, umbral de stock bajo y estado',
  )
})

// --- ajuste de stock: motivo, delta con signo y nota ---

test('parseAdjustDelta reads a signed whole number only', () => {
  assert.equal(parseAdjustDelta('5'), 5)
  assert.equal(parseAdjustDelta('+5'), 5)
  assert.equal(parseAdjustDelta('-3'), -3)
  assert.equal(parseAdjustDelta(' 12 '), 12)
  for (const bad of ['', '+', '-', '1.5', '1,5', 'abc', '5 unidades']) {
    assert.equal(parseAdjustDelta(bad), null, `should refuse ${JSON.stringify(bad)}`)
  }
})

test('the reason of an adjustment is one of the three of the contract', () => {
  assert.deepEqual(ADJUST_REASONS, ['restock', 'loss', 'correction'])
  assert.deepEqual(ADJUST_REASON_LABELS, {
    restock: 'Reposición',
    loss: 'Pérdida',
    correction: 'Corrección',
  })
  const check = validateAdjustDraft({ delta: '5', reason: 'sale', note: '' })
  assert.equal(check.ok === false ? check.field : '', 'reason')
  assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.adjustReason)
  assert.equal(PRODUCT_MESSAGES.adjustReason, 'Motivo inválido: usá restock, loss o correction')
})

test('the sign of the adjustment has to match the reason', () => {
  assert.equal(validateAdjustDraft({ delta: '5', reason: 'restock', note: '' }).ok, true)
  assert.equal(validateAdjustDraft({ delta: '-2', reason: 'loss', note: '' }).ok, true)
  assert.equal(validateAdjustDraft({ delta: '-3', reason: 'correction', note: '' }).ok, true)
  assert.equal(validateAdjustDraft({ delta: '+3', reason: 'correction', note: '' }).ok, true)
  for (const draft of [
    { delta: '-5', reason: 'restock', note: '' },
    { delta: '+2', reason: 'loss', note: '' },
  ]) {
    const check = validateAdjustDraft(draft)
    assert.equal(check.ok === false ? check.field : '', 'delta', `${draft.reason} ${draft.delta}`)
    assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.adjustSign)
  }
  assert.equal(PRODUCT_MESSAGES.adjustSign, 'El signo del ajuste no coincide con el motivo')
})

test('a zero adjustment is refused with the sentence of the API', () => {
  for (const delta of ['0', '+0', '-0']) {
    const check = validateAdjustDraft({ delta, reason: 'correction', note: '' })
    assert.equal(check.ok === false ? check.field : '', 'delta')
    assert.equal(check.ok === false ? check.message : '', PRODUCT_MESSAGES.adjustZero)
  }
  assert.equal(PRODUCT_MESSAGES.adjustZero, 'El ajuste no puede ser cero')
})

test('the adjustment stays inside the contract range and reads the note', () => {
  const unreadable = validateAdjustDraft({ delta: 'abc', reason: 'restock', note: '' })
  assert.equal(unreadable.ok === false ? unreadable.field : '', 'delta')
  assert.equal(unreadable.ok === false ? unreadable.message : '', PRODUCT_MESSAGES.adjustUnreadable)
  const tooBig = validateAdjustDraft({
    delta: String(MAX_ADJUST_DELTA + 1),
    reason: 'restock',
    note: '',
  })
  assert.equal(tooBig.ok === false ? tooBig.message : '', PRODUCT_MESSAGES.adjustRange)
  const note = 'ñ'.repeat(MAX_ADJUST_NOTE_LENGTH)
  assert.equal(validateAdjustDraft({ delta: '1', reason: 'restock', note }).ok, true)
  const longNote = validateAdjustDraft({ delta: '1', reason: 'restock', note: `${note}ñ` })
  assert.equal(longNote.ok === false ? longNote.field : '', 'note')
  assert.equal(longNote.ok === false ? longNote.message : '', PRODUCT_MESSAGES.adjustNoteTooLong)
  const nul = validateAdjustDraft({ delta: '1', reason: 'restock', note: 'a\u0000b' })
  assert.equal(nul.ok === false ? nul.field : '', 'note')
  assert.equal(nul.ok === false ? nul.message : '', PRODUCT_MESSAGES.invalidText)
})

test('adjustBody sends the note only when it has text', () => {
  assert.deepEqual(adjustBody({ delta: 5, reason: 'restock', note: '' }), {
    delta: 5,
    reason: 'restock',
  })
  assert.deepEqual(adjustBody({ delta: -2, reason: 'loss', note: '  se rompió  ' }), {
    delta: -2,
    reason: 'loss',
    note: 'se rompió',
  })
})

// --- la marca de stock bajo viene del servidor ---

test('the «Stock bajo» mark comes from the low flag of the server', () => {
  assert.equal(LOW_STOCK_LABEL, 'Stock bajo')
  assert.equal(lowStockMark(product({ id: 'p1', low: true })), LOW_STOCK_LABEL)
  assert.equal(lowStockMark(product({ id: 'p1', low: false })), null)
  // A row without the flag never gets a mark invented by the client.
  assert.equal(lowStockMark(normalizeProduct({ ...RAW_PRODUCT, low: undefined })!), null)
  // The client never recomputes the rule: stock 9 over threshold 5 is not low.
  assert.equal(lowStockMark(product({ id: 'p1', stock: 9, low_stock_threshold: 5 })), null)
})

test('the 409 of the sale keeps the sentence of the contract with the name', () => {
  assert.equal(insufficientStockMessage('Tinte rubio'), 'No hay stock suficiente de «Tinte rubio»')
  assert.equal(isInsufficientStockMessage(insufficientStockMessage('Tinte')), true)
  assert.equal(isInsufficientStockMessage(PRODUCT_MESSAGES.adjustNegativeStock), false)
  assert.equal(isInsufficientStockMessage('otra cosa'), false)
})

test('movement reasons have their Spanish labels', () => {
  assert.equal(movementReasonLabel('sale'), 'Venta')
  assert.equal(movementReasonLabel('initial'), 'Stock inicial')
  assert.equal(movementReasonLabel('restock'), 'Reposición')
  assert.equal(movementReasonLabel('loss'), 'Pérdida')
  assert.equal(movementReasonLabel('correction'), 'Corrección')
})

// --- lectura defensiva de la API ---

test('normalizeProduct reads the frozen shape and never invents a number', () => {
  assert.deepEqual(normalizeProduct(RAW_PRODUCT), {
    id: 'p1',
    name: 'Tinte rubio',
    price_cents: 1250,
    stock: 4,
    low_stock_threshold: 5,
    active: true,
    low: true,
    created_at: '2026-11-03T18:00:00Z',
  })
  const broken = normalizeProduct({
    id: 'p1',
    name: 'Tinte',
    price_cents: 12.5,
    stock: -1,
    low_stock_threshold: 'x',
    active: 'yes',
  })
  assert.equal(broken?.price_cents, 0)
  assert.equal(broken?.stock, 0)
  assert.equal(broken?.low_stock_threshold, 0)
  assert.equal(broken?.active, false)
  assert.equal(broken?.low, false)
  assert.equal(broken?.created_at, null)
  assert.equal(normalizeProduct({ name: 'sin id' }), null)
  assert.equal(normalizeProduct({ id: 'p1' }), null)
  assert.equal(normalizeProduct(null), null)
})

test('normalizeProducts drops unusable rows and leaves them ordered by name', () => {
  const products = normalizeProducts({
    products: [
      RAW_PRODUCT,
      { ...RAW_PRODUCT, id: 'p2', name: 'Acondicionador' },
      { id: 'p3' },
      { name: 'sin id' },
    ],
  })
  assert.deepEqual(
    products.map((row) => row.id),
    ['p2', 'p1'],
  )
  assert.deepEqual(normalizeProducts(null), [])
  assert.deepEqual(normalizeProducts({ products: 'nope' }), [])
})

test('normalizeMovements keeps the order of the server and drops unusable rows', () => {
  const movements = normalizeMovements({
    movements: [
      { id: 'm2', delta: -2, reason: 'sale', note: '', sale_id: 's1', created_at: '2026-11-05T10:00:00Z' },
      { id: 'm1', delta: 10, reason: 'initial', note: 'alta', sale_id: null, created_at: '2026-11-01T10:00:00Z' },
      { id: '', delta: 1, reason: 'restock', note: '', sale_id: null, created_at: '' },
      { id: 'm3', delta: 1.5, reason: 'restock', note: '', sale_id: null, created_at: '' },
      { id: 'm4', delta: 1, reason: 'regalo', note: '', sale_id: null, created_at: '' },
    ],
  })
  assert.deepEqual(
    movements.map((row) => row.id),
    ['m2', 'm1'],
  )
  assert.equal(movements[0]?.sale_id, 's1')
  assert.equal(movements[1]?.sale_id, null)
  assert.deepEqual(normalizeMovements(null), [])
})

test('movementsLimit keeps the limit inside 1..100 and defaults to 50', () => {
  assert.equal(MOVEMENTS_DEFAULT_LIMIT, 50)
  assert.equal(MOVEMENTS_MAX_LIMIT, 100)
})

// --- permisos (D7) ---

test('reception reads the inventory; only owner and admin manage it', () => {
  assert.equal(canViewInventory('owner'), true)
  assert.equal(canViewInventory('admin'), true)
  assert.equal(canViewInventory('reception'), true)
  assert.equal(canViewInventory('professional'), false)
  assert.equal(canViewInventory(null), false)
  assert.equal(canManageInventory('owner'), true)
  assert.equal(canManageInventory('admin'), true)
  assert.equal(canManageInventory('reception'), false)
  assert.equal(canManageInventory('professional'), false)
})

// --- cliente de la API (formas congeladas del contrato F6) ---

test('fetchProducts sends the GET of the salon with the bearer token', async () => {
  const stub = stubFetch(() => json({ products: [RAW_PRODUCT] }))
  try {
    setToken('jwt-products')
    const list = await fetchProducts('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/products`)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-products')
    assert.equal(list[0]?.id, 'p1')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('createProduct POSTs the price in integer cents and the optional numbers', async () => {
  const stub = stubFetch(() => json(RAW_PRODUCT, 201))
  try {
    setToken('jwt-products')
    const saved = await createProduct('s1', {
      name: 'Tinte rubio',
      price_cents: 1250,
      stock: 5,
      low_stock_threshold: 2,
    })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/products`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      name: 'Tinte rubio',
      price_cents: 1250,
      stock: 5,
      low_stock_threshold: 2,
    })
    assert.equal(saved.id, 'p1')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('createProduct without stock nor threshold sends only the two keys of its body', async () => {
  const stub = stubFetch(() => json(RAW_PRODUCT, 201))
  try {
    setToken('jwt-products')
    await createProduct('s1', { name: 'Tinte', price_cents: 1250 })
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      name: 'Tinte',
      price_cents: 1250,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('updateProduct PUTs the four keys and never the stock', async () => {
  const stub = stubFetch(() => json(RAW_PRODUCT))
  try {
    setToken('jwt-products')
    await updateProduct('s1', 'p1', {
      name: 'Tinte rubio',
      price_cents: 1250,
      low_stock_threshold: 2,
      active: false,
    })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/products/p1`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    const body = JSON.parse(stub.calls[0]?.body ?? '{}') as Record<string, unknown>
    assert.deepEqual(body, {
      name: 'Tinte rubio',
      price_cents: 1250,
      low_stock_threshold: 2,
      active: false,
    })
    assert.equal('stock' in body, false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('adjustProductStock POSTs the delta, the reason and the note', async () => {
  const stub = stubFetch(() => json(RAW_PRODUCT))
  try {
    setToken('jwt-products')
    await adjustProductStock('s1', 'p1', { delta: -2, reason: 'loss', note: 'se rompió' })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/products/p1/adjust`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      delta: -2,
      reason: 'loss',
      note: 'se rompió',
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchProductMovements sends the limit and clamps it to 1..100', async () => {
  const stub = stubFetch(() => json({ movements: [] }))
  try {
    setToken('jwt-products')
    await fetchProductMovements('s1', 'p1')
    assert.equal(
      stub.calls[0]?.url,
      `${API_BASE}/api/v1/salons/s1/products/p1/movements?limit=50`,
    )
    await fetchProductMovements('s1', 'p1', 20)
    assert.equal(
      stub.calls[1]?.url,
      `${API_BASE}/api/v1/salons/s1/products/p1/movements?limit=20`,
    )
    await fetchProductMovements('s1', 'p1', 250)
    assert.equal(
      stub.calls[2]?.url,
      `${API_BASE}/api/v1/salons/s1/products/p1/movements?limit=100`,
    )
    await fetchProductMovements('s1', 'p1', 0)
    assert.equal(
      stub.calls[3]?.url,
      `${API_BASE}/api/v1/salons/s1/products/p1/movements?limit=50`,
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- las frases del servidor llegan verbatim ---

test('a 400 of a create keeps the sentence of the API', async () => {
  const stub = stubFetch(() => json({ error: PRODUCT_MESSAGES.priceRange }, 400))
  try {
    setToken('jwt-products')
    await assert.rejects(
      () => createProduct('s1', { name: 'Tinte', price_cents: 10_000_001 }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 400 &&
        error.message === 'El precio debe estar entre 0 y 10000000 centavos',
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a 404 of an update and a 409 of a duplicate name keep their sentences', async () => {
  const notFound = stubFetch(() => json({ error: PRODUCT_MESSAGES.notFound }, 404))
  try {
    setToken('jwt-products')
    await assert.rejects(
      () =>
        updateProduct('s1', 'missing', {
          name: 'Tinte',
          price_cents: 100,
          low_stock_threshold: 0,
          active: true,
        }),
      (error: unknown) =>
        error instanceof ApiError && error.status === 404 && error.message === 'Producto no encontrado',
    )
  } finally {
    notFound.restore()
  }
  const duplicate = stubFetch(() => json({ error: PRODUCT_MESSAGES.duplicateName }, 409))
  try {
    await assert.rejects(
      () => createProduct('s1', { name: 'Tinte', price_cents: 100 }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 409 &&
        error.message === 'Ya hay un producto activo con ese nombre',
    )
  } finally {
    duplicate.restore()
    clearToken()
  }
})

test('a 403 and a 409 of an adjustment keep the sentence of the API', async () => {
  const forbidden = stubFetch(() => json({ error: 'sin permiso' }, 403))
  try {
    setToken('jwt-products')
    await assert.rejects(
      () => adjustProductStock('s1', 'p1', { delta: 1, reason: 'restock' }),
      (error: unknown) =>
        error instanceof ApiError && error.status === 403 && error.message === 'sin permiso',
    )
  } finally {
    forbidden.restore()
  }
  const negative = stubFetch(() => json({ error: PRODUCT_MESSAGES.adjustNegativeStock }, 409))
  try {
    await assert.rejects(
      () => adjustProductStock('s1', 'p1', { delta: -100, reason: 'loss' }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 409 &&
        error.message === 'El stock no puede quedar negativo',
    )
  } finally {
    negative.restore()
    clearToken()
  }
})

test('productApiMessage keeps the sentence of the server and falls back when there is none', () => {
  assert.equal(productApiMessage(new ApiError(400, 'El ajuste no puede ser cero'), 'x'), 'El ajuste no puede ser cero')
  assert.equal(productApiMessage(new Error('Error de red'), 'x'), 'Error de red')
  assert.equal(productApiMessage('nope', 'No pudimos guardar el producto.'), 'No pudimos guardar el producto.')
})

// --- un guardado a la vez: el doble clic no manda dos pedidos ---

test('a double click on save sends exactly one POST and the second answers busy', async () => {
  const gate = createInventoryGate()
  let release: () => void = () => {}
  const hold = new Promise<void>((resolve) => {
    release = resolve
  })
  const stub = stubFetch(async () => {
    await hold
    return json(RAW_PRODUCT, 201)
  })
  try {
    setToken('jwt-products')
    const first = writeProduct(gate, 's1', {
      kind: 'create',
      payload: { name: 'Tinte', price_cents: 1250 },
    })
    const second = writeProduct(gate, 's1', {
      kind: 'create',
      payload: { name: 'Tinte', price_cents: 1250 },
    })
    assert.deepEqual(await second, { status: 'busy', message: INVENTORY_BUSY_MESSAGE })
    release()
    const outcome = await first
    assert.equal(outcome.status, 'saved')
    assert.equal(stub.calls.length, 1)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a double click on an update or an adjustment also sends one request', async () => {
  for (const write of [
    {
      kind: 'update' as const,
      productId: 'p1',
      payload: { name: 'Tinte', price_cents: 100, low_stock_threshold: 0, active: true },
    },
    { kind: 'adjust' as const, productId: 'p1', payload: { delta: 2, reason: 'restock' as const } },
  ]) {
    const gate = createInventoryGate()
    let release: () => void = () => {}
    const hold = new Promise<void>((resolve) => {
      release = resolve
    })
    const stub = stubFetch(async () => {
      await hold
      return json(RAW_PRODUCT)
    })
    try {
      setToken('jwt-products')
      const first = writeProduct(gate, 's1', write)
      const second = writeProduct(gate, 's1', write)
      assert.equal((await second).status, 'busy')
      release()
      assert.equal((await first).status, 'saved')
      assert.equal(stub.calls.length, 1)
    } finally {
      stub.restore()
    }
  }
  clearToken()
})

test('a rejected write frees the gate and returns the sentence of the server', async () => {
  const gate = createInventoryGate()
  const stub = stubFetch(() => json({ error: PRODUCT_MESSAGES.adjustSign }, 400))
  try {
    setToken('jwt-products')
    const outcome = await writeProduct(gate, 's1', {
      kind: 'adjust',
      productId: 'p1',
      payload: { delta: -1, reason: 'restock' },
    })
    assert.deepEqual(outcome, { status: 'failed', message: PRODUCT_MESSAGES.adjustSign })
    assert.equal(gate.busy(), false)
    assert.equal(gate.start(), true)
    gate.finish()
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a saved write returns the row the server stored, never a local guess', async () => {
  const gate = createInventoryGate()
  const stub = stubFetch(() => json({ ...RAW_PRODUCT, stock: 9 }))
  try {
    setToken('jwt-products')
    const outcome = await writeProduct(gate, 's1', {
      kind: 'adjust',
      productId: 'p1',
      payload: { delta: 5, reason: 'restock', note: 'compra' },
    })
    assert.equal(outcome.status, 'saved')
    assert.equal(outcome.status === 'saved' ? outcome.product.stock : -1, 9)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      delta: 5,
      reason: 'restock',
      note: 'compra',
    })
  } finally {
    stub.restore()
    clearToken()
  }
})
