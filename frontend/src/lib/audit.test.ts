import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError } from './api.ts'
import {
  AUDIT_ACTIONS,
  AUDIT_FILTER_OPTIONS,
  AUDIT_PAGE_SIZE,
  actorLabel,
  auditActionLabel,
  auditEmptyMessage,
  auditLogPath,
  fetchAuditLog,
  formatAuditWhen,
  mergeAuditEvents,
  normalizeAuditPage,
  type AuditEvent,
} from './audit.ts'
import { canViewAuditLog } from './permissions.ts'
import { clearToken, setToken } from './session.ts'

type Call = { url: string; method: string; headers: Record<string, string> }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers: (init?.headers ?? {}) as Record<string, string>,
    }
    calls.push(call)
    return handler(call)
  }) as typeof globalThis.fetch
  return {
    calls,
    restore: () => {
      globalThis.fetch = original
    },
  }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function event(id: string, over: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id,
    action: 'expense.create',
    entity_id: 'exp-1',
    actor_user_id: 'u1',
    actor_name: 'Ana',
    created_at: '2026-11-03T18:00:00Z',
    ...over,
  }
}

// Every action of contract D2 and D4, exact names.
const CONTRACT_ACTIONS = [
  'member.add',
  'member.update',
  'member.remove',
  'member.reactivate',
  'member.services',
  'member.pay_model',
  'invitation.create',
  'invitation.revoke',
  'payroll_run.create',
  'payroll_run.adjust',
  'payroll_run.pay',
  'payroll_run.delete',
  'cash_close.create',
  'expense.create',
  'expense.update',
  'expense.delete',
  'export.sales',
  'export.clients',
  'export.expenses',
  'settings.update',
  'profile.update',
  'loyalty.program',
  'product.adjust',
  'auth.2fa_enabled',
  'auth.2fa_disabled',
]

// --- permissions ---

test('canViewAuditLog: owner and admin only', () => {
  assert.equal(canViewAuditLog('owner'), true)
  assert.equal(canViewAuditLog('admin'), true)
  assert.equal(canViewAuditLog('reception'), false)
  assert.equal(canViewAuditLog('professional'), false)
  assert.equal(canViewAuditLog(null), false)
  assert.equal(canViewAuditLog(undefined), false)
})

// --- labels ---

test('every action of D2 and D4 has a Spanish label (and nothing else is invented)', () => {
  assert.deepEqual(
    AUDIT_ACTIONS.map((entry) => entry.action).sort(),
    [...CONTRACT_ACTIONS].sort(),
  )
  const labels = AUDIT_ACTIONS.map((entry) => entry.label)
  assert.equal(new Set(labels).size, labels.length, 'labels are distinct')
  for (const entry of AUDIT_ACTIONS) {
    assert.notEqual(entry.label.trim(), '')
    assert.notEqual(entry.label, entry.action)
    assert.equal(auditActionLabel(entry.action), entry.label)
  }
  assert.equal(auditActionLabel('payroll_run.pay'), 'Pagó una liquidación')
  assert.equal(auditActionLabel('auth.2fa_enabled'), 'Activó la verificación en dos pasos')
})

test('an unknown action shows its raw name, never a blank or a guess', () => {
  assert.equal(auditActionLabel('future.thing'), 'future.thing')
  assert.equal(auditActionLabel('  future.thing '), 'future.thing')
  assert.equal(auditActionLabel(''), 'Acción desconocida')
})

test('filter options: «Todas» first (empty value) then every known action once', () => {
  assert.deepEqual(AUDIT_FILTER_OPTIONS[0], { value: '', label: 'Todas' })
  assert.deepEqual(
    AUDIT_FILTER_OPTIONS.slice(1).map((option) => option.value),
    AUDIT_ACTIONS.map((entry) => entry.action),
  )
  for (const option of AUDIT_FILTER_OPTIONS.slice(1)) {
    assert.equal(option.label, auditActionLabel(option.value))
  }
})

// --- actor and date ---

test('actorLabel: the stored name, or a neutral sentence when the snapshot is empty', () => {
  assert.equal(actorLabel(event('1', { actor_name: ' Ana Pérez ' })), 'Ana Pérez')
  assert.equal(actorLabel(event('1', { actor_name: '' })), 'Persona sin nombre')
})

test('formatAuditWhen: date and time in the SALON zone, not the browser one', () => {
  // 02:30Z is still the 2nd at 20:30 in Mexico City (UTC-6).
  assert.equal(formatAuditWhen('2026-11-03T02:30:00Z', 'America/Mexico_City'), '2026-11-02 20:30')
  assert.equal(formatAuditWhen('2026-11-03T02:30:00Z', 'Asia/Tokyo'), '2026-11-03 11:30')
  // Unusable zone falls back to the default zone instead of throwing in a render.
  assert.equal(formatAuditWhen('2026-11-03T02:30:00Z', 'No/Such_Zone'), '2026-11-02 20:30')
  assert.equal(formatAuditWhen('not a date', 'America/Mexico_City'), '—')
})

// --- normalizing a page ---

test('normalizeAuditPage: events in the order of the server and a cursor or null', () => {
  const page = normalizeAuditPage({
    events: [
      {
        id: 'e2',
        action: 'member.add',
        entity_id: 'm1',
        actor_user_id: 'u1',
        actor_name: 'Ana',
        created_at: '2026-11-03T18:00:00Z',
      },
      {
        id: 'e1',
        action: 'auth.2fa_enabled',
        entity_id: null,
        actor_user_id: 'u2',
        actor_name: null,
        created_at: '2026-11-03T17:00:00Z',
      },
    ],
    next_cursor: 'c-2',
  })
  assert.ok(page)
  assert.deepEqual(
    page.events.map((e) => e.id),
    ['e2', 'e1'],
  )
  assert.equal(page.next_cursor, 'c-2')
  assert.equal(page.events[1]?.entity_id, null)
  assert.equal(page.events[1]?.actor_name, '')
  assert.equal(normalizeAuditPage({ events: [], next_cursor: null })?.next_cursor, null)
  assert.deepEqual(normalizeAuditPage({ events: [], next_cursor: '' })?.next_cursor, null)
})

test('normalizeAuditPage: a malformed body is null (an error), never an empty list', () => {
  for (const raw of [null, undefined, 'x', [], {}, { events: 'no' }, { next_cursor: null }]) {
    assert.equal(normalizeAuditPage(raw), null, JSON.stringify(raw))
  }
})

test('normalizeAuditPage: rows without id, action or a real date are dropped, valid ones kept', () => {
  const page = normalizeAuditPage({
    events: [
      { id: '', action: 'member.add', created_at: '2026-11-03T18:00:00Z' },
      { id: 'e1', action: '', created_at: '2026-11-03T18:00:00Z' },
      { id: 'e2', action: 'member.add', created_at: 'garbage' },
      null,
      'x',
      { id: 'e3', action: 'member.add', created_at: '2026-11-03T18:00:00Z', actor_name: 'Ana', entity_id: 7 },
    ],
    next_cursor: null,
  })
  assert.ok(page)
  assert.deepEqual(
    page.events.map((e) => e.id),
    ['e3'],
  )
  // A non-string entity id is not shown as data.
  assert.equal(page.events[0]?.entity_id, null)
})

// --- cursor merge ---

test('mergeAuditEvents appends the next page without duplicating an id, keeping the server order', () => {
  const first = [event('e5'), event('e4'), event('e3')]
  const second = [event('e3'), event('e2'), event('e1')]
  assert.deepEqual(
    mergeAuditEvents(first, second).map((e) => e.id),
    ['e5', 'e4', 'e3', 'e2', 'e1'],
  )
  // Nothing new: same list. The inputs are never mutated.
  assert.deepEqual(mergeAuditEvents(first, []).map((e) => e.id), ['e5', 'e4', 'e3'])
  assert.deepEqual(first.map((e) => e.id), ['e5', 'e4', 'e3'])
  assert.deepEqual(second.map((e) => e.id), ['e3', 'e2', 'e1'])
  // Duplicates inside the incoming page itself are collapsed too.
  assert.deepEqual(
    mergeAuditEvents([], [event('a'), event('a'), event('b')]).map((e) => e.id),
    ['a', 'b'],
  )
  // The copy already on screen wins over the repeated one.
  const kept = mergeAuditEvents([event('x', { actor_name: 'Vieja' })], [event('x', { actor_name: 'Nueva' })])
  assert.equal(kept[0]?.actor_name, 'Vieja')
})

// --- request ---

test('auditLogPath: salon scoped, limit always, action and before only when set, all encoded', () => {
  assert.equal(AUDIT_PAGE_SIZE, 50)
  assert.equal(auditLogPath('s 1', {}), '/api/v1/salons/s%201/audit-log?limit=50')
  assert.equal(
    auditLogPath('s1', { action: 'member.add', before: 'a+b/c=' }),
    '/api/v1/salons/s1/audit-log?limit=50&before=a%2Bb%2Fc%3D&action=member.add',
  )
  assert.equal(auditLogPath('s1', { action: '  ', before: null }), '/api/v1/salons/s1/audit-log?limit=50')
})

test('fetchAuditLog: GET with the token, returns the normalized page', async () => {
  setToken('jwt-audit')
  const stub = stubFetch(() =>
    json({
      events: [
        {
          id: 'e1',
          action: 'cash_close.create',
          entity_id: 'cc1',
          actor_user_id: 'u1',
          actor_name: 'Ana',
          created_at: '2026-11-03T18:00:00Z',
        },
      ],
      next_cursor: 'next-1',
    }),
  )
  try {
    const page = await fetchAuditLog('s1', { action: 'cash_close.create' })
    assert.equal(page.events.length, 1)
    assert.equal(page.next_cursor, 'next-1')
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(
      stub.calls[0]?.url.endsWith('/api/v1/salons/s1/audit-log?limit=50&action=cash_close.create'),
      true,
    )
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-audit')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchAuditLog: server sentences verbatim, a malformed body is an error', async () => {
  const denied = stubFetch(() => json({ error: 'sin permiso' }, 403))
  try {
    await assert.rejects(fetchAuditLog('s1', {}), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.status, 403)
      assert.equal(error.message, 'sin permiso')
      return true
    })
  } finally {
    denied.restore()
  }
  const invalid = stubFetch(() => json({ error: 'Cursor invalido' }, 400))
  try {
    await assert.rejects(fetchAuditLog('s1', { before: 'zzz' }), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.message, 'Cursor invalido')
      return true
    })
  } finally {
    invalid.restore()
  }
  const garbage = stubFetch(() => json({ hello: 1 }))
  try {
    await assert.rejects(fetchAuditLog('s1', {}), ApiError)
  } finally {
    garbage.restore()
  }
})

// --- copy ---

test('auditEmptyMessage: says if a filter is the reason for the empty list', () => {
  assert.equal(auditEmptyMessage(''), 'Todavía no hay actividad registrada.')
  assert.equal(
    auditEmptyMessage('member.add'),
    'No hay actividad de «Agregó a una persona al equipo». Probá con «Todas».',
  )
  assert.equal(auditEmptyMessage('future.thing'), 'No hay actividad de «future.thing». Probá con «Todas».')
})
