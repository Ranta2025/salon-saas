import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from './api.ts'
import { clearToken, setToken } from './session.ts'
import {
  addMember,
  fetchMembers,
  normalizeMemberEmail,
  normalizeMembers,
  removeMember,
  setMemberAttends,
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
    { id: 'u1', name: 'Ana', email: 'ana@salon.mx', role: 'owner', attends: true },
    { id: 'u2', name: 'Beto', email: 'beto@salon.mx', role: 'professional', attends: true },
    { id: 'u3', name: 'Rita', email: 'rita@salon.mx', role: 'reception', attends: false },
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
