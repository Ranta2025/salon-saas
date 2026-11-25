/**
 * Activity log of the salon (contract F10, D2-D4, D13). Pure rules plus the thin API
 * client: `/panel/actividad` renders what these return. The server is the authority
 * on what is recorded, who may read it and the order; this module only labels,
 * pages and formats it.
 *
 * Online only (D13): nothing here is cached or queued. A body that is not the
 * documented shape is an error, never an empty list: «no hay actividad» must mean
 * the server said so.
 */

import { ApiError, request } from './api.ts'
import { toDateInputValue, formatTimeOfDay, TIME_ZONE } from './datetime.ts'

/** Rows per request (D3: default 50, maximum 100). */
export const AUDIT_PAGE_SIZE = 50

export type AuditActionEntry = { action: string; label: string }

/**
 * Every action of the contract (D2 middleware + D4 two-factor service), exact names,
 * with its Spanish sentence (past tense, neutral). An action the server adds later
 * and this list does not know is shown with its raw name (`auditActionLabel`).
 */
export const AUDIT_ACTIONS: readonly AuditActionEntry[] = [
  { action: 'member.add', label: 'Agregó a una persona al equipo' },
  { action: 'member.update', label: 'Editó a un miembro del equipo' },
  { action: 'member.remove', label: 'Dio de baja a un miembro del equipo' },
  { action: 'member.reactivate', label: 'Reactivó a un miembro del equipo' },
  { action: 'member.services', label: 'Cambió los servicios de un miembro' },
  { action: 'member.pay_model', label: 'Cambió el modelo de pago de un miembro' },
  { action: 'invitation.create', label: 'Creó una invitación al equipo' },
  { action: 'invitation.revoke', label: 'Revocó una invitación al equipo' },
  { action: 'payroll_run.create', label: 'Creó una liquidación' },
  { action: 'payroll_run.adjust', label: 'Ajustó una liquidación' },
  { action: 'payroll_run.pay', label: 'Pagó una liquidación' },
  { action: 'payroll_run.delete', label: 'Borró una liquidación' },
  { action: 'cash_close.create', label: 'Hizo un cierre de caja' },
  { action: 'expense.create', label: 'Registró un gasto' },
  { action: 'expense.update', label: 'Editó un gasto' },
  { action: 'expense.delete', label: 'Borró un gasto' },
  { action: 'export.sales', label: 'Exportó las ventas' },
  { action: 'export.clients', label: 'Exportó las clientas' },
  { action: 'export.expenses', label: 'Exportó los gastos' },
  { action: 'settings.update', label: 'Cambió los ajustes del salón' },
  { action: 'profile.update', label: 'Editó el perfil del salón' },
  { action: 'loyalty.program', label: 'Cambió el programa de fidelidad' },
  { action: 'product.adjust', label: 'Ajustó el stock de un producto' },
  { action: 'auth.2fa_enabled', label: 'Activó la verificación en dos pasos' },
  { action: 'auth.2fa_disabled', label: 'Desactivó la verificación en dos pasos' },
]

const LABELS = new Map(AUDIT_ACTIONS.map((entry) => [entry.action, entry.label]))

/** Sentence of an action; an unknown one keeps its raw name, an empty one says so. */
export function auditActionLabel(action: string): string {
  const key = action.trim()
  if (key === '') {
    return 'Acción desconocida'
  }
  return LABELS.get(key) ?? key
}

export type AuditFilterOption = { value: string; label: string }

/** Options of the action filter: «Todas» (no filter) and every known action. */
export const AUDIT_FILTER_OPTIONS: readonly AuditFilterOption[] = [
  { value: '', label: 'Todas' },
  ...AUDIT_ACTIONS.map((entry) => ({ value: entry.action, label: entry.label })),
]

// --- types and normalizers ---

export type AuditEvent = {
  id: string
  action: string
  /** Id of the affected record, when the action has one. Shown small. */
  entity_id: string | null
  actor_user_id: string
  /** Snapshot of the actor's name at the time of the event (may be empty). */
  actor_name: string
  created_at: string
}

export type AuditPage = {
  events: AuditEvent[]
  /** Opaque cursor for the next (older) page; `null` = no more. */
  next_cursor: string | null
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function normalizeEvent(raw: unknown): AuditEvent | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const action = text(raw['action'])
  const createdAt = text(raw['created_at'])
  if (id === '' || action === '' || createdAt === '' || Number.isNaN(Date.parse(createdAt))) {
    return null
  }
  const entity = text(raw['entity_id'])
  return {
    id,
    action,
    entity_id: entity === '' ? null : entity,
    actor_user_id: text(raw['actor_user_id']),
    actor_name: text(raw['actor_name']),
    created_at: createdAt,
  }
}

/** `GET /audit-log` -> `{events, next_cursor}`. A body of another shape is `null`. */
export function normalizeAuditPage(raw: unknown): AuditPage | null {
  if (!isRecord(raw) || !Array.isArray(raw['events'])) {
    return null
  }
  const events: AuditEvent[] = []
  for (const item of raw['events'] as unknown[]) {
    const parsed = normalizeEvent(item)
    if (parsed) {
      events.push(parsed)
    }
  }
  const cursor = text(raw['next_cursor'])
  return { events, next_cursor: cursor === '' ? null : cursor }
}

/**
 * Appends the next page to what is on screen without repeating an id (an event
 * written between two requests can shift a page boundary). The server order
 * (newest first) is kept and the copy already on screen wins. Inputs are not mutated.
 */
export function mergeAuditEvents(
  current: readonly AuditEvent[],
  incoming: readonly AuditEvent[],
): AuditEvent[] {
  const seen = new Set<string>()
  const merged: AuditEvent[] = []
  for (const item of [...current, ...incoming]) {
    if (!seen.has(item.id)) {
      seen.add(item.id)
      merged.push(item)
    }
  }
  return merged
}

// --- presentation ---

/** Name of whoever did it; the snapshot can be empty. */
export function actorLabel(event: Pick<AuditEvent, 'actor_name'>): string {
  const name = event.actor_name.trim()
  return name === '' ? 'Persona sin nombre' : name
}

function usableZone(timeZone: string): string {
  const candidate = typeof timeZone === 'string' ? timeZone.trim() : ''
  if (candidate === '') {
    return TIME_ZONE
  }
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: candidate })
    return candidate
  } catch {
    return TIME_ZONE
  }
}

/** `2026-11-02 20:30`: local date and time in the zone of the SALON (24 h). */
export function formatAuditWhen(iso: string, timeZone: string): string {
  if (Number.isNaN(Date.parse(iso))) {
    return '—'
  }
  const zone = usableZone(timeZone)
  return `${toDateInputValue(new Date(iso), zone)} ${formatTimeOfDay(iso, zone)}`
}

/** Sentence of an empty list; a filter is named so it is not mistaken for «no activity at all». */
export function auditEmptyMessage(action: string): string {
  const key = action.trim()
  if (key === '') {
    return 'Todavía no hay actividad registrada.'
  }
  return `No hay actividad de «${auditActionLabel(key)}». Probá con «Todas».`
}

// --- API client ---

export type AuditQuery = {
  /** Exact action (or prefix, per D3); empty/absent = all. */
  action?: string | null
  /** Opaque cursor of the previous page. */
  before?: string | null
  signal?: AbortSignal
}

/** Path of one page: salon scoped, `limit` always, `before` and `action` only when set. */
export function auditLogPath(salonId: string, query: Pick<AuditQuery, 'action' | 'before'>): string {
  const params = [`limit=${AUDIT_PAGE_SIZE}`]
  const before = query.before?.trim() ?? ''
  if (before !== '') {
    params.push(`before=${encodeURIComponent(before)}`)
  }
  const action = query.action?.trim() ?? ''
  if (action !== '') {
    params.push(`action=${encodeURIComponent(action)}`)
  }
  return `/api/v1/salons/${encodeURIComponent(salonId)}/audit-log?${params.join('&')}`
}

/** `GET /salons/:id/audit-log` (owner/admin). Server sentences reach the caller verbatim. */
export async function fetchAuditLog(salonId: string, query: AuditQuery = {}): Promise<AuditPage> {
  const page = normalizeAuditPage(
    await request<unknown>(auditLogPath(salonId, query), query.signal ? { signal: query.signal } : {}),
  )
  if (!page) {
    throw new ApiError(200, 'La API no devolvió el registro de actividad.')
  }
  return page
}
