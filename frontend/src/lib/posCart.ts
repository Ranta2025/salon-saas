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

/**
 * Client file picked in the POS selector (contract F5). Only the two fields the
 * checkout needs: the id that travels to the API and the name that fills the
 * free-text field of the ticket.
 */
export type PosPickedClient = { id: string; name: string }

/**
 * Sale body with an optional client file (F5, D3c). With nobody picked the body
 * is EXACTLY the one of today: the `client_id` key is absent, never `undefined`.
 * A picked file fills the name when the field is empty (the person may have
 * typed another one) and travels as `client_id`.
 */
export function saleWithClient<T extends { client_name: string }>(
  body: T,
  picked: PosPickedClient | null,
): T & { client_id?: string } {
  const clientId = picked?.id.trim() ?? ''
  if (!picked || clientId === '') {
    return body
  }
  const typed = body.client_name.trim()
  return {
    ...body,
    client_name: typed === '' ? picked.name.trim() : body.client_name,
    client_id: clientId,
  }
}
