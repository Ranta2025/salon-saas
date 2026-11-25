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
 * Editing an existing client file (contract F5, D5): the same rule as creating
 * one — `puedeGestionarClientes` on the server, which also answers 403 to anyone
 * else. A professional reads the file (allergies included) but never edits it.
 */
export function canEditClient(role: Role | null | undefined): boolean {
  return canCreateClient(role)
}

/**
 * Money of a client history (contract F5, D6: `puedeVerPlata`): owner, admin and
 * reception. The server sends `spend` and `sales` as null to everyone else, so
 * this only decides the explanatory copy, never a figure.
 */
export function canSeeClientMoney(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'reception'
}

/**
 * Inventory of the salon (contract F6, D7): owner, admin and reception read the
 * list, the price and the stock. A professional gets 403 from the server, so the
 * page shows `ManagerOnlyNotice` instead of the list.
 */
export function canViewInventory(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'reception'
}

/**
 * Creating and editing products, adjusting the stock and reading the movements of
 * a product are only for owner and admin (contract F6, D7). Reception sees the
 * list without a single write control.
 */
export function canManageInventory(role: Role | null | undefined): boolean {
  return isManager(role)
}

/**
 * Waiting list (contract F7, D8): owner, admin and reception read the avisos and
 * can remove one. A professional gets 403 from the server, so the page shows a
 * `ManagerOnlyNotice`-style explanation instead of the table, and the menu entry
 * stays hidden.
 */
export function canViewWaitlist(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'reception'
}

/**
 * Prepaid packages (contract F8, D8/D10): owner, admin and reception read the
 * catalog and the balances of a client, sell a package and take a session off it.
 * A professional gets 403 from the server on every route (the answers carry prices
 * and balances), so the page shows a notice and the menu entry stays hidden.
 */
export function canViewPackages(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'reception'
}

/** Creating and editing the catalog of packages: owner and admin only (D10). */
export function canManagePackages(role: Role | null | undefined): boolean {
  return isManager(role)
}

/**
 * Adjusting the balance of a client package and reading its movements: owner and
 * admin only (D9). Reception sells and redeems but never corrects a balance.
 */
export function canAdjustPackages(role: Role | null | undefined): boolean {
  return isManager(role)
}

/**
 * Home of `/panel` for a role. A professional has no dashboard (the API answers
 * 403) and lands on their own agenda (S-F2b-4). `null` means the dashboard is
 * the right page: owner/admin keep it, with a "Mi agenda" card when they attend.
 */
export function panelHomePath(role: Role | null | undefined): string | null {
  return role === 'professional' ? '/panel/mi-agenda' : null
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

/**
 * «Mi salón» (contract F16, D7): owner, admin and reception read the profile of the
 * salon and its gallery; a professional gets 403 from the server on the profile, so
 * the page shows a notice and the menu entry stays hidden.
 */
export function canViewSalonProfile(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'reception'
}

/** Editing the profile and managing the gallery: owner and admin only (D1, D3). */
export function canEditSalonProfile(role: Role | null | undefined): boolean {
  return isManager(role)
}

/**
 * Loyalty (contract F9, D4-D7): owner, admin and reception read the status of a
 * client, deliver a reward and use it. A professional gets 403 on the client
 * routes (they carry spending data), so the page shows a notice and the menu
 * entry stays hidden.
 */
export function canViewLoyalty(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'reception'
}

/** Editing the program (`PUT /loyalty-program`): owner and admin only (D4). */
export function canEditLoyaltyProgram(role: Role | null | undefined): boolean {
  return isManager(role)
}
