/**
 * History of a client (contract F5, «Contrato F5 (historial de la clienta…)»,
 * decision D6): visits, no-shows, next and last visit, linked bookings, and the
 * money — which the server sends as `null` to anyone without `puedeVerPlata`.
 *
 * Money is integer cents everywhere: the screen formats with `formatCents` and
 * never divides. A `spend` that is not there hides the spend card, the sales list
 * and every figure; the page renders what this module returns, so a role without
 * money cannot paint one.
 */

import { ApiError, request } from '../../../lib/api.ts'
import { TIME_ZONE, formatTimeOfDay, toDateInputValue } from '../../../lib/datetime.ts'
import { formatCents } from '../../../lib/money.ts'

/**
 * The empty state of the history, exactly as the contract freezes it. It explains
 * why there is nothing: new bookings link themselves, old ones only when the phone
 * matches a single file, and old sales are never attributed.
 */
export const EMPTY_HISTORY_TEXT =
  'Todavía no hay turnos vinculados a esta clienta. Los turnos nuevos se vinculan solos; los anteriores, cuando el teléfono coincide con una sola ficha. Las ventas anteriores no se atribuyen.'

/** Heading of the history section. */
export const HISTORY_TITLE = 'Historial'
/** Value of the card when the client has no upcoming booking. */
export const NO_NEXT_VISIT_TEXT = 'Sin turno agendado'
/** Value of the card when the client has no `done` booking yet. */
export const NO_LAST_VISIT_TEXT = 'Sin visitas registradas'
/** Explanation for a role the server does not send money to (D6). */
export const NO_MONEY_HINT =
  'Tu rol ve las visitas y los turnos, no los importes de las ventas.'
/** Heading of the spend card, shown only when `spend` is not null. */
export const SPEND_TITLE = 'Gasto vinculado'
/** Label of the spend card, next to the amount. */
export const SPEND_CARD_LABEL = 'Gasto total'
/** Heading of the recent sales list, shown only when `spend` is not null. */
export const SALES_TITLE = 'Ventas vinculadas'
/** Heading of the recent bookings list. */
export const BOOKINGS_TITLE = 'Turnos recientes'
/** Nothing to read: the server answered 200 with an unusable body. */
export const CLIENT_HISTORY_UNREADABLE_MESSAGE =
  'La API no devolvió el historial de esta clienta.'

export type ClientHistorySummary = {
  /** `done` bookings of the client. */
  visits: number
  /** `no_show` bookings of the client. */
  no_shows: number
  /** `cancelled` bookings of the client. */
  cancelled: number
  /** `booked` bookings in the future. */
  upcoming: number
  /** Local day of the salon (`YYYY-MM-DD`) of the newest `done` booking, or null. */
  last_visit: string | null
  /** RFC3339 of the next `booked` booking, or null. */
  next_visit: string | null
  /** IANA zone of the salon: every day of the history is read in it. */
  timezone: string
}

export type ClientHistoryBooking = {
  id: string
  starts_at: string
  status: string
  service_name: string
  professional_name: string
}

export type ClientHistorySpend = {
  sales_count: number
  /** Integer cents: only the sales linked to the file (D6). */
  total_cents: number
}

export type ClientHistorySale = {
  id: string
  created_at: string | null
  /** Integer cents. */
  total_cents: number
  method: string
}

export type ClientHistory = {
  client_id: string
  summary: ClientHistorySummary
  bookings: ClientHistoryBooking[]
  /** `null` when the role does not see money (D6): nothing of the spend is painted. */
  spend: ClientHistorySpend | null
  /** `null` when the role does not see money. */
  sales: ClientHistorySale[] | null
}

/** The money the screen may paint, or `null` when there is none to paint. */
export type ClientHistoryMoney = {
  spend: ClientHistorySpend
  sales: ClientHistorySale[]
}

/** One card of the summary (`kpi` of the panel). */
export type ClientHistoryCard = { label: string; value: string; hint: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Non-negative whole count; anything else is zero instead of `NaN` on screen. */
function count(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : 0
}

/** Integer cents or `null`: a value with decimals is never painted as money. */
function cents(value: unknown): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : null
}

/** Local day `YYYY-MM-DD` as sent by the server, or `null`. */
function localDay(value: unknown): string | null {
  const raw = text(value)
  return /^\d{4}-\d{2}-\d{2}$/.test(raw) ? raw : null
}

/** RFC3339 instant as sent by the server, or `null` when it cannot be read. */
function instant(value: unknown): string | null {
  const raw = text(value)
  if (raw === '' || Number.isNaN(Date.parse(raw))) {
    return null
  }
  return raw
}

/** The zone when `Intl` accepts it; otherwise the known one, never a throw. */
function usableTimeZone(timeZone: unknown): string {
  const candidate = text(timeZone)
  if (candidate === '') {
    return TIME_ZONE
  }
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: candidate })
    return candidate
  } catch {
    return TIME_ZONE
  }
}

function normalizeBooking(raw: unknown): ClientHistoryBooking | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const startsAt = instant(raw['starts_at'])
  if (id === '' || startsAt === null) {
    return null
  }
  return {
    id,
    starts_at: startsAt,
    status: text(raw['status']),
    service_name: text(raw['service_name']),
    professional_name: text(raw['professional_name']),
  }
}

function normalizeSale(raw: unknown): ClientHistorySale | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const total = cents(raw['total_cents'])
  if (id === '' || total === null) {
    return null
  }
  return {
    id,
    created_at: instant(raw['created_at']),
    total_cents: total,
    method: text(raw['method']),
  }
}

/**
 * Reads the frozen shape of `GET /clients/:cid/history`. A missing or malformed
 * `spend` means "no money for this role" and takes the sales with it: the screen
 * shows nothing of the money instead of a zero that never was.
 */
export function normalizeClientHistory(raw: unknown): ClientHistory | null {
  if (!isRecord(raw)) {
    return null
  }
  const clientId = text(raw['client_id'])
  if (clientId === '') {
    return null
  }

  const summaryRaw = isRecord(raw['summary']) ? raw['summary'] : {}
  const summary: ClientHistorySummary = {
    visits: count(summaryRaw['visits']),
    no_shows: count(summaryRaw['no_shows']),
    cancelled: count(summaryRaw['cancelled']),
    upcoming: count(summaryRaw['upcoming']),
    last_visit: localDay(summaryRaw['last_visit']),
    next_visit: instant(summaryRaw['next_visit']),
    timezone: text(summaryRaw['timezone']),
  }

  const bookingRows = Array.isArray(raw['bookings']) ? (raw['bookings'] as unknown[]) : []
  const bookings: ClientHistoryBooking[] = []
  for (const row of bookingRows) {
    const booking = normalizeBooking(row)
    if (booking) {
      bookings.push(booking)
    }
  }
  // The contract promises the most recent first; a wrong order never hides it.
  bookings.sort((a, b) => Date.parse(b.starts_at) - Date.parse(a.starts_at))

  const spendRaw = raw['spend']
  const spendTotal = isRecord(spendRaw) ? cents(spendRaw['total_cents']) : null
  const spend: ClientHistorySpend | null =
    isRecord(spendRaw) && spendTotal !== null
      ? { sales_count: isRecord(spendRaw) ? count(spendRaw['sales_count']) : 0, total_cents: spendTotal }
      : null

  let sales: ClientHistorySale[] | null = null
  if (spend !== null && Array.isArray(raw['sales'])) {
    sales = []
    for (const row of raw['sales'] as unknown[]) {
      const sale = normalizeSale(row)
      if (sale) {
        sales.push(sale)
      }
    }
    sales.sort((a, b) => Date.parse(b.created_at ?? '') - Date.parse(a.created_at ?? ''))
  }

  return { client_id: clientId, summary, bookings, spend, sales }
}

/** True when the client has at least one linked booking to list. */
export function hasLinkedBookings(history: ClientHistory): boolean {
  return history.bookings.length > 0
}

/**
 * The money the screen may paint. `spend === null` (professional, D6) returns
 * `null`: no card, no list, no figure — even if a sales array arrived by mistake.
 */
export function clientHistoryMoney(history: ClientHistory): ClientHistoryMoney | null {
  if (history.spend === null) {
    return null
  }
  return { spend: history.spend, sales: history.sales ?? [] }
}

/** `2026-11-20T21:30:00Z` in the zone of the salon → `2026-11-20 15:30`. */
export function formatHistoryInstant(value: string | null, timeZone: string): string {
  const raw = text(value)
  if (raw === '' || Number.isNaN(Date.parse(raw))) {
    return ''
  }
  const zone = usableTimeZone(timeZone)
  return `${toDateInputValue(new Date(raw), zone)} ${formatTimeOfDay(raw, zone)}`
}

/** Spend card copy: the amount with `formatCents` and how many sales it sums. */
export function spendText(spend: ClientHistorySpend): string {
  const sales = spend.sales_count === 1 ? '1 venta vinculada' : `${spend.sales_count} ventas vinculadas`
  return `${formatCents(spend.total_cents)} en ${sales}`
}

/**
 * The four cards of the summary, in the order of the contract: visits, no-shows,
 * next visit (read in the zone of the salon) and last visit (the local day the
 * server sent, never re-formatted).
 */
export function summaryCards(history: ClientHistory): ClientHistoryCard[] {
  const { summary } = history
  const next = formatHistoryInstant(summary.next_visit, summary.timezone)
  return [
    { label: 'Visitas', value: String(summary.visits), hint: 'Turnos atendidos' },
    { label: 'No asistió', value: String(summary.no_shows), hint: 'Turnos sin presentarse' },
    {
      label: 'Próxima visita',
      value: next === '' ? NO_NEXT_VISIT_TEXT : next,
      hint: 'En la zona del salón',
    },
    {
      label: 'Última visita',
      value: summary.last_visit ?? NO_LAST_VISIT_TEXT,
      hint: 'Día local del salón',
    },
  ]
}

// --- API client (frozen shape of the F5 contract) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

/** `GET /salons/:id/clients/:cid/history`; 404 `Ficha no encontrada` verbatim. */
export async function fetchClientHistory(
  salonId: string,
  clientId: string,
  signal?: AbortSignal,
): Promise<ClientHistory> {
  const history = normalizeClientHistory(
    await request<unknown>(
      salonPath(salonId, `/clients/${encodeURIComponent(clientId)}/history`),
      { signal },
    ),
  )
  if (!history) {
    throw new ApiError(200, CLIENT_HISTORY_UNREADABLE_MESSAGE)
  }
  return history
}
