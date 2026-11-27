/**
 * Products and stock (contract F6, «Contrato F6 (productos e inventario en el POS:
 * stock y alerta de stock bajo)»). Pure rules plus the thin API client:
 * `/panel/inventario` and the POS render what these return, and the server stays
 * the authority on amounts, stock, the low-stock mark and permissions.
 *
 * Money is integer cents everywhere: the price a person types is turned into cents
 * with the same integer parser the expenses use, so `12.50` never passes through a
 * float. The stock is a whole number of units, and it is NEVER painted locally: a
 * write returns the row the server stored and the page asks for the list again.
 */

import { ApiError, request } from './api.ts'
import { expenseAmountInputFromCents, parseExpenseAmountToCents } from '../features/expenses/lib/expenses.ts'

/** Domain limit of a product name (`products.name` CHECK of 029). */
export const MAX_PRODUCT_NAME_LENGTH = 120

/** Cap of a product price in cents (D1). */
export const MAX_PRODUCT_PRICE_CENTS = 10_000_000

/** Cap of the stock and of the low-stock threshold (D1). */
export const MAX_PRODUCT_STOCK = 1_000_000

/** Cap of one adjustment, in units, with sign (contract of `/adjust`). */
export const MAX_ADJUST_DELTA = 1_000_000

/** Characters of an adjustment note (contract of `/adjust`). */
export const MAX_ADJUST_NOTE_LENGTH = 200

/** Default `limit` of the list of movements (contract of `/movements`). */
export const MOVEMENTS_DEFAULT_LIMIT = 50

/** Longest list of movements the endpoint accepts. */
export const MOVEMENTS_MAX_LIMIT = 100

/** Mark of the list when the server flags a product as low (D8). */
export const LOW_STOCK_LABEL = 'Stock bajo'

/**
 * Sentences of the API, printed verbatim (contract F6), plus the three the client
 * says before sending a request that would be rejected for the same reason.
 */
export const PRODUCT_MESSAGES = {
  nameRequired: 'El nombre del producto es obligatorio',
  nameTooLong: 'El nombre del producto no puede superar los 120 caracteres',
  priceRange: 'El precio debe estar entre 0 y 10000000 centavos',
  stockRange: 'El stock y el umbral deben estar entre 0 y 1000000',
  invalidText: 'El texto tiene caracteres no válidos',
  missingFields: 'Faltan datos del producto: nombre, precio, umbral de stock bajo y estado',
  notFound: 'Producto no encontrado',
  duplicateName: 'Ya hay un producto activo con ese nombre',
  adjustZero: 'El ajuste no puede ser cero',
  adjustReason: 'Motivo inválido: usá restock, loss o correction',
  adjustNegativeStock: 'El stock no puede quedar negativo',
  adjustSign: 'El signo del ajuste no coincide con el motivo',
  invalidProduct: 'Producto inválido',
  // Client-side copies: the field cannot be read as a number of dollars / units.
  priceUnreadable: 'Escribí el precio en dólares, por ejemplo 12.50',
  stockUnreadable: 'Escribí el stock en unidades, por ejemplo 12',
  adjustUnreadable: 'Escribí el ajuste en unidades, por ejemplo 5 o -5',
  adjustRange: 'El ajuste debe estar entre -1000000 y 1000000',
  adjustNoteTooLong: 'La nota no puede superar los 200 caracteres',
} as const

/** Reasons of an adjustment the API accepts (contract of `/adjust`). */
export type AdjustReason = 'restock' | 'loss' | 'correction'

export const ADJUST_REASONS: readonly AdjustReason[] = ['restock', 'loss', 'correction']

export const ADJUST_REASON_LABELS: Record<AdjustReason, string> = {
  restock: 'Reposición',
  loss: 'Pérdida',
  correction: 'Corrección',
}

/** Reason of a movement of the append-only ledger (`stock_movements.reason`). */
export type MovementReason = 'initial' | AdjustReason | 'sale'

const MOVEMENT_REASON_LABELS: Record<MovementReason, string> = {
  initial: 'Stock inicial',
  restock: 'Reposición',
  loss: 'Pérdida',
  correction: 'Corrección',
  sale: 'Venta',
}

export function movementReasonLabel(reason: MovementReason): string {
  return MOVEMENT_REASON_LABELS[reason]
}

/** One product of the salon (`product` of the frozen shape). */
export type Product = {
  id: string
  name: string
  /** Integer cents, 0..`MAX_PRODUCT_PRICE_CENTS`. */
  price_cents: number
  /** Whole units, 0..`MAX_PRODUCT_STOCK`; the server is the only writer. */
  stock: number
  low_stock_threshold: number
  active: boolean
  /** The SERVER decided the product is low; the client never recomputes it. */
  low: boolean
  created_at: string | null
}

/** One entry of the ledger of a product (`stock_movements`). */
export type StockMovement = {
  id: string
  /** Signed units: `sale` is always negative, `restock` positive. */
  delta: number
  reason: MovementReason
  note: string
  /** Sale that produced the movement, when it came from the POS. */
  sale_id: string | null
  created_at: string | null
}

/** Body of `POST /products`: the last two keys travel only when they have a value. */
export type ProductPayload = {
  name: string
  price_cents: number
  stock?: number
  low_stock_threshold?: number
}

/** Body of `PUT /products/:pid`: REPLACES, so the four keys always travel and never the stock. */
export type ProductUpdatePayload = {
  name: string
  price_cents: number
  low_stock_threshold: number
  active: boolean
}

/** Body of `POST /products/:pid/adjust`. */
export type AdjustPayload = {
  delta: number
  reason: AdjustReason
  note?: string
}

/** Fields of the product form, in the order the messages are checked. */
export type ProductField = 'name' | 'price' | 'stock' | 'low_stock_threshold'

export type ProductDraftCheck =
  | { ok: true; mode: 'create'; payload: ProductPayload }
  | { ok: true; mode: 'update'; payload: ProductUpdatePayload }
  | { ok: false; field: ProductField; message: string }

/** What the form holds: text as typed, numbers still unparsed. */
export type ProductDraft = {
  name: string
  price: string
  stock: string
  low_stock_threshold: string
  active: boolean
}

/** Fields of the adjustment form. */
export type AdjustField = 'delta' | 'reason' | 'note'

export type AdjustCheck =
  | { ok: true; payload: AdjustPayload }
  | { ok: false; field: AdjustField; message: string }

/** What the adjustment form holds. */
export type AdjustDraft = { delta: string; reason: string; note: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Whole units of a stock or of a threshold, or `0` (a malformed one is never guessed). */
function units(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : 0
}

/** A PostgreSQL text column rejects a NUL; the client never sends one (H20/H21). */
function hasNul(value: string): boolean {
  return value.includes('\u0000')
}

/**
 * Price typed by a person → integer cents, without a float in between.
 * The rule is the one of the expenses (contract F3b): accepts `12`, `12,5`,
 * `12.50` and `$1250` (one separator with one or two decimals) and refuses an
 * empty field, `abc`, a negative number, more than two decimals and `12,345`.
 */
export function parseProductPriceToCents(input: string): number | null {
  return parseExpenseAmountToCents(input)
}

/** Value of the price field of an edit form (`125050` → `1250.50`), integer arithmetic only. */
export function productPriceInputFromCents(value: number): string {
  return expenseAmountInputFromCents(value)
}

/**
 * Stock or threshold typed by a person → whole units; anything else is `null`. A
 * negative number is read here so the caller answers with the sentence of the range
 * of the contract instead of "type a number".
 */
export function parseStockText(input: string): number | null {
  const cleaned = typeof input === 'string' ? input.trim() : ''
  if (!/^[+-]?\d+$/.test(cleaned)) {
    return null
  }
  const value = Number(cleaned)
  return Number.isSafeInteger(value) ? value : null
}

/** Signed units of an adjustment; anything that is not a whole number is `null`. */
export function parseAdjustDelta(input: string): number | null {
  const cleaned = typeof input === 'string' ? input.trim() : ''
  if (!/^[+-]?\d+$/.test(cleaned)) {
    return null
  }
  const value = Number(cleaned)
  return Number.isSafeInteger(value) ? value : null
}

/**
 * Reads the product form before sending it. `create` sends the stock and the
 * threshold only when they were typed (the API defaults both to 0); `update`
 * sends the four keys of the PUT and never the stock.
 */
export function validateProductDraft(
  draft: ProductDraft,
  mode: 'create' | 'update',
): ProductDraftCheck {
  const name = draft.name.trim()
  const priceText = draft.price.trim()
  const stockText = draft.stock.trim()
  const thresholdText = draft.low_stock_threshold.trim()

  if (name === '') {
    return { ok: false, field: 'name', message: PRODUCT_MESSAGES.nameRequired }
  }
  if (hasNul(name)) {
    return { ok: false, field: 'name', message: PRODUCT_MESSAGES.invalidText }
  }
  if (name.length > MAX_PRODUCT_NAME_LENGTH) {
    return { ok: false, field: 'name', message: PRODUCT_MESSAGES.nameTooLong }
  }
  if (priceText === '' && mode === 'update') {
    // The PUT replaces the row: a missing key would be refused with this sentence.
    return { ok: false, field: 'price', message: PRODUCT_MESSAGES.missingFields }
  }
  const price = parseProductPriceToCents(priceText)
  if (price === null) {
    return { ok: false, field: 'price', message: PRODUCT_MESSAGES.priceUnreadable }
  }
  if (price > MAX_PRODUCT_PRICE_CENTS) {
    return { ok: false, field: 'price', message: PRODUCT_MESSAGES.priceRange }
  }
  if (thresholdText === '' && mode === 'update') {
    return { ok: false, field: 'low_stock_threshold', message: PRODUCT_MESSAGES.missingFields }
  }
  const stock = stockText === '' ? null : parseStockText(stockText)
  if (stockText !== '' && stock === null) {
    return { ok: false, field: 'stock', message: PRODUCT_MESSAGES.stockUnreadable }
  }
  if (stock !== null && (stock < 0 || stock > MAX_PRODUCT_STOCK)) {
    return { ok: false, field: 'stock', message: PRODUCT_MESSAGES.stockRange }
  }
  const threshold = thresholdText === '' ? null : parseStockText(thresholdText)
  if (thresholdText !== '' && threshold === null) {
    return {
      ok: false,
      field: 'low_stock_threshold',
      message: PRODUCT_MESSAGES.stockUnreadable,
    }
  }
  if (threshold !== null && (threshold < 0 || threshold > MAX_PRODUCT_STOCK)) {
    return { ok: false, field: 'low_stock_threshold', message: PRODUCT_MESSAGES.stockRange }
  }

  if (mode === 'update') {
    return {
      ok: true,
      mode: 'update',
      payload: {
        name,
        price_cents: price,
        low_stock_threshold: threshold ?? 0,
        active: draft.active,
      },
    }
  }
  const payload: ProductPayload = { name, price_cents: price }
  if (stock !== null) {
    payload.stock = stock
  }
  if (threshold !== null) {
    payload.low_stock_threshold = threshold
  }
  return { ok: true, mode: 'create', payload }
}

/** Body of `POST /products`: optional keys are absent, never `undefined`. */
export function productCreateBody(payload: ProductPayload): {
  name: string
  price_cents: number
  stock?: number
  low_stock_threshold?: number
} {
  const body: {
    name: string
    price_cents: number
    stock?: number
    low_stock_threshold?: number
  } = {
    name: payload.name.trim(),
    price_cents: units(payload.price_cents),
  }
  if (payload.stock !== undefined) {
    body.stock = units(payload.stock)
  }
  if (payload.low_stock_threshold !== undefined) {
    body.low_stock_threshold = units(payload.low_stock_threshold)
  }
  return body
}

/** Body of `PUT /products/:pid`: the four keys, never the stock (the API keeps it). */
export function productUpdateBody(payload: ProductUpdatePayload): {
  name: string
  price_cents: number
  low_stock_threshold: number
  active: boolean
} {
  return {
    name: payload.name.trim(),
    price_cents: units(payload.price_cents),
    low_stock_threshold: units(payload.low_stock_threshold),
    active: payload.active,
  }
}

/**
 * Reads the adjustment form: reason of the contract, `delta` different from zero
 * and inside ±`MAX_ADJUST_DELTA`, sign that matches the reason and a note of up to
 * 200 characters.
 */
export function validateAdjustDraft(draft: AdjustDraft): AdjustCheck {
  const reason = draft.reason.trim()
  const deltaText = draft.delta.trim()
  const note = draft.note.trim()

  if (!ADJUST_REASONS.includes(reason as AdjustReason)) {
    return { ok: false, field: 'reason', message: PRODUCT_MESSAGES.adjustReason }
  }
  const delta = parseAdjustDelta(deltaText)
  if (delta === null) {
    return { ok: false, field: 'delta', message: PRODUCT_MESSAGES.adjustUnreadable }
  }
  if (delta === 0) {
    return { ok: false, field: 'delta', message: PRODUCT_MESSAGES.adjustZero }
  }
  if (Math.abs(delta) > MAX_ADJUST_DELTA) {
    return { ok: false, field: 'delta', message: PRODUCT_MESSAGES.adjustRange }
  }
  if (reason === 'restock' && delta < 0) {
    return { ok: false, field: 'delta', message: PRODUCT_MESSAGES.adjustSign }
  }
  if (reason === 'loss' && delta > 0) {
    return { ok: false, field: 'delta', message: PRODUCT_MESSAGES.adjustSign }
  }
  if (hasNul(note)) {
    return { ok: false, field: 'note', message: PRODUCT_MESSAGES.invalidText }
  }
  if (note.length > MAX_ADJUST_NOTE_LENGTH) {
    return { ok: false, field: 'note', message: PRODUCT_MESSAGES.adjustNoteTooLong }
  }
  const payload: AdjustPayload = { delta, reason: reason as AdjustReason }
  if (note !== '') {
    payload.note = note
  }
  return { ok: true, payload }
}

/** Body of `POST /products/:pid/adjust`: the note travels only when it has text. */
export function adjustBody(payload: AdjustPayload): {
  delta: number
  reason: AdjustReason
  note?: string
} {
  const body: { delta: number; reason: AdjustReason; note?: string } = {
    delta: Number.isSafeInteger(payload.delta) ? payload.delta : 0,
    reason: payload.reason,
  }
  const note = payload.note?.trim() ?? ''
  if (note !== '') {
    body.note = note
  }
  return body
}

/** «Stock bajo» when the server flagged the product, `null` when it did not (D8). */
export function lowStockMark(product: Pick<Product, 'low'>): string | null {
  return product.low === true ? LOW_STOCK_LABEL : null
}

/** Sentence of the 409 of a sale whose product ran out of stock (D3). */
export function insufficientStockMessage(productName: string): string {
  return `No hay stock suficiente de «${productName.trim()}»`
}

/** Whether a message of the API is the 409 of insufficient stock. */
export function isInsufficientStockMessage(message: string): boolean {
  return /^No hay stock suficiente de «.*»$/.test(message.trim())
}

/** `GET /products`: rows without id nor name, or with an unusable shape, are dropped. */
export function normalizeProduct(raw: unknown): Product | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const name = text(raw['name'])
  if (id === '' || name === '') {
    return null
  }
  const createdAt = text(raw['created_at'])
  return {
    id,
    name,
    price_cents: units(raw['price_cents']),
    stock: units(raw['stock']),
    low_stock_threshold: units(raw['low_stock_threshold']),
    active: raw['active'] === true,
    low: raw['low'] === true,
    created_at: createdAt === '' ? null : createdAt,
  }
}

/**
 * Reads `{products:[…]}` (or a bare list) and orders it by name, the order the
 * contract promises, so a malformed answer never shows the list unsorted.
 */
export function normalizeProducts(raw: unknown): Product[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['products'])
      ? (raw['products'] as unknown[])
      : []
  const products: Product[] = []
  for (const row of rows) {
    const product = normalizeProduct(row)
    if (product) {
      products.push(product)
    }
  }
  return products
    .map((product, index) => ({ product, index }))
    .sort((a, b) => {
      const byName = a.product.name.localeCompare(b.product.name, 'es')
      return byName !== 0 ? byName : a.index - b.index
    })
    .map((entry) => entry.product)
}

/** `GET /products/:pid/movements`: newest first, as the server sends them. */
export function normalizeMovements(raw: unknown): StockMovement[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['movements'])
      ? (raw['movements'] as unknown[])
      : []
  const movements: StockMovement[] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const id = text(row['id'])
    const delta = row['delta']
    const reason = text(row['reason'])
    if (id === '' || typeof delta !== 'number' || !Number.isSafeInteger(delta) || delta === 0) {
      continue
    }
    if (!MOVEMENT_REASON_LABELS[reason as MovementReason]) {
      continue
    }
    const saleId = text(row['sale_id'])
    const createdAt = text(row['created_at'])
    movements.push({
      id,
      delta,
      reason: reason as MovementReason,
      note: text(row['note']),
      sale_id: saleId === '' ? null : saleId,
      created_at: createdAt === '' ? null : createdAt,
    })
  }
  return movements
}

/** `limit` of the list of movements: an integer inside 1..100, default 50. */
export function movementsLimit(value: number | undefined): number {
  if (value === undefined || !Number.isSafeInteger(value)) {
    return MOVEMENTS_DEFAULT_LIMIT
  }
  if (value < 1) {
    return MOVEMENTS_DEFAULT_LIMIT
  }
  return Math.min(value, MOVEMENTS_MAX_LIMIT)
}

/** Spanish sentence of a failed request: the server's when it answered one (F6). */
export function productApiMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.message
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return fallback
}

// --- API client (frozen shapes of the F6 contract) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function productPath(salonId: string, productId: string, suffix = ''): string {
  return salonPath(
    salonId,
    `/products/${encodeURIComponent(productId)}${suffix}`,
  )
}

function requireProduct(raw: unknown, message: string): Product {
  const product = normalizeProduct(raw)
  if (!product) {
    throw new ApiError(200, message)
  }
  return product
}

/** `GET /products`: every product of the salon, active and inactive, by name. */
export async function fetchProducts(salonId: string): Promise<Product[]> {
  return normalizeProducts(await request<unknown>(salonPath(salonId, '/products')))
}

/** `POST /products` (201). A 400/409 keeps the Spanish sentence verbatim. */
export async function createProduct(
  salonId: string,
  payload: ProductPayload,
): Promise<Product> {
  return requireProduct(
    await request<unknown>(salonPath(salonId, '/products'), {
      method: 'POST',
      body: productCreateBody(payload),
    }),
    'El servidor guardó el producto pero no devolvió sus datos.',
  )
}

/** `PUT /products/:pid`: replaces the four keys and keeps the stock. */
export async function updateProduct(
  salonId: string,
  productId: string,
  payload: ProductUpdatePayload,
): Promise<Product> {
  return requireProduct(
    await request<unknown>(productPath(salonId, productId), {
      method: 'PUT',
      body: productUpdateBody(payload),
    }),
    'El servidor guardó el producto pero no devolvió sus datos.',
  )
}

/** `POST /products/:pid/adjust`: restock, loss or correction of the stock. */
export async function adjustProductStock(
  salonId: string,
  productId: string,
  payload: AdjustPayload,
): Promise<Product> {
  return requireProduct(
    await request<unknown>(productPath(salonId, productId, '/adjust'), {
      method: 'POST',
      body: adjustBody(payload),
    }),
    'El servidor guardó el ajuste pero no devolvió el producto.',
  )
}

/** `GET /products/:pid/movements?limit=`: the ledger of the product, newest first. */
export async function fetchProductMovements(
  salonId: string,
  productId: string,
  limit: number = MOVEMENTS_DEFAULT_LIMIT,
): Promise<StockMovement[]> {
  const query = `?limit=${movementsLimit(limit)}`
  return normalizeMovements(
    await request<unknown>(productPath(salonId, productId, `/movements${query}`)),
  )
}

// --- one write at a time (a double click must not send two requests) ---

export type InventoryGate = {
  /** True while a write is running. */
  busy: () => boolean
  /** Claims the gate; `false` when another write is already running. */
  start: () => boolean
  /** Releases the gate; `true` when it was busy. */
  finish: () => boolean
}

/**
 * Synchronous gate of the page. React state alone is not enough for a double
 * click: both presses can read the same render, so the guard lives here and the
 * state only paints the disabled button (same pattern as the client file save).
 */
export function createInventoryGate(): InventoryGate {
  let running = false
  return {
    busy: () => running,
    start: () => {
      if (running) {
        return false
      }
      running = true
      return true
    },
    finish: () => {
      const wasBusy = running
      running = false
      return wasBusy
    },
  }
}

/** Sentence of the write the page refused to send twice. */
export const INVENTORY_BUSY_MESSAGE = 'Ya se está guardando el producto: esperá a que termine.'

/** What the page asks the API to do: create, replace or adjust a product. */
export type ProductWrite =
  | { kind: 'create'; payload: ProductPayload }
  | { kind: 'update'; productId: string; payload: ProductUpdatePayload }
  | { kind: 'adjust'; productId: string; payload: AdjustPayload }

export type ProductWriteOutcome =
  | { status: 'saved'; product: Product }
  | { status: 'busy'; message: string }
  | { status: 'failed'; message: string }

/**
 * One press of a write button: claims the gate, performs the request and frees the
 * gate whatever happens. The row it returns is NOT painted as the source of truth:
 * the page asks the server for the list again, so the stock on screen is never the
 * one the client guessed (F6: no optimistic stock).
 */
export async function writeProduct(
  gate: InventoryGate,
  salonId: string,
  write: ProductWrite,
): Promise<ProductWriteOutcome> {
  if (!gate.start()) {
    return { status: 'busy', message: INVENTORY_BUSY_MESSAGE }
  }
  try {
    const product =
      write.kind === 'create'
        ? await createProduct(salonId, write.payload)
        : write.kind === 'update'
          ? await updateProduct(salonId, write.productId, write.payload)
          : await adjustProductStock(salonId, write.productId, write.payload)
    return { status: 'saved', product }
  } catch (error: unknown) {
    return {
      status: 'failed',
      message: productApiMessage(error, 'No pudimos guardar el producto.'),
    }
  } finally {
    gate.finish()
  }
}
