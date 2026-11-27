/**
 * Reviews after the visit (contract F13, «Contrato F13 (resenas posvisita)», D4-D8).
 * Pure rules plus the thin API clients: the public `/resena` page, the «Reseñas»
 * section of the landing and the panel page `/panel/resenas` render what these
 * return. The server stays the authority on the link, the token, the averages, the
 * permissions and the pagination; this module only validates, pages and labels.
 *
 * Security rules of the public page (same as the other links with a token, F15):
 * - the token travels in the FRAGMENT of the emailed link (`/resena#<token>`), so it
 *   never reaches a server or proxy log; the page reads it once, wipes it from the
 *   address bar with `history.replaceState` and keeps it only in memory (never
 *   storage, never the console, never an error message, never the query or the path);
 * - the public requests go WITHOUT the session of whoever is browsing (`auth:
 *   'none'`): the review belongs to the person of the link, not to a logged-in owner;
 * - server sentences are printed verbatim, and a 404 is the uniform answer for an
 *   unknown, expired or already used link.
 *
 * Online only (D8): nothing here touches the offline queue. Offline the review is not
 * even sent, and the page says so.
 */

import { ApiError, NetworkError, request } from '../../../lib/api.ts'
import { isBrowserOnline } from '../../../lib/connectivity.ts'
import { toDateInputValue, TIME_ZONE } from '../../../lib/datetime.ts'
import { formatAuditWhen } from '../../audit/lib/audit.ts'
import { createPackagesGate, type PackagesGate } from '../../packages/lib/packages.ts'

/**
 * The token of `/resena#<token>` is read with the SAME function as the other public
 * token pages (F15): the fragment is exactly the token, the address bar is cleaned
 * and anything else is dropped. Re-exported so this module documents its own rule.
 */
export { readFragmentToken } from '../../auth/lib/recovery.ts'

/** Rating of the contract (D4): a whole number from 1 to 5. */
export const MIN_REVIEW_RATING = 1
export const MAX_REVIEW_RATING = 5

/** Characters of the comment (D4: `comment <= 500`). */
export const MAX_REVIEW_COMMENT_LENGTH = 500

/** Rows per request of the panel (the same cursor pagination as the activity log). */
export const REVIEWS_PAGE_SIZE = 20

/** Sentences of this module: the ones the page says before a request would be rejected. */
export const REVIEW_MESSAGES = {
  /** No token in the fragment at all: nothing is requested. */
  noToken: 'Este enlace está incompleto o ya se usó. Pedí un enlace nuevo al salón.',
  ratingRequired: 'Elegí una calificación de 1 a 5 estrellas',
  commentTooLong: 'El comentario no puede superar los 500 caracteres',
  invalidText: 'El comentario no puede tener caracteres no válidos ni saltos de línea',
  offline:
    'Sin conexión. La reseña se envía en línea: no se envió ni quedó en cola. Volvé a intentarlo cuando tengas conexión.',
  busy: 'Ya se está enviando tu reseña: esperá a que termine.',
  thanks: '¡Gracias por tu reseña!',
  linkFallback: 'No pudimos cargar el enlace de la reseña.',
  delayRange: 'La demora debe ser entre 1 y 72 horas',
  fallback: 'No pudimos enviar la reseña.',
} as const

/** Every sentence of the panel list an empty answer gets (the server said «none»). */
export const REVIEWS_EMPTY_MESSAGE =
  'Todavía no hay reseñas de este salón. Se piden solas por correo después de cada turno atendido.'

/** Labels of the five stars, from worst to best. */
export const REVIEW_RATING_LABELS: readonly string[] = [
  'Muy mala',
  'Mala',
  'Regular',
  'Buena',
  'Excelente',
]

/** Name of a rating; anything outside 1..5 is not a rating. */
export function reviewRatingLabel(rating: number): string {
  return REVIEW_RATING_LABELS[rating - 1] ?? 'Sin calificación'
}

/** «★★★★☆»: the five stars of a rating, for a read-only row or a badge. */
export function reviewStarsText(rating: number): string {
  const whole = Number.isFinite(rating) ? Math.min(MAX_REVIEW_RATING, Math.max(0, Math.floor(rating))) : 0
  return '★'.repeat(whole) + '☆'.repeat(MAX_REVIEW_RATING - whole)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function isInt(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value)
}

// --- D4: the public link of one booking ---

/** `GET /public/reviews/:token` (D4): what the page needs to greet the person. */
export type ReviewLink = {
  salonName: string
  /** May be empty: the page hides the line instead of inventing a service. */
  serviceName: string
  /** ISO date of the atendido turno. */
  startsAt: string
}

/** A link without a salon or without a readable date is not a link: `null`. */
export function normalizeReviewLink(raw: unknown): ReviewLink | null {
  if (!isRecord(raw)) {
    return null
  }
  const salonName = text(raw['salon_name'])
  const startsAt = text(raw['starts_at'])
  if (salonName === '' || startsAt === '' || Number.isNaN(Date.parse(startsAt))) {
    return null
  }
  return { salonName, serviceName: text(raw['service_name']), startsAt }
}

/** Path of the public link; the token is one path segment, always encoded. */
export function reviewLinkPath(token: string): string {
  return `/api/v1/public/reviews/${encodeURIComponent(token)}`
}

/**
 * `GET /public/reviews/:token` (public, no session). A 404 is the uniform answer for
 * an unknown, expired or already used link and its sentence reaches the caller.
 */
export async function fetchReviewLink(token: string, signal?: AbortSignal): Promise<ReviewLink | null> {
  return normalizeReviewLink(
    await request<unknown>(reviewLinkPath(token), {
      auth: 'none',
      keepSessionOn401: true,
      ...(signal ? { signal } : {}),
    }),
  )
}

export type ReviewLinkFailure = { kind: 'invalid' | 'error'; message: string }

/**
 * What a failed load of the link is: `invalid` for the uniform 404 (expired, used or
 * unknown: nothing to fill in), `error` for everything else (retry makes sense).
 */
export function describeReviewLinkError(error: unknown, fallback: string): ReviewLinkFailure {
  if (error instanceof ApiError) {
    return error.status === 404
      ? { kind: 'invalid', message: error.message }
      : { kind: 'error', message: error.message }
  }
  if (error instanceof NetworkError) {
    return { kind: 'error', message: error.message }
  }
  // Anything else is not a sentence for a client on a public page: never leak it.
  const clean = typeof fallback === 'string' ? fallback.trim() : ''
  return { kind: 'error', message: clean === '' ? REVIEW_MESSAGES.linkFallback : clean }
}

// --- the comment and the rating of the form ---

/** C0 and C1 controls, plus the two Unicode line separators: never in a comment. */
const CONTROL_CHARACTERS = /[\u0000-\u001F\u007F\u0080-\u009F\u2028\u2029]/

function hasControlCharacter(value: string): boolean {
  return CONTROL_CHARACTERS.test(value)
}

/**
 * Removes every control character from what the person types or pastes: a NUL or a
 * line break is a 400 on the server, so it never reaches the textarea's value. The
 * accents and emoji are valid text and are NOT touched.
 */
export function sanitizeReviewComment(value: string): string {
  return typeof value === 'string' ? value.replace(/[\u0000-\u001F\u007F\u0080-\u009F\u2028\u2029]/g, '') : ''
}

/** «0/500», «12/500»: the live counter of the comment. */
export function commentCounterText(length: number): string {
  const count = Number.isFinite(length) && length > 0 ? Math.floor(length) : 0
  return `${count}/${MAX_REVIEW_COMMENT_LENGTH}`
}

export type ReviewField = 'rating' | 'comment'

/** What the form holds: the rating chosen and the comment as typed. */
export type ReviewDraft = { rating: number; comment: string }

export type ReviewDraftCheck =
  | { ok: true; payload: { rating: number; comment: string } }
  | { ok: false; field: ReviewField; message: string }

/**
 * Reads the form before sending it (D4): a whole rating 1..5, a comment of up to 500
 * characters without control characters, and nothing else. The body always carries
 * both keys, with the comment trimmed.
 */
export function validateReviewDraft(draft: ReviewDraft): ReviewDraftCheck {
  const rating = draft.rating
  if (!isInt(rating) || rating < MIN_REVIEW_RATING || rating > MAX_REVIEW_RATING) {
    return { ok: false, field: 'rating', message: REVIEW_MESSAGES.ratingRequired }
  }
  const comment = draft.comment ?? ''
  if (hasControlCharacter(comment)) {
    return { ok: false, field: 'comment', message: REVIEW_MESSAGES.invalidText }
  }
  const trimmed = comment.trim()
  if (trimmed.length > MAX_REVIEW_COMMENT_LENGTH) {
    return { ok: false, field: 'comment', message: REVIEW_MESSAGES.commentTooLong }
  }
  return { ok: true, payload: { rating, comment: trimmed } }
}

// --- D4: POST of the review, one at a time and online only ---

/** Synchronous gate of the page (same one as packages and loyalty): one request at a time. */
export type ReviewGate = PackagesGate
export const createReviewGate: () => ReviewGate = createPackagesGate

export type ReviewSubmitOutcome =
  | { status: 'sent' }
  | { status: 'invalid'; field: ReviewField; message: string }
  | { status: 'busy'; message: string }
  | { status: 'offline'; message: string }
  /** 404: the link was used (maybe by a second POST) or expired. */
  | { status: 'used'; message: string }
  | { status: 'failed'; message: string }

/**
 * `POST /public/reviews/:token` with `{rating, comment}` (public, no session). An
 * invalid draft never leaves the browser; offline nothing is sent or queued; the
 * server sentence of a 400 is returned verbatim for the page to print.
 */
export async function submitReview(
  gate: ReviewGate,
  token: string,
  draft: ReviewDraft,
  isOnline: () => boolean = isBrowserOnline,
): Promise<ReviewSubmitOutcome> {
  const checked = validateReviewDraft(draft)
  if (!checked.ok) {
    return { status: 'invalid', field: checked.field, message: checked.message }
  }
  if (!gate.start()) {
    return { status: 'busy', message: REVIEW_MESSAGES.busy }
  }
  try {
    if (!isOnline()) {
      return { status: 'offline', message: REVIEW_MESSAGES.offline }
    }
    await request<unknown>(reviewLinkPath(token), {
      method: 'POST',
      body: checked.payload,
      auth: 'none',
      keepSessionOn401: true,
    })
    return { status: 'sent' }
  } catch (error: unknown) {
    if (error instanceof ApiError) {
      if (error.status === 404) {
        return { status: 'used', message: error.message }
      }
      return { status: 'failed', message: error.message }
    }
    if (error instanceof NetworkError) {
      return { status: 'failed', message: error.message }
    }
    return {
      status: 'failed',
      message: error instanceof Error && error.message.trim() !== '' ? error.message : REVIEW_MESSAGES.fallback,
    }
  } finally {
    gate.finish()
  }
}

// --- D6: the panel list ---

/** One review of the panel (`GET /salons/:id/reviews`, D6). */
export type PanelReview = {
  id: string
  /** 1..5. */
  rating: number
  /** Plain text: the panel never renders it as HTML. */
  comment: string
  /** «Lía G.»: first name and the initial the server decided (D5). */
  author_name: string
  hidden: boolean
  created_at: string
  service_name: string
}

export type ReviewPage = {
  items: PanelReview[]
  /** Opaque cursor of the next (older) page; `null` = no more. */
  next_cursor: string | null
}

/** A row without id, a rating 1..5 or a readable date cannot be shown: `null`. */
export function normalizePanelReview(raw: unknown): PanelReview | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const rating = raw['rating']
  const createdAt = text(raw['created_at'])
  if (id === '' || !isInt(rating) || rating < MIN_REVIEW_RATING || rating > MAX_REVIEW_RATING) {
    return null
  }
  if (createdAt === '' || Number.isNaN(Date.parse(createdAt))) {
    return null
  }
  return {
    id,
    rating,
    comment: text(raw['comment']),
    author_name: text(raw['author_name']),
    hidden: raw['hidden'] === true,
    created_at: createdAt,
    service_name: text(raw['service_name']),
  }
}

/** `GET /salons/:id/reviews` -> `{items, next_cursor}`. A body of another shape is `null`. */
export function normalizeReviewPage(raw: unknown): ReviewPage | null {
  if (!isRecord(raw) || !Array.isArray(raw['items'])) {
    return null
  }
  const items: PanelReview[] = []
  for (const row of raw['items'] as unknown[]) {
    const parsed = normalizePanelReview(row)
    if (parsed) {
      items.push(parsed)
    }
  }
  const cursor = text(raw['next_cursor'])
  return { items, next_cursor: cursor === '' ? null : cursor }
}

/**
 * Appends the next page without repeating an id. The copy already on screen wins, so
 * a page that arrives after an optimistic hide does not undo it. Inputs are not
 * mutated and the server order (newest first) is kept.
 */
export function mergeReviews(
  current: readonly PanelReview[],
  incoming: readonly PanelReview[],
): PanelReview[] {
  const seen = new Set<string>()
  const merged: PanelReview[] = []
  for (const item of [...current, ...incoming]) {
    if (!seen.has(item.id)) {
      seen.add(item.id)
      merged.push(item)
    }
  }
  return merged
}

/** Flips the visibility of ONE row; the same call with the previous value is the rollback. */
export function applyReviewHidden(
  items: readonly PanelReview[],
  reviewId: string,
  hidden: boolean,
): PanelReview[] {
  return items.map((item) => (item.id === reviewId ? { ...item, hidden } : item))
}

export type ReviewQuery = {
  /** Opaque cursor of the previous page. */
  before?: string | null
  signal?: AbortSignal
}

/** Path of one page: salon scoped, `limit` always, `before` only when set. */
export function reviewsPath(salonId: string, query: Pick<ReviewQuery, 'before'> = {}): string {
  const params = [`limit=${REVIEWS_PAGE_SIZE}`]
  const before = query.before?.trim() ?? ''
  if (before !== '') {
    params.push(`before=${encodeURIComponent(before)}`)
  }
  return `/api/v1/salons/${encodeURIComponent(salonId)}/reviews?${params.join('&')}`
}

/** `GET /salons/:id/reviews` (owner/admin/reception). Server sentences reach the caller verbatim. */
export async function fetchReviews(salonId: string, query: ReviewQuery = {}): Promise<ReviewPage> {
  const page = normalizeReviewPage(
    await request<unknown>(reviewsPath(salonId, query), query.signal ? { signal: query.signal } : {}),
  )
  if (!page) {
    throw new ApiError(200, 'La API no devolvió las reseñas.')
  }
  return page
}

/** `/salons/:id/reviews/:rid/hide` or `/unhide` (D6: owner/admin only). */
export function reviewActionPath(salonId: string, reviewId: string, hidden: boolean): string {
  const action = hidden ? 'hide' : 'unhide'
  return `/api/v1/salons/${encodeURIComponent(salonId)}/reviews/${encodeURIComponent(reviewId)}/${action}`
}

/** `POST .../hide` or `.../unhide`: 200 `{ok:true}`. A 404 keeps the server sentence. */
export async function setReviewHidden(
  salonId: string,
  reviewId: string,
  hidden: boolean,
): Promise<void> {
  await request<unknown>(reviewActionPath(salonId, reviewId, hidden), { method: 'POST' })
}

// --- D6: review settings ---

/** `GET`/`PUT /salons/:id/review-settings` (both keys always). */
export type ReviewSettings = {
  enabled: boolean
  /** Hours after the turno (1..72). */
  delay_hours: number
}

export const MIN_REVIEW_DELAY_HOURS = 1
export const MAX_REVIEW_DELAY_HOURS = 72

/** A setting without a whole delay between 1 and 72 hours is not usable: `null`. */
export function normalizeReviewSettings(raw: unknown): ReviewSettings | null {
  if (!isRecord(raw)) {
    return null
  }
  const enabled = raw['enabled']
  const delay = raw['delay_hours']
  if (typeof enabled !== 'boolean') {
    return null
  }
  if (!isInt(delay) || delay < MIN_REVIEW_DELAY_HOURS || delay > MAX_REVIEW_DELAY_HOURS) {
    return null
  }
  return { enabled, delay_hours: delay }
}

export function reviewSettingsPath(salonId: string): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}/review-settings`
}

/** `GET /salons/:id/review-settings` (owner/admin/reception). */
export async function fetchReviewSettings(
  salonId: string,
  signal?: AbortSignal,
): Promise<ReviewSettings | null> {
  return normalizeReviewSettings(
    await request<unknown>(reviewSettingsPath(salonId), signal ? { signal } : {}),
  )
}

/** `PUT /salons/:id/review-settings` (owner/admin): REPLACES, so both keys always travel. */
export async function saveReviewSettings(
  salonId: string,
  settings: ReviewSettings,
): Promise<ReviewSettings | null> {
  return normalizeReviewSettings(
    await request<unknown>(reviewSettingsPath(salonId), {
      method: 'PUT',
      body: { enabled: settings.enabled, delay_hours: settings.delay_hours },
    }),
  )
}

/** What the settings form holds: the switch and the hours as typed. */
export type ReviewSettingsDraft = { enabled: boolean; delayHours: string }

/** The saved settings, or the empty form when the salon never saved them. */
export function draftFromReviewSettings(settings: ReviewSettings | null): ReviewSettingsDraft {
  if (settings === null) {
    return { enabled: false, delayHours: '' }
  }
  return { enabled: settings.enabled, delayHours: String(settings.delay_hours) }
}

export type ReviewSettingsField = 'delay'

export type ReviewSettingsCheck =
  | { ok: true; payload: ReviewSettings }
  | { ok: false; field: ReviewSettingsField; message: string }

/** The same rule as the server: a whole number from 1 to 72, no decimals. */
export function validateReviewSettingsDraft(draft: ReviewSettingsDraft): ReviewSettingsCheck {
  const cleaned = draft.delayHours.trim()
  if (!/^\d+$/.test(cleaned)) {
    return { ok: false, field: 'delay', message: REVIEW_MESSAGES.delayRange }
  }
  const hours = Number(cleaned)
  if (
    !Number.isSafeInteger(hours) ||
    hours < MIN_REVIEW_DELAY_HOURS ||
    hours > MAX_REVIEW_DELAY_HOURS
  ) {
    return { ok: false, field: 'delay', message: REVIEW_MESSAGES.delayRange }
  }
  return { ok: true, payload: { enabled: draft.enabled, delay_hours: hours } }
}

// --- presentation of the panel ---

/** «24 horas después del turno»: what the demora means, in plain Spanish. */
export function reviewDelayLabel(hours: number): string {
  return `${hours} ${hours === 1 ? 'hora' : 'horas'} después del turno`
}

/** `2026-11-04 12:30`: local date and time in the zone of the SALON (24 h). */
export function reviewCreatedText(iso: string, timeZone: string): string {
  return formatAuditWhen(iso, timeZone)
}

/**
 * `04/11/2026`: the day of the turno or of the review, for the public page. An empty
 * or unreadable date says nothing instead of inventing one. The public link does not
 * carry the zone of the salon, so the default zone of the app is used (the rest of
 * the front does the same, see H14).
 */
export function reviewDayLabel(value: string): string {
  const clean = typeof value === 'string' ? value.trim() : ''
  if (clean === '') {
    return ''
  }
  const day = /^\d{4}-\d{2}-\d{2}$/.test(clean)
    ? clean
    : Number.isNaN(Date.parse(clean))
      ? ''
      : toDateInputValue(new Date(clean), TIME_ZONE)
  if (day === '') {
    return ''
  }
  const [year, month, rest] = day.split('-')
  return `${rest}/${month}/${year}`
}

/** Sentence of the state of a row: `null` when the review is public. */
export function reviewHiddenLabel(hidden: boolean): string | null {
  return hidden ? 'Oculta' : null
}
