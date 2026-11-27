/**
 * Waiting list (contract F7, section «Contrato F7» of
 * `odd/tasks/fase6-roadmap.md`). D9 is the frontend half; the shapes below are
 * the frozen ones of D4, D5 and D8 that the backend implements in parallel:
 *
 * - `POST /api/v1/public/salons/:slug/waitlist {service_id, professional_id?,
 *   date, name, email, phone?}` → 201, or 200 for an identical aviso that is
 *   already active → `{status:"waiting", date, service_id}`. The API answers
 *   `409 {"error":"Todavía hay horarios libres ese día"}` when the day still has
 *   slots, `409 {"error":"Ya tenés el máximo de avisos activos"}` (3 per email
 *   and salon) and `409 {"error":"La lista de espera de este salón está
 *   completa"}` (500 active); it shares the rate limiter of the public booking,
 *   so a 429 keeps the sentence of that flow.
 * - `POST /api/v1/public/waitlist/leave {token}` → 200 `{status:"left"}`, the
 *   same 200 when the token was already left (idempotent), and
 *   `404 {"error":"Aviso no encontrado"}` for an unknown or malformed token.
 * - `GET /api/v1/salons/:id/waitlist` → the staff list of D8;
 *   `DELETE /api/v1/salons/:id/waitlist/:eid` → 204.
 *
 * Rules kept by this module:
 * - there is no money in this feature: not a single cent, not a float;
 * - the token of the baja link lives ONLY in the fragment of the URL
 *   (`/lista-de-espera#<token>`): it is read once, wiped from the address bar
 *   with `history.replaceState` BEFORE any request and kept only in memory —
 *   never localStorage, never a cookie, never the query, never the path, never
 *   an error message, never the console. It travels only in the request BODY.
 *   The fragment contract is the one of the booking links of F1 (same 32 random
 *   bytes in base64url, same read-and-clean), so it is read with the same code.
 * - the server keeps the authority on the day range, the caps and the
 *   permissions; these helpers only avoid sending what the API would reject.
 */

import { ApiError, NetworkError, request } from '../../../lib/api.ts'
import { readBookingLinkHash, type HashRead, type HistoryLike } from '../../../lib/bookingLinks.ts'
import { EMAIL_INVALID_MESSAGE, isValidEmail, normalizeEmail } from '../../../lib/email.ts'
import { splitPhone, toE164, validatePhone } from '../../../lib/phone.ts'
import { RATE_LIMIT_MESSAGE } from '../../../lib/publicBooking.ts'

// --- copy of the contract (D9), verbatim where the contract quotes it ------

/** Title of the short form of the landing (D9a). */
export const WAITLIST_FORM_TITLE = 'Avisame si se libera un turno'

/** Success of the union (D9a). */
export const WAITLIST_SUCCESS = 'Listo, te avisamos por email si se libera un turno.'

/** Action of the public baja page (D9b). */
export const WAITLIST_LEAVE_ACTION = 'Dejar de recibir avisos'

/** Result of the baja; the server confirms it before the page says it. */
export const WAITLIST_LEAVE_DONE = 'Listo, ya no vas a recibir avisos de esta lista.'

/** Copy for a link without a token (the fragment was lost on the way). */
export const WAITLIST_NO_TOKEN_TITLE = 'El enlace no trae el código del aviso'
export const WAITLIST_NO_TOKEN_HELP =
  'El código viaja después del «#» del enlace del email y algunos programas lo cortan al reenviarlo. Abrí el botón original del correo o pedí la baja escribiéndole al salón.'

/** Help of the baja page before acting: says what it does and what it does not. */
export const WAITLIST_LEAVE_HELP =
  'Si dejás de recibir avisos, no te escribimos más por esta entrada. El horario sigue libre para cualquiera hasta que alguien lo reserve: darte de baja no cancela nada.'

/** Empty state of the staff table (D9c). */
export const WAITLIST_EMPTY =
  'No hay nadie esperando un turno: cuando alguien se anote desde la página del salón, aparece en esta lista.'

/** A row without a professional was asked for "cualquiera" (D2). */
export const WAITLIST_ANY_PROFESSIONAL = 'Cualquiera'

/** Status of a row in Spanish (D3). */
export const WAITLIST_STATUS_LABELS = {
  waiting: 'Esperando',
  notified: 'Avisada',
} as const

/** Confirmation of «Quitar» (D9c): asks first and says what it does. */
export const WAITLIST_REMOVE_QUESTION = '¿Quitar este aviso de la lista?'
export const WAITLIST_REMOVE_NOTE =
  'La persona deja de recibir el aviso de este día. Si el turno se libera, no se le escribe.'

/** Fallbacks of a failure nobody could explain (the server sentence wins). */
export const WAITLIST_JOIN_FALLBACK = 'No pudimos guardar tu aviso. Intentá de nuevo.'
export const WAITLIST_LEAVE_FALLBACK =
  'No pudimos darte de baja. Revisá tu conexión e intentá de nuevo.'
export const WAITLIST_LOAD_FALLBACK = 'No pudimos cargar la lista de espera.'
export const WAITLIST_REMOVE_FALLBACK = 'No pudimos quitar el aviso. Intentá de nuevo.'

/** Validation sentences of the form (the server applies the same rules). */
export const WAITLIST_NAME_MISSING = 'Escribí tu nombre para que sepamos a quién avisar.'
export const WAITLIST_NAME_TOO_LONG = 'El nombre no puede superar los 120 caracteres.'
export const WAITLIST_EMAIL_MISSING = 'Necesitamos tu email: es por ahí donde te avisamos.'
export const WAITLIST_DATE_MISSING = 'Elegí el día en el que querés que te avisemos.'

/** Caps of the contract that the form can check without asking the server. */
export const WAITLIST_NAME_MAX = 120

/** Days ahead the union accepts (D4): the picker of the landing follows it. */
export const WAITLIST_MAX_DAYS_AHEAD = 30

const DATE_PATTERN = /^(\d{4})-(\d{2})-(\d{2})$/

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

// --- the form of the landing (D9a) ---------------------------------------

export type WaitlistDraft = {
  name: string
  email: string
  /** Optional: only for a phone call if the salon prefers it. */
  phone: string
}

export type WaitlistField = 'name' | 'email' | 'phone' | 'date'

export type WaitlistValidation =
  | { ok: true; value: { name: string; email: string; phone: string } }
  | { ok: false; field: WaitlistField; message: string }

/** A real local calendar day: `2026-02-31` is not one. */
export function isWaitlistDate(value: string): boolean {
  const match = DATE_PATTERN.exec(value.trim())
  if (!match) {
    return false
  }
  const [, year = '', month = '', day = ''] = match
  const date = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day)))
  return (
    date.getUTCFullYear() === Number(year) &&
    date.getUTCMonth() === Number(month) - 1 &&
    date.getUTCDate() === Number(day)
  )
}

/**
 * Rules of D2 and D4 the browser can check: name 1..120 without NUL, a valid
 * email (it is the ONLY channel of the aviso, so unlike the booking it is
 * required) and an optional phone that must be usable when it is filled.
 */
export function validateWaitlistDraft(draft: WaitlistDraft): WaitlistValidation {
  const name = draft.name.trim()
  if (name === '') {
    return { ok: false, field: 'name', message: WAITLIST_NAME_MISSING }
  }
  if (name.includes('\u0000') || name.length > WAITLIST_NAME_MAX) {
    return { ok: false, field: 'name', message: WAITLIST_NAME_TOO_LONG }
  }
  const email = normalizeEmail(draft.email)
  if (email === '') {
    return { ok: false, field: 'email', message: WAITLIST_EMAIL_MISSING }
  }
  if (!isValidEmail(email)) {
    return { ok: false, field: 'email', message: EMAIL_INVALID_MESSAGE }
  }
  const phone = draft.phone.trim()
  if (phone !== '') {
    const parts = splitPhone(phone)
    const problem = validatePhone(parts)
    if (problem !== null) {
      return { ok: false, field: 'phone', message: problem }
    }
    return { ok: true, value: { name, email, phone: toE164(parts) } }
  }
  return { ok: true, value: { name, email, phone: '' } }
}

export type WaitlistJoinInput = {
  serviceId: string
  /** Absent = the salon assigns whoever is free ("cualquiera", D2). */
  professionalId?: string
  date: string
  name: string
  email: string
  phone?: string
}

/** Body of `POST /public/salons/:slug/waitlist` (D4). */
export type WaitlistJoinPayload = {
  service_id: string
  date: string
  name: string
  email: string
  professional_id?: string
  phone?: string
}

export type WaitlistJoined = { status: 'waiting'; date: string; service_id: string }

/**
 * Body of the union: the optional keys are ABSENT when empty (never `""`), so a
 * row without professional stays "cualquiera" and a row without phone has no
 * phone at all.
 */
export function waitlistPayload(input: WaitlistJoinInput): WaitlistJoinPayload {
  const payload: WaitlistJoinPayload = {
    service_id: input.serviceId.trim(),
    date: input.date.trim(),
    name: input.name.trim(),
    email: normalizeEmail(input.email),
  }
  const professionalId = input.professionalId?.trim() ?? ''
  if (professionalId !== '') {
    payload.professional_id = professionalId
  }
  const phone = input.phone?.trim() ?? ''
  if (phone !== '') {
    payload.phone = phone
  }
  return payload
}

/** Reads the 201/200 of the union; anything else is an answer we cannot show. */
export function normalizeWaitlistJoined(raw: unknown): WaitlistJoined | null {
  if (!isRecord(raw)) {
    return null
  }
  const status = text(raw['status'])
  const date = text(raw['date'])
  const serviceId = text(raw['service_id'])
  if (status !== 'waiting' || date === '' || serviceId === '') {
    return null
  }
  return { status: 'waiting', date, service_id: serviceId }
}

// --- failures -------------------------------------------------------------

export type WaitlistFailureKind =
  | 'conflict'
  | 'rate_limited'
  | 'validation'
  | 'not_found'
  | 'network'
  | 'unknown'

export type WaitlistFailure = { kind: WaitlistFailureKind; message: string }

/**
 * Copy of a failed request. The server is the authority on the reason, so its
 * sentence is shown verbatim (the three 409 of D4 and the 404 of D5 included);
 * a 429 keeps the sentence the public booking flow already uses, and a network
 * failure keeps the one of the client.
 */
export function describeWaitlistError(error: unknown, fallback: string): WaitlistFailure {
  if (error instanceof ApiError) {
    if (error.status === 429) {
      return { kind: 'rate_limited', message: RATE_LIMIT_MESSAGE }
    }
    if (error.status === 409) {
      return { kind: 'conflict', message: error.message }
    }
    if (error.status === 404) {
      return { kind: 'not_found', message: error.message }
    }
    if (error.status === 400 || error.status === 422) {
      return { kind: 'validation', message: error.message }
    }
    return { kind: 'unknown', message: error.message }
  }
  if (error instanceof NetworkError) {
    return { kind: 'network', message: error.message }
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return { kind: 'unknown', message: error.message }
  }
  return { kind: 'unknown', message: fallback }
}

// --- the token of the baja link (D5) -------------------------------------

/**
 * Reads the token of `location.hash` and returns the URL without it. It is the
 * fragment contract of `/turno` (F1): 32 random bytes in base64url, in the
 * FIRST fragment, wiped from the address bar in every case — a malformed
 * fragment is dropped whole and never read as a token.
 */
export function readWaitlistHash(href: string): HashRead {
  return readBookingLinkHash(href)
}

/**
 * First step of `/lista-de-espera`: read the token and wipe it from the address
 * bar in the same tick, BEFORE any request. `null` means the link cannot be
 * used and the page must not call the API at all.
 */
export function takeWaitlistToken(href: string, history: HistoryLike | null): string | null {
  const read = readWaitlistHash(href)
  if (read.url !== href && history) {
    history.replaceState(null, '', read.url)
  }
  return read.token
}

// --- the staff list (D8) --------------------------------------------------

export type WaitlistStatus = 'waiting' | 'notified'

export type WaitlistEntry = {
  id: string
  service_id: string
  service_name: string
  professional_id: string | null
  professional_name: string | null
  /** Local day of the salon, `YYYY-MM-DD`: it is never converted to a zone. */
  date: string
  name: string
  email: string
  phone: string | null
  status: WaitlistStatus
  created_at: string
  notified_at: string | null
}

function status(value: unknown): WaitlistStatus {
  return value === 'notified' ? 'notified' : 'waiting'
}

function normalizeEntry(value: unknown): WaitlistEntry | null {
  if (!isRecord(value)) {
    return null
  }
  // Without an id the row could not be removed later, so it is not shown.
  const id = text(value['id'])
  if (id === '') {
    return null
  }
  return {
    id,
    service_id: text(value['service_id']),
    service_name: text(value['service_name']),
    professional_id: optionalText(value['professional_id']),
    professional_name: optionalText(value['professional_name']),
    date: text(value['date']),
    name: text(value['name']),
    email: text(value['email']),
    phone: optionalText(value['phone']),
    status: status(value['status']),
    created_at: text(value['created_at']),
    notified_at: optionalText(value['notified_at']),
  }
}

/** Order of the contract: the day, and inside a day the moment of the union. */
export function sortWaitlist(entries: readonly WaitlistEntry[]): WaitlistEntry[] {
  return [...entries].sort((a, b) => {
    if (a.date !== b.date) {
      return a.date < b.date ? -1 : 1
    }
    if (a.created_at !== b.created_at) {
      return a.created_at < b.created_at ? -1 : 1
    }
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
  })
}

/** Reads `{entries:[…]}` (or a bare array) and drops the rows nobody can use. */
export function normalizeWaitlistEntries(raw: unknown): WaitlistEntry[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['entries'])
      ? raw['entries']
      : []
  const entries: WaitlistEntry[] = []
  for (const row of rows) {
    const entry = normalizeEntry(row)
    if (entry) {
      entries.push(entry)
    }
  }
  return sortWaitlist(entries)
}

/** The day of the table is the day of the salon: UTC noon, no zone shift. */
export function waitlistDayLabel(date: string): string {
  const match = DATE_PATTERN.exec(date.trim())
  if (!match) {
    return date.trim()
  }
  const [, year = '1970', month = '01', day = '01'] = match
  const parsed = new Date(Date.UTC(Number(year), Number(month) - 1, Number(day), 12))
  return new Intl.DateTimeFormat('es-MX', {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
    timeZone: 'UTC',
  }).format(parsed)
}

export function waitlistStatusLabel(value: WaitlistStatus): string {
  return WAITLIST_STATUS_LABELS[value]
}

/** `null` (or blank) is the "cualquiera" of D2, said out loud. */
export function professionalLabel(name: string | null): string {
  const trimmed = name?.trim() ?? ''
  return trimmed === '' ? WAITLIST_ANY_PROFESSIONAL : trimmed
}

export function waitlistCountLabel(count: number): string {
  return count === 1 ? '1 aviso' : `${count} avisos`
}

// --- API client (D4, D5 and D8) -----------------------------------------

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function publicSalonPath(slug: string, suffix = ''): string {
  return `/api/v1/public/salons/${encodeURIComponent(slug)}${suffix}`
}

/**
 * `POST /public/salons/:slug/waitlist` (D4). No session: the landing is
 * anonymous. A 409 keeps its sentence for the screen («Todavía hay horarios
 * libres ese día», «Ya tenés el máximo de avisos activos», «La lista de espera
 * de este salón está completa»).
 */
export async function joinWaitlist(
  slug: string,
  input: WaitlistJoinInput,
  options: { fetchImpl?: typeof fetch } = {},
): Promise<WaitlistJoined> {
  const body = await request<unknown>(publicSalonPath(slug, '/waitlist'), {
    method: 'POST',
    body: waitlistPayload(input),
    auth: 'none',
    fetchImpl: options.fetchImpl,
  })
  const joined = normalizeWaitlistJoined(body)
  if (!joined) {
    throw new ApiError(
      200,
      'El salón guardó el aviso pero no devolvió los datos de la lista de espera.',
    )
  }
  return joined
}

/** `POST /public/waitlist/leave {token}` (D5): idempotent, `{status:"left"}`. */
export async function leaveWaitlist(
  token: string,
  fetchImpl?: typeof fetch,
): Promise<void> {
  await request<unknown>('/api/v1/public/waitlist/leave', {
    method: 'POST',
    body: { token },
    auth: 'none',
    fetchImpl,
  })
}

/** `GET /salons/:id/waitlist` (D8): the avisos from today on, still active. */
export async function fetchWaitlist(
  salonId: string,
  options: { signal?: AbortSignal; fetchImpl?: typeof fetch } = {},
): Promise<WaitlistEntry[]> {
  const payload = await request<unknown>(salonPath(salonId, '/waitlist'), {
    signal: options.signal,
    fetchImpl: options.fetchImpl,
  })
  return normalizeWaitlistEntries(payload)
}

/** `DELETE /salons/:id/waitlist/:eid` (D8): 204, or 404 «Aviso no encontrado». */
export async function removeWaitlistEntry(
  salonId: string,
  entryId: string,
  fetchImpl?: typeof fetch,
): Promise<void> {
  await request<unknown>(salonPath(salonId, `/waitlist/${encodeURIComponent(entryId)}`), {
    method: 'DELETE',
    fetchImpl,
  })
}
