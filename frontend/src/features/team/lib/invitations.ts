/**
 * Team invitations (contract F2g1, S-F2g1-3/-4 shapes and S-F2g1-6). Pure rules
 * plus the thin API client: `/panel/equipo` renders the invite block, the
 * pending list and the one-time link, and the public `/invitacion` page renders
 * the lookup and the accept forms. The server stays the authority.
 *
 * Security rules of the page (S-F2g1, non negotiable):
 * - the token travels in the FRAGMENT of the link, so it never reaches a log;
 * - the page reads it once, wipes it from the URL with history.replaceState
 *   BEFORE any request, and keeps it only in memory (never localStorage, never
 *   sessionStorage, never the console, never an error message, never the query
 *   or the path);
 * - every request sends it in the BODY (`{token}`);
 * - an invalid, expired, used or revoked token gets the ONE generic sentence of
 *   the server: the UI never distinguishes the causes.
 */

import { ApiError, request } from '../../../lib/api.ts'
import { TIME_ZONE } from '../../../lib/datetime.ts'
import { isManager, roleLabel, panelHomePath, type Role } from '../../../lib/permissions.ts'
import { normalizeEmail } from '../../../lib/email.ts'
import type { SalonRole } from '../../../lib/me.ts'

/** Domain limits of the contract (S-F2g1-1, S-F2g1-3 and the registration policy). */
export const INVITATION_MAX_PENDING = 50
export const INVITATION_NAME_MAX = 120
export const INVITATION_PASSWORD_MIN_BYTES = 8
export const INVITATION_PASSWORD_MAX_BYTES = 72

/** Copy of the one-time link note (S-F2g1-6, verbatim). */
export const LINK_ONCE_NOTE = 'Este enlace se muestra una sola vez'

/** Shown when the server sent no usable `email_hint`. */
export const EMAIL_HINT_FALLBACK = 'el correo de la invitación'

/**
 * Sentences of the API shown verbatim (S-F2g1-3/-4). `invalid` is the single
 * message of a token that does not exist, expired, was used or was revoked: the
 * API answers it with the same status and the same text for all four causes.
 */
export const INVITATION_MESSAGES = {
  alreadyMember: 'Esa persona ya es parte del equipo',
  maxPending: 'Máximo 50 invitaciones pendientes',
  role: 'No podés invitar con ese rol',
  email: 'Email inválido',
  invalid: 'Invitación inválida o vencida',
  accountExists: 'Ya tenés cuenta: iniciá sesión para aceptar la invitación',
  otherAccount: 'Esta invitación es para otra cuenta',
} as const

/** Roles of the team a manager can hand out by invitation. */
export type InvitationRole = 'professional' | 'admin' | 'reception'

/** A pending invitation (`GET /salons/:id/invitations`). Never carries a token. */
export type PendingInvitation = {
  id: string
  email: string
  role: SalonRole
  expires_at: string
}

/** Answer of `POST /salons/:id/invitations` (201). `link` is shown only once. */
export type CreatedInvitation = {
  invitation: PendingInvitation | null
  link: string
  email_sent: boolean
}

/** Answer of `POST /public/invitations/lookup` (S-F2g1-4). */
export type InvitationLookup = {
  salon_name: string
  role: SalonRole
  /** Label of the server when it sends one; the Spanish fallback otherwise. */
  role_label: string
  /** Masked email exactly as the server sent it (e.g. `l***@mail.com`). */
  email_hint: string
  expires_at: string
  has_account: boolean
}

/**
 * The one-time link while it is on screen. Once dismissed (or after a reload, or
 * once another invitation is created) it is gone: the API cannot return it again.
 */
export type LinkOnceState = { link: string; emailSent: boolean } | null

const ROLES: readonly SalonRole[] = ['owner', 'admin', 'professional', 'reception']
const INVITABLE: readonly InvitationRole[] = ['professional', 'admin', 'reception']

const MONTHS = [
  'enero',
  'febrero',
  'marzo',
  'abril',
  'mayo',
  'junio',
  'julio',
  'agosto',
  'septiembre',
  'octubre',
  'noviembre',
  'diciembre',
] as const

/** `crypto/rand` gives 32 bytes → 43 characters in base64url. */
const TOKEN_PATTERN = /^[A-Za-z0-9_-]{20,128}$/
const DATE_PARTS = new Map<string, Intl.DateTimeFormat>()

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function role(value: unknown): SalonRole {
  return ROLES.includes(value as SalonRole) ? (value as SalonRole) : 'professional'
}

// --- the token of the fragment ---

/**
 * A token is base64url: no padding, no spaces, no percent-encoding. Anything
 * else in the fragment is not an invitation token and is never treated as one.
 */
export function isInvitationToken(value: string): boolean {
  return TOKEN_PATTERN.test(value)
}

export type HashRead = { token: string | null; url: string }

/**
 * Reads the token of `location.hash` and returns the URL without it, ready for
 * `history.replaceState`. The invitation slot is the FIRST fragment: only there
 * is a token trusted (it is the `/invitacion#<token>` link of the contract).
 *
 * The URL is cleaned in every case: a token that appears in another fragment is
 * removed and IGNORED (never used, never left behind for a later page to read),
 * a fragment without any token is junk and is dropped whole, and the anchors of
 * the rest stay. So a broken link leaves no trace and no secret in the history.
 */
export function readInvitationHash(href: string): HashRead {
  const hashIndex = href.indexOf('#')
  if (hashIndex === -1) {
    return { token: null, url: href }
  }
  const base = href.slice(0, hashIndex)
  const segments = href.slice(hashIndex + 1).split('#')
  const slot = segments.findIndex(isInvitationToken)
  const token = slot === 0 ? (segments[0] ?? null) : null
  // Never keep something that looks like a token in the address bar.
  const anchors = segments.filter((segment, index) => index !== slot && segment !== '' && !isInvitationToken(segment))
  const hash = slot === -1 || anchors.length === 0 ? '' : `#${anchors.join('#')}`
  return { token, url: `${base}${hash}` }
}

// --- the account of the guest (same rules as the registration endpoint) ---

export type SignupDraft = { name: string; password: string }

export type SignupCheck =
  | { ok: true; value: { name: string; password: string } }
  | { ok: false; field: 'name' | 'password'; message: string }

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

/**
 * Validates the name and the password exactly like `POST /auth/register`:
 * a trimmed non-empty name of at most 120 characters, and a password of 8..72
 * BYTES (bcrypt truncates at 72 bytes, so an accented letter counts twice).
 * The password is never trimmed.
 */
export function validateSignup(input: SignupDraft): SignupCheck {
  const name = input.name.trim()
  if (name === '') {
    return { ok: false, field: 'name', message: 'Escribe tu nombre' }
  }
  if ([...name].length > INVITATION_NAME_MAX) {
    return {
      ok: false,
      field: 'name',
      message: `El nombre no puede superar los ${INVITATION_NAME_MAX} caracteres`,
    }
  }
  const passwordBytes = byteLength(input.password)
  if (passwordBytes < INVITATION_PASSWORD_MIN_BYTES) {
    return {
      ok: false,
      field: 'password',
      message: `La contraseña debe tener al menos ${INVITATION_PASSWORD_MIN_BYTES} caracteres`,
    }
  }
  if (passwordBytes > INVITATION_PASSWORD_MAX_BYTES) {
    return {
      ok: false,
      field: 'password',
      message: `La contraseña no puede superar los ${INVITATION_PASSWORD_MAX_BYTES} caracteres`,
    }
  }
  return { ok: true, value: { name, password: input.password } }
}

// --- masked email, role labels and expiry ---

/**
 * The masked email of the invitation, printed AS RECEIVED: the UI neither
 * re-masks nor tries to rebuild the real address (it cannot: the server never
 * sends it).
 */
export function emailHintLabel(hint: unknown): string {
  const value = text(hint)
  return value === '' ? EMAIL_HINT_FALLBACK : value
}

/** Role of the invitation in Spanish; the server label wins when it sends one. */
export function invitationRoleLabel(input: { role: unknown; role_label?: unknown }): string {
  const serverLabel = text(input.role_label)
  if (serverLabel !== '') {
    return serverLabel
  }
  return roleLabel(role(input.role))
}

function dateParts(
  iso: string,
  timeZone: string,
): { year: number; month: number; day: number } | null {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return null
  }
  const cached = DATE_PARTS.get(timeZone)
  const formatter =
    cached ??
    new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    })
  if (!cached) {
    DATE_PARTS.set(timeZone, formatter)
  }
  const parts = formatter.formatToParts(date)
  const read = (type: Intl.DateTimeFormatPartTypes): number => {
    const value = parts.find((part) => part.type === type)?.value ?? '0'
    return Number.parseInt(value, 10)
  }
  const year = read('year')
  const month = read('month')
  const day = read('day')
  if (!Number.isFinite(year) || month < 1 || month > 12 || day < 1 || day > 31) {
    return null
  }
  return { year, month, day }
}

export type ExpiryOptions = { timeZone?: string; now?: Date }

/**
 * "el 29 de noviembre" of the salon zone (the year only when it is not the
 * current one). `null` when the date is unusable, so the UI omits the sentence
 * instead of printing an invented day.
 */
export function expiryDay(iso: string, options: ExpiryOptions = {}): string | null {
  const timeZone = options.timeZone ?? TIME_ZONE
  const day = dateParts(iso, timeZone)
  if (!day) {
    return null
  }
  const now = dateParts((options.now ?? new Date()).toISOString(), timeZone)
  const label = `${day.day} de ${MONTHS[day.month - 1]}`
  return now && now.year !== day.year ? `${label} de ${day.year}` : label
}

/** «Vence el 29 de noviembre»: the label of the pending list. */
export function expiryLabel(iso: string, options: ExpiryOptions = {}): string | null {
  const day = expiryDay(iso, options)
  return day === null ? null : `Vence el ${day}`
}

/** The same date in lowercase for a sentence («…, vence el 29 de noviembre»). */
export function expirySentence(iso: string, options: ExpiryOptions = {}): string | null {
  const day = expiryDay(iso, options)
  return day === null ? null : `vence el ${day}`
}

// --- who can invite which role (mirror of domain.Role.AddableBy) ---

/**
 * Mirrors `Role.AddableBy` of the API: the owner hands out admin, professional
 * and reception; an admin only professional and reception (a second owner is
 * never created through the API, and clients are never invited).
 */
export function canInviteRole(
  actorRole: Role | null | undefined,
  targetRole: Role,
): boolean {
  if (!INVITABLE.includes(targetRole as InvitationRole)) {
    return false
  }
  if (actorRole === 'owner') {
    return true
  }
  if (actorRole === 'admin') {
    return targetRole === 'professional' || targetRole === 'reception'
  }
  return false
}

/** Roles of the invite form, in the order they are offered. */
export function invitableRoles(actorRole: Role | null | undefined): InvitationRole[] {
  return INVITABLE.filter((target) => canInviteRole(actorRole, target))
}

/** Only a manager reaches the invite endpoints (the API answers 403 to the rest). */
export function canInvite(actorRole: Role | null | undefined): boolean {
  return isManager(actorRole)
}

/** True when the typed email already belongs to someone of the team. */
export function emailIsMember(
  members: readonly { email: string }[],
  email: string,
): boolean {
  const target = normalizeEmail(email)
  if (target === '') {
    return false
  }
  return members.some((member) => normalizeEmail(member.email) === target)
}

// --- pending list: optimistic revoke and rollback ---

function sortedInvitations(list: readonly PendingInvitation[]): PendingInvitation[] {
  return [...list].sort((a, b) => {
    if (a.expires_at !== b.expires_at) {
      return a.expires_at < b.expires_at ? -1 : 1
    }
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
  })
}

/** The row disappears before the answer; calling it again is a no-op. */
export function applyRevoke(
  list: readonly PendingInvitation[],
  invitationId: string,
): PendingInvitation[] {
  return list.filter((row) => row.id !== invitationId)
}

/**
 * Puts a revoked invitation back exactly where it was (sorted by expiry and id)
 * when the server rejects the DELETE. Rolling back twice never duplicates it.
 */
export function rollbackRevoke(
  list: readonly PendingInvitation[],
  invitation: PendingInvitation,
): PendingInvitation[] {
  if (list.some((row) => row.id === invitation.id)) {
    return sortedInvitations(list)
  }
  return sortedInvitations([...list, invitation])
}

// --- the one-time link ---

/**
 * The link of the creation answer, while it is shown. The API returns it only
 * in that response, so an empty link is `null` and never an empty box.
 */
export function linkOnceFrom(created: CreatedInvitation): LinkOnceState {
  const link = created.link.trim()
  return link === '' ? null : { link, emailSent: created.email_sent }
}

/** Dismissing the link is forever: it cannot be requested again. */
export function dismissLinkOnce(): LinkOnceState {
  return null
}

/** What happened with the email of the invitation (best-effort on the server). */
export function linkDeliveryNotice(emailSent: boolean): string {
  return emailSent
    ? 'Te enviamos el enlace por correo.'
    : 'No pudimos enviar el correo: compartí el enlace por WhatsApp o a mano.'
}

export type ClipboardLike = { writeText(text: string): Promise<void> }

/**
 * Copies the link with the async clipboard. `false` means the browser refused or
 * has no clipboard: the page then shows the link selected so it can be copied by
 * hand. The link is never written to storage or to the console.
 */
export async function copyInvitationLink(
  link: string,
  clipboard?: ClipboardLike | null,
): Promise<boolean> {
  const value = link.trim()
  if (value === '') {
    return false
  }
  const api =
    clipboard ?? (typeof navigator !== 'undefined' ? (navigator.clipboard as ClipboardLike) : null)
  if (!api || typeof api.writeText !== 'function') {
    return false
  }
  try {
    await api.writeText(value)
    return true
  } catch {
    return false
  }
}

/** Where the new member lands after accepting: professional → Mi agenda. */
export function invitationLandingPath(actorRole: Role | null | undefined): string {
  return panelHomePath(actorRole) ?? '/panel'
}

/** The server sentence verbatim (a 403 is not rewritten: it is the contract's). */
export function invitationErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return fallback
}

// --- reading the payloads of the contract ---

function normalizePending(value: unknown): PendingInvitation | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  if (id === '') {
    return null
  }
  return {
    id,
    email: text(value['email']),
    role: role(value['role']),
    expires_at: text(value['expires_at']),
  }
}

/**
 * Reads `{invitations:[…]}` (or a bare array). Only the four fields of the
 * contract are copied, so a link or a token a server might leak never reaches
 * the screen.
 */
export function normalizePendingInvitations(raw: unknown): PendingInvitation[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['invitations'])
      ? raw['invitations']
      : []
  const invitations: PendingInvitation[] = []
  for (const row of rows) {
    const invitation = normalizePending(row)
    if (invitation) {
      invitations.push(invitation)
    }
  }
  return sortedInvitations(invitations)
}

/** Reads the 201 of the creation: the invitation, the one-time link, the email flag. */
export function normalizeCreatedInvitation(raw: unknown): CreatedInvitation {
  if (!isRecord(raw)) {
    return { invitation: null, link: '', email_sent: false }
  }
  return {
    invitation: normalizePending(raw['invitation']),
    link: text(raw['link']),
    email_sent: raw['email_sent'] === true,
  }
}

/** Reads the public lookup; `null` when the answer is not a usable invitation. */
export function normalizeInvitationLookup(raw: unknown): InvitationLookup | null {
  if (!isRecord(raw)) {
    return null
  }
  const salonName = text(raw['salon_name'])
  if (salonName === '') {
    return null
  }
  return {
    salon_name: salonName,
    role: role(raw['role']),
    role_label: invitationRoleLabel({ role: raw['role'], role_label: raw['role_label'] }),
    // As received: masked by the server, never rebuilt in the browser.
    email_hint: emailHintLabel(raw['email_hint']),
    expires_at: text(raw['expires_at']),
    has_account: raw['has_account'] === true,
  }
}

// --- API client (contract S-F2g1-3 and S-F2g1-4) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

/**
 * `POST /salons/:id/invitations` → 201. The email travels normalized; `attends`
 * only when the form sends it (the server decides the default of the role).
 * Errors keep the server sentence verbatim: «Esa persona ya es parte del
 * equipo», «Máximo 50 invitaciones pendientes», «No podés invitar con ese rol»,
 * «Email inválido».
 */
export async function createInvitation(
  salonId: string,
  input: { email: string; role: InvitationRole; attends?: boolean },
): Promise<CreatedInvitation> {
  const body: Record<string, unknown> = {
    email: normalizeEmail(input.email),
    role: input.role,
  }
  if (typeof input.attends === 'boolean') {
    body['attends'] = input.attends
  }
  const payload = await request<unknown>(salonPath(salonId, '/invitations'), {
    method: 'POST',
    body,
  })
  return normalizeCreatedInvitation(payload)
}

/** `GET /salons/:id/invitations`: pending invitations, never a token or a link. */
export async function fetchInvitations(
  salonId: string,
  signal?: AbortSignal,
): Promise<PendingInvitation[]> {
  return normalizePendingInvitations(
    await request<unknown>(salonPath(salonId, '/invitations'), { signal }),
  )
}

/** `DELETE /salons/:id/invitations/:iid` → 204 (revoke). */
export async function revokeInvitation(
  salonId: string,
  invitationId: string,
): Promise<void> {
  await request<unknown>(salonPath(salonId, `/invitations/${encodeURIComponent(invitationId)}`), {
    method: 'DELETE',
  })
}

/**
 * `POST /public/invitations/lookup` with the token in the BODY (never the path
 * or the query). A 404 keeps the single generic sentence of the contract and an
 * unusable answer is treated as the same invalid invitation: the page never
 * learns which of the four causes it was.
 */
export async function lookupInvitation(token: string): Promise<InvitationLookup> {
  const payload = await request<unknown>('/api/v1/public/invitations/lookup', {
    method: 'POST',
    body: { token },
    auth: 'none',
  })
  const lookup = normalizeInvitationLookup(payload)
  if (!lookup) {
    throw new ApiError(200, INVITATION_MESSAGES.invalid)
  }
  return lookup
}

/**
 * `POST /public/invitations/accept` creates the account and returns the session
 * like the login (`{token}`). The name travels trimmed; the password verbatim.
 */
export async function acceptInvitation(
  token: string,
  name: string,
  password: string,
): Promise<string> {
  const payload = await request<unknown>('/api/v1/public/invitations/accept', {
    method: 'POST',
    body: { token, name: name.trim(), password },
    auth: 'none',
  })
  const jwt = isRecord(payload) ? text(payload['token']) : ''
  if (jwt === '') {
    throw new ApiError(200, 'El servidor aceptó la invitación pero no devolvió la sesión.')
  }
  return jwt
}

/**
 * `POST /invitations/accept` (authenticated) joins the logged-in account. The
 * API rejects a different email with 403 «Esta invitación es para otra cuenta».
 */
export async function acceptInvitationSignedIn(token: string): Promise<void> {
  await request<unknown>('/api/v1/invitations/accept', { method: 'POST', body: { token } })
}
