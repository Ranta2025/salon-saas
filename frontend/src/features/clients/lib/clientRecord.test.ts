import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError, createSale, fetchClients } from '../../../lib/api.ts'
import {
  ALLERGIES_LONG_MESSAGE,
  ALLERGIES_TITLE,
  CLIENT_NAME_LONG_MESSAGE,
  CLIENT_NAME_REQUIRED_MESSAGE,
  CLIENT_NOTES_LONG_MESSAGE,
  CLIENT_PHONE_LONG_MESSAGE,
  CLIENT_SAVE_BUSY_MESSAGE,
  INVALID_CHARS_MESSAGE,
  MAX_ALLERGIES_LENGTH,
  MAX_NAME_LENGTH,
  MAX_NOTES_LENGTH,
  MAX_PHONE_LENGTH,
  MISSING_KEYS_MESSAGE,
  NO_ALLERGIES_TEXT,
  clientApiMessage,
  clientRecordBody,
  createClientSaveGate,
  fetchClientRecord,
  normalizeClientRecord,
  saveClientRecord,
  searchClients,
  updateClientRecord,
  validateClientRecordDraft,
  type ClientRecordInput,
} from './clientRecord.ts'
import { formatCents } from '../../../lib/money.ts'
import { canEditClient, canSeeClientMoney } from '../../../lib/permissions.ts'
import { saleWithClient } from '../../../lib/posCart.ts'
import { clearToken, setToken } from '../../../lib/session.ts'

type Call = { url: string; method: string; body: string | undefined; headers: Record<string, string> }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
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

const CLIENT_RAW = {
  id: 'c1',
  name: 'Ana Pérez',
  phone: '+5355528595',
  email: 'ana@correo.com',
  notes: 'Prefiere la mañana',
  allergies: 'Tinte con amoníaco',
  created_at: '2026-01-02T10:00:00Z',
}

const INPUT: ClientRecordInput = {
  name: 'Ana',
  phone: '+5355528595',
  email: 'ana@correo.com',
  notes: '',
  allergies: 'Níquel',
}

// --- la ficha que devuelve GET /salons/:id/clients/:cid ---

test('normalizeClientRecord reads the frozen client shape including allergies', () => {
  assert.deepEqual(normalizeClientRecord(CLIENT_RAW), CLIENT_RAW)
})

test('normalizeClientRecord never invents a field the server did not send', () => {
  assert.deepEqual(normalizeClientRecord({ id: 'c2', name: 'Bea' }), {
    id: 'c2',
    name: 'Bea',
    phone: '',
    email: '',
    notes: '',
    allergies: '',
    created_at: null,
  })
  assert.equal(normalizeClientRecord({ name: 'sin id' }), null)
  assert.equal(normalizeClientRecord(null), null)
  assert.equal(normalizeClientRecord([]), null)
})

test('the allergies notice is a fixed text of the contract', () => {
  assert.equal(ALLERGIES_TITLE, 'Alergias')
  assert.equal(NO_ALLERGIES_TEXT, 'Sin alergias registradas.')
  assert.ok(MAX_ALLERGIES_LENGTH === 500)
})

// --- cuerpo del PUT: SIEMPRE las cinco claves ---

test('clientRecordBody sends exactly the five keys of PUT', () => {
  const body = clientRecordBody({
    name: '  Ana ',
    phone: ' +53 5552 ',
    email: ' Ana@Correo.COM ',
    notes: ' ',
    allergies: '  ',
  })
  assert.deepEqual(Object.keys(body).sort(), ['allergies', 'email', 'name', 'notes', 'phone'])
  assert.deepEqual(body, {
    name: 'Ana',
    phone: '+53 5552',
    email: 'ana@correo.com',
    notes: '',
    allergies: '',
  })
})

// --- GET y PUT con fetch simulado (contrato congelado) ---

test('fetchClientRecord asks GET /salons/:id/clients/:cid', async () => {
  setToken('token-1')
  const stub = stubFetch(() => json(CLIENT_RAW))
  try {
    const loaded = await fetchClientRecord('s1', 'c1')
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1`)
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer token-1')
    assert.equal(loaded.allergies, 'Tinte con amoníaco')
    assert.equal(loaded.notes, 'Prefiere la mañana')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('updateClientRecord replaces the file with all five keys', async () => {
  const stub = stubFetch(() => json({ ...CLIENT_RAW, name: 'Ana' }))
  try {
    const saved = await updateClientRecord('s1', 'c1', INPUT)
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1`)
    assert.equal(
      stub.calls[0]?.body,
      '{"name":"Ana","phone":"+5355528595","email":"ana@correo.com","notes":"","allergies":"Níquel"}',
    )
    assert.equal(saved.name, 'Ana')
  } finally {
    stub.restore()
  }
})

test('the 400, 403 and 404 of the ficha keep the sentence of the server verbatim', async () => {
  const cases: [number, string][] = [
    [400, MISSING_KEYS_MESSAGE],
    [400, ALLERGIES_LONG_MESSAGE],
    [400, INVALID_CHARS_MESSAGE],
    [403, 'sin permiso'],
    [404, 'Ficha no encontrada'],
  ]
  for (const [status, message] of cases) {
    const stub = stubFetch(() => json({ error: message }, status))
    try {
      await assert.rejects(
        () => updateClientRecord('s1', 'c1', INPUT),
        (error: unknown) => {
          assert.ok(error instanceof ApiError)
          assert.equal(error.status, status)
          assert.equal(error.message, message)
          assert.equal(clientApiMessage(error, 'No pudimos guardar la ficha.'), message)
          return true
        },
        `status ${status} should keep "${message}"`,
      )
    } finally {
      stub.restore()
    }
  }
})

test('clientApiMessage falls back only when the error says nothing', () => {
  assert.equal(clientApiMessage(new Error(''), 'No pudimos guardar la ficha.'), 'No pudimos guardar la ficha.')
  assert.equal(clientApiMessage('boom', 'No pudimos guardar la ficha.'), 'No pudimos guardar la ficha.')
  assert.equal(clientApiMessage(new Error('Se cayó la red'), 'x'), 'Se cayó la red')
})

// --- validación antes de gastar un request ---

test('validateClientRecordDraft accepts a filled file and trims it', () => {
  const check = validateClientRecordDraft({
    name: '  Ana ',
    phone: ' +5355528595 ',
    email: ' ANA@correo.com ',
    notes: ' Prefiere la tarde ',
    allergies: ' Níquel ',
  })
  assert.deepEqual(check, {
    ok: true,
    payload: {
      name: 'Ana',
      phone: '+5355528595',
      email: 'ana@correo.com',
      notes: 'Prefiere la tarde',
      allergies: 'Níquel',
    },
  })
})

test('validateClientRecordDraft keeps the sentences of the API on the right field', () => {
  const base: ClientRecordInput = { name: 'Ana', phone: '', email: '', notes: '', allergies: '' }
  assert.deepEqual(validateClientRecordDraft({ ...base, name: '   ' }), {
    ok: false,
    field: 'name',
    message: CLIENT_NAME_REQUIRED_MESSAGE,
  })
  assert.deepEqual(validateClientRecordDraft({ ...base, name: 'a'.repeat(MAX_NAME_LENGTH + 1) }), {
    ok: false,
    field: 'name',
    message: CLIENT_NAME_LONG_MESSAGE,
  })
  assert.deepEqual(
    validateClientRecordDraft({ ...base, phone: '1'.repeat(MAX_PHONE_LENGTH + 1) }),
    { ok: false, field: 'phone', message: CLIENT_PHONE_LONG_MESSAGE },
  )
  assert.deepEqual(validateClientRecordDraft({ ...base, email: 'ana@' }), {
    ok: false,
    field: 'email',
    message: 'Email inválido',
  })
  assert.deepEqual(
    validateClientRecordDraft({ ...base, notes: 'n'.repeat(MAX_NOTES_LENGTH + 1) }),
    { ok: false, field: 'notes', message: CLIENT_NOTES_LONG_MESSAGE },
  )
  assert.deepEqual(
    validateClientRecordDraft({ ...base, allergies: 'a'.repeat(MAX_ALLERGIES_LENGTH + 1) }),
    { ok: false, field: 'allergies', message: ALLERGIES_LONG_MESSAGE },
  )
})

test('validateClientRecordDraft lets exactly 500 allergies through and counts characters, not bytes', () => {
  const base: ClientRecordInput = { name: 'Ana', phone: '', email: '', notes: '', allergies: '' }
  const okAllergies = validateClientRecordDraft({ ...base, allergies: 'ñ'.repeat(MAX_ALLERGIES_LENGTH) })
  assert.equal(okAllergies.ok, true)
  const okName = validateClientRecordDraft({ ...base, name: 'ñ'.repeat(MAX_NAME_LENGTH) })
  assert.equal(okName.ok, true)
})

test('a NUL in any of the five fields is refused before the request is sent', () => {
  const base: ClientRecordInput = { name: 'Ana', phone: '', email: '', notes: '', allergies: '' }
  for (const field of ['name', 'phone', 'email', 'notes', 'allergies'] as const) {
    const check = validateClientRecordDraft({ ...base, [field]: `a\u0000b` })
    assert.deepEqual(check, { ok: false, field, message: INVALID_CHARS_MESSAGE }, `field ${field}`)
  }
})

// --- quién edita y quién ve el dinero (D4, D5, D6) ---

test('only owner, admin and reception edit a client file', () => {
  assert.equal(canEditClient('owner'), true)
  assert.equal(canEditClient('admin'), true)
  assert.equal(canEditClient('reception'), true)
  assert.equal(canEditClient('professional'), false)
  assert.equal(canEditClient(null), false)
})

test('the money of a history belongs to the roles that see money', () => {
  assert.equal(canSeeClientMoney('owner'), true)
  assert.equal(canSeeClientMoney('admin'), true)
  assert.equal(canSeeClientMoney('reception'), true)
  assert.equal(canSeeClientMoney('professional'), false)
  assert.equal(canSeeClientMoney(null), false)
})

// --- un guardado a la vez: el doble clic no manda dos PUT ---

test('a double click on save sends exactly one PUT and the second answers busy', async () => {
  const gate = createClientSaveGate()
  let release: () => void = () => {}
  const hold = new Promise<void>((resolve) => {
    release = resolve
  })
  const stub = stubFetch(async () => {
    await hold
    return json({ ...CLIENT_RAW, name: 'Ana' })
  })
  try {
    const first = saveClientRecord(gate, 's1', 'c1', INPUT)
    const second = saveClientRecord(gate, 's1', 'c1', INPUT)
    assert.deepEqual(await second, { status: 'busy', message: CLIENT_SAVE_BUSY_MESSAGE })
    release()
    const outcome = await first
    assert.equal(outcome.status, 'saved')
    assert.equal(stub.calls.length, 1)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
  }
})

test('a rejected save frees the gate and returns the sentence of the server', async () => {
  const gate = createClientSaveGate()
  const stub = stubFetch(() => json({ error: ALLERGIES_LONG_MESSAGE }, 400))
  try {
    const outcome = await saveClientRecord(gate, 's1', 'c1', INPUT)
    assert.deepEqual(outcome, { status: 'failed', message: ALLERGIES_LONG_MESSAGE })
    assert.equal(gate.busy(), false)
    assert.equal(gate.start(), true)
    gate.finish()
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
  }
})

// --- POS: selector opcional de clienta (D3c) ---

test('with nobody picked the sale body is exactly the one of today', async () => {
  const body = {
    professional_id: 'u1',
    client_name: 'Ana',
    method: 'cash' as const,
    items: [{ service_id: 'svc', qty: 2, unit_price_cents: 35000 }],
    client_op_id: 'op-1',
  }
  const withNobody = saleWithClient(body, null)
  assert.equal('client_id' in withNobody, false)
  assert.deepEqual(withNobody, body)

  const stub = stubFetch(() => json({ sale_id: 'sale-1', total: 70000 }, 201))
  try {
    const created = await createSale('salon-1', withNobody)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/salon-1/sales`)
    assert.equal(
      stub.calls[0]?.body,
      '{"professional_id":"u1","client_name":"Ana","method":"cash","items":[{"service_id":"svc","qty":2,"unit_price_cents":35000}],"client_op_id":"op-1"}',
    )
    assert.equal(created.total, 70000)
    assert.equal(Number.isInteger(created.total), true)
    assert.equal(formatCents(created.total), '$700.00')
  } finally {
    stub.restore()
  }
})

test('a picked client file fills the name and travels as client_id', async () => {
  const picked = { id: 'c1', name: 'Ana Pérez' }
  const body = {
    professional_id: 'u1',
    client_name: '   ',
    method: 'card' as const,
    items: [{ service_id: 'svc', qty: 1, unit_price_cents: 35000 }],
    client_op_id: 'op-2',
  }
  const withClient = saleWithClient(body, picked)
  assert.equal(withClient.client_name, 'Ana Pérez')
  assert.equal(withClient.client_id, 'c1')

  // The name typed by the person wins over the name of the file.
  assert.equal(saleWithClient({ ...body, client_name: 'Ana P.' }, picked).client_name, 'Ana P.')

  // A file without id behaves like nobody picked.
  assert.equal('client_id' in saleWithClient(body, { id: '  ', name: 'Ana' }), false)

  const stub = stubFetch(() => json({ sale_id: 'sale-2', total: 35000, client_id: 'c1' }, 201))
  try {
    const created = await createSale('salon-1', saleWithClient(body, picked))
    assert.deepEqual(JSON.parse(String(stub.calls[0]?.body)), {
      professional_id: 'u1',
      client_name: 'Ana Pérez',
      method: 'card',
      items: [{ service_id: 'svc', qty: 1, unit_price_cents: 35000 }],
      client_op_id: 'op-2',
      client_id: 'c1',
    })
    assert.equal(created.client_id, 'c1')
  } finally {
    stub.restore()
  }
})

test('the picker searches the list already fetched and never asks the API again', async () => {
  const stub = stubFetch(() =>
    json({
      clients: [
        { id: 'c1', name: 'Ana Pérez', phone: '+53 5552 8595' },
        { id: 'c2', name: 'Bea Ruiz', phone: '' },
      ],
    }),
  )
  try {
    const list = await fetchClients('s1')
    assert.equal(stub.calls.length, 1)
    assert.deepEqual(
      searchClients(list, 'ana').map((client) => client.id),
      ['c1'],
    )
    assert.deepEqual(
      searchClients(list, 'BEA').map((client) => client.id),
      ['c2'],
    )
    // The phone is searched by digits, with or without the prefix.
    assert.deepEqual(
      searchClients(list, '5552').map((client) => client.id),
      ['c1'],
    )
    assert.deepEqual(searchClients(list, 'zzz'), [])
    assert.deepEqual(
      searchClients(list, '   ').map((client) => client.id),
      ['c1', 'c2'],
    )
    assert.equal(stub.calls.length, 1)
  } finally {
    stub.restore()
  }
})

test('the picker rows never carry a money figure', () => {
  const picked = saleWithClient(
    { professional_id: 'u1', client_name: '', method: 'cash', items: [] },
    { id: 'c1', name: 'Ana Pérez' },
  )
  assert.equal('total_cents' in picked, false)
  assert.equal(JSON.stringify(picked).includes('$'), false)
})
