import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from '../../../lib/api.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  activeUnitCount,
  addResourceUnits,
  applyUnitActive,
  canAddType,
  canAddUnits,
  canEditEquipment,
  checkUnitCount,
  createResourceType,
  deleteResourceType,
  EQUIPMENT_COUNT_MESSAGE,
  EQUIPMENT_MESSAGES,
  fetchEquipment,
  futureBookingsNotice,
  MAX_RESOURCE_TYPES,
  MAX_UNITS_PER_TYPE,
  nextUnitName,
  NO_ACTIVE_EQUIPMENT_NOTICE,
  normalizeEquipment,
  normalizeResourceUnitUpdate,
  renameResourceType,
  RESOURCE_NAME_MAX,
  resourceTypeName,
  serviceOffersSlots,
  setResourceUnit,
  validateResourceName,
  type Equipment,
  type ResourceType,
} from './equipment.ts'

type Call = { url: string; method: string; body: string | undefined }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
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

function type(name: string, units = 0, id = `t-${name}`): ResourceType {
  return {
    id,
    name,
    services: 0,
    units: Array.from({ length: units }, (_, index) => ({
      id: `${id}-u${index + 1}`,
      name: `${name} ${index + 1}`,
      active: true,
    })),
  }
}

test('normalizeEquipment reads types with units and services, dropping unusable rows', () => {
  const equipment = normalizeEquipment({
    types: [
      {
        id: 't1',
        name: 'Pelo',
        services: 2,
        units: [
          { id: 'u1', name: 'Pelo 1', active: true },
          { id: 'u2', name: 'Pelo 2', active: false },
          { name: 'sin id' },
        ],
      },
      { id: 't2', name: '  Barbería ', services: 0, units: [] },
      { name: 'sin id' },
    ],
  })
  assert.deepEqual(equipment, [
    {
      id: 't1',
      name: 'Pelo',
      services: 2,
      units: [
        { id: 'u1', name: 'Pelo 1', active: true },
        { id: 'u2', name: 'Pelo 2', active: false },
      ],
    },
    { id: 't2', name: 'Barbería', services: 0, units: [] },
  ])
  assert.deepEqual(normalizeEquipment(null), [])
  assert.deepEqual(normalizeEquipment({ types: 'nope' }), [])
  // A unit without an explicit flag counts as active (the column defaults to true)
  // and a type without services counts as used by none.
  assert.deepEqual(
    normalizeEquipment({
      types: [{ id: 't3', name: 'Lavacabezas', units: [{ id: 'u9', name: 'Lavacabezas 1' }] }],
    }),
    [
      {
        id: 't3',
        name: 'Lavacabezas',
        services: 0,
        units: [{ id: 'u9', name: 'Lavacabezas 1', active: true }],
      },
    ],
  )
})

test('only owner and admin edit equipment; the menu entry follows isManager', () => {
  assert.equal(canEditEquipment('owner'), true)
  assert.equal(canEditEquipment('admin'), true)
  assert.equal(canEditEquipment('professional'), false)
  assert.equal(canEditEquipment('reception'), false)
  assert.equal(canEditEquipment(null), false)
})

test('activeUnitCount counts only active units of a type', () => {
  const withUnits = normalizeEquipment({
    types: [
      {
        id: 't1',
        name: 'Pelo',
        services: 0,
        units: [
          { id: 'u1', name: 'Pelo 1', active: true },
          { id: 'u2', name: 'Pelo 2', active: false },
          { id: 'u3', name: 'Pelo 3', active: true },
        ],
      },
    ],
  })
  assert.equal(activeUnitCount(withUnits[0]!), 2)
  assert.equal(activeUnitCount(type('Vacio', 0)), 0)
})

test('a service with a type and no active unit offers no slots', () => {
  const equipment: Equipment = [
    type('Pelo', 2, 't1'),
    {
      id: 't2',
      name: 'Barbería',
      services: 1,
      units: [{ id: 'u9', name: 'Silla 1', active: false }],
    },
  ]
  assert.equal(serviceOffersSlots(equipment, null), true)
  assert.equal(serviceOffersSlots(equipment, ''), true)
  assert.equal(serviceOffersSlots(equipment, 't1'), true)
  assert.equal(serviceOffersSlots(equipment, 't2'), false)
  assert.equal(serviceOffersSlots(equipment, 'borrado'), false)
  assert.equal(NO_ACTIVE_EQUIPMENT_NOTICE, 'No hay equipo activo: no se ofrecen turnos')
  assert.equal(resourceTypeName(equipment, 't2'), 'Barbería')
  assert.equal(resourceTypeName(equipment, 'borrado'), null)
})

test('the future bookings notice counts in singular and plural', () => {
  assert.equal(futureBookingsNotice(0), null)
  assert.equal(futureBookingsNotice(-1), null)
  assert.equal(futureBookingsNotice(1), '1 turno futuro usa este puesto (lo conserva)')
  assert.equal(futureBookingsNotice(3), '3 turnos futuros usan este puesto (los conservan)')
})

test('validateResourceName mirrors the server: trim, one line, 1..60, no duplicates', () => {
  assert.deepEqual(validateResourceName('  Pelo  ', []), { ok: true, name: 'Pelo' })
  assert.deepEqual(validateResourceName('Pelo\r\n2', []), { ok: true, name: 'Pelo 2' })
  assert.deepEqual(validateResourceName('   ', []), {
    ok: false,
    message: 'El nombre del equipo es obligatorio',
  })
  assert.deepEqual(validateResourceName('\n\n', []), {
    ok: false,
    message: EQUIPMENT_MESSAGES.required,
  })
  assert.equal(RESOURCE_NAME_MAX, 60)
  assert.equal(validateResourceName('x'.repeat(60), []).ok, true)
  assert.deepEqual(validateResourceName('x'.repeat(61), []), {
    ok: false,
    message: 'El nombre no puede superar los 60 caracteres',
  })
  const siblings = [
    { id: 't1', name: 'Pelo' },
    { id: 't2', name: 'Barbería' },
  ]
  assert.deepEqual(validateResourceName(' pelo ', siblings), {
    ok: false,
    message: 'Ya existe un equipo con ese nombre',
  })
  assert.deepEqual(validateResourceName('PELO', siblings, 't1'), { ok: true, name: 'PELO' })
  assert.deepEqual(validateResourceName('PELO', siblings), {
    ok: false,
    message: EQUIPMENT_MESSAGES.duplicate,
  })
})

test('the limits are 20 types and 50 units, with the server sentences', () => {
  assert.equal(MAX_RESOURCE_TYPES, 20)
  assert.equal(MAX_UNITS_PER_TYPE, 50)
  const nineteen = Array.from({ length: 19 }, (_, index) => type(`T${index + 1}`))
  const twenty = Array.from({ length: 20 }, (_, index) => type(`T${index + 1}`))
  assert.deepEqual(canAddType(nineteen), { ok: true })
  assert.deepEqual(canAddType(twenty), { ok: false, message: 'Máximo 20 tipos de equipo' })
  assert.deepEqual(canAddUnits(type('Pelo', 49)), { ok: true })
  assert.deepEqual(canAddUnits(type('Pelo', 49), 2), {
    ok: false,
    message: 'Máximo 50 unidades por tipo',
  })
  assert.deepEqual(canAddUnits(type('Pelo', 50)), {
    ok: false,
    message: 'Máximo 50 unidades por tipo',
  })
  assert.deepEqual(checkUnitCount(1), { ok: true })
  assert.deepEqual(checkUnitCount(50), { ok: true })
  assert.deepEqual(checkUnitCount(0), { ok: false, message: EQUIPMENT_COUNT_MESSAGE })
  assert.deepEqual(checkUnitCount(51), { ok: false, message: EQUIPMENT_COUNT_MESSAGE })
  assert.deepEqual(checkUnitCount(1.5), { ok: false, message: EQUIPMENT_COUNT_MESSAGE })
  assert.deepEqual(canAddUnits(type('Pelo', 10), 0), {
    ok: false,
    message: EQUIPMENT_COUNT_MESSAGE,
  })
})

test('nextUnitName takes the first free number of the type', () => {
  assert.equal(nextUnitName(type('Silla', 2)), 'Silla 3')
  assert.equal(
    nextUnitName({
      name: 'Silla',
      units: [
        { id: 'u1', name: 'Silla 1', active: true },
        { id: 'u3', name: 'Silla 3', active: false },
      ],
    }),
    'Silla 2',
  )
  assert.equal(nextUnitName(type('Lavacabezas', 0)), 'Lavacabezas 1')
  assert.equal(
    nextUnitName({
      name: 'Pelo',
      units: [
        { id: 'u1', name: 'pelo 2', active: true },
        { id: 'u2', name: 'Pelo 1', active: true },
      ],
    }),
    'Pelo 3',
  )
})

test('applyUnitActive flips one unit and the previous value is the exact rollback', () => {
  const equipment: Equipment = [type('Pelo', 2, 't1'), type('Silla', 1, 't2')]
  const off = applyUnitActive(equipment, 't1-u1', false)
  assert.equal(off[0]?.units[0]?.active, false)
  assert.equal(off[0]?.units[1]?.active, true)
  // The original list is never mutated (the rollback keeps its own copy).
  assert.equal(equipment[0]?.units[0]?.active, true)
  assert.deepEqual(applyUnitActive(off, 't1-u1', true), equipment)
  assert.deepEqual(applyUnitActive(equipment, 'no-existe', false), equipment)
})

test('normalizeResourceUnitUpdate reads the flag and future bookings of a PATCH', () => {
  assert.deepEqual(
    normalizeResourceUnitUpdate({
      unit: { id: 'u1', name: 'Pelo 1', active: false },
      future_bookings: 2,
    }),
    { active: false, futureBookings: 2 },
  )
  assert.deepEqual(normalizeResourceUnitUpdate({ id: 'u1', active: true, future_bookings: 'x' }), {
    active: true,
    futureBookings: 0,
  })
  assert.deepEqual(normalizeResourceUnitUpdate({}), { active: null, futureBookings: 0 })
  assert.deepEqual(
    normalizeResourceUnitUpdate({ unit: { id: 'u1', active: false, future_bookings: 4 } }),
    { active: false, futureBookings: 4 },
  )
})

test('fetchEquipment reads GET /salons/:id/resources', async () => {
  const stub = stubFetch(() =>
    json({
      types: [
        {
          id: 't1',
          name: 'Pelo',
          services: 3,
          units: [{ id: 'u1', name: 'Pelo 1', active: true }],
        },
      ],
    }),
  )
  try {
    setToken('jwt-equip')
    const equipment = await fetchEquipment('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/resources`)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.deepEqual(equipment, [
      {
        id: 't1',
        name: 'Pelo',
        services: 3,
        units: [{ id: 'u1', name: 'Pelo 1', active: true }],
      },
    ])
  } finally {
    stub.restore()
    clearToken()
  }
})

test('the equipment client sends the contract bodies and keeps server sentences', async () => {
  const stub = stubFetch((call) => {
    if (call.method === 'DELETE') {
      return json({ error: 'Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo' }, 409)
    }
    if (call.method === 'PATCH' && call.url.includes('/resource-units/')) {
      return json({ unit: { id: 'u1', name: 'Pelo 1', active: false }, future_bookings: 1 })
    }
    if (call.method === 'POST' && call.url.endsWith('/units')) {
      return json({ units: [] }, 201)
    }
    return json({}, 201)
  })
  try {
    setToken('jwt-equip')
    await createResourceType('s1', { name: 'Pelo', count: 3 })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/resources`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { name: 'Pelo', count: 3 })

    await renameResourceType('s1', 't1', 'Pelo y plancha')
    assert.equal(stub.calls[1]?.url, `${API_BASE}/api/v1/salons/s1/resources/t1`)
    assert.equal(stub.calls[1]?.method, 'PATCH')
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), { name: 'Pelo y plancha' })

    await addResourceUnits('s1', 't1', 2)
    assert.equal(stub.calls[2]?.url, `${API_BASE}/api/v1/salons/s1/resources/t1/units`)
    assert.equal(stub.calls[2]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[2]?.body ?? '{}'), { count: 2 })

    const update = await setResourceUnit('s1', 'u1', { active: false })
    assert.equal(stub.calls[3]?.url, `${API_BASE}/api/v1/salons/s1/resource-units/u1`)
    assert.equal(stub.calls[3]?.method, 'PATCH')
    assert.deepEqual(JSON.parse(stub.calls[3]?.body ?? '{}'), { active: false })
    assert.deepEqual(update, { active: false, futureBookings: 1 })

    await assert.rejects(
      deleteResourceType('s1', 't1'),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 409 &&
        error.message ===
          'Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo',
    )
    assert.equal(stub.calls[4]?.url, `${API_BASE}/api/v1/salons/s1/resources/t1`)
    assert.equal(stub.calls[4]?.method, 'DELETE')
  } finally {
    stub.restore()
    clearToken()
  }
})
