/**
 * Team of the salon (`GET/POST/DELETE /api/v1/salons/:id/members`). Only owner
 * and admin reach this endpoint; the UI still guards the page so a professional
 * sees an explanation instead of a 403.
 */

import { request } from './api.ts'
import type { SalonRole } from './me.ts'

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

function normalizeMember(value: unknown): SalonMember | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  if (id === '') {
    return null
  }
  const memberRole = role(value['role'])
  return {
    id,
    name: text(value['name']) || text(value['email']) || 'Persona sin nombre',
    email: text(value['email']),
    role: memberRole,
    attends: attends(value['attends'], memberRole),
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

export async function removeMember(salonId: string, userId: string): Promise<void> {
  await request<unknown>(`${membersPath(salonId)}/${encodeURIComponent(userId)}`, {
    method: 'DELETE',
  })
}
