/**
 * Loyalty vouchers: «cada N visitas, un servicio de regalo» (contract F9, «Contrato
 * F9 (fidelidad…)», D4-D9). Pure rules plus the thin API client: `/panel/fidelidad`
 * and the «Fidelidad» section of the client file render what these return, and the
 * server stays the authority on visits, vouchers, expiry and permissions.
 *
 * The visits are CALCULATED by the server (D2), never counted here: the screens only
 * paint what `GET .../loyalty` says and ask again after every action (no optimistic
 * update). A voucher is not a sale and moves no money, so there is no money in this
 * module.
 *
 * Online only (D9-d): nothing here touches the offline queue. Offline the write is not
 * even sent, and a network error is reported as such; the queue would discard the 4xx
 * of a voucher the person was told had been delivered.
 */

import { ApiError, NetworkError, request } from '../../../lib/api.ts'
import { isBrowserOnline } from '../../../lib/connectivity.ts'
import { toDateInputValue, TIME_ZONE } from '../../../lib/datetime.ts'
import { createOpAttempt, createPackagesGate, type OpAttempt, type PackagesGate } from '../../packages/lib/packages.ts'
import { loyaltySentence } from '../../../lib/publicSalon.ts'

export { createOpAttempt }
export type { OpAttempt }

/** Visits of a program (D4: 2..50). */
export const MIN_LOYALTY_VISITS = 2
export const MAX_LOYALTY_VISITS = 50

/** Validity of a voucher in days (D4: 1..3650); no expiry is `null`. */
export const MIN_REWARD_VALIDITY_DAYS = 1
export const MAX_REWARD_VALIDITY_DAYS = 3650

/** Characters of the note of a redeem (D3 `redeem_note <= 200`). */
export const MAX_REDEEM_NOTE_LENGTH = 200

/** Help text of the program form (D9-a), verbatim. */
export const LOYALTY_HELP = 'Esto es lo que ve el cliente en tu página pública'

/**
 * Sentences of the API, printed verbatim (contract F9), plus the ones the client says
 * before sending a request that would be rejected for the same reason.
 */
export const LOYALTY_MESSAGES = {
  missingProgram: 'Faltan datos del programa: activado, visitas, servicio y vigencia',
  visitsRange: 'Las visitas necesarias deben ser entre 2 y 50',
  invalidService: 'Servicio inválido',
  validityRange: 'La vigencia debe ser entre 1 y 3650 días',
  noRewards: 'No hay premios disponibles',
  notEnabled: 'La fidelidad no está activada',
  rewardNotFound: 'Premio no encontrado',
  alreadyRedeemed: 'Ese premio ya se usó',
  rewardExpired: 'El premio venció',
  // Client-side copies: the server would answer 400 for the same reason.
  invalidText: 'El texto no puede tener saltos de línea ni caracteres no válidos',
  noteTooLong: 'La nota no puede superar los 200 caracteres',
} as const

/** Sentence of the write the page refused to send twice. */
export const LOYALTY_BUSY_MESSAGE =
  'Ya se está guardando una operación de fidelidad: esperá a que termine.'

/** Nothing was sent: the browser reports being offline (D9-d). */
export const LOYALTY_OFFLINE_MESSAGE =
  'Sin conexión. La fidelidad solo se usa en línea: esta operación no se envió ni quedó en cola. Volvé a intentarlo cuando tengas conexión.'

/** The request failed on the way: the server may or may not have seen it. */
export const LOYALTY_NETWORK_MESSAGE =
  'No pudimos conectar con el servidor, así que no sabemos si la operación llegó. No quedó en cola ni se guardó para más tarde.'

// --- types ---

/** `GET /loyalty-program` (D4). A never-saved program is `enabled:false` and the rest `null`. */
export type LoyaltyProgram = {
  enabled: boolean
  visits_required: number | null
  reward_service_id: string | null
  reward_service_name: string | null
  /** Days of validity of a voucher, or `null` for no expiry. */
  reward_validity_days: number | null
  counting_from: string | null
}

/** Body of `PUT /loyalty-program`: REPLACES, so the four keys always travel. */
export type LoyaltyProgramPayload = {
  enabled: boolean
  visits_required: number
  reward_service_id: string
  /** `null` = sin vencimiento, explicit (D4). */
  reward_validity_days: number | null
}

/** What the program form holds: text as typed, numbers still unparsed. */
export type LoyaltyDraft = {
  enabled: boolean
  visits_required: string
  reward_service_id: string
  no_expiry: boolean
  validity_days: string
}

export type LoyaltyField = 'visits' | 'service' | 'validity'

export type LoyaltyDraftCheck =
  | { ok: true; payload: LoyaltyProgramPayload }
  | { ok: false; field: LoyaltyField; message: string }

export type RewardState = 'available' | 'redeemed' | 'expired'

/** One voucher (D5 `rewards[]`, D6/D7 `reward`). */
export type LoyaltyReward = {
  id: string
  reward_number: number
  service_id: string
  /** Snapshot of the name when it was granted. */
  service_name: string
  granted_at: string | null
  expires_at: string | null
  redeemed_at: string | null
  /** The SERVER decided it; the client never compares dates. */
  state: RewardState
}

/** `GET /clients/:cid/loyalty` (D5). */
export type ClientLoyalty = {
  enabled: boolean
  visits: number
  visits_required: number | null
  rewards_earned: number
  rewards_granted: number
  rewards_available: number
  next_in: number | null
  rewards: LoyaltyReward[]
}

/** Body of `POST .../redeem`: both keys optional (the op id is added by `redeemRewardBody`). */
export type RedeemRewardPayload = {
  professional_id?: string
  note?: string
}

export type RedeemRewardDraft = { professional_id: string; note: string }
export type RedeemRewardCheck =
  | { ok: true; payload: RedeemRewardPayload }
  | { ok: false; field: 'note'; message: string }

/** A service of the picker (only the fields the form needs). */
export type LoyaltyServiceSource = { id: string; name: string; active: boolean }
export type LoyaltyServiceOption = { id: string; label: string; inactive: boolean }

// --- helpers ---

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

function intOrNull(value: unknown): number | null {
  return isInt(value) ? value : null
}

/** A NUL or a line break in a one-line text is a 400 on the server; never sent. */
function hasForbiddenChar(value: string): boolean {
  return /[\u0000\r\n\u2028\u2029]/.test(value)
}

/** Whole number typed by a person (`5`, `+5`); anything else is `null`. */
function parseWhole(input: string): number | null {
  const cleaned = typeof input === 'string' ? input.trim() : ''
  if (!/^[+-]?\d+$/.test(cleaned)) {
    return null
  }
  const value = Number(cleaned)
  return Number.isSafeInteger(value) ? value : null
}

// --- program: normalizer, draft, validation ---

/** `GET /loyalty-program`: `null` when the answer is not an object at all. */
export function normalizeLoyaltyProgram(raw: unknown): LoyaltyProgram | null {
  if (!isRecord(raw)) {
    return null
  }
  const visits = intOrNull(raw['visits_required'])
  const validity = intOrNull(raw['reward_validity_days'])
  return {
    enabled: raw['enabled'] === true,
    visits_required: visits !== null && visits >= 1 ? visits : null,
    reward_service_id: optionalText(raw['reward_service_id']),
    reward_service_name: optionalText(raw['reward_service_name']),
    reward_validity_days: validity !== null && validity >= MIN_REWARD_VALIDITY_DAYS ? validity : null,
    counting_from: optionalText(raw['counting_from']),
  }
}

/** The form of a saved program, or the empty one when it was never saved. */
export function draftFromProgram(program: LoyaltyProgram | null): LoyaltyDraft {
  if (program === null) {
    return { enabled: false, visits_required: '', reward_service_id: '', no_expiry: false, validity_days: '' }
  }
  const saved = program.visits_required !== null || program.reward_service_id !== null
  return {
    enabled: program.enabled,
    visits_required: program.visits_required === null ? '' : String(program.visits_required),
    reward_service_id: program.reward_service_id ?? '',
    // A saved program without validity is «sin vencimiento»; a never-saved one is still blank.
    no_expiry: saved && program.reward_validity_days === null,
    validity_days: program.reward_validity_days === null ? '' : String(program.reward_validity_days),
  }
}

/**
 * Reads the program form before sending it. The server asks for the four keys even
 * when the program is switched off, so visits and service are required either way;
 * `reward_validity_days` is `null` (explicit) for «Sin vencimiento».
 */
export function validateLoyaltyDraft(draft: LoyaltyDraft): LoyaltyDraftCheck {
  const missing = LOYALTY_MESSAGES.missingProgram
  const visitsText = draft.visits_required.trim()
  const serviceId = draft.reward_service_id.trim()
  const validityText = draft.validity_days.trim()

  if (visitsText === '') {
    return { ok: false, field: 'visits', message: missing }
  }
  const visits = parseWhole(visitsText)
  if (visits === null || visits < MIN_LOYALTY_VISITS || visits > MAX_LOYALTY_VISITS) {
    return { ok: false, field: 'visits', message: LOYALTY_MESSAGES.visitsRange }
  }
  if (serviceId === '') {
    return { ok: false, field: 'service', message: missing }
  }
  let validity: number | null = null
  if (!draft.no_expiry) {
    if (validityText === '') {
      return { ok: false, field: 'validity', message: missing }
    }
    const days = parseWhole(validityText)
    if (days === null || days < MIN_REWARD_VALIDITY_DAYS || days > MAX_REWARD_VALIDITY_DAYS) {
      return { ok: false, field: 'validity', message: LOYALTY_MESSAGES.validityRange }
    }
    validity = days
  }
  return {
    ok: true,
    payload: {
      enabled: draft.enabled,
      visits_required: visits,
      reward_service_id: serviceId,
      reward_validity_days: validity,
    },
  }
}

/** Body of `PUT /loyalty-program`: the four keys, `null` explicit, never `undefined`. */
export function programBody(payload: LoyaltyProgramPayload): LoyaltyProgramPayload {
  return {
    enabled: payload.enabled,
    visits_required: payload.visits_required,
    reward_service_id: payload.reward_service_id,
    reward_validity_days: payload.reward_validity_days === undefined ? null : payload.reward_validity_days,
  }
}

/**
 * Options of the reward picker: the active services by name and, so the form never
 * hides what is saved, the current reward service when it is inactive (marked). The
 * server still answers «Servicio inválido» if that one is saved again.
 */
export function loyaltyServiceOptions(
  services: readonly LoyaltyServiceSource[],
  current: { id: string; name: string } | null,
): LoyaltyServiceOption[] {
  const options: LoyaltyServiceOption[] = services
    .filter((service) => service.active)
    .map((service) => ({ id: service.id, label: service.name, inactive: false }))
    .sort((a, b) => a.label.localeCompare(b.label, 'es'))
  if (current !== null && current.id !== '' && !options.some((option) => option.id === current.id)) {
    const known = services.find((service) => service.id === current.id)
    const name = known?.name ?? current.name
    options.push({ id: current.id, label: `${name} (inactivo)`, inactive: true })
  }
  return options
}

/**
 * The public sentence of the draft (what the landing will print), or `null` while
 * the visits or the service are not usable. It does not look at `enabled`: the page
 * says separately that a switched-off program is not shown.
 */
export function previewSentence(
  draft: LoyaltyDraft,
  options: readonly LoyaltyServiceOption[],
): string | null {
  const visits = parseWhole(draft.visits_required)
  if (visits === null || visits < MIN_LOYALTY_VISITS || visits > MAX_LOYALTY_VISITS) {
    return null
  }
  const option = options.find((candidate) => candidate.id === draft.reward_service_id)
  if (!option) {
    return null
  }
  const name = option.inactive ? option.label.replace(/ \(inactivo\)$/, '') : option.label
  return loyaltySentence(visits, name)
}

// --- client: normalizers ---

const REWARD_STATES: readonly RewardState[] = ['available', 'redeemed', 'expired']

/** One voucher; a row without id, number or a known state is dropped. */
export function normalizeLoyaltyReward(raw: unknown): LoyaltyReward | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const number = raw['reward_number']
  const state = text(raw['state'])
  if (id === '' || !isInt(number) || number < 1) {
    return null
  }
  if (!(REWARD_STATES as readonly string[]).includes(state)) {
    return null
  }
  return {
    id,
    reward_number: number,
    service_id: text(raw['service_id']),
    service_name: text(raw['service_name']),
    granted_at: optionalText(raw['granted_at']),
    expires_at: optionalText(raw['expires_at']),
    redeemed_at: optionalText(raw['redeemed_at']),
    state: state as RewardState,
  }
}

/**
 * `GET /clients/:cid/loyalty` (D5). The counters must be non-negative integers or the
 * whole answer is `null`: a malformed counter is never shown as «0 visitas».
 */
export function normalizeClientLoyalty(raw: unknown): ClientLoyalty | null {
  if (!isRecord(raw)) {
    return null
  }
  const visits = raw['visits']
  const earned = raw['rewards_earned']
  const granted = raw['rewards_granted']
  const available = raw['rewards_available']
  for (const value of [visits, earned, granted, available]) {
    if (!isInt(value) || value < 0) {
      return null
    }
  }
  const required = intOrNull(raw['visits_required'])
  const nextIn = intOrNull(raw['next_in'])
  const rewards: LoyaltyReward[] = []
  for (const row of Array.isArray(raw['rewards']) ? raw['rewards'] : []) {
    const reward = normalizeLoyaltyReward(row)
    if (reward) {
      rewards.push(reward)
    }
  }
  return {
    enabled: raw['enabled'] === true,
    visits: visits as number,
    visits_required: required !== null && required >= 1 ? required : null,
    rewards_earned: earned as number,
    rewards_granted: granted as number,
    rewards_available: available as number,
    next_in: nextIn !== null && nextIn >= 0 ? nextIn : null,
    rewards,
  }
}

/** A voucher from a write answer: `{reward}` or a bare row. */
function rewardFromAnswer(raw: unknown): LoyaltyReward | null {
  if (isRecord(raw) && 'reward' in raw) {
    return normalizeLoyaltyReward(raw['reward'])
  }
  return normalizeLoyaltyReward(raw)
}

// --- presentation ---

export type LoyaltyProgress = {
  /** Visits of the cycle in course: `visits mod N` (the earned vouchers are counted apart). */
  inCycle: number
  required: number
  /** Integer 0..99 (a full cycle starts again at 0). */
  percent: number
  label: string
}

/** «2 de 5 visitas» and its bar; `null` without a program to measure against. */
export function loyaltyProgress(status: ClientLoyalty): LoyaltyProgress | null {
  const required = status.visits_required
  if (!status.enabled || required === null || required < 1) {
    return null
  }
  const inCycle = status.visits % required
  return {
    inCycle,
    required,
    percent: Math.floor((inCycle * 100) / required),
    label: `${inCycle} de ${required} visitas`,
  }
}

/** «Sin premios disponibles» / «1 premio disponible» / «3 premios disponibles». */
export function rewardsAvailableLabel(count: number): string {
  if (count <= 0) {
    return 'Sin premios disponibles'
  }
  return count === 1 ? '1 premio disponible' : `${count} premios disponibles`
}

/** «Faltan 4 visitas para el próximo premio»; `null` when the server sent none. */
export function nextInLabel(nextIn: number | null): string | null {
  if (nextIn === null || nextIn < 1) {
    return null
  }
  return nextIn === 1
    ? 'Falta 1 visita para el próximo premio'
    : `Faltan ${nextIn} visitas para el próximo premio`
}

const STATE_LABELS: Record<RewardState, { label: string; on: boolean }> = {
  available: { label: 'Disponible', on: true },
  redeemed: { label: 'Usado', on: false },
  expired: { label: 'Vencido', on: false },
}

export function rewardStateLabel(state: RewardState): { label: string; on: boolean } {
  return STATE_LABELS[state]
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

function localDay(iso: string | null, timeZone: string): string | null {
  if (iso === null || Number.isNaN(Date.parse(iso))) {
    return null
  }
  return toDateInputValue(new Date(iso), usableZone(timeZone))
}

/**
 * «Vence el 2027-02-01» / «Venció el …» / «Usado el …» / «Sin vencimiento», with the
 * LOCAL day of the salon (the expiry is the end of that day, D6: in UTC it falls on
 * the next day).
 */
export function rewardDateText(
  reward: Pick<LoyaltyReward, 'state' | 'expires_at' | 'redeemed_at'>,
  timeZone: string,
): string {
  if (reward.state === 'redeemed') {
    const used = localDay(reward.redeemed_at, timeZone)
    return used === null ? 'Usado' : `Usado el ${used}`
  }
  const expires = localDay(reward.expires_at, timeZone)
  if (expires === null) {
    return 'Sin vencimiento'
  }
  return `${reward.state === 'expired' ? 'Venció' : 'Vence'} el ${expires}`
}

/** `2026-11-03`: the LOCAL day the voucher was granted; a dash when the server sent none. */
export function rewardGrantedDay(reward: Pick<LoyaltyReward, 'granted_at'>, timeZone: string): string {
  return localDay(reward.granted_at, timeZone) ?? '—'
}

/** Whether a voucher can be delivered now according to what the server reported. */
export function canGrantReward(status: Pick<ClientLoyalty, 'enabled' | 'rewards_available'>): boolean {
  return status.enabled && status.rewards_available >= 1
}

/** Whether a voucher can be used now: the program is on and the server says «available». */
export function canUseReward(
  status: Pick<ClientLoyalty, 'enabled'>,
  reward: Pick<LoyaltyReward, 'state'>,
): boolean {
  return status.enabled && reward.state === 'available'
}

// --- redeem form and bodies ---

/** Reads the redeem form: the professional and the note are both optional. */
export function validateRedeemDraft(draft: RedeemRewardDraft): RedeemRewardCheck {
  const professionalId = draft.professional_id.trim()
  const note = draft.note.trim()
  if (hasForbiddenChar(note)) {
    return { ok: false, field: 'note', message: LOYALTY_MESSAGES.invalidText }
  }
  if (Array.from(note).length > MAX_REDEEM_NOTE_LENGTH) {
    return { ok: false, field: 'note', message: LOYALTY_MESSAGES.noteTooLong }
  }
  const payload: RedeemRewardPayload = {}
  if (professionalId !== '') {
    payload.professional_id = professionalId
  }
  if (note !== '') {
    payload.note = note
  }
  return { ok: true, payload }
}

/** Body of the grant: the op id travels only when there is one. */
export function grantBody(clientOpId?: string): { client_op_id?: string } {
  const opId = clientOpId?.trim() ?? ''
  return opId === '' ? {} : { client_op_id: opId }
}

/** Body of the redeem: only what has a value travels (D7: all three keys optional). */
export function redeemRewardBody(
  payload: RedeemRewardPayload,
  clientOpId?: string,
): RedeemRewardPayload & { client_op_id?: string } {
  const body: RedeemRewardPayload & { client_op_id?: string } = {}
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

// --- errors ---

export type LoyaltyFailure = { kind: 'server' | 'network'; message: string }

/**
 * Sentence of a failed request. The server's own text is printed verbatim (400, 403,
 * 404, 409); a connectivity failure is its own kind and says nothing was queued.
 */
export function describeLoyaltyError(error: unknown, fallback: string): LoyaltyFailure {
  if (error instanceof ApiError) {
    return { kind: 'server', message: error.message }
  }
  if (error instanceof NetworkError) {
    return { kind: 'network', message: LOYALTY_NETWORK_MESSAGE }
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return { kind: 'server', message: error.message }
  }
  return { kind: 'server', message: fallback }
}

// --- one operation, one client_op_id ---

/**
 * Identity of a grant for `createOpAttempt`: the client AND how many vouchers were
 * already granted when the person pressed. A retry of the same press reuses the id
 * (the server answers with the original voucher); once the screen reloaded and the
 * count moved, the next press is another operation with another id, so a second
 * voucher is never swallowed by the idempotency of the first.
 */
export function grantFingerprint(clientId: string, rewardsGranted: number): string {
  return JSON.stringify(['grant', clientId, rewardsGranted])
}

/** Identity of a redeem for `createOpAttempt`: client, voucher and what was typed. */
export function redeemFingerprint(
  clientId: string,
  rewardId: string,
  payload: RedeemRewardPayload,
): string {
  return JSON.stringify([
    'redeem',
    clientId,
    rewardId,
    payload.professional_id?.trim() ?? '',
    payload.note?.trim() ?? '',
  ])
}

// --- API client (frozen shapes of the F9 contract) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function clientLoyaltyPath(salonId: string, clientId: string, suffix = ''): string {
  return salonPath(salonId, `/clients/${encodeURIComponent(clientId)}/loyalty${suffix}`)
}

/** `GET /loyalty-program` (any active member). */
export async function fetchLoyaltyProgram(
  salonId: string,
  signal?: AbortSignal,
): Promise<LoyaltyProgram | null> {
  return normalizeLoyaltyProgram(
    await request<unknown>(salonPath(salonId, '/loyalty-program'), signal ? { signal } : {}),
  )
}

/** `PUT /loyalty-program` (owner/admin): replaces the four keys. */
export async function saveLoyaltyProgram(
  salonId: string,
  payload: LoyaltyProgramPayload,
): Promise<LoyaltyProgram | null> {
  return normalizeLoyaltyProgram(
    await request<unknown>(salonPath(salonId, '/loyalty-program'), {
      method: 'PUT',
      body: programBody(payload),
    }),
  )
}

/** `GET /clients/:cid/loyalty`: visits, vouchers and what is missing. */
export async function fetchClientLoyalty(
  salonId: string,
  clientId: string,
  signal?: AbortSignal,
): Promise<ClientLoyalty | null> {
  return normalizeClientLoyalty(
    await request<unknown>(clientLoyaltyPath(salonId, clientId), signal ? { signal } : {}),
  )
}

/** `POST .../loyalty/grant` (201): delivers ONE voucher. */
export async function grantLoyaltyReward(
  salonId: string,
  clientId: string,
  clientOpId?: string,
): Promise<LoyaltyReward | null> {
  return rewardFromAnswer(
    await request<unknown>(clientLoyaltyPath(salonId, clientId, '/grant'), {
      method: 'POST',
      body: grantBody(clientOpId),
    }),
  )
}

/** `POST .../loyalty/rewards/:rid/redeem` (200): uses a voucher. */
export async function redeemLoyaltyReward(
  salonId: string,
  clientId: string,
  rewardId: string,
  payload: RedeemRewardPayload,
  clientOpId?: string,
): Promise<LoyaltyReward | null> {
  return rewardFromAnswer(
    await request<unknown>(
      clientLoyaltyPath(salonId, clientId, `/rewards/${encodeURIComponent(rewardId)}/redeem`),
      { method: 'POST', body: redeemRewardBody(payload, clientOpId) },
    ),
  )
}

// --- one write at a time, online only ---

/** Synchronous gate of the screens (same one as the packages): the state only paints the button. */
export type LoyaltyGate = PackagesGate
export const createLoyaltyGate: () => LoyaltyGate = createPackagesGate

/** What a screen asks the API to do. */
export type LoyaltyWrite =
  | { kind: 'program'; payload: LoyaltyProgramPayload }
  | { kind: 'grant'; clientId: string; clientOpId: string }
  | {
      kind: 'redeem'
      clientId: string
      rewardId: string
      payload: RedeemRewardPayload
      clientOpId: string
    }

export type LoyaltyWriteOutcome =
  | { status: 'saved'; reward: LoyaltyReward | null }
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

/**
 * One press of a write button: refuses a second one while the first runs, sends
 * NOTHING when the browser is offline, and never enqueues anything (D9-d). What it
 * returns is not painted: the screen reads the server again.
 */
export async function writeLoyalty(
  gate: LoyaltyGate,
  salonId: string,
  write: LoyaltyWrite,
  isOnline: () => boolean = isBrowserOnline,
): Promise<LoyaltyWriteOutcome> {
  if (!gate.start()) {
    return { status: 'busy', message: LOYALTY_BUSY_MESSAGE }
  }
  try {
    if (!isOnline()) {
      return { status: 'offline', message: LOYALTY_OFFLINE_MESSAGE }
    }
    let reward: LoyaltyReward | null = null
    switch (write.kind) {
      case 'program':
        await saveLoyaltyProgram(salonId, write.payload)
        break
      case 'grant':
        reward = await grantLoyaltyReward(salonId, write.clientId, write.clientOpId)
        break
      case 'redeem':
        reward = await redeemLoyaltyReward(
          salonId,
          write.clientId,
          write.rewardId,
          write.payload,
          write.clientOpId,
        )
        break
    }
    return { status: 'saved', reward }
  } catch (error: unknown) {
    const failure = describeLoyaltyError(error, 'No pudimos completar la operación.')
    if (failure.kind === 'network') {
      const hint =
        write.kind === 'program'
          ? ' Antes de reintentar, recargá la página para ver si ya se guardó.'
          : ' Podés reintentar: la operación lleva una clave y no se repite.'
      return { status: 'network', message: `${LOYALTY_NETWORK_MESSAGE}${hint}` }
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
