/**
 * Deposits (señas) of the salon — panel side (contract F11, D3, D4, D10, D13). Pure
 * rules plus the thin API clients: `/panel/senas` renders the policy form, the
 * preview of what the client sees and the list with its status filter.
 *
 * The server is the authority: the amount of each deposit (`amount_cents`), its
 * state, the hold that expires it and who may read or write a policy all come from
 * it. Nothing here recomputes money: the landing prints the `deposit_cents` the
 * server sends, and the panel prints `amount_cents` as it arrives. No floats.
 *
 * Online only: a policy write is never queued (the offline queue would discard the
 * 400/409 of a policy the person was told had been saved), and the list is read
 * again after every write — there is no optimistic state.
 */

import { ApiError, request } from './api.ts'
import { isBrowserOnline } from './connectivity.ts'
import { createPackagesGate, type PackagesGate } from './packages.ts'
import { formatCents } from './money.ts'
import { depositSentence } from './publicSalon.ts'

// --- limits and bounds of the contract (D2, D3) ---

export const MIN_DEPOSIT_PERCENT = 10
export const MAX_DEPOSIT_PERCENT = 100
export const MIN_HOLD_MINUTES = 5
export const MAX_HOLD_MINUTES = 120
export const MIN_REFUND_HOURS = 0
export const MAX_REFUND_HOURS = 168

/** Rows per request of `GET /salons/:id/deposits` (D10, the same cursor as the audit log). */
export const DEPOSIT_PAGE_SIZE = 50

/** The words of every simulated payment (D13-b, D13-c). */
export const DEPOSIT_SIMULATED_NOTICE = 'Pago simulado'

/**
 * Note of the panel (D13-a, verbatim): the deposit is NOT discounted by the POS, so
 * reception has to do it by hand when it charges the booking at the register.
 */
export const DEPOSIT_CASH_NOTE = 'Descuenta la sena cobrada al cobrar el turno en caja'

/**
 * Sentences of the API of D3, printed verbatim (the client pre-checks the same
 * rules with the same words) plus the ones this module says itself.
 */
export const DEPOSIT_MESSAGES = {
  missingPolicy: 'Faltan datos de la sena: activada, porcentaje, espera y devolucion',
  percentRange: 'El porcentaje debe ser entre 10 y 100',
  holdRange: 'La espera debe ser entre 5 y 120 minutos',
  refundRange: 'La devolucion debe ser entre 0 y 168 horas',
} as const

/** Sentence of the write the page refused to send twice. */
export const DEPOSIT_BUSY_MESSAGE =
  'Ya se está guardando la política de señas: esperá a que termine.'

/** Nothing was sent: the browser reports being offline. */
export const DEPOSIT_OFFLINE_MESSAGE =
  'Sin conexión. La seña solo se configura en línea: esta operación no se envió ni quedó en cola. Volvé a intentarlo cuando tengas conexión.'

/** The request failed on the way: the server may or may not have seen it. */
export const DEPOSIT_NETWORK_MESSAGE =
  'No pudimos conectar con el servidor, así que no sabemos si la política se guardó. No quedó en cola ni se guardó para más tarde.'

// --- policy: types, normalizer, draft, validation ---

/** `GET /deposit-policy` (D3). A never-saved policy is `enabled:false` and the rest `null`. */
export type DepositPolicy = {
  enabled: boolean
  percent: number | null
  hold_minutes: number | null
  refund_hours: number | null
}

/** Body of `PUT /deposit-policy` (D3): the four keys always travel, as integers. */
export type DepositPolicyPayload = {
  enabled: boolean
  percent: number
  hold_minutes: number
  refund_hours: number
}

/** What the form holds: numbers still unparsed, exactly as typed. */
export type DepositPolicyDraft = {
  enabled: boolean
  percent: string
  hold_minutes: string
  refund_hours: string
}

export type DepositPolicyField = 'percent' | 'hold' | 'refund'

export type DepositPolicyCheck =
  | { ok: true; payload: DepositPolicyPayload }
  | { ok: false; field: DepositPolicyField; message: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function isInt(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value)
}

function intOrNull(value: unknown): number | null {
  return isInt(value) ? value : null
}

/** Whole number typed by a person (`30`, `+30`); anything else is `null`. */
function parseWhole(input: string): number | null {
  const cleaned = typeof input === 'string' ? input.trim() : ''
  if (!/^[+-]?\d+$/.test(cleaned)) {
    return null
  }
  const value = Number(cleaned)
  return Number.isSafeInteger(value) ? value : null
}

/** `GET /deposit-policy`: `null` when the answer is not an object at all. */
export function normalizeDepositPolicy(raw: unknown): DepositPolicy | null {
  if (!isRecord(raw)) {
    return null
  }
  // A wrapped answer reads the same as a bare one (both shapes exist in the API).
  const source = isRecord(raw['deposit_policy']) ? raw['deposit_policy'] : raw
  if (!('enabled' in source) && !('percent' in source)) {
    return null
  }
  return {
    enabled: source['enabled'] === true,
    percent: intOrNull(source['percent']),
    hold_minutes: intOrNull(source['hold_minutes']),
    refund_hours: intOrNull(source['refund_hours']),
  }
}

/** The form of a saved policy, or the empty one when it was never saved. */
export function draftFromPolicy(policy: DepositPolicy | null): DepositPolicyDraft {
  if (policy === null) {
    return { enabled: false, percent: '', hold_minutes: '', refund_hours: '' }
  }
  return {
    enabled: policy.enabled,
    percent: policy.percent === null ? '' : String(policy.percent),
    hold_minutes: policy.hold_minutes === null ? '' : String(policy.hold_minutes),
    refund_hours: policy.refund_hours === null ? '' : String(policy.refund_hours),
  }
}

/**
 * Reads the policy form before sending it. The server asks for the four keys even
 * when the deposit is switched off (D3), so all three numbers are required either
 * way and each one is checked against its range with the sentence of the contract.
 */
export function validateDepositPolicy(draft: DepositPolicyDraft): DepositPolicyCheck {
  const missing = DEPOSIT_MESSAGES.missingPolicy
  const percentText = draft.percent.trim()
  const holdText = draft.hold_minutes.trim()
  const refundText = draft.refund_hours.trim()

  if (percentText === '') {
    return { ok: false, field: 'percent', message: missing }
  }
  const percent = parseWhole(percentText)
  if (percent === null || percent < MIN_DEPOSIT_PERCENT || percent > MAX_DEPOSIT_PERCENT) {
    return { ok: false, field: 'percent', message: DEPOSIT_MESSAGES.percentRange }
  }
  if (holdText === '') {
    return { ok: false, field: 'hold', message: missing }
  }
  const hold = parseWhole(holdText)
  if (hold === null || hold < MIN_HOLD_MINUTES || hold > MAX_HOLD_MINUTES) {
    return { ok: false, field: 'hold', message: DEPOSIT_MESSAGES.holdRange }
  }
  if (refundText === '') {
    return { ok: false, field: 'refund', message: missing }
  }
  const refund = parseWhole(refundText)
  if (refund === null || refund < MIN_REFUND_HOURS || refund > MAX_REFUND_HOURS) {
    return { ok: false, field: 'refund', message: DEPOSIT_MESSAGES.refundRange }
  }
  return {
    ok: true,
    payload: {
      enabled: draft.enabled,
      percent,
      hold_minutes: hold,
      refund_hours: refund,
    },
  }
}

/** Body of `PUT /deposit-policy`: the four keys, explicit, never `undefined`. */
export function policyBody(payload: DepositPolicyPayload): DepositPolicyPayload {
  return {
    enabled: payload.enabled === true,
    percent: payload.percent,
    hold_minutes: payload.hold_minutes,
    refund_hours: payload.refund_hours,
  }
}

/**
 * The preview of what the client sees on the landing and on the confirmation
 * screen (D11, D13-a): the sentence of the public page, or `null` while the
 * percentage is not usable.
 */
export function depositPreview(percent: number): string | null {
  if (!isInt(percent) || percent < MIN_DEPOSIT_PERCENT || percent > MAX_DEPOSIT_PERCENT) {
    return null
  }
  return depositSentence(percent)
}

// --- the list of deposits (D10) ---

export type DepositStatus = 'pending' | 'paid' | 'expired' | 'refunded' | 'forfeited'

/** The five words of the contract, exact. */
export const DEPOSIT_STATUS_LABELS: Record<DepositStatus, string> = {
  pending: 'Pendiente',
  paid: 'Pagada',
  expired: 'Vencida',
  refunded: 'Devuelta',
  forfeited: 'Perdida',
}

const DEPOSIT_STATUSES: readonly DepositStatus[] = [
  'pending',
  'paid',
  'expired',
  'refunded',
  'forfeited',
]

export function isDepositStatus(value: string): value is DepositStatus {
  return (DEPOSIT_STATUSES as readonly string[]).includes(value)
}

/** Spanish word of a state; an unknown one keeps its raw name instead of inventing a chip. */
export function depositStatusLabel(status: string): string {
  return isDepositStatus(status) ? DEPOSIT_STATUS_LABELS[status] : status
}

/** CSS tone of a chip: «on» is money in, «warn» is waiting, «off» is closed. */
export function depositStatusTone(status: string): 'on' | 'warn' | 'off' {
  if (status === 'paid') {
    return 'on'
  }
  if (status === 'pending') {
    return 'warn'
  }
  return 'off'
}

export type DepositFilterOption = { value: string; label: string }

/** Options of the status filter: «Todas» plus every state of D2. */
export const DEPOSIT_STATUS_FILTERS: readonly DepositFilterOption[] = [
  { value: '', label: 'Todas' },
  ...DEPOSIT_STATUSES.map((status) => ({ value: status as string, label: DEPOSIT_STATUS_LABELS[status] })),
]

/** One row of `GET /salons/:id/deposits` (D10). */
export type DepositRow = {
  id: string
  booking_id: string
  client_name: string
  /** Start of the booking the deposit holds, RFC3339. */
  starts_at: string
  amount_cents: number
  status: string
  paid_at: string | null
  settled_at: string | null
}

export type DepositPage = {
  deposits: DepositRow[]
  /** Opaque cursor for the next (older) page; `null` = no more. */
  next_cursor: string | null
}

function instant(value: unknown): string | null {
  const raw = text(value)
  return raw !== '' && !Number.isNaN(Date.parse(raw)) ? raw : null
}

function normalizeDepositRow(raw: unknown): DepositRow | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const startsAt = instant(raw['starts_at'])
  const amount = raw['amount_cents']
  const status = text(raw['status'])
  if (id === '' || startsAt === null || !isInt(amount) || amount < 0 || status === '') {
    return null
  }
  return {
    id,
    booking_id: text(raw['booking_id']),
    client_name: text(raw['client_name']),
    starts_at: startsAt,
    amount_cents: amount,
    status,
    paid_at: instant(raw['paid_at']),
    settled_at: instant(raw['settled_at']),
  }
}

/**
 * `GET /salons/:id/deposits` -> `{deposits, next_cursor}`. A body of another shape is
 * `null` (an error), never «no hay señas»: an empty list has to mean the server said so.
 */
export function normalizeDepositPage(raw: unknown): DepositPage | null {
  if (!isRecord(raw)) {
    return null
  }
  const rows = Array.isArray(raw['deposits'])
    ? raw['deposits']
    : Array.isArray(raw['rows'])
      ? raw['rows']
      : null
  if (rows === null) {
    return null
  }
  const deposits: DepositRow[] = []
  for (const row of rows) {
    const parsed = normalizeDepositRow(row)
    if (parsed) {
      deposits.push(parsed)
    }
  }
  const cursor = text(raw['next_cursor'])
  return { deposits, next_cursor: cursor === '' ? null : cursor }
}

/**
 * Appends the next page without repeating an id (a deposit written between two
 * requests can shift a page boundary). The server order (newest first) is kept and
 * the copy already on screen wins. Inputs are not mutated.
 */
export function mergeDepositRows(
  current: readonly DepositRow[],
  incoming: readonly DepositRow[],
): DepositRow[] {
  const seen = new Set<string>()
  const merged: DepositRow[] = []
  for (const row of [...current, ...incoming]) {
    if (!seen.has(row.id)) {
      seen.add(row.id)
      merged.push(row)
    }
  }
  return merged
}

// --- API client of the panel ---

export type DepositQuery = {
  /** Exact state; empty/absent = «Todas». */
  status?: string | null
  /** Opaque cursor of the previous page. */
  before?: string | null
  signal?: AbortSignal
}

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

/** Path of one page: salon scoped, `limit` always, `before` and `status` only when set. */
export function depositsPath(salonId: string, query: Pick<DepositQuery, 'status' | 'before'>): string {
  const params = [`limit=${DEPOSIT_PAGE_SIZE}`]
  const before = query.before?.trim() ?? ''
  if (before !== '') {
    params.push(`before=${encodeURIComponent(before)}`)
  }
  const status = query.status?.trim() ?? ''
  if (status !== '') {
    params.push(`status=${encodeURIComponent(status)}`)
  }
  return `${salonPath(salonId, '/deposits')}?${params.join('&')}`
}

/** `GET /salons/:id/deposits` (owner/admin/reception). Server sentences reach the caller as they are. */
export async function fetchDeposits(salonId: string, query: DepositQuery = {}): Promise<DepositPage> {
  const page = normalizeDepositPage(
    await request<unknown>(depositsPath(salonId, query), query.signal ? { signal: query.signal } : {}),
  )
  if (!page) {
    throw new ApiError(200, 'La API no devolvió la lista de senas.')
  }
  return page
}

/** `GET /deposit-policy` (any active member): `null` when the body is unusable. */
export async function fetchDepositPolicy(
  salonId: string,
  signal?: AbortSignal,
): Promise<DepositPolicy | null> {
  return normalizeDepositPolicy(
    await request<unknown>(salonPath(salonId, '/deposit-policy'), signal ? { signal } : {}),
  )
}

/** `PUT /deposit-policy` (owner/admin): the four keys, explicit. */
export async function saveDepositPolicy(
  salonId: string,
  payload: DepositPolicyPayload,
): Promise<DepositPolicy | null> {
  return normalizeDepositPolicy(
    await request<unknown>(salonPath(salonId, '/deposit-policy'), {
      method: 'PUT',
      body: policyBody(payload),
    }),
  )
}

// --- one write at a time, online only ---

/** Synchronous gate of the screens (the same one as packages, loyalty and 2FA). */
export type DepositGate = PackagesGate
export const createDepositGate: () => DepositGate = createPackagesGate

export type DepositWriteOutcome =
  | { status: 'saved'; policy: DepositPolicy | null }
  | { status: 'busy'; message: string }
  /** Nothing was sent: the browser is offline. */
  | { status: 'offline'; message: string }
  /**
   * The server answered an error. `definitive` is true for a 4xx (it stored nothing);
   * a 5xx keeps the sentence and the form can be sent again.
   */
  | { status: 'failed'; message: string; definitive: boolean }

/**
 * One press of «Guardar»: refuses a second one while the first runs, sends NOTHING
 * when the browser is offline and never enqueues anything. The server sentence is
 * returned verbatim (409 without bank, 400 of a range, 403 of a role).
 */
export async function writeDepositPolicy(
  gate: DepositGate,
  salonId: string,
  payload: DepositPolicyPayload,
  isOnline: () => boolean = isBrowserOnline,
): Promise<DepositWriteOutcome> {
  if (!gate.start()) {
    return { status: 'busy', message: DEPOSIT_BUSY_MESSAGE }
  }
  try {
    if (!isOnline()) {
      return { status: 'offline', message: DEPOSIT_OFFLINE_MESSAGE }
    }
    return { status: 'saved', policy: await saveDepositPolicy(salonId, payload) }
  } catch (error: unknown) {
    if (error instanceof ApiError) {
      return { status: 'failed', message: error.message, definitive: error.status < 500 }
    }
    return { status: 'failed', message: DEPOSIT_NETWORK_MESSAGE, definitive: false }
  } finally {
    gate.finish()
  }
}

// --- the additive `payment` of the public booking (D8, D13-b) ---

/** `payment` of the 201 of `POST /public/salons/:slug/bookings` (D8). */
export type BookingPayment = {
  depositId: string
  amountCents: number
  /** The hold of the slot ends here; the sweeper cancels the booking (D9). */
  expiresAt: string
  /** `/pagar#<charge_id>` as the server built it (D8). */
  payUrl: string
  simulated: boolean
}

/** Reads `payment`; anything the confirmation screen cannot use is dropped. */
export function normalizeBookingPayment(raw: unknown): BookingPayment | null {
  if (!isRecord(raw)) {
    return null
  }
  const depositId = text(raw['deposit_id'])
  const amount = raw['amount_cents']
  const expiresAt = instant(raw['expires_at'])
  const payUrl = text(raw['pay_url'])
  if (depositId === '' || !isInt(amount) || amount < 1 || expiresAt === null || payUrl === '') {
    return null
  }
  return { depositId, amountCents: amount, expiresAt, payUrl, simulated: raw['simulated'] === true }
}

export type PaymentTarget =
  | { kind: 'path'; path: string }
  | { kind: 'external'; href: string }

/**
 * Where «Pagar seña» goes. The server builds an absolute `pay_url`; a SAME-ORIGIN one
 * becomes a path plus fragment, so the app navigates inside the SPA and the charge id
 * never travels to another host through a full page load. Another origin is offered
 * as an external link (never rewritten). Anything unusable is `null`.
 */
export function paymentTarget(payUrl: string, origin: string): PaymentTarget | null {
  const raw = typeof payUrl === 'string' ? payUrl.trim() : ''
  if (raw === '') {
    return null
  }
  // A value that is neither absolute nor rooted is not a payment url: it would
  // navigate the SPA somewhere nobody asked for.
  if (!/^https?:\/\//i.test(raw) && !raw.startsWith('/')) {
    return null
  }
  let url: URL
  try {
    url = new URL(raw, origin)
  } catch {
    return null
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return null
  }
  if (url.origin === origin) {
    // Same origin without a fragment is not the payment page: the charge id lives there.
    return url.hash.length > 1
      ? { kind: 'path', path: `${url.pathname}${url.search}${url.hash}` }
      : null
  }
  return { kind: 'external', href: url.toString() }
}

// --- countdown of the hold ---

export type Countdown = { expired: boolean; label: string }

/**
 * `mm:ss` while less than an hour is left and `H h MM min` from there on; seconds
 * are rounded UP so a hold never reads «Vencido» while it still lasts.
 */
export function formatCountdown(ms: number): Countdown {
  if (!Number.isFinite(ms) || ms <= 0) {
    return { expired: true, label: 'Vencido' }
  }
  const totalSeconds = Math.ceil(ms / 1000)
  if (totalSeconds < 3600) {
    const minutes = Math.floor(totalSeconds / 60)
    const seconds = totalSeconds % 60
    return {
      expired: false,
      label: `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`,
    }
  }
  const totalMinutes = Math.ceil(totalSeconds / 60)
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  return {
    expired: false,
    label: `${hours} h ${String(minutes).padStart(2, '0')} min`,
  }
}

/**
 * `26 nov 2026, 12:40`: an instant of the payment pages in a zone (the visitor's, as
 * in the rest of the public page). An unusable date is a dash, never «Invalid Date».
 */
export function formatPublicInstant(iso: string, timeZone: string): string {
  const when = Date.parse(iso)
  if (Number.isNaN(when)) {
    return '—'
  }
  const zone = (typeof timeZone === 'string' ? timeZone.trim() : '') || 'UTC'
  try {
    return new Intl.DateTimeFormat('es-MX', {
      timeZone: zone,
      day: 'numeric',
      month: 'short',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(new Date(when))
  } catch {
    return new Intl.DateTimeFormat('es-MX', {
      timeZone: 'UTC',
      day: 'numeric',
      month: 'short',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(new Date(when))
  }
}

/** What the confirmation screen of the booking paints about the deposit (D13-b). */
export type DepositPaymentSummary = {
  amountLabel: string
  /** `14:59` / `1 h 02 min` / `Vencido`; `null` when the server sent no usable date. */
  countdown: string | null
  expired: boolean
  /** «Pago simulado» while the gateway is the simulated bank (D13-b). */
  notice: string | null
}

/** Deposit block of the confirmation: amount, countdown of the hold and the notice. */
export function depositPaymentSummary(
  payment: BookingPayment,
  now: Date = new Date(),
): DepositPaymentSummary {
  const expires = Date.parse(payment.expiresAt)
  if (Number.isNaN(expires)) {
    return {
      amountLabel: formatCents(payment.amountCents),
      countdown: null,
      expired: false,
      notice: payment.simulated ? DEPOSIT_SIMULATED_NOTICE : null,
    }
  }
  const countdown = formatCountdown(expires - now.getTime())
  return {
    amountLabel: formatCents(payment.amountCents),
    countdown: countdown.label,
    expired: countdown.expired,
    notice: payment.simulated ? DEPOSIT_SIMULATED_NOTICE : null,
  }
}

// --- public state of a payment: `/pago-resultado` (D8, D13-c) ---

/** `POST /api/v1/public/deposits/lookup {charge_id}` (D8). */
export type PublicDepositLookup = {
  status: string
  amountCents: number
  expiresAt: string
  startsAt: string
}

export function normalizePublicDepositLookup(raw: unknown): PublicDepositLookup | null {
  if (!isRecord(raw)) {
    return null
  }
  const status = text(raw['status'])
  const amount = raw['amount_cents']
  const expiresAt = instant(raw['expires_at'])
  const startsAt = instant(raw['starts_at'])
  if (status === '' || !isInt(amount) || amount < 0 || expiresAt === null || startsAt === null) {
    return null
  }
  return { status, amountCents: amount, expiresAt, startsAt }
}

/** How often `/pago-resultado` asks again while the deposit is pending. */
export const PAYMENT_POLL_INTERVAL_MS = 3_000
/** How long it keeps asking before it stops and says so. */
export const PAYMENT_POLL_TIMEOUT_MS = 60_000
export const PAYMENT_POLL_MAX_ATTEMPTS = PAYMENT_POLL_TIMEOUT_MS / PAYMENT_POLL_INTERVAL_MS

export type PollDecision = 'poll' | 'stop'

/**
 * Polling policy of `/pago-resultado` (D13-c): only a `pending` deposit is asked
 * again, at most `PAYMENT_POLL_MAX_ATTEMPTS` times and never past the 60 s budget.
 * Every other state (paid, expired, refunded, forfeited, an unknown one) stops.
 */
export function pollDecision(input: {
  status: string
  elapsedMs: number
  attempt: number
}): PollDecision {
  if (input.status.trim() !== 'pending') {
    return 'stop'
  }
  if (!Number.isFinite(input.elapsedMs) || input.elapsedMs >= PAYMENT_POLL_TIMEOUT_MS) {
    return 'stop'
  }
  if (input.attempt >= PAYMENT_POLL_MAX_ATTEMPTS) {
    return 'stop'
  }
  return 'poll'
}

/** Sentence of the public page for the state the server reported. */
export function depositStatusSentence(status: string): string {
  switch (status.trim()) {
    case 'pending':
      return 'Estamos confirmando el pago simulado…'
    case 'paid':
      return 'Pago confirmado: la seña está pagada y el turno queda reservado.'
    case 'expired':
      return 'El plazo para pagar venció: la seña no se pagó y la reserva se canceló.'
    case 'refunded':
      return 'El pago se devolvió: la seña se reintegró al medio de pago simulado.'
    case 'forfeited':
      return 'La seña se perdió: la reserva se canceló con menos anticipación de la que permite la devolución.'
    default:
      return 'No pudimos leer el estado del pago.'
  }
}

/**
 * `POST /api/v1/public/deposits/lookup`: anonymous, the charge id travels in the
 * BODY (it is the credential of the payment) and the server sentence of a 404
 * «Pago no encontrado» reaches the page verbatim.
 */
export async function lookupPublicDeposit(
  chargeId: string,
  options: { fetchImpl?: typeof fetch; signal?: AbortSignal } = {},
): Promise<PublicDepositLookup | null> {
  const lookup = normalizePublicDepositLookup(
    await request<unknown>('/api/v1/public/deposits/lookup', {
      method: 'POST',
      body: { charge_id: chargeId },
      auth: 'none',
      ...(options.fetchImpl ? { fetchImpl: options.fetchImpl } : {}),
      ...(options.signal ? { signal: options.signal } : {}),
    }),
  )
  return lookup
}
