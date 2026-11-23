/**
 * Trade and services per professional (contract F2c, S-F2c-6 and S-F2c-7).
 * Pure helpers: `/panel/equipo` and `/panel/servicios` render what these return
 * and the server stays the authority. A trade is visible text, not a permission;
 * a booking is only offered to a bookable professional (F2a) who does the
 * service, so the count of who does each service decides the notices.
 */

import { memberAttends } from './attends.ts'
import type { SalonMember } from './members.ts'
import { isManager, type Role } from './permissions.ts'
import { isActiveMember } from './teamStatus.ts'

/** Domain limit of `memberships.trade` (contract S-F2c-2). */
export const TRADE_MAX_LENGTH = 40

/** Copy of the notice of a service nobody does (S-F2c-7). */
export const NOBODY_DOES_SERVICE_NOTICE = 'Nadie hace este servicio: no se ofrecen turnos'

/** Copy of the hint of a bookable professional without services. */
export const NO_SERVICES_HINT = 'Todavía no hace ningún servicio: no recibirá turnos'

/** Gift of the form: barbero, estilista, colorista, manicurista… */
export const TRADE_PLACEHOLDER = 'Barbero, estilista, colorista…'

/**
 * Reads a trade the way the server does: trimmed, without line breaks and cut at
 * 40 characters. A text that arrives dirty is repaired instead of rejected.
 */
export function normalizeTrade(value: string): string {
  return value
    .replace(/[\r\n\t]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, TRADE_MAX_LENGTH)
    .trimEnd()
}

/**
 * Reads a list of service ids (the ones a member does, or the `professional_ids`
 * of a new service): unique trimmed strings, everything else dropped.
 */
export function normalizeServiceIds(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return []
  }
  const ids: string[] = []
  for (const row of value) {
    if (typeof row !== 'string') {
      continue
    }
    const id = row.trim()
    if (id !== '' && !ids.includes(id)) {
      ids.push(id)
    }
  }
  return ids
}

type WorkMember = Pick<SalonMember, 'role' | 'attends' | 'active' | 'ended_at'>

/**
 * Who can edit the trade of a person: only owner/admin, and only for someone who
 * attends clients (a professional or an owner/admin with `attends`).
 */
export function canEditTrade(actorRole: Role | null | undefined, member: WorkMember): boolean {
  return isManager(actorRole) && memberAttends(member)
}

/** Same rule for the services: only owner/admin, only who attends clients. */
export function canEditServices(actorRole: Role | null | undefined, member: WorkMember): boolean {
  return isManager(actorRole) && memberAttends(member)
}

/**
 * Bookable members of the list: a professional always, an owner/admin only with
 * `attends`. Reception, stale flags and everybody who left the team never count
 * (F2a, F2g2), so every picker of professionals is built on this filter.
 */
export function bookableMembers(members: readonly SalonMember[]): SalonMember[] {
  return members.filter((member) => isActiveMember(member) && memberAttends(member))
}

/**
 * The professionals that make a service bookable: bookable members whose
 * `service_ids` include it. An empty id selects nobody.
 */
export function professionalsForService(
  members: readonly SalonMember[],
  serviceId: string,
): SalonMember[] {
  const id = serviceId.trim()
  if (id === '') {
    return []
  }
  return bookableMembers(members).filter((member) => member.service_ids.includes(id))
}

/** Count of professionals that do a service, read from `GET /members`. */
export function professionalCount(members: readonly SalonMember[], serviceId: string): number {
  return professionalsForService(members, serviceId).length
}

/** True when no bookable professional does the service: no slots are offered. */
export function nobodyDoesService(members: readonly SalonMember[], serviceId: string): boolean {
  return professionalCount(members, serviceId) === 0
}

/** «1 profesional lo hace», «3 profesionales lo hacen», «Ningún profesional lo hace». */
export function professionalsCountLabel(count: number): string {
  if (!Number.isFinite(count) || count <= 0) {
    return 'Ningún profesional lo hace'
  }
  if (count === 1) {
    return '1 profesional lo hace'
  }
  return `${Math.trunc(count)} profesionales lo hacen`
}

/**
 * Optimistic update and its roll-back: returns a new list with the services of
 * one member changed. Calling it again with the previous ids undoes the change
 * without touching anybody else.
 */
export function applyMemberServices(
  members: readonly SalonMember[],
  userId: string,
  serviceIds: readonly string[],
): SalonMember[] {
  const ids = normalizeServiceIds(serviceIds)
  return members.map((member) => (member.id === userId ? { ...member, service_ids: ids } : member))
}
