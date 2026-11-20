/**
 * Salon team of `GET /api/v1/salons/:id/staff`. The POS needs real ids to
 * register a sale, so they come from the server instead of being typed by hand.
 */

export type StaffMember = {
  id: string
  name: string
  role: 'owner' | 'admin' | 'professional'
}

const ROLES: readonly StaffMember['role'][] = ['owner', 'admin', 'professional']

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function role(value: unknown): StaffMember['role'] {
  return ROLES.includes(value as StaffMember['role'])
    ? (value as StaffMember['role'])
    : 'professional'
}

/** Reads `{staff:[…]}` (or a bare array) and drops rows that cannot identify a person. */
export function normalizeStaff(raw: unknown): StaffMember[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['staff'])
      ? raw['staff']
      : []
  const staff: StaffMember[] = []
  for (const row of rows) {
    if (!isRecord(row)) {
      continue
    }
    const id = text(row['id'])
    const name = text(row['name'])
    if (!id || !name) {
      continue
    }
    staff.push({ id, name, role: role(row['role']) })
  }
  return staff
}
