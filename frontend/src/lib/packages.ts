/**
 * Prepaid packages with a balance per client (contract F8, «Contrato F8 (paquetes
 * y bonos prepagos con saldo por clienta)», D5/D7/D8/D9/D10/D11). Pure rules plus
 * the thin API client: `/panel/paquetes` and the «Paquetes» section of the client
 * file render what these return, and the server stays the authority on prices,
 * balances, expiry and permissions.
 *
 * Money is integer cents everywhere (the price a person types goes through the
 * integer parser of the expenses, never a float). The balance is NEVER painted
 * locally: a write returns what the server stored and the screen asks again.
 *
 * Online only (D11-c): nothing here touches the offline queue. When the browser is
 * offline the write is not even sent, and a network error is reported as such; the
 * 4xx of a queued operation would be discarded by the queue, which is exactly what
 * a sale of money must not go through.
 */

import { ApiError, NetworkError, request } from './api.ts'
import { toDateInputValue, TIME_ZONE } from './datetime.ts'
import { isBrowserOnline } from './connectivity.ts'
import { expenseAmountInputFromCents, parseExpenseAmountToCents } from './expenses.ts'
import { newClientOpId } from './offlineQueue.ts'
import { SALE_METHODS, type SaleMethod } from '../types.ts'

/** Domain limit of a package name (`service_packages.name` CHECK of 031). */
export const MAX_PACKAGE_NAME_LENGTH = 120

/** Sessions of a package (D3: 1..100). */
export const MIN_PACKAGE_SESSIONS = 1
export const MAX_PACKAGE_SESSIONS = 100

/** Cap of a package price in cents (D3). */
export const MAX_PACKAGE_PRICE_CENTS = 10_000_000

/** Validity in days (D3: 1..3650); no expiry is `null`. */
export const MIN_VALIDITY_DAYS = 1
export const MAX_VALIDITY_DAYS = 3650

/** Characters of a movement note (D3 `note <= 200`, D9 `1..200`). */
export const MAX_PACKAGE_NOTE_LENGTH = 200

/**
 * Sentences of the API, printed verbatim (contract F8), plus the ones the client
 * says before sending a request that would be rejected for the same reason.
 */
export const PACKAGE_MESSAGES = {
  missingCreate: 'Faltan datos del paquete: nombre, servicio, sesiones y precio',
  missingUpdate: 'Faltan datos del paquete: nombre, precio, vigencia y estado',
  duplicateName: 'Ya hay un paquete activo con ese nombre',
  invalidPackage: 'Paquete inválido',
  notFound: 'Paquete no encontrado',
  noSessions: 'No quedan sesiones en este paquete',
  expired: 'El paquete venció',
  adjustNoteRequired: 'Escribí el motivo del ajuste',
  adjustOutOfRange: 'El ajuste deja el saldo fuera de rango',
  // Client-side copies: the server would answer 400 for the same reason.
  invalidText: 'El texto no puede tener saltos de línea ni caracteres no válidos',
  nameTooLong: 'El nombre del paquete no puede superar los 120 caracteres',
  sessionsRange: 'Las sesiones deben ser un número entero entre 1 y 100',
  priceUnreadable: 'Escribí el precio en dólares, por ejemplo 250 o 250.50',
  priceRange: 'El precio debe estar entre 0 y 10000000 centavos',
  validityRequired: 'Escribí los días de vigencia o marcá «Sin vencimiento»',
  validityRange: 'La vigencia debe ser un número entero de días entre 1 y 3650',
  noteTooLong: 'La nota no puede superar los 200 caracteres',
  adjustZero: 'El ajuste no puede ser cero',
  adjustUnreadable: 'Escribí el ajuste en sesiones, por ejemplo 1 o -1',
  adjustRange: 'El ajuste debe estar entre -100 y 100 sesiones',
  sellPackageRequired: 'Elegí el paquete que se vende',
  sellProfessionalRequired: 'Elegí quién lo vende',
  sellMethodRequired: 'Elegí el método de pago',
} as const

/** Sentence of the write the page refused to send twice. */
export const PACKAGES_BUSY_MESSAGE = 'Ya se está guardando una operación de paquetes: esperá a que termine.'

/** Nothing was sent: the browser reports being offline (D11-c). */
export const PACKAGES_OFFLINE_MESSAGE =
  'Sin conexión. Los paquetes solo se usan en línea: esta operación no se envió ni quedó en cola. Volvé a intentarlo cuando tengas conexión.'

/** The request failed on the way: the server may or may not have seen it. */
export const PACKAGES_NETWORK_MESSAGE =
  'No pudimos conectar con el servidor, así que no sabemos si la operación llegó. No quedó en cola ni se guardó para más tarde.'

/** One package of the catalog (`service_packages`). */
export type ServicePackage = {
  id: string
  name: string
  service_id: string
  /** Empty when the server did not send it; the pages look it up in the services. */
  service_name: string
  /** Sessions the package gives, 1..100. */
  sessions: number
  /** Integer cents, 0..`MAX_PACKAGE_PRICE_CENTS`. */
  price_cents: number
  /** Days of validity, or `null` for no expiry. */
  validity_days: number | null
  active: boolean
  created_at: string | null
}

/** One balance of a client (D8 `packages[]`). */
export type ClientPackage = {
  id: string
  name: string
  service_id: string
  service_name: string
  sessions_total: number
  /** The server is the only writer: 0..`sessions_total`. */
  sessions_left: number
  /** Snapshot of the catalog price at purchase time, integer cents. */
  price_cents: number
  purchased_at: string | null
  expires_at: string | null
  /** The SERVER decided it expired; the client never compares dates. */
  expired: boolean
}

export type PackageMovementReason = 'purchase' | 'redeem' | 'adjust'

/** One entry of the append-only ledger of a balance (D8 `movements[]`). */
export type PackageMovement = {
  id: string
  /** Signed sessions: `purchase` positive, `redeem` -1. */
  delta: number
  reason: PackageMovementReason
  note: string
  professional_id: string | null
  created_at: string | null
}

/** Body of `POST /packages`: `validity_days` travels only when it has a value. */
export type PackageCreatePayload = {
  name: string
  service_id: string
  sessions: number
  price_cents: number
  validity_days?: number
}

/** Body of `PUT /packages/:pid`: REPLACES, so the four keys always travel. */
export type PackageUpdatePayload = {
  name: string
  price_cents: number
  validity_days: number | null
  active: boolean
}

/** Body of `POST /clients/:cid/packages` (without the op id). */
export type SellPayload = {
  package_id: string
  professional_id: string
  method: SaleMethod
}

/** Body of `POST .../redeem`: both keys optional. */
export type RedeemPayload = {
  professional_id?: string
  note?: string
}

/** Body of `POST .../adjust`. */
export type PackageAdjustPayload = {
  delta: number
  note: string
}

/** What the catalog form holds: text as typed, numbers still unparsed. */
export type PackageDraft = {
  name: string
  service_id: string
  sessions: string
  price: string
  no_expiry: boolean
  validity_days: string
  active: boolean
}

export type PackageField = 'name' | 'service' | 'sessions' | 'price' | 'validity'

export type PackageDraftCheck =
  | { ok: true; mode: 'create'; payload: PackageCreatePayload }
  | { ok: true; mode: 'update'; payload: PackageUpdatePayload }
  | { ok: false; field: PackageField; message: string }

export type SellDraft = { package_id: string; professional_id: string; method: string }
export type SellField = 'package' | 'professional' | 'method'
export type SellCheck =
  | { ok: true; payload: SellPayload }
  | { ok: false; field: SellField; message: string }

export type RedeemDraft = { professional_id: string; note: string }
export type RedeemCheck =
  | { ok: true; payload: RedeemPayload }
  | { ok: false; field: 'note'; message: string }

export type PackageAdjustDraft = { delta: string; note: string }
export type PackageAdjustField = 'delta' | 'note'
export type PackageAdjustCheck =
  | { ok: true; payload: PackageAdjustPayload }
  | { ok: false; field: PackageAdjustField; message: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function optionalText(value: unknown): string | null {
  const trimmed = text(value)
  return trimmed === '' ? null : trimmed
}

function isInt(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value)
}

/** A NUL or a line break in a one-line text is a 400 on the server (D10); never sent. */
function hasForbiddenChar(value: string): boolean {
  return /[\u0000\r\n\u2028\u2029]/.test(value)
}

/** Whole number typed by a person (`+2`, `-1`, `5`); anything else is `null`. */
function parseWhole(input: string): number | null {
  const cleaned = typeof input === 'string' ? input.trim() : ''
  if (!/^[+-]?\d+$/.test(cleaned)) {
    return null
  }
  const value = Number(cleaned)
  return Number.isSafeInteger(value) ? value : null
}

/** Price typed by a person → integer cents (the integer parser of the expenses). */
export function parsePackagePriceToCents(input: string): number | null {
  return parseExpenseAmountToCents(input)
}

/** Value of the price field of an edit form (`25050` → `250.50`), integer arithmetic only. */
export function packagePriceInputFromCents(value: number): string {
  return expenseAmountInputFromCents(value)
}

// --- catalog form ---

/**
 * Reads the catalog form before sending it. `create` sends `validity_days` only
 * when the package expires; `update` sends the four keys of the PUT with
 * `validity_days: null` for no expiry, and never the service nor the sessions
 * (they are not editable).
 */
export function validatePackageDraft(
  draft: PackageDraft,
  mode: 'create' | 'update',
): PackageDraftCheck {
  const missing = mode === 'create' ? PACKAGE_MESSAGES.missingCreate : PACKAGE_MESSAGES.missingUpdate
  const name = draft.name.trim()
  const serviceId = draft.service_id.trim()
  const sessionsText = draft.sessions.trim()
  const priceText = draft.price.trim()
  const validityText = draft.validity_days.trim()

  if (name === '') {
    return { ok: false, field: 'name', message: missing }
  }
  if (hasForbiddenChar(name)) {
    return { ok: false, field: 'name', message: PACKAGE_MESSAGES.invalidText }
  }
  if (name.length > MAX_PACKAGE_NAME_LENGTH) {
    return { ok: false, field: 'name', message: PACKAGE_MESSAGES.nameTooLong }
  }

  let sessions = 0
  if (mode === 'create') {
    if (serviceId === '') {
      return { ok: false, field: 'service', message: missing }
    }
    if (sessionsText === '') {
      return { ok: false, field: 'sessions', message: missing }
    }
    const parsed = parseWhole(sessionsText)
    if (parsed === null || parsed < MIN_PACKAGE_SESSIONS || parsed > MAX_PACKAGE_SESSIONS) {
      return { ok: false, field: 'sessions', message: PACKAGE_MESSAGES.sessionsRange }
    }
    sessions = parsed
  }

  if (priceText === '') {
    return { ok: false, field: 'price', message: missing }
  }
  const price = parsePackagePriceToCents(priceText)
  if (price === null) {
    return { ok: false, field: 'price', message: PACKAGE_MESSAGES.priceUnreadable }
  }
  if (price > MAX_PACKAGE_PRICE_CENTS) {
    return { ok: false, field: 'price', message: PACKAGE_MESSAGES.priceRange }
  }

  let validity: number | null = null
  if (!draft.no_expiry) {
    if (validityText === '') {
      return {
        ok: false,
        field: 'validity',
        message: mode === 'create' ? PACKAGE_MESSAGES.validityRequired : PACKAGE_MESSAGES.missingUpdate,
      }
    }
    const days = parseWhole(validityText)
    if (days === null || days < MIN_VALIDITY_DAYS || days > MAX_VALIDITY_DAYS) {
      return { ok: false, field: 'validity', message: PACKAGE_MESSAGES.validityRange }
    }
    validity = days
  }

  if (mode === 'update') {
    return {
      ok: true,
      mode: 'update',
      payload: { name, price_cents: price, validity_days: validity, active: draft.active },
    }
  }
  const payload: PackageCreatePayload = {
    name,
    service_id: serviceId,
    sessions,
    price_cents: price,
  }
  if (validity !== null) {
    payload.validity_days = validity
  }
  return { ok: true, mode: 'create', payload }
}

/** Body of `POST /packages`: optional keys are absent, never `undefined`. */
export function packageCreateBody(payload: PackageCreatePayload): PackageCreatePayload {
  const body: PackageCreatePayload = {
    name: payload.name.trim(),
    service_id: payload.service_id,
    sessions: payload.sessions,
    price_cents: payload.price_cents,
  }
  if (payload.validity_days !== undefined && payload.validity_days !== null) {
    body.validity_days = payload.validity_days
  }
  return body
}

/** Body of `PUT /packages/:pid`: the four keys, `validity_days: null` explicit (D10). */
export function packageUpdateBody(payload: PackageUpdatePayload): PackageUpdatePayload {
  return {
    name: payload.name.trim(),
    price_cents: payload.price_cents,
    validity_days: payload.validity_days === undefined ? null : payload.validity_days,
    active: payload.active,
  }
}

// --- sell, redeem, adjust ---

/** Reads the sale form: package, professional and a method of the POS. */
export function validateSellDraft(draft: SellDraft): SellCheck {
  const packageId = draft.package_id.trim()
  const professionalId = draft.professional_id.trim()
  const method = draft.method.trim()
  if (packageId === '') {
    return { ok: false, field: 'package', message: PACKAGE_MESSAGES.sellPackageRequired }
  }
  if (professionalId === '') {
    return { ok: false, field: 'professional', message: PACKAGE_MESSAGES.sellProfessionalRequired }
  }
  if (!Object.prototype.hasOwnProperty.call(SALE_METHODS, method)) {
    return { ok: false, field: 'method', message: PACKAGE_MESSAGES.sellMethodRequired }
  }
  return {
    ok: true,
    payload: { package_id: packageId, professional_id: professionalId, method: method as SaleMethod },
  }
}

/** Body of the sale: the op id travels only when there is one. */
export function sellBody(
  payload: SellPayload,
  clientOpId?: string,
): SellPayload & { client_op_id?: string } {
  const body: SellPayload & { client_op_id?: string } = {
    package_id: payload.package_id,
    professional_id: payload.professional_id,
    method: payload.method,
  }
  const opId = clientOpId?.trim() ?? ''
  if (opId !== '') {
    body.client_op_id = opId
  }
  return body
}

/** Reads the redeem form: the professional and the note are both optional. */
export function validateRedeemDraft(draft: RedeemDraft): RedeemCheck {
  const professionalId = draft.professional_id.trim()
  const note = draft.note.trim()
  if (hasForbiddenChar(note)) {
    return { ok: false, field: 'note', message: PACKAGE_MESSAGES.invalidText }
  }
  if (note.length > MAX_PACKAGE_NOTE_LENGTH) {
    return { ok: false, field: 'note', message: PACKAGE_MESSAGES.noteTooLong }
  }
  const payload: RedeemPayload = {}
  if (professionalId !== '') {
    payload.professional_id = professionalId
  }
  if (note !== '') {
    payload.note = note
  }
  return { ok: true, payload }
}

/** Body of the redeem: only what has a value travels (D7: all three keys optional). */
export function redeemBody(
  payload: RedeemPayload,
  clientOpId?: string,
): RedeemPayload & { client_op_id?: string } {
  const body: RedeemPayload & { client_op_id?: string } = {}
  const professionalId = payload.professional_id?.trim() ?? ''
  if (professionalId !== '') {
    body.professional_id = professionalId
  }
  const note = payload.note?.trim() ?? ''
  if (note !== '') {
    body.note = note
  }
  const opId = clientOpId?.trim() ?? ''
  if (opId !== '') {
    body.client_op_id = opId
  }
  return body
}

/**
 * Reads the adjustment form (D9): `delta` a whole number other than 0 and a note
 * of 1..200 characters. The final range of the balance is the server's call (409).
 */
export function validatePackageAdjustDraft(draft: PackageAdjustDraft): PackageAdjustCheck {
  const note = draft.note.trim()
  const delta = parseWhole(draft.delta)
  if (delta === null) {
    return { ok: false, field: 'delta', message: PACKAGE_MESSAGES.adjustUnreadable }
  }
  if (delta === 0) {
    return { ok: false, field: 'delta', message: PACKAGE_MESSAGES.adjustZero }
  }
  if (Math.abs(delta) > MAX_PACKAGE_SESSIONS) {
    return { ok: false, field: 'delta', message: PACKAGE_MESSAGES.adjustRange }
  }
  if (note === '') {
    return { ok: false, field: 'note', message: PACKAGE_MESSAGES.adjustNoteRequired }
  }
  if (hasForbiddenChar(note)) {
    return { ok: false, field: 'note', message: PACKAGE_MESSAGES.invalidText }
  }
  if (note.length > MAX_PACKAGE_NOTE_LENGTH) {
    return { ok: false, field: 'note', message: PACKAGE_MESSAGES.noteTooLong }
  }
  return { ok: true, payload: { delta, note } }
}

// --- normalizers ---

/**
 * `GET /packages` row. A row without id or name, or whose price/sessions/validity
 * is not an integer in range, is dropped: a malformed price is never shown as $0.
 */
export function normalizePackage(raw: unknown): ServicePackage | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const name = text(raw['name'])
  const price = raw['price_cents']
  const sessions = raw['sessions']
  const validity = raw['validity_days']
  if (id === '' || name === '') {
    return null
  }
  if (!isInt(price) || price < 0) {
    return null
  }
  if (!isInt(sessions) || sessions < MIN_PACKAGE_SESSIONS) {
    return null
  }
  if (validity !== null && validity !== undefined && (!isInt(validity) || validity < MIN_VALIDITY_DAYS)) {
    return null
  }
  return {
    id,
    name,
    service_id: text(raw['service_id']),
    service_name: text(raw['service_name']),
    sessions,
    price_cents: price,
    validity_days: isInt(validity) ? validity : null,
    active: raw['active'] === true,
    created_at: optionalText(raw['created_at']),
  }
}

function rowsOf(raw: unknown, key: string): unknown[] {
  if (Array.isArray(raw)) {
    return raw
  }
  if (isRecord(raw) && Array.isArray(raw[key])) {
    return raw[key] as unknown[]
  }
  return []
}

/** Active packages first, then by name (Spanish collation); stable for equal names. */
export function sortPackages(list: readonly ServicePackage[]): ServicePackage[] {
  return list
    .map((pkg, index) => ({ pkg, index }))
    .sort((a, b) => {
      if (a.pkg.active !== b.pkg.active) {
        return a.pkg.active ? -1 : 1
      }
      const byName = a.pkg.name.localeCompare(b.pkg.name, 'es')
      return byName !== 0 ? byName : a.index - b.index
    })
    .map((entry) => entry.pkg)
}

/** `GET /packages`: reads `{packages:[…]}` (or a bare list) and sorts it. */
export function normalizePackages(raw: unknown): ServicePackage[] {
  const list: ServicePackage[] = []
  for (const row of rowsOf(raw, 'packages')) {
    const pkg = normalizePackage(row)
    if (pkg) {
      list.push(pkg)
    }
  }
  return sortPackages(list)
}

/** One balance of a client (D8). The counters must be consistent or the row is dropped. */
export function normalizeClientPackage(raw: unknown): ClientPackage | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const name = text(raw['name'])
  const total = raw['sessions_total']
  const left = raw['sessions_left']
  const price = raw['price_cents']
  if (id === '' || name === '') {
    return null
  }
  if (!isInt(total) || total < MIN_PACKAGE_SESSIONS) {
    return null
  }
  if (!isInt(left) || left < 0 || left > total) {
    return null
  }
  if (!isInt(price) || price < 0) {
    return null
  }
  return {
    id,
    name,
    service_id: text(raw['service_id']),
    service_name: text(raw['service_name']),
    sessions_total: total,
    sessions_left: left,
    price_cents: price,
    purchased_at: optionalText(raw['purchased_at']),
    expires_at: optionalText(raw['expires_at']),
    expired: raw['expired'] === true,
  }
}

/** Whether a session can be taken off it according to what the server reported. */
export function canRedeemPackage(pkg: Pick<ClientPackage, 'expired' | 'sessions_left'>): boolean {
  return !pkg.expired && pkg.sessions_left > 0
}

/**
 * Usable balances first (stable): the server already promises «vigentes con saldo
 * primero», this only guarantees it on a malformed answer without reordering the
 * rows inside each group.
 */
export function sortClientPackages(list: readonly ClientPackage[]): ClientPackage[] {
  const usable = list.filter((pkg) => canRedeemPackage(pkg))
  const rest = list.filter((pkg) => !canRedeemPackage(pkg))
  return [...usable, ...rest]
}

/** `GET /clients/:cid/packages`. */
export function normalizeClientPackages(raw: unknown): ClientPackage[] {
  const list: ClientPackage[] = []
  for (const row of rowsOf(raw, 'packages')) {
    const pkg = normalizeClientPackage(row)
    if (pkg) {
      list.push(pkg)
    }
  }
  return sortClientPackages(list)
}

const MOVEMENT_REASON_LABELS: Record<PackageMovementReason, string> = {
  purchase: 'Compra',
  redeem: 'Sesión descontada',
  adjust: 'Ajuste',
}

export function movementReasonLabel(reason: PackageMovementReason): string {
  return MOVEMENT_REASON_LABELS[reason]
}

/** `GET .../movements`: newest first, as the server sends them. */
export function normalizePackageMovements(raw: unknown): PackageMovement[] {
  const list: PackageMovement[] = []
  for (const row of rowsOf(raw, 'movements')) {
    if (!isRecord(row)) {
      continue
    }
    const id = text(row['id'])
    const delta = row['delta']
    const reason = text(row['reason'])
    if (id === '' || !isInt(delta) || delta === 0) {
      continue
    }
    if (!Object.prototype.hasOwnProperty.call(MOVEMENT_REASON_LABELS, reason)) {
      continue
    }
    list.push({
      id,
      delta,
      reason: reason as PackageMovementReason,
      note: text(row['note']),
      professional_id: optionalText(row['professional_id']),
      created_at: optionalText(row['created_at']),
    })
  }
  return list
}

// --- presentation ---

/** «3 de 5 sesiones» (singular for a package of one). */
export function sessionsLabel(pkg: Pick<ClientPackage, 'sessions_left' | 'sessions_total'>): string {
  return `${pkg.sessions_left} de ${pkg.sessions_total} ${pkg.sessions_total === 1 ? 'sesión' : 'sesiones'}`
}

export type PackageStatus = { label: 'Vigente' | 'Vencido' | 'Sin sesiones'; on: boolean }

/** `Vencido` wins over `Sin sesiones`; both are «off». */
export function clientPackageStatus(
  pkg: Pick<ClientPackage, 'expired' | 'sessions_left'>,
): PackageStatus {
  if (pkg.expired) {
    return { label: 'Vencido', on: false }
  }
  if (pkg.sessions_left === 0) {
    return { label: 'Sin sesiones', on: false }
  }
  return { label: 'Vigente', on: true }
}

function usableZone(timeZone: string): string {
  const candidate = typeof timeZone === 'string' ? timeZone.trim() : ''
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

/** «Vence el 2027-02-01»: the LOCAL day of the salon (the end of that day, D5). */
export function expiryText(
  pkg: Pick<ClientPackage, 'expires_at' | 'expired'>,
  timeZone: string,
): string {
  if (pkg.expires_at === null || Number.isNaN(Date.parse(pkg.expires_at))) {
    return 'Sin vencimiento'
  }
  const day = toDateInputValue(new Date(pkg.expires_at), usableZone(timeZone))
  return `${pkg.expired ? 'Venció' : 'Vence'} el ${day}`
}

/** «90 días», «1 día» or «Sin vencimiento». */
export function packageValidityLabel(days: number | null): string {
  if (days === null) {
    return 'Sin vencimiento'
  }
  return `${days} ${days === 1 ? 'día' : 'días'}`
}

/** «5 sesiones» / «1 sesión» of a catalog row. */
export function packageSessionsLabel(sessions: number): string {
  return `${sessions} ${sessions === 1 ? 'sesión' : 'sesiones'}`
}

// --- errors ---

export type PackageFailure = { kind: 'server' | 'network'; message: string }

/**
 * Sentence of a failed request. The server's own text is printed verbatim (400,
 * 403, 404, 409); a connectivity failure is its own kind and says nothing was
 * queued (D11-c).
 */
export function describePackageError(error: unknown, fallback: string): PackageFailure {
  if (error instanceof ApiError) {
    return { kind: 'server', message: error.message }
  }
  if (error instanceof NetworkError) {
    return { kind: 'network', message: PACKAGES_NETWORK_MESSAGE }
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return { kind: 'server', message: error.message }
  }
  return { kind: 'server', message: fallback }
}

// --- one operation, one client_op_id ---

export type OpAttempt = {
  /**
   * The `client_op_id` of the operation described by `fingerprint`: the same one
   * while the person retries THAT operation, a new one when it is another.
   */
  idFor: (fingerprint: string) => string
  /** The id in use, or `null` when no attempt is open. */
  current: () => string | null
  /** The operation ended (saved, rejected or cancelled): the next one gets a fresh id. */
  settle: () => void
}

/**
 * Holds the `client_op_id` of the operation in progress. A retry after a network
 * error is the SAME attempt, so it reuses the id and the server answers with the
 * original result instead of doing it twice (D5/D7); once the attempt is settled
 * the next press is a new operation with a new id (like the POS and the booking
 * attempt of the landing).
 */
export function createOpAttempt(newId: () => string = newClientOpId): OpAttempt {
  let fingerprint: string | null = null
  let id: string | null = null
  return {
    idFor: (next) => {
      if (id === null || fingerprint !== next) {
        fingerprint = next
        id = newId()
      }
      return id
    },
    current: () => id,
    settle: () => {
      fingerprint = null
      id = null
    },
  }
}

/** Identity of a sale for `createOpAttempt`: client, package, professional and method. */
export function sellFingerprint(clientId: string, payload: SellPayload): string {
  return JSON.stringify(['sell', clientId, payload.package_id, payload.professional_id, payload.method])
}

/** Identity of a redeem for `createOpAttempt`: client, balance and what was typed. */
export function redeemFingerprint(
  clientId: string,
  clientPackageId: string,
  payload: RedeemPayload,
): string {
  return JSON.stringify([
    'redeem',
    clientId,
    clientPackageId,
    payload.professional_id?.trim() ?? '',
    payload.note?.trim() ?? '',
  ])
}

// --- API client (frozen shapes of the F8 contract) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function clientPackagesPath(salonId: string, clientId: string, suffix = ''): string {
  return salonPath(salonId, `/clients/${encodeURIComponent(clientId)}/packages${suffix}`)
}

function balancePath(salonId: string, clientId: string, clientPackageId: string, suffix = ''): string {
  return clientPackagesPath(salonId, clientId, `/${encodeURIComponent(clientPackageId)}${suffix}`)
}

/** A package from a write answer: bare row or `{package}`; `null` when it has neither. */
function packageFromAnswer(raw: unknown): ServicePackage | null {
  if (isRecord(raw) && 'package' in raw) {
    return normalizePackage(raw['package'])
  }
  return normalizePackage(raw)
}

/** A balance from a write answer: `{client_package}` or a bare row. */
function balanceFromAnswer(raw: unknown): ClientPackage | null {
  if (isRecord(raw) && 'client_package' in raw) {
    return normalizeClientPackage(raw['client_package'])
  }
  return normalizeClientPackage(raw)
}

/** `GET /packages`: the catalog of the salon, active and inactive. */
export async function fetchPackages(salonId: string): Promise<ServicePackage[]> {
  return normalizePackages(await request<unknown>(salonPath(salonId, '/packages')))
}

/** `POST /packages` (201). A 400/409 keeps the Spanish sentence verbatim. */
export async function createPackage(
  salonId: string,
  payload: PackageCreatePayload,
): Promise<ServicePackage | null> {
  return packageFromAnswer(
    await request<unknown>(salonPath(salonId, '/packages'), {
      method: 'POST',
      body: packageCreateBody(payload),
    }),
  )
}

/** `PUT /packages/:pid`: replaces the four keys. */
export async function updatePackage(
  salonId: string,
  packageId: string,
  payload: PackageUpdatePayload,
): Promise<ServicePackage | null> {
  return packageFromAnswer(
    await request<unknown>(salonPath(salonId, `/packages/${encodeURIComponent(packageId)}`), {
      method: 'PUT',
      body: packageUpdateBody(payload),
    }),
  )
}

/** `GET /clients/:cid/packages`: the balances of a client. */
export async function fetchClientPackages(
  salonId: string,
  clientId: string,
  signal?: AbortSignal,
): Promise<ClientPackage[]> {
  return normalizeClientPackages(
    await request<unknown>(clientPackagesPath(salonId, clientId), signal ? { signal } : {}),
  )
}

/** `GET .../packages/:cpid/movements`: the ledger of a balance, newest first. */
export async function fetchPackageMovements(
  salonId: string,
  clientId: string,
  clientPackageId: string,
  signal?: AbortSignal,
): Promise<PackageMovement[]> {
  return normalizePackageMovements(
    await request<unknown>(
      balancePath(salonId, clientId, clientPackageId, '/movements'),
      signal ? { signal } : {},
    ),
  )
}

export type SellResult = { sale_id: string | null; client_package: ClientPackage | null }

/** `POST /clients/:cid/packages` (201): the sale and the balance it created. */
export async function sellPackage(
  salonId: string,
  clientId: string,
  payload: SellPayload,
  clientOpId?: string,
): Promise<SellResult> {
  const raw = await request<unknown>(clientPackagesPath(salonId, clientId), {
    method: 'POST',
    body: sellBody(payload, clientOpId),
  })
  return {
    sale_id: isRecord(raw) ? optionalText(raw['sale_id']) : null,
    client_package: balanceFromAnswer(raw),
  }
}

/** `POST .../redeem` (200): takes ONE session off the balance. */
export async function redeemPackageSession(
  salonId: string,
  clientId: string,
  clientPackageId: string,
  payload: RedeemPayload,
  clientOpId?: string,
): Promise<ClientPackage | null> {
  return balanceFromAnswer(
    await request<unknown>(balancePath(salonId, clientId, clientPackageId, '/redeem'), {
      method: 'POST',
      body: redeemBody(payload, clientOpId),
    }),
  )
}

/** `POST .../adjust`: owner and admin correct a balance with a mandatory note. */
export async function adjustPackageBalance(
  salonId: string,
  clientId: string,
  clientPackageId: string,
  payload: PackageAdjustPayload,
): Promise<ClientPackage | null> {
  return balanceFromAnswer(
    await request<unknown>(balancePath(salonId, clientId, clientPackageId, '/adjust'), {
      method: 'POST',
      body: { delta: payload.delta, note: payload.note.trim() },
    }),
  )
}

// --- one write at a time, online only ---

export type PackagesGate = {
  busy: () => boolean
  start: () => boolean
  finish: () => boolean
}

/**
 * Synchronous gate of the screens: two presses of the same render can both read
 * `saving === false`, so the guard lives here and the state only paints the button.
 */
export function createPackagesGate(): PackagesGate {
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

/** What a screen asks the API to do. */
export type PackageWrite =
  | { kind: 'create'; payload: PackageCreatePayload }
  | { kind: 'update'; packageId: string; payload: PackageUpdatePayload }
  | { kind: 'sell'; clientId: string; payload: SellPayload; clientOpId: string }
  | {
      kind: 'redeem'
      clientId: string
      clientPackageId: string
      payload: RedeemPayload
      clientOpId: string
    }
  | { kind: 'adjust'; clientId: string; clientPackageId: string; payload: PackageAdjustPayload }

export type PackageWriteOutcome =
  | { status: 'saved'; sale_id: string | null }
  | { status: 'busy'; message: string }
  /** Nothing was sent: the browser is offline. */
  | { status: 'offline'; message: string }
  /** Sent, but the answer never arrived: the result is unknown. */
  | { status: 'network'; message: string }
  /**
   * The server answered an error. `definitive` is true for a 4xx (it stored nothing,
   * the attempt can close) and false for a 5xx or anything else (the write may have
   * committed: the attempt stays open and a retry reuses its `client_op_id`).
   */
  | { status: 'failed'; message: string; definitive: boolean }

/** Create, update and adjust carry no op id: a blind retry could repeat them. */
function carriesOpId(write: PackageWrite): boolean {
  return write.kind === 'sell' || write.kind === 'redeem'
}

/**
 * One press of a write button: refuses a second one while the first runs, sends
 * NOTHING when the browser is offline, and never enqueues anything (D11-c). The
 * row it returns is not painted: the screen reads the server again.
 */
export async function writePackage(
  gate: PackagesGate,
  salonId: string,
  write: PackageWrite,
  isOnline: () => boolean = isBrowserOnline,
): Promise<PackageWriteOutcome> {
  if (!gate.start()) {
    return { status: 'busy', message: PACKAGES_BUSY_MESSAGE }
  }
  try {
    if (!isOnline()) {
      return { status: 'offline', message: PACKAGES_OFFLINE_MESSAGE }
    }
    let saleId: string | null = null
    switch (write.kind) {
      case 'create':
        await createPackage(salonId, write.payload)
        break
      case 'update':
        await updatePackage(salonId, write.packageId, write.payload)
        break
      case 'sell':
        saleId = (await sellPackage(salonId, write.clientId, write.payload, write.clientOpId)).sale_id
        break
      case 'redeem':
        await redeemPackageSession(
          salonId,
          write.clientId,
          write.clientPackageId,
          write.payload,
          write.clientOpId,
        )
        break
      case 'adjust':
        await adjustPackageBalance(salonId, write.clientId, write.clientPackageId, write.payload)
        break
    }
    return { status: 'saved', sale_id: saleId }
  } catch (error: unknown) {
    const failure = describePackageError(error, 'No pudimos completar la operación.')
    if (failure.kind === 'network') {
      const hint = carriesOpId(write)
        ? ' Podés reintentar: la operación lleva una clave y no se repite.'
        : ' Antes de reintentar, revisá la lista para ver si ya se registró.'
      return { status: 'network', message: `${PACKAGES_NETWORK_MESSAGE}${hint}` }
    }
    return {
      status: 'failed',
      message: failure.message,
      definitive: error instanceof ApiError && error.status < 500,
    }
  } finally {
    gate.finish()
  }
}
