/**
 * Role permissions of the panel. The server is the authority; these helpers only
 * decide what the UI offers so a professional never sees a button that would 403.
 */

import type { SalonRole } from './me.ts'

export type Role = SalonRole

const ROLE_LABELS: Record<Role, string> = {
  owner: 'Dueña o dueño',
  admin: 'Administración',
  professional: 'Profesional',
  reception: 'Recepción',
}

export function roleLabel(role: Role | null | undefined): string {
  return role ? ROLE_LABELS[role] : 'Sin rol'
}

/** Owner and admin: settings, team and the whole agenda. */
export function isManager(role: Role | null | undefined): boolean {
  return role === 'owner' || role === 'admin'
}

export function canManageMembers(role: Role | null | undefined): boolean {
  return isManager(role)
}

export function canEditSettings(role: Role | null | undefined): boolean {
  return isManager(role)
}

/** Owner, admin and reception create client files by hand; a professional does not. */
export function canCreateClient(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'reception'
}

/**
 * Home of `/panel` for a role. A professional has no dashboard (the API answers
 * 403), so the index sends them straight to the agenda instead of asking for it.
 * `null` means the dashboard is the right page.
 */
export function panelHomePath(role: Role | null | undefined): string | null {
  return role === 'professional' ? '/panel/calendario' : null
}

/**
 * Owner/admin act on every booking of the salon; a professional only on their
 * own (`/me` id compared with the `professional_id` of the booking).
 */
export function canManageBooking(
  role: Role | null | undefined,
  actorUserId: string | null | undefined,
  bookingProfessionalId: string,
): boolean {
  // The API lets owner, admin and reception change any booking of the salon.
  if (isManager(role) || role === 'reception') {
    return true
  }
  if (role !== 'professional') {
    return false
  }
  const actor = actorUserId?.trim() ?? ''
  return actor !== '' && actor === bookingProfessionalId
}

/** Only the owner may add another admin (contract of POST /members). */
export function canAssignRole(
  actorRole: Role | null | undefined,
  targetRole: Role,
): boolean {
  if (actorRole === 'owner') {
    return true
  }
  if (actorRole === 'admin') {
    return targetRole === 'professional'
  }
  return false
}

/** Role of the active salon; null when the user does not belong to it. */
export function activeRole(
  salons: readonly { id: string; role: Role }[],
  salonId: string | null,
): Role | null {
  if (!salonId) {
    return null
  }
  return salons.find((salon) => salon.id === salonId)?.role ?? null
}

/** Roles the actor may choose in the "add member" form. */
export function assignableRoles(actorRole: Role | null | undefined): Role[] {
  return (['professional', 'admin', 'owner'] as const).filter((role) =>
    canAssignRole(actorRole, role),
  )
}
