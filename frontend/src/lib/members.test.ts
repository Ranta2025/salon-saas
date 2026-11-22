import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from './api.ts'
import { clearToken, setToken } from './session.ts'
import {
  addMember,
  fetchMemberHours,
  fetchMembers,
  normalizeMemberEmail,
  normalizeMembers,
  removeMember,
  saveMemberHours,
  setMemberAttends,
  setMemberServices,
  setMemberTrade,
} from './members.ts'

type Call = { url: string; method: string; headers: Record<string, string>; body: string | undefined }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers: (init?.headers ?? {}) as Record<string, string>,
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

test('normalizeMembers reads the team with id, name, email, role and attends', () => {
  const members = normalizeMembers({
    members: [
      { id: 'u1', name: 'Ana', email: 'ana@salon.mx', role: 'owner', attends: true },
      { id: 'u2', name: 'Beto', email: 'beto@salon.mx', role: 'professional', attends: true },
      { id: 'u3', name: 'Rita', email: 'rita@salon.mx', role: 'reception', attends: false },
    ],
  })
  assert.deepEqual(members, [
    {
      id: 'u1',
      name: 'Ana',
      email: 'ana@salon.mx',
      role: 'owner',
      attends: true,
      trade: '',
      service_ids: [],
    },
    {
      id: 'u2',
      name: 'Beto',
      email: 'beto@salon.mx',
      role: 'professional',
      attends: true,
      trade: '',
      service_ids: [],
    },
    {
      id: 'u3',
      name: 'Rita',
      email: 'rita@salon.mx',
      role: 'reception',
      attends: false,
      trade: '',
      service_ids: [],
    },
  ])
})

test('normalizeMembers defaults attends from the role when the payload omits it', () => {
  const members = normalizeMembers({
    members: [
      { id: 'u2', name: 'Beto', role: 'professional' },
      { id: 'u1', name: 'Ana', role: 'owner' },
      { id: 'u4', name: 'Raro', role: 'admin', attends: 'sí' },
    ],
  })
  assert.deepEqual(
    members.map((row) => row.attends),
    [true, false, false],
  )
})

test('normalizeMembers skips rows without id and never throws', () => {
  assert.deepEqual(normalizeMembers(null), [])
  assert.deepEqual(normalizeMembers({ members: 'nope' }), [])
  const members = normalizeMembers({
    members: [
      { id: 'u1', name: 'Sin correo', role: 'admin' },
      { name: 'Sin id', email: 'x@y.z' },
      { id: 'u3', name: 'Raro', email: 'r@y.z', role: 'superstar' },
    ],
  })
  assert.deepEqual(
    members.map((member) => ({
      id: member.id,
      email: member.email,
      role: member.role,
      attends: member.attends,
    })),
    [
      { id: 'u1', email: '', role: 'admin', attends: false },
      { id: 'u3', email: 'r@y.z', role: 'professional', attends: true },
    ],
  )
})

test('normalizeMemberEmail trims and lowercases what the person typed', () => {
  assert.equal(normalizeMemberEmail('  Ana@Salon.MX '), 'ana@salon.mx')
  assert.equal(normalizeMemberEmail(''), '')
})

test('fetchMembers reads the team of the salon with the session token', async () => {
  const stub = stubFetch(() =>
    json({ members: [{ id: 'u1', name: 'Ana', email: 'ana@salon.mx', role: 'owner' }] }),
  )
  try {
    setToken('jwt-members')
    const members = await fetchMembers('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members`)
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-members')
    assert.equal(members[0]?.email, 'ana@salon.mx')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('addMember POSTs a normalised email and the chosen role, and returns the new member', async () => {
  const stub = stubFetch(() =>
    json(
      { id: 'u9', name: 'Carla', email: 'carla@salon.mx', role: 'professional', attends: true },
      201,
    ),
  )
  try {
    setToken('jwt-members')
    const member = await addMember('s1', { email: '  Carla@Salon.MX ', role: 'professional' })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      email: 'carla@salon.mx',
      role: 'professional',
    })
    assert.deepEqual(member, {
      id: 'u9',
      name: 'Carla',
      email: 'carla@salon.mx',
      role: 'professional',
      attends: true,
      trade: '',
      service_ids: [],
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('addMember surfaces the server explanation when the person has no account yet', async () => {
  const stub = stubFetch(() =>
    json({ error: 'Esa persona todavía no tiene cuenta. Pídele que se registre primero.' }, 400),
  )
  try {
    setToken('jwt-members')
    await assert.rejects(
      () => addMember('s1', { email: 'nadie@salon.mx', role: 'professional' }),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.match(err.message, /todavía no tiene cuenta/)
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('removeMember DELETEs the membership and accepts the empty 204 response', async () => {
  const stub = stubFetch(() => new Response(null, { status: 204 }))
  try {
    setToken('jwt-members')
    await removeMember('s1', 'u2')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members/u2`)
    assert.equal(stub.calls[0]?.method, 'DELETE')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('setMemberAttends PATCHes only the attends flag and returns the saved member', async () => {
  const stub = stubFetch(() =>
    json({ id: 'u1', name: 'Ana', email: 'ana@salon.mx', role: 'owner', attends: true }),
  )
  try {
    setToken('jwt-members')
    const member = await setMemberAttends('s1', 'u1', true)
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members/u1`)
    assert.equal(stub.calls[0]?.method, 'PATCH')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { attends: true })
    assert.deepEqual(member, {
      id: 'u1',
      name: 'Ana',
      email: 'ana@salon.mx',
      role: 'owner',
      attends: true,
      trade: '',
      service_ids: [],
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('setMemberAttends shows the server rule verbatim when the role always attends', async () => {
  const stub = stubFetch(() => json({ error: 'Las profesionales siempre atienden' }, 400))
  try {
    setToken('jwt-members')
    await assert.rejects(
      () => setMemberAttends('s1', 'u2', false),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, 'Las profesionales siempre atienden')
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('removeMember surfaces the rule about the last owner', async () => {
  const stub = stubFetch(() =>
    json({ error: 'No se puede quitar al último owner del salón' }, 409),
  )
  try {
    setToken('jwt-members')
    await assert.rejects(
      () => removeMember('s1', 'u1'),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 409)
        assert.match(err.message, /último owner/)
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('normalizeMembers reads the trade and the services each person does', () => {
  const members = normalizeMembers({
    members: [
      {
        id: 'u2',
        name: 'Beto',
        role: 'professional',
        trade: '  Barbero ',
        service_ids: ['s1', 's1', 's2'],
      },
      { id: 'u3', name: 'Rita', role: 'reception' },
    ],
  })
  assert.equal(members[0]?.trade, 'Barbero')
  assert.deepEqual(members[0]?.service_ids, ['s1', 's2'])
  assert.equal(members[1]?.trade, '')
  assert.deepEqual(members[1]?.service_ids, [])
})

test('setMemberTrade PATCHes the normalised trade and returns the saved member', async () => {
  const stub = stubFetch(() =>
    json({
      id: 'u2',
      name: 'Beto',
      email: 'beto@salon.mx',
      role: 'professional',
      trade: 'Barbero',
      service_ids: ['s1'],
    }),
  )
  try {
    setToken('jwt-members')
    const member = await setMemberTrade('s1', 'u2', '  Barbero\r\n')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members/u2`)
    assert.equal(stub.calls[0]?.method, 'PATCH')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { trade: 'Barbero' })
    assert.equal(member.trade, 'Barbero')
    assert.deepEqual(member.service_ids, ['s1'])
  } finally {
    stub.restore()
    clearToken()
  }
})

test('setMemberTrade keeps the server sentence verbatim when the role has no trade', async () => {
  const stub = stubFetch(() => json({ error: 'Ese rol no tiene oficio' }, 400))
  try {
    setToken('jwt-members')
    await assert.rejects(
      () => setMemberTrade('s1', 'r1', 'Barbero'),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, 'Ese rol no tiene oficio')
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('setMemberServices PUTs the list and returns what the server kept', async () => {
  const stub = stubFetch(() => json({ service_ids: ['s1', 's2'] }))
  try {
    setToken('jwt-members')
    const ids = await setMemberServices('s1', 'u2', [' s1 ', 's2', 's2'])
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members/u2/services`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { service_ids: ['s1', 's2'] })
    assert.deepEqual(ids, ['s1', 's2'])
  } finally {
    stub.restore()
    clearToken()
  }
})

test('setMemberServices accepts the empty list and surfaces the server message verbatim', async () => {
  const stub = stubFetch((call) =>
    JSON.parse(call.body ?? '{}').service_ids.length === 0
      ? json({ service_ids: [] })
      : json({ error: 'Servicio inválido' }, 400),
  )
  try {
    setToken('jwt-members')
    assert.deepEqual(await setMemberServices('s1', 'u2', []), [])
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { service_ids: [] })
    await assert.rejects(
      () => setMemberServices('s1', 'u2', ['s-ajeno']),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, 'Servicio inválido')
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchMemberHours reads the segments of one member and its custom flag', async () => {
  const stub = stubFetch(() =>
    json({
      segments: [
        { weekday: 1, start: '09:00', end: '13:00' },
        { weekday: 1, start: '14:00', end: '19:00' },
      ],
      custom: true,
    }),
  )
  try {
    setToken('jwt-member-hours')
    const hours = await fetchMemberHours('s1', 'u2')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members/u2/hours`)
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-member-hours')
    assert.deepEqual(hours, {
      segments: [
        { weekday: 1, start: '09:00', end: '13:00' },
        { weekday: 1, start: '14:00', end: '19:00' },
      ],
      custom: true,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('saveMemberHours PUTs the segments and the empty list means "same as the salon"', async () => {
  const stub = stubFetch(() => json({ segments: [], custom: false }))
  try {
    setToken('jwt-member-hours')
    await saveMemberHours('s1', 'u2', [{ weekday: 3, start: '10:00', end: '16:00' }])
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/members/u2/hours`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      segments: [{ weekday: 3, start: '10:00', end: '16:00' }],
    })
    assert.deepEqual(await saveMemberHours('s1', 'u2', []), { segments: [], custom: false })
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), { segments: [] })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('saveMemberHours keeps the server sentence verbatim and never rewrites it', async () => {
  const stub = stubFetch(() => json({ error: 'Máximo 4 tramos por día' }, 400))
  try {
    setToken('jwt-member-hours')
    await assert.rejects(
      () => saveMemberHours('s1', 'u2', [{ weekday: 1, start: '09:00', end: '13:00' }]),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, 'Máximo 4 tramos por día')
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})
