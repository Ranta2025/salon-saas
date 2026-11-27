/**
 * Equipment with capacity (contract F2e, shape of S-F2e-3 and S-F2e-5). Pure
 * rules plus the thin API client: `/panel/equipamiento` and `/panel/servicios`
 * render what these return and the server stays the authority.
 *
 * A service may use a TYPE of equipment (hair post, barber chair, washbasin) and
 * every booking of it occupies one UNIT of that type besides the professional.
 * The server sends the types with their units and how many services use each
 * one; the sentences of a rejected request are printed verbatim, so the copy of
 * the domain limits lives here with the exact API wording.
 */

import { request } from '../../../lib/api.ts'
import { isManager, type Role } from '../../../lib/permissions.ts'
import type { Service } from '../../../types.ts'

/** Domain limit of `resource_types` per salon (S-F2e-2). */
export const MAX_RESOURCE_TYPES = 20

/** Domain limit of `resource_units` per type (S-F2e-2). */
export const MAX_UNITS_PER_TYPE = 50

/** Domain limit of a type or unit name (S-F2e-2). */
export const RESOURCE_NAME_MAX = 60

/** Sentences of the API, shown verbatim (S-F2e-3). */
export const EQUIPMENT_MESSAGES = {
  required: 'El nombre del equipo es obligatorio',
  tooLong: 'El nombre no puede superar los 60 caracteres',
  duplicate: 'Ya existe un equipo con ese nombre',
  typeLimit: 'Máximo 20 tipos de equipo',
  unitLimit: 'Máximo 50 unidades por tipo',
} as const

/** Copy of the notice of a service whose type has no active unit (S-F2e-5). */
export const NO_ACTIVE_EQUIPMENT_NOTICE = 'No hay equipo activo: no se ofrecen turnos'

/** Only the form: the quantity is a whole number between 1 and 50. */
export const EQUIPMENT_COUNT_MESSAGE = 'La cantidad debe ser un número entero entre 1 y 50'

/** One unit of a type (`resource_units`); removing equipment is deactivating it. */
export type ResourceUnit = {
  id: string
  name: string
  active: boolean
}

/** A type with its units and how many services use it (S-F2e-3). */
export type ResourceType = {
  id: string
  name: string
  units: ResourceUnit[]
  services: number
}

/** The whole equipment of the salon, as rendered by every screen. */
export type Equipment = readonly ResourceType[]

/** Result of a local rule: nothing to show or the sentence of the server. */
export type EquipmentCheck = { ok: true } | { ok: false; message: string }

/** Result of validating a name: the normalized value or the sentence. */
export type ResourceNameCheck = { ok: true; name: string } | { ok: false; message: string }

/** Answer of `PATCH /salons/:id/resource-units/:uid`, read tolerantly. */
export type ResourceUnitUpdate = {
  /** `true`/`false` when the server echoed the flag; `null` when it did not. */
  active: boolean | null
  /** Future `booked` bookings using the unit: informative, never a rejection. */
  futureBookings: number
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function count(value: unknown): number {
  return typeof value === 'number' && Number.isInteger(value) && value > 0 ? value : 0
}

/** Owner and admin only; everyone else does not even see the menu entry. */
export function canEditEquipment(role: Role | null | undefined): boolean {
  return isManager(role)
}

function normalizeUnit(value: unknown): ResourceUnit | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  if (id === '') {
    return null
  }
  return {
    id,
    name: text(value['name']) || id,
    // A payload without the flag (or with something else) is an existing unit:
    // the column defaults to true and a unit is only removed by deactivating it.
    active: value['active'] !== false,
  }
}

function normalizeType(value: unknown): ResourceType | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  if (id === '') {
    return null
  }
  const rows = Array.isArray(value['units']) ? value['units'] : []
  const units: ResourceUnit[] = []
  for (const row of rows) {
    const unit = normalizeUnit(row)
    if (unit) {
      units.push(unit)
    }
  }
  return { id, name: text(value['name']) || id, units, services: count(value['services']) }
}

/** Reads `{types:[…]}` (or a bare array) and drops rows without id. */
export function normalizeEquipment(raw: unknown): ResourceType[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['types'])
      ? raw['types']
      : []
  const types: ResourceType[] = []
  for (const row of rows) {
    const type = normalizeType(row)
    if (type) {
      types.push(type)
    }
  }
  return types
}

/** Units of a type that accept a booking (S-F2e-4: only active ones are offered). */
export function activeUnitCount(type: Pick<ResourceType, 'units'>): number {
  return type.units.filter((unit) => unit.active).length
}

/** Type of the equipment by id; `null` when it no longer exists. */
export function resourceType(equipment: Equipment, typeId: string | null | undefined): ResourceType | null {
  const id = (typeId ?? '').trim()
  if (id === '') {
    return null
  }
  return equipment.find((type) => type.id === id) ?? null
}

/** Name of the type of a service; `null` when the service uses no equipment. */
export function resourceTypeName(equipment: Equipment, typeId: string | null | undefined): string | null {
  return resourceType(equipment, typeId)?.name ?? null
}

/** `resource_type_id` of a service row, read defensively (older API: absent). */
export function serviceResourceTypeId(service: Pick<Service, 'resource_type_id'>): string | null {
  const id = service.resource_type_id
  return typeof id === 'string' && id.trim() !== '' ? id.trim() : null
}

/**
 * Rule of S-F2e-4 read from the screen: a service with no equipment always
 * offers slots; a service whose type has no active unit offers none, and so does
 * one whose type was deleted.
 */
export function serviceOffersSlots(
  equipment: Equipment,
  typeId: string | null | undefined,
): boolean {
  const id = (typeId ?? '').trim()
  if (id === '') {
    return true
  }
  const type = equipment.find((row) => row.id === id)
  return type ? activeUnitCount(type) > 0 : false
}

/**
 * Notice of a unit that is being deactivated while future bookings use it
 * (S-F2e-5): they keep their unit, so the copy says so. Nothing to announce
 * when no booking uses it; singular and plural are written out.
 */
export function futureBookingsNotice(futureBookings: number): string | null {
  if (!Number.isFinite(futureBookings) || futureBookings <= 0) {
    return null
  }
  const total = Math.trunc(futureBookings)
  if (total === 1) {
    return '1 turno futuro usa este puesto (lo conserva)'
  }
  return `${total} turnos futuros usan este puesto (los conservan)`
}

/**
 * Reads a name the way the server does: trimmed, without line breaks and
 * collapsed spaces. The checks follow the API order so the message under the
 * field is the one the request would return.
 */
export function validateResourceName(
  value: string,
  siblings: readonly { id?: string; name: string }[] = [],
  selfId = '',
): ResourceNameCheck {
  const name = value
    .replace(/[\r\n\t]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  if (name === '') {
    return { ok: false, message: EQUIPMENT_MESSAGES.required }
  }
  if (name.length > RESOURCE_NAME_MAX) {
    return { ok: false, message: EQUIPMENT_MESSAGES.tooLong }
  }
  const key = name.toLowerCase()
  const duplicate = siblings.some(
    (sibling) => sibling.id !== selfId && sibling.name.trim().toLowerCase() === key,
  )
  if (duplicate) {
    return { ok: false, message: EQUIPMENT_MESSAGES.duplicate }
  }
  return { ok: true, name }
}

/** The salon may not hold more than 20 types of equipment. */
export function canAddType(equipment: Equipment): EquipmentCheck {
  if (equipment.length >= MAX_RESOURCE_TYPES) {
    return { ok: false, message: EQUIPMENT_MESSAGES.typeLimit }
  }
  return { ok: true }
}

/** The quantity of a request is a whole number between 1 and 50. */
export function checkUnitCount(value: number): EquipmentCheck {
  if (!Number.isInteger(value) || value < 1 || value > MAX_UNITS_PER_TYPE) {
    return { ok: false, message: EQUIPMENT_COUNT_MESSAGE }
  }
  return { ok: true }
}

/** A type may not hold more than 50 units: the request is refused, not trimmed. */
export function canAddUnits(type: Pick<ResourceType, 'units'>, value = 1): EquipmentCheck {
  const quantity = checkUnitCount(value)
  if (!quantity.ok) {
    return quantity
  }
  if (type.units.length + value > MAX_UNITS_PER_TYPE) {
    return { ok: false, message: EQUIPMENT_MESSAGES.unitLimit }
  }
  return { ok: true }
}

/**
 * Name of the next unit the server would create: the first free number of the
 * type («<tipo> 1», «<tipo> 2»…, S-F2e-2). Numbers are read from the names that
 * already look like that, so a gap is reused.
 */
export function nextUnitName(type: Pick<ResourceType, 'name' | 'units'>): string {
  const base = type.name.trim()
  const used = new Set<number>()
  const prefix = `${base} `
  for (const unit of type.units) {
    const name = unit.name.trim().toLowerCase()
    if (!name.startsWith(prefix.toLowerCase())) {
      continue
    }
    const suffix = name.slice(prefix.length)
    if (/^\d+$/.test(suffix)) {
      const number = Number(suffix)
      if (Number.isInteger(number) && number > 0) {
        used.add(number)
      }
    }
  }
  let next = 1
  while (used.has(next)) {
    next += 1
  }
  return `${base} ${next}`
}

/**
 * Optimistic state of one unit: the list is painted with the new value before
 * the answer and repeating the call with the previous value is the exact
 * rollback (the original list is never mutated).
 */
export function applyUnitActive(equipment: Equipment, unitId: string, active: boolean): Equipment {
  let changed = false
  const next = equipment.map((type) => {
    if (!type.units.some((unit) => unit.id === unitId && unit.active !== active)) {
      return type
    }
    changed = true
    return {
      ...type,
      units: type.units.map((unit) => (unit.id === unitId ? { ...unit, active } : unit)),
    }
  })
  return changed ? next : equipment
}

/** Reads the answer of `PATCH /resource-units/:uid`: the flag and `future_bookings`. */
export function normalizeResourceUnitUpdate(raw: unknown): ResourceUnitUpdate {
  if (!isRecord(raw)) {
    return { active: null, futureBookings: 0 }
  }
  const unit = isRecord(raw['unit']) ? raw['unit'] : null
  const flag = unit?.['active'] ?? raw['active']
  const future = count(unit?.['future_bookings']) || count(raw['future_bookings'])
  return { active: typeof flag === 'boolean' ? flag : null, futureBookings: future }
}

// --- API client (`/api/v1`, contract S-F2e-3) ---

function resourcesPath(salonId: string): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}/resources`
}

export async function fetchEquipment(salonId: string): Promise<ResourceType[]> {
  return normalizeEquipment(await request<unknown>(resourcesPath(salonId)))
}

/** Creates a type with `count` auto-named units; a `400` keeps its sentence. */
export async function createResourceType(
  salonId: string,
  input: { name: string; count: number },
): Promise<void> {
  await request<unknown>(resourcesPath(salonId), {
    method: 'POST',
    body: { name: input.name, count: input.count },
  })
}

export async function renameResourceType(
  salonId: string,
  typeId: string,
  name: string,
): Promise<void> {
  await request<unknown>(`${resourcesPath(salonId)}/${encodeURIComponent(typeId)}`, {
    method: 'PATCH',
    body: { name },
  })
}

/** Adds `count` units with the next free number of the type. */
export async function addResourceUnits(
  salonId: string,
  typeId: string,
  units: number,
): Promise<void> {
  await request<unknown>(`${resourcesPath(salonId)}/${encodeURIComponent(typeId)}/units`, {
    method: 'POST',
    body: { count: units },
  })
}

/**
 * Renames or activates/deactivates one unit. Deactivating is how equipment is
 * removed: the bookings already scheduled keep their unit (S-F2e-3).
 */
export async function setResourceUnit(
  salonId: string,
  unitId: string,
  patch: { name?: string; active?: boolean },
): Promise<ResourceUnitUpdate> {
  const body: Record<string, unknown> = {}
  if (typeof patch.name === 'string') {
    body['name'] = patch.name
  }
  if (typeof patch.active === 'boolean') {
    body['active'] = patch.active
  }
  return normalizeResourceUnitUpdate(
    await request<unknown>(
      `/api/v1/salons/${encodeURIComponent(salonId)}/resource-units/${encodeURIComponent(unitId)}`,
      { method: 'PATCH', body },
    ),
  )
}

/** Deletes a type; a `409` keeps the sentence of the server verbatim. */
export async function deleteResourceType(salonId: string, typeId: string): Promise<void> {
  await request<unknown>(`${resourcesPath(salonId)}/${encodeURIComponent(typeId)}`, {
    method: 'DELETE',
  })
}
