/**
 * Identity of the current session: `GET /api/v1/me` returns the user and the
 * salons they belong to, so the panel never asks anyone to paste a salon id.
 * Pure normalisation lives here; the HTTP call is in `api.ts`.
 */

import type { AuthUser } from '../types'

export type SalonRole = 'owner' | 'admin' | 'professional'

export type SalonMembership = {
  id: string
  slug: string
  name: string
  role: SalonRole
}

export type Me = {
  user: AuthUser
  salons: SalonMembership[]
}

const ROLES: readonly SalonRole[] = ['owner', 'admin', 'professional']

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function role(value: unknown): SalonRole {
  return ROLES.includes(value as SalonRole) ? (value as SalonRole) : 'professional'
}

function normalizeSalon(value: unknown): SalonMembership | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  if (!id) {
    return null
  }
  return {
    id,
    slug: text(value['slug']),
    name: text(value['name']) || id,
    role: role(value['role']),
  }
}

/** Reads `{user, salons}`; returns null when there is no usable user id. */
export function normalizeMe(raw: unknown): Me | null {
  if (!isRecord(raw) || !isRecord(raw['user'])) {
    return null
  }
  const user = raw['user']
  const id = text(user['id'])
  if (!id) {
    return null
  }
  const salons: SalonMembership[] = []
  const rows = Array.isArray(raw['salons']) ? raw['salons'] : []
  for (const row of rows) {
    const salon = normalizeSalon(row)
    if (salon) {
      salons.push(salon)
    }
  }
  return {
    user: { id, name: text(user['name']), email: text(user['email']) },
    salons,
  }
}

/**
 * The active salon: the stored one while the user still belongs to it, else the
 * first salon of the account (the contract does not define an ordering flag).
 */
export function pickSalonId(
  currentSalonId: string | null,
  salons: readonly SalonMembership[],
): string | null {
  if (currentSalonId && salons.some((salon) => salon.id === currentSalonId)) {
    return currentSalonId
  }
  return salons[0]?.id ?? null
}
