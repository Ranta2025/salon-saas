/**
 * Who attends clients (contract F2a, S-F2a-6). Pure helpers: `/panel/equipo`
 * renders what these return and the server stays the authority. A person is
 * bookable when their role is `professional` or, being owner/admin, when the
 * `attends` flag is on (the owner-barber); reception and clients never attend.
 */

import type { SalonMember } from './members.ts'
import { isManager, type Role } from '../../../lib/permissions.ts'
import { isActiveMember } from './teamStatus.ts'

/** Copy of the notice shown when nobody of the team attends clients. */
export const ATTENDS_NOTICE =
  "Nadie atiende todavía: activá 'Atiende clientes' en alguien para recibir turnos"

export type AttendsMember = Pick<SalonMember, 'role' | 'attends' | 'active' | 'ended_at'>

/**
 * Whether a member shows up in the agenda and the availability. A professional
 * always does — the flag is only meaningful for owner and admin — so a stale
 * payload can never take a professional out of the calendar.
 */
export function memberAttends(member: Pick<AttendsMember, 'role' | 'attends'>): boolean {
  if (member.role === 'professional') {
    return true
  }
  if (member.role === 'owner' || member.role === 'admin') {
    return member.attends === true
  }
  return false
}

/** What the "Atiende" cell shows for a member. */
export type AttendsControl = 'switch' | 'fixed' | 'none'

/**
 * Control of the "Atiende" cell: a switch for owner/admin members (the PATCH of
 * S-F2a-5 only accepts those roles), the fixed "Atiende" label for a
 * professional and nothing for reception.
 */
export function attendsControl(
  actorRole: Role | null | undefined,
  targetRole: Role,
): AttendsControl {
  if (targetRole === 'professional') {
    return 'fixed'
  }
  if (targetRole === 'reception') {
    return 'none'
  }
  return isManager(actorRole) ? 'switch' : 'none'
}

/**
 * True when the team has at least one ACTIVE person and nobody of them attends
 * clients: the salon cannot receive bookings until someone does. Somebody who
 * already left («dar de baja», F2g2) never silences the warning.
 */
export function nobodyAttends(members: readonly AttendsMember[]): boolean {
  const team = members.filter(isActiveMember)
  return team.length > 0 && !team.some(memberAttends)
}

/**
 * Optimistic update and its roll-back: returns a new list with the flag of one
 * member changed. Calling it again with the previous value undoes the change
 * without touching anybody else.
 */
export function applyAttends(
  members: readonly SalonMember[],
  userId: string,
  attends: boolean,
): SalonMember[] {
  return members.map((member) => (member.id === userId ? { ...member, attends } : member))
}
