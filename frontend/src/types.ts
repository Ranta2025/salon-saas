/** Types of the salon API contract (`/api/v1`). Money is always integer cents. */

export type Service = {
  id: string
  salon_id: string
  name: string
  duration_min: number
  price_cents: number
  commission_pct: number
  active: boolean
  /**
   * F2e: type of equipment the service uses (S-F2e-3). Optional because an API
   * without the field leaves it undefined; read it through
   * `serviceResourceTypeId` of `lib/equipment.ts`.
   */
  resource_type_id?: string | null
  /**
   * F16 (D2): text of the service shown on the public page; `null` when the salon
   * wrote none. Optional because an API without the field leaves it undefined.
   */
  description?: string | null
}

/** Body accepted by POST services. The server assigns id, salon_id and active=true. */
export type ServiceInput = {
  name: string
  duration_min: number
  price_cents: number
  commission_pct: number
  /** Optional (S-F2c-5): bookable members that do the new service, max 50. */
  professional_ids?: string[]
  /**
   * Equipment type the service uses (S-F2e-3): a type id, or `null` for none.
   * Omitted when absent so an older server sees the body of always.
   */
  resource_type_id?: string | null
  /**
   * F16 (D2): public description (<= 300). Optional on the server: absent keeps
   * what is stored, so it is sent as text, or `null` to clear it.
   */
  description?: string | null
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
  /** Idempotency key of the offline queue; the server deduplicates on it. */
  client_op_id?: string
  /**
   * F5 (D3c): client file the sale is linked to. Optional and absent unless the
   * POS picker chose one; the server validates it belongs to the salon.
   */
  client_id?: string
  /**
   * F6 (D5): product lines of the sale. Only `{product_id, qty}` travels — the price
   * is read by the server from its own catalogue — and the key is absent unless the
   * cart has at least one product, so a sale of services keeps the body of today.
   */
  products?: { product_id: string; qty: number }[]
}

/** Body accepted by POST bookings. */
export type BookingInput = {
  professional_id: string
  client_name: string
  starts_at: string
  ends_at: string
  /** Idempotency key of the offline queue; the server deduplicates on it. */
  client_op_id?: string
}

/**
 * Contract: `201 {sale_id, total}`. `total` is handled as integer cents, the same
 * unit used by every other money field.
 */
export type SaleCreated = {
  sale_id: string
  total: number
  /** F5: client file the sale got linked to (`""` when it has none). */
  client_id?: string
}

/**
 * What the POS gets back: the server confirmation or the local queue receipt
 * (offline). A queued sale is not on the server until the queue flushes.
 */
export type SaleOutcome =
  | { kind: 'confirmed'; server: SaleCreated }
  | { kind: 'queued'; client_op_id: string }

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
  /**
   * F2d3: id of the professional the turn was moved away from, when the salon
   * covered her absence by reassigning it. Optional: an API without it leaves
   * the field undefined and "Mi agenda" simply has no notice to show.
   */
  covered_from?: string | null
  /** F2e: unit of equipment the turn occupies (`null` when it uses none). */
  resource_unit_id?: string | null
}

export type AuthUser = {
  id: string
  name: string
  email: string
}

/** Client row of GET /api/v1/salons/:id/clients. */
export type Client = {
  id: string
  name: string
  phone: string | null
  email: string | null
  created_at: string | null
  /**
   * F5 (D4): allergies of the client. Optional because the field is new; the
   * full file with the editable text is read through `lib/clientRecord.ts`.
   */
  allergies?: string
}

/** Body accepted by POST /api/v1/salons/:id/clients (owner/admin/reception). */
export type ClientInput = {
  name: string
  phone: string
  email: string
  notes: string
}

/** Photo row of GET/POST /api/v1/salons/:id/clients/:cid/photos. */
export type ClientPhoto = {
  id: string
  url: string
  width: number | null
  height: number | null
  bytes: number | null
  created_at: string | null
}
