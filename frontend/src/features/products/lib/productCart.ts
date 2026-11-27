/**
 * Product lines of the POS cart (contract F6). Pure rules: the cart is keyed by
 * `product_id`, the quantity is a whole number capped by the stock the server last
 * reported and by the 1000 units the sale endpoint accepts, and the money of a
 * line stays in integer cents.
 *
 * The lines travel to `POST /sales` as `products: [{product_id, qty}]` ONLY: the
 * price is read by the server from its own catalogue, so the client never sends one
 * and never decides the amount of the sale.
 */

import type { Product } from './products.ts'

/** Whole units of one product line the API accepts (contract F6, D5). */
export const MAX_SALE_QTY = 1000

/** A product as the POS lists it: active, with the stock the server last reported. */
export type PosProduct = {
  id: string
  name: string
  /** Integer cents of the catalogue; only used to show the total of the UI. */
  price_cents: number
  stock: number
}

export type ProductCartLine = {
  product_id: string
  name: string
  unit_price_cents: number
  qty: number
}

export type ProductCart = readonly ProductCartLine[]

/** Body line of `products` in `POST /sales`: no price ever travels here. */
export type ProductSaleLine = { product_id: string; qty: number }

export function emptyProductCart(): ProductCart {
  return []
}

/** Highest quantity a line may hold: the known stock, never above the contract cap. */
export function productQtyCap(stock: number): number {
  if (!Number.isFinite(stock) || stock <= 0) {
    return 0
  }
  return Math.min(Math.trunc(stock), MAX_SALE_QTY)
}

/** Stock the POS knows for a product; an unknown product has none, never "infinite". */
export function stockAvailable(products: readonly PosProduct[], productId: string): number {
  return products.find((product) => product.id === productId)?.stock ?? 0
}

/** The active products of the catalogue, with the stock to cap the cart. */
export function sellableProducts(products: readonly Product[]): PosProduct[] {
  return products
    .filter((product) => product.active)
    .map((product) => ({
      id: product.id,
      name: product.name,
      price_cents: product.price_cents,
      stock: product.stock,
    }))
}

export function canAddProduct(cart: ProductCart, product: PosProduct): boolean {
  const cap = productQtyCap(product.stock)
  if (cap === 0) {
    return false
  }
  const current = cart.find((line) => line.product_id === product.id)?.qty ?? 0
  return current < cap
}

/**
 * Adds units of a product, merging with the existing line. It never goes above the
 * known stock nor above `MAX_SALE_QTY`: the extra units of a double click are
 * dropped instead of arming a sale the server would reject.
 */
export function addProduct(cart: ProductCart, product: PosProduct, qty = 1): ProductCart {
  const cap = productQtyCap(product.stock)
  const step = Math.max(1, Math.trunc(Number.isFinite(qty) ? qty : 1))
  const existing = cart.find((line) => line.product_id === product.id)
  const current = existing?.qty ?? 0
  const next = Math.min(cap, current + step)
  if (next <= current) {
    return cart
  }
  if (existing) {
    return cart.map((line) =>
      line.product_id === product.id ? { ...line, qty: next } : line,
    )
  }
  if (next === 0) {
    return cart
  }
  return [
    ...cart,
    {
      product_id: product.id,
      name: product.name,
      unit_price_cents: product.price_cents,
      qty: next,
    },
  ]
}

/**
 * Sets an absolute quantity of a line: whole units between one and the known stock
 * (capped at `MAX_SALE_QTY`). Below one the line stays at one; use `removeProduct`.
 */
export function changeProductQty(
  cart: ProductCart,
  productId: string,
  qty: number,
  stock: number,
): ProductCart {
  const cap = Math.max(1, productQtyCap(stock))
  const value = Number.isFinite(qty) ? Math.trunc(qty) : 1
  const next = Math.min(Math.max(1, value), cap)
  return cart.map((line) =>
    line.product_id === productId ? { ...line, qty: next } : line,
  )
}

export function removeProduct(cart: ProductCart, productId: string): ProductCart {
  return cart.filter((line) => line.product_id !== productId)
}

export function productLineSubtotal(line: ProductCartLine): number {
  return line.unit_price_cents * line.qty
}

/** Total of the products for the UI. The server recalculates and is authoritative. */
export function productCartTotalCents(cart: ProductCart): number {
  return cart.reduce((total, line) => total + productLineSubtotal(line), 0)
}

/** Units of product in the cart. */
export function productCartQty(cart: ProductCart): number {
  return cart.reduce((count, line) => count + line.qty, 0)
}

/** The lines of the request body: only `product_id` and `qty`, never a price. */
export function productSaleLines(cart: ProductCart): ProductSaleLine[] {
  return cart.map((line) => ({ product_id: line.product_id, qty: line.qty }))
}

/**
 * Sale body with the product lines (F6, D5). Without product lines the body is
 * EXACTLY the one of today: the `products` key is absent, never `undefined`, so the
 * request and the whole flow of a sale of services do not change.
 */
export function saleWithProducts<T extends object>(
  body: T,
  cart: ProductCart,
): T & { products?: ProductSaleLine[] } {
  const lines = productSaleLines(cart)
  if (lines.length === 0) {
    return body
  }
  return { ...body, products: lines }
}
