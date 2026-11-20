/** Types of the salon API contract (`/api/v1`). Money is always integer cents. */

export type Service = {
  id: string
  salon_id: string
  name: string
  duration_min: number
  price_cents: number
  commission_pct: number
  active: boolean
}

/** Body accepted by POST services. The server assigns id, salon_id and active=true. */
export type ServiceInput = {
  name: string
  duration_min: number
  price_cents: number
  commission_pct: number
}

/** Body accepted by PUT services; `active` absent means "keep active". */
export type ServiceUpdateInput = ServiceInput & { active: boolean }

export type SaleMethod = 'cash' | 'card' | 'transfer'

export type SaleMethodLabel = Record<SaleMethod, string>

export const SALE_METHODS: SaleMethodLabel = {
  cash: 'Efectivo',
  card: 'Tarjeta',
  transfer: 'Transferencia',
}

export type SaleItem = {
  service_id: string
  qty: number
  unit_price_cents: number
}

export type Sale = {
  id: string
  salon_id: string
  professional_id: string
  client_name: string
  items: SaleItem[]
  total_cents: number
  method: SaleMethod
  created_at: string
}

/** What the POS sends: `total` is never sent, the server computes it from qty * unit price. */
export type SaleInput = {
  professional_id: string
  client_name: string
  method: SaleMethod
  items: { service_id: string; qty: number; unit_price_cents: number }[]
}

/**
 * Contract: `201 {sale_id, total}`. `total` is handled as integer cents, the same
 * unit used by every other money field.
 */
export type SaleCreated = {
  sale_id: string
  total: number
}

export type CashClose = {
  id: string
  salon_id: string
  opened_at: string
  closed_at: string
  system_cash_cents: number
  counted_cents: number
  difference_cents: number
  note: string
}

export type Booking = {
  id: string
  professional_id: string
  client_name: string
  starts_at: string
  ends_at: string
  status: string
}

export type AuthUser = {
  id: string
  name: string
  email: string
}
