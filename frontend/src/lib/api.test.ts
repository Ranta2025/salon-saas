import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, NetworkError, API_BASE, request } from './api.ts'
import { clearToken, getToken, setToken } from './session.ts'

type Call = { url: string; headers: Record<string, string>; body: string | undefined }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
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

test('request joins the configured base URL and returns parsed JSON', async () => {
  clearToken()
  const stub = stubFetch(() => json({ token: 'jwt-abc' }))
  try {
    const out = await request<{ token: string }>('/api/v1/auth/login', {
      method: 'POST',
      body: { email: 'ana@salon.mx', password: 'secreta' },
    })
    assert.deepEqual(out, { token: 'jwt-abc' })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/auth/login`)
    assert.equal(stub.calls[0]?.headers['Content-Type'], 'application/json')
    assert.equal(stub.calls[0]?.body, '{"email":"ana@salon.mx","password":"secreta"}')
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
  } finally {
    stub.restore()
  }
})

test('request sends the bearer token only while a session exists', async () => {
  const stub = stubFetch(() => json({ services: [] }))
  try {
    setToken('jwt-abc')
    assert.equal(getToken(), 'jwt-abc')
    await request('/api/v1/salons/s1/services')
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-abc')

    clearToken()
    await request('/api/v1/salons/s1/services')
    assert.equal(stub.calls[1]?.headers.Authorization, undefined)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('request surfaces the server error message with its status', async () => {
  clearToken()
  const stub = stubFetch(() => json({ error: 'sin acceso al salón' }, 403))
  try {
    await assert.rejects(
      () => request('/api/v1/salons/s1/services'),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 403)
        assert.equal(err.message, 'sin acceso al salón')
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('request falls back to a readable message when the error body is not JSON', async () => {
  clearToken()
  const stub = stubFetch(() => new Response('<html>bad gateway</html>', { status: 502 }))
  try {
    await assert.rejects(
      () => request('/api/v1/salons'),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 502)
        assert.match(err.message, /502/)
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('a 401 clears the stored token so the UI returns to login', async () => {
  const stub = stubFetch(() => json({ error: 'token vencido' }, 401))
  try {
    setToken('stale-token')
    await assert.rejects(() => request('/api/v1/salons/s1/dashboard'))
    assert.equal(getToken(), null)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('request accepts empty responses (204) without failing', async () => {
  clearToken()
  const stub = stubFetch(() => new Response(null, { status: 204 }))
  try {
    const out = await request('/api/v1/salons/s1/services/svc-1', { method: 'PUT', body: {} })
    assert.equal(out, undefined)
  } finally {
    stub.restore()
  }
})

test('request reports network failures as NetworkError, not as an HTTP error', async () => {
  clearToken()
  const original = globalThis.fetch
  globalThis.fetch = (() => Promise.reject(new TypeError('fetch failed'))) as typeof globalThis.fetch
  try {
    await assert.rejects(
      () => request('/api/v1/salons/s1/dashboard'),
      (err: unknown) => {
        assert.ok(err instanceof NetworkError)
        assert.match(err.message, /conectar/)
        return true
      },
    )
  } finally {
    globalThis.fetch = original
  }
})
