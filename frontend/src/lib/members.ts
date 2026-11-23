/**
 * Team of the salon (`GET/POST/DELETE /api/v1/salons/:id/members`). Only owner
 * and admin reach this endpoint; the UI still guards the page so a professional
 * sees an explanation instead of a 403.
 */

import { request } from './api.ts'
import type { SalonRole } from './me.ts'
import { normalizeServiceIds, normalizeTrade } from './tradeServices.ts'
import { normalizeMemberHours, type DaySegment, type MemberHours } from './weeklyHours.ts'

export type SalonMember = {
  id: string
  name: string
  email: string
  role: SalonRole
  /**
   * `memberships.attends` (F2a): whether an owner or admin also attends clients.
   * A professional always attends, so the flag is read through
   * `memberAttends()` instead of being trusted on its own.
   */
  attends: boolean
  /**
   * `memberships.trade` (F2c): visible craft (barbero, estilista…). It is text,
   * not a permission; it is trimmed and capped at 40 characters like the server.
   */
  trade: string
  /** Services of `professional_services` that this person does (empty is valid). */
  service_ids: string[]
  /**
   * `active` (F2g2): false once the person was given de baja (`ended_at` set). A
   * payload without the flag counts as active, so an older server keeps working;
   * every picker filters with `isActiveMember`.
   */
  active: boolean
  /** `memberships.ended_at` (F2g2): ISO date of the leave, or `null`. */
  ended_at: string | null
}

const ROLES: readonly SalonRole[] = ['owner', 'admin', 'professional', 'reception']

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function role(value: unknown): SalonRole {
  return ROLES.includes(value as SalonRole) ? (value as SalonRole) : 'professional'
}

/**
 * Reads `attends`. A payload that omits it (or sends something else) falls back
 * to the domain rule: a professional always attends, everyone else does not.
 */
function attends(value: unknown, role: SalonRole): boolean {
  if (typeof value === 'boolean') {
    return value
  }
  return role === 'professional'
}

/**
 * Reads `ended_at`: the ISO date of the leave, or `null`. A value that is not a
 * date is dropped instead of printed as a day nobody can trust (F2g2).
 */
function endedAt(value: unknown): string | null {
  const iso = text(value)
  if (iso === '' || Number.isNaN(new Date(iso).getTime())) {
    return null
  }
  return iso
}

/**
 * Reads `active`. A date of leave always means the person left; without one, a
 * missing or malformed flag counts as active (an older server does not send it).
 */
function active(value: unknown, left: string | null): boolean {
  if (left !== null) {
    return false
  }
  return typeof value === 'boolean' ? value : true
}

function normalizeMember(value: unknown): SalonMember | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  if (id === '') {
    return null
  }
  const memberRole = role(value['role'])
  const left = endedAt(value['ended_at'])
  return {
    id,
    name: text(value['name']) || text(value['email']) || 'Persona sin nombre',
    email: text(value['email']),
    role: memberRole,
    attends: attends(value['attends'], memberRole),
    trade: normalizeTrade(text(value['trade'])),
    service_ids: normalizeServiceIds(value['service_ids']),
    active: active(value['active'], left),
    ended_at: left,
  }
}

/** Reads `{members:[…]}` (or a bare array) and drops rows without id. */
export function normalizeMembers(raw: unknown): SalonMember[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['members'])
      ? raw['members']
      : []
  const members: SalonMember[] = []
  for (const row of rows) {
    const member = normalizeMember(row)
    if (member) {
      members.push(member)
    }
  }
  return members
}

/** Emails are sent trimmed and in lowercase, like the server stores them. */
export function normalizeMemberEmail(email: string): string {
  return email.trim().toLowerCase()
}

function membersPath(salonId: string): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}/members`
}

export async function fetchMembers(salonId: string): Promise<SalonMember[]> {
  return normalizeMembers(await request<unknown>(membersPath(salonId)))
}

export async function addMember(
  salonId: string,
  input: { email: string; role: SalonRole },
): Promise<SalonMember> {
  const body = await request<unknown>(membersPath(salonId), {
    method: 'POST',
    body: { email: normalizeMemberEmail(input.email), role: input.role },
  })
  const member = normalizeMember(body)
  if (!member) {
    throw new Error('El servidor agregó a la persona pero no devolvió sus datos.')
  }
  return member
}

/**
 * Marks whether an owner or admin also attends clients
 * (`PATCH /salons/:id/members/:uid`, contract S-F2a-5). A 400 keeps the server
 * sentence verbatim: «Las profesionales siempre atienden», «Ese rol no atiende
 * clientes».
 */
export async function setMemberAttends(
  salonId: string,
  userId: string,
  attends: boolean,
): Promise<SalonMember> {
  const body = await request<unknown>(`${membersPath(salonId)}/${encodeURIComponent(userId)}`, {
    method: 'PATCH',
    body: { attends },
  })
  const member = normalizeMember(body)
  if (!member) {
    throw new Error('El servidor guardó el cambio pero no devolvió los datos de la persona.')
  }
  return member
}

/**
 * Saves the trade of a bookable member
 * (`PATCH /salons/:id/members/:uid`, contract S-F2c-3). A 400 keeps the server
 * sentence verbatim: «Ese rol no tiene oficio».
 */
export async function setMemberTrade(
  salonId: string,
  userId: string,
  trade: string,
): Promise<SalonMember> {
  const body = await request<unknown>(`${membersPath(salonId)}/${encodeURIComponent(userId)}`, {
    method: 'PATCH',
    body: { trade: normalizeTrade(trade) },
  })
  const member = normalizeMember(body)
  if (!member) {
    throw new Error('El servidor guardó el oficio pero no devolvió los datos de la persona.')
  }
  return member
}

/**
 * Replaces the services a member does
 * (`PUT /salons/:id/members/:uid/services`, contract S-F2c-3). The empty list is
 * valid; a 400 keeps the server sentence verbatim: «Servicio inválido».
 */
export async function setMemberServices(
  salonId: string,
  userId: string,
  serviceIds: readonly string[],
): Promise<string[]> {
  const body = await request<unknown>(
    `${membersPath(salonId)}/${encodeURIComponent(userId)}/services`,
    { method: 'PUT', body: { service_ids: normalizeServiceIds(serviceIds) } },
  )
  if (!isRecord(body) || !Array.isArray(body['service_ids'])) {
    throw new Error('El servidor guardó los servicios pero no devolvió la lista.')
  }
  return normalizeServiceIds(body['service_ids'])
}

// --- weekly hours of one professional (F2d1) ---

function memberHoursPath(salonId: string, userId: string): string {
  return `${membersPath(salonId)}/${encodeURIComponent(userId)}/hours`
}

/**
 * `GET /salons/:id/members/:uid/hours` (contract S-F2d1-4). No rows means the
 * professional follows the salon schedule.
 */
export async function fetchMemberHours(
  salonId: string,
  userId: string,
): Promise<MemberHours> {
  return normalizeMemberHours(
    await request<unknown>(memberHoursPath(salonId, userId)),
  )
}

/**
 * `PUT /salons/:id/members/:uid/hours`, atomic replacement. The empty list
 * means "same as the salon" (S-F2d1-2) and a `400` keeps the server sentence
 * verbatim («Los tramos de un mismo día no pueden solaparse»).
 */
export async function saveMemberHours(
  salonId: string,
  userId: string,
  segments: readonly DaySegment[],
): Promise<MemberHours> {
  const body = await request<unknown>(memberHoursPath(salonId, userId), {
    method: 'PUT',
    body: {
      segments: segments.map((segment) => ({
        weekday: segment.weekday,
        start: segment.start,
        end: segment.end,
      })),
    },
  })
  return normalizeMemberHours(body)
}

/**
 * Gives a member de baja (`DELETE /salons/:id/members/:uid`, contract S-F2g2-4).
 * The route still answers 204 and now keeps the row with `ended_at`, so the
 * history survives. A 409 keeps the Spanish sentence verbatim: «Tiene 2 turnos
 * pendientes: reasignalos o cancelalos antes de darla de baja».
 */
export async function deactivateMember(salonId: string, userId: string): Promise<void> {
  await request<unknown>(`${membersPath(salonId)}/${encodeURIComponent(userId)}`, {
    method: 'DELETE',
  })
}

/**
 * Brings a member back (`POST /salons/:id/members/:uid/reactivate`, S-F2g2-4):
 * the role, `attends` and `trade` of before are kept. A 409 keeps the sentence
 * verbatim: «Esa persona ya está en el equipo».
 */
export async function reactivateMember(
  salonId: string,
  userId: string,
): Promise<SalonMember> {
  const body = await request<unknown>(
    `${membersPath(salonId)}/${encodeURIComponent(userId)}/reactivate`,
    { method: 'POST' },
  )
  const member = normalizeMember(body)
  if (!member) {
    throw new Error('El servidor reactivó a la persona pero no devolvió sus datos.')
  }
  return member
}
