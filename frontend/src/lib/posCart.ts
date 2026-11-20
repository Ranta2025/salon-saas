/** Pure cart logic for the POS. Money stays in integer cents at all times. */

export type PosService = {
  id: string
  name: string
  price_cents: number
}

export type CartLine = {
  service_id: string
  name: string
  unit_price_cents: number
  qty: number
}

export type Cart = readonly CartLine[]

export function emptyCart(): Cart {
  return []
}

/** Adds a service, merging with the existing line for the same service. */
export function addItem(cart: Cart, service: PosService, qty = 1): Cart {
  const step = Math.max(1, Math.trunc(qty))
  const existing = cart.find((line) => line.service_id === service.id)
  if (existing) {
    return cart.map((line) =>
      line.service_id === service.id ? { ...line, qty: line.qty + step } : line,
    )
  }
  return [
    ...cart,
    {
      service_id: service.id,
      name: service.name,
      unit_price_cents: service.price_cents,
      qty: step,
    },
  ]
}

/** Sets an absolute quantity. Below one, the line stays at one (use removeItem). */
export function changeQty(cart: Cart, serviceId: string, qty: number): Cart {
  const next = Math.max(1, Math.trunc(qty))
  return cart.map((line) => (line.service_id === serviceId ? { ...line, qty: next } : line))
}

export function removeItem(cart: Cart, serviceId: string): Cart {
  return cart.filter((line) => line.service_id !== serviceId)
}

export function lineSubtotal(line: CartLine): number {
  return line.unit_price_cents * line.qty
}

/** Total shown in the UI. The server recalculates it and stays authoritative. */
export function totalCents(cart: Cart): number {
  return cart.reduce((total, line) => total + lineSubtotal(line), 0)
}

export function itemCount(cart: Cart): number {
  return cart.reduce((count, line) => count + line.qty, 0)
}
