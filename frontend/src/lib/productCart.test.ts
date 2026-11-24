import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, API_BASE, createSale } from './api.ts'
import { dropReasonLabel, type DroppedOperation } from './offlineQueue.ts'
import { clearToken, setToken } from './session.ts'
import { insufficientStockMessage, isInsufficientStockMessage, type Product } from './products.ts'
import {
  addProduct,
  canAddProduct,
  changeProductQty,
  emptyProductCart,
  MAX_SALE_QTY,
  productCartQty,
  productCartTotalCents,
  productLineSubtotal,
  productQtyCap,
  productSaleLines,
  removeProduct,
  saleWithProducts,
  sellableProducts,
  stockAvailable,
  type PosProduct,
  type ProductCartLine,
} from './productCart.ts'
import type { SaleInput } from '../types.ts'

const tinte: PosProduct = { id: 'p1', name: 'Tinte rubio', price_cents: 1250, stock: 3 }
const shampoo: PosProduct = { id: 'p2', name: 'Shampoo', price_cents: 900, stock: 0 }
const cera: PosProduct = { id: 'p3', name: 'Cera', price_cents: 2500, stock: 5000 }

function rawProduct(over: Partial<Product> & Pick<Product, 'id' | 'name'>): Product {
  return {
    price_cents: 1250,
    stock: 3,
    low_stock_threshold: 0,
    active: true,
    low: false,
    created_at: null,
    ...over,
  }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function stubFetch(handler: () => Response | Promise<Response>) {
  const calls: { url: string; method: string; body: string | undefined }[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
    })
    return handler()
  }) as typeof globalThis.fetch
  return {
    calls,
    restore: () => {
      globalThis.fetch = original
    },
  }
}

const SERVICE_SALE: SaleInput = {
  professional_id: 'u1',
  client_name: 'Ana',
  method: 'cash',
  items: [{ service_id: 'svc-corte', qty: 1, unit_price_cents: 35000 }],
  client_op_id: 'op-1',
}

// --- el carrito de productos: clave product_id y cantidad entera ---

test('adding the same product merges it into one line', () => {
  let cart = emptyProductCart()
  cart = addProduct(cart, tinte)
  cart = addProduct(cart, tinte)
  assert.equal(cart.length, 1)
  assert.equal(cart[0]?.qty, 2)
  assert.deepEqual(cart[0], {
    product_id: 'p1',
    name: 'Tinte rubio',
    unit_price_cents: 1250,
    qty: 2,
  })
  assert.equal(productCartQty(cart), 2)
})

test('a product without stock cannot be added', () => {
  const cart = addProduct(emptyProductCart(), shampoo)
  assert.deepEqual(cart, [])
  assert.equal(canAddProduct(emptyProductCart(), shampoo), false)
  assert.equal(canAddProduct(emptyProductCart(), tinte), true)
})

test('the cart never holds more units than the known stock', () => {
  let cart = addProduct(emptyProductCart(), tinte)
  cart = addProduct(cart, tinte)
  cart = addProduct(cart, tinte)
  cart = addProduct(cart, tinte)
  assert.equal(cart[0]?.qty, 3)
  assert.equal(canAddProduct(cart, tinte), false)
  // A quantity typed above the stock is clamped, never accepted.
  assert.equal(changeProductQty(cart, 'p1', 9, 3)[0]?.qty, 3)
  assert.equal(changeProductQty(cart, 'p1', 9, 0)[0]?.qty, 1)
})

test('the quantity is a whole number between one and 1000', () => {
  const cart = addProduct(emptyProductCart(), cera, 2.7)
  assert.equal(cart[0]?.qty, 2)
  assert.equal(MAX_SALE_QTY, 1000)
  assert.equal(productQtyCap(5000), 1000)
  assert.equal(productQtyCap(0), 0)
  assert.equal(productQtyCap(-4), 0)
  assert.equal(changeProductQty(cart, 'p3', 5000, 5000)[0]?.qty, 1000)
  assert.equal(changeProductQty(cart, 'p3', 0, 5000)[0]?.qty, 1)
  assert.equal(changeProductQty(cart, 'p3', -7, 5000)[0]?.qty, 1)
})

test('removing a line empties the product cart', () => {
  const cart = removeProduct(addProduct(emptyProductCart(), tinte), 'p1')
  assert.deepEqual(cart, [])
  assert.equal(productCartTotalCents(cart), 0)
})

test('the totals of the products are integer cents', () => {
  let cart = addProduct(addProduct(emptyProductCart(), tinte), cera)
  cart = changeProductQty(cart, 'p1', 3, 3)
  assert.equal(productLineSubtotal(cart[0] as ProductCartLine), 3750)
  assert.equal(productCartTotalCents(cart), 3750 + 2500)
  assert.ok(Number.isInteger(productCartTotalCents(cart)))
  assert.equal(productCartQty(cart), 4)
  assert.deepEqual(productCartTotalCents(emptyProductCart()), 0)
})

// --- el POS lista solo los productos activos, con su stock ---

test('only active products reach the products section of the POS', () => {
  const products = sellableProducts([
    rawProduct({ id: 'p1', name: 'Tinte rubio' }),
    rawProduct({ id: 'pX', name: 'Viejo', active: false }),
    rawProduct({ id: 'p2', name: 'Shampoo', stock: 0, price_cents: 900 }),
  ])
  assert.deepEqual(products, [
    { id: 'p1', name: 'Tinte rubio', price_cents: 1250, stock: 3 },
    { id: 'p2', name: 'Shampoo', price_cents: 900, stock: 0 },
  ])
  assert.equal(stockAvailable(products, 'p1'), 3)
  // An unknown product has no known stock: the cap is zero, never "infinite".
  assert.equal(stockAvailable(products, 'nope'), 0)
})

// --- el cuerpo del POST /sales: solo product_id y qty ---

test('without products the sale body is exactly the one of today', () => {
  const body = saleWithProducts(SERVICE_SALE, emptyProductCart())
  assert.equal('products' in body, false)
  assert.deepEqual(body, SERVICE_SALE)
  assert.equal(JSON.stringify(body), JSON.stringify(SERVICE_SALE))
})

test('a mixed sale travels with the product lines and no product price', () => {
  let cart = addProduct(emptyProductCart(), tinte)
  cart = changeProductQty(cart, 'p1', 2, 3)
  const before = JSON.stringify(cart)
  const body = saleWithProducts(SERVICE_SALE, cart)
  const parsed = JSON.parse(JSON.stringify(body)) as Record<string, unknown>
  assert.deepEqual(parsed['products'], [{ product_id: 'p1', qty: 2 }])
  assert.deepEqual(parsed['items'], [
    { service_id: 'svc-corte', qty: 1, unit_price_cents: 35000 },
  ])
  const serialized = JSON.stringify(parsed['products'])
  assert.equal(serialized.includes('price'), false)
  assert.equal(serialized.includes('1250'), false)
  // The cart is not mutated by building the body and it keeps its own price only
  // for the display total.
  assert.equal(JSON.stringify(cart), before)
})

test('a sale of products only has product lines and no unit price at all', () => {
  const cart = addProduct(emptyProductCart(), tinte, 2)
  const body = saleWithProducts(
    { ...SERVICE_SALE, items: [] },
    cart,
  )
  const parsed = JSON.parse(JSON.stringify(body)) as Record<string, unknown>
  assert.deepEqual(parsed['items'], [])
  assert.deepEqual(parsed['products'], [{ product_id: 'p1', qty: 2 }])
  assert.equal(JSON.stringify(body).includes('unit_price_cents'), false)
  assert.deepEqual(productSaleLines(cart), [{ product_id: 'p1', qty: 2 }])
  assert.deepEqual(productSaleLines(emptyProductCart()), [])
})

test('the queued sale of the offline queue keeps the product lines without prices', () => {
  // The queue stores the very body of the request, so a queued product sale is
  // described exactly like a confirmed one (F6: the queue is not changed).
  const cart = addProduct(emptyProductCart(), tinte)
  const payload = saleWithProducts(SERVICE_SALE, cart)
  const parsed = JSON.parse(JSON.stringify(payload)) as Record<string, unknown>
  assert.deepEqual(parsed['products'], [{ product_id: 'p1', qty: 1 }])
  assert.equal(parsed['client_op_id'], 'op-1')
  assert.equal(
    JSON.stringify(payload),
    JSON.stringify({
      professional_id: 'u1',
      client_name: 'Ana',
      method: 'cash',
      items: [{ service_id: 'svc-corte', qty: 1, unit_price_cents: 35000 }],
      client_op_id: 'op-1',
      products: [{ product_id: 'p1', qty: 1 }],
    }),
  )
})

test('a queued product sale the server rejects is described as before', () => {
  // The queue drops every definitive 4xx and the POS keeps its old copy; a stock
  // rejection reads with the contract sentence after the existing prefix.
  const dropped = {
    client_op_id: 'op-9',
    kind: 'sale',
    salon_id: 's1',
    payload: saleWithProducts(SERVICE_SALE, addProduct(emptyProductCart(), tinte)),
    created_at: '2026-11-05T10:00:00Z',
    attempts: 1,
    next_attempt_at: 0,
    last_error: insufficientStockMessage('Tinte rubio'),
    dropped_at: 1,
    cause: 'server_rejected',
    reason: insufficientStockMessage('Tinte rubio'),
  } satisfies DroppedOperation
  assert.equal(
    dropReasonLabel(dropped),
    'El servidor rechazó esta operación: No hay stock suficiente de «Tinte rubio»',
  )
  assert.equal(isInsufficientStockMessage(dropped.reason), true)
})

// --- el 409 del servidor llega verbatim (y el carrito no se toca) ---

test('a 409 of insufficient stock keeps the sentence of the contract', async () => {
  const stub = stubFetch(() => json({ error: insufficientStockMessage('Tinte rubio') }, 409))
  try {
    setToken('jwt-pos')
    const cart = addProduct(emptyProductCart(), tinte)
    const body = saleWithProducts(SERVICE_SALE, cart)
    let status = 0
    await assert.rejects(
      () => createSale('s1', body),
      (error: unknown) => {
        assert.ok(error instanceof ApiError)
        status = error.status
        assert.equal(error.message, 'No hay stock suficiente de «Tinte rubio»')
        return true
      },
    )
    assert.equal(status, 409)
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/sales`)
    // The cart the screen keeps is untouched: the sale is retried as it was.
    assert.equal(cart.length, 1)
    assert.equal(cart[0]?.qty, 1)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), JSON.parse(JSON.stringify(body)))
  } finally {
    stub.restore()
    clearToken()
  }
})
