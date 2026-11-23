/**
 * Giving a member de baja instead of deleting them (contract F2g2, S-F2g2-5).
 * Pure helpers: `/panel/equipo` renders what these return and the server stays
 * the authority. A person who left keeps their row (`ended_at`) so the history of
 * turns and sales survives, and can come back with the same role.
 *
 * Every picker of professionals reads the team through `isActiveMember` (or
 * through `bookableMembers`, which applies it), so somebody who left is never
 * offered as a professional again.
 */

import { expiryDay } from './invitations.ts'
import type { SalonMember } from './members.ts'

/** Copy of the confirmation of a leave (S-F2g2-5): it says what is kept. */
export const DEACTIVATE_CONFIRM_NOTE =
  'Conserva su historial y puede volver a entrar cuando quieras'

/** Copy of the notice after a leave (S-F2g2-5, verbatim). */
export const DEACTIVATE_SUCCESS =
  'La persona ya no tiene acceso al salón. Su historial se conserva.'

/** Note of the confirmation of a reactivation: the person comes back as before. */
export const REACTIVATE_CONFIRM_NOTE = 'Vuelve a tener acceso al salón con el mismo rol.'

export type EndedAtOptions = { timeZone?: string; now?: Date }

/** «¿Dar de baja a Ana?» — the question of the confirmation, with the name. */
export function deactivateConfirmQuestion(name: string): string {
  const who = name.trim()
  return who === '' ? '¿Dar de baja a esta persona?' : `¿Dar de baja a ${who}?`
}

/** «¿Reactivar a Ana?» — the question before bringing somebody back. */
export function reactivateConfirmQuestion(name: string): string {
  const who = name.trim()
  return who === '' ? '¿Reactivar a esta persona?' : `¿Reactivar a ${who}?`
}

/** «Ana vuelve al equipo.» */
export function reactivatedSuccess(name: string): string {
  const who = name.trim()
  return who === '' ? 'La persona vuelve al equipo.' : `${who} vuelve al equipo.`
}

/**
 * Whether a member still belongs to the team. A date of leave always wins over a
 * stale `active` flag, so a half-written payload can never put somebody back in
 * the agenda.
 */
export function isActiveMember(member: Pick<SalonMember, 'active' | 'ended_at'>): boolean {
  return member.active !== false && (member.ended_at ?? null) === null
}

/** The team: who is active, in the order the server sent them. */
export function activeMembers(members: readonly SalonMember[]): SalonMember[] {
  return members.filter(isActiveMember)
}

/**
 * The ex-team, most recent leave first and a leave without a date last; ties keep
 * the order of the server. Deterministic so the block never jumps around.
 */
export function formerMembers(members: readonly SalonMember[]): SalonMember[] {
  return members.filter((member) => !isActiveMember(member)).sort(byLeaveDesc)
}

function leaveTime(member: Pick<SalonMember, 'ended_at'>): number {
  if (member.ended_at === null) {
    return Number.NEGATIVE_INFINITY
  }
  const time = new Date(member.ended_at).getTime()
  return Number.isNaN(time) ? Number.NEGATIVE_INFINITY : time
}

function byLeaveDesc(a: SalonMember, b: SalonMember): number {
  const left = leaveTime(b)
  const right = leaveTime(a)
  if (left === right) {
    return 0
  }
  return left > right ? 1 : -1
}

/** «Ex equipo (3)»: the title of the collapsible block, with its count. */
export function formerTeamLabel(count: number): string {
  const total = Number.isFinite(count) && count > 0 ? Math.trunc(count) : 0
  return `Ex equipo (${total})`
}

/**
 * «Dio de baja el 29 de noviembre» in the zone of the salon (the year only when
 * it is not the current one). `null` with an unusable date, so the block omits
 * the day instead of inventing one.
 */
export function endedOnLabel(endedAt: string | null, options: EndedAtOptions = {}): string | null {
  if (endedAt === null) {
    return null
  }
  const day = expiryDay(endedAt, options)
  return day === null ? null : `Dio de baja el ${day}`
}

/**
 * Optimistic update and its roll-back (`null` brings the person back): returns a
 * new list with the leave of one member changed. The list it receives is never
 * mutated, so the previous value of `ended_at` is the exact rollback.
 */
export function applyMemberEndedAt(
  members: readonly SalonMember[],
  userId: string,
  endedAt: string | null,
): SalonMember[] {
  return members.map((member) =>
    member.id === userId ? { ...member, active: endedAt === null, ended_at: endedAt } : member,
  )
}

/**
 * Exact rollback of a refused leave or reactivation: the row goes back to the
 * snapshot the caller kept, with its `active`, `ended_at` and every other field.
 */
export function restoreMember(
  members: readonly SalonMember[],
  previous: SalonMember,
): SalonMember[] {
  return members.map((member) => (member.id === previous.id ? previous : member))
}
