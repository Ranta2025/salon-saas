import test from 'node:test'
import assert from 'node:assert/strict'
import {
  ApiError,
  NetworkError,
  API_BASE,
  fetchMe,
  fetchPublicSalon,
  fetchStaff,
  createClient,
  describeApiError,
  friendlyApiMessage,
  request,
} from './api.ts'
import { subscribeOfflineData } from './offlineData.ts'
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

test('a request that never answers times out as NetworkError so the POS can queue it', async () => {
  clearToken()
  const original = globalThis.fetch
  globalThis.fetch = ((_input: RequestInfo | URL, init?: RequestInit) =>
    new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => {
        reject(new DOMException('Aborted', 'AbortError'))
      })
    })) as typeof globalThis.fetch
  try {
    await assert.rejects(
      () =>
        request('/api/v1/salons/s1/sales', {
          method: 'POST',
          body: {},
          timeoutMs: 10,
        }),
      (err: unknown) => {
        assert.ok(err instanceof NetworkError)
        assert.match(err.message, /tardó/)
        return true
      },
    )
  } finally {
    globalThis.fetch = original
  }
})

test('fetchMe reads the identity endpoint for the current session', async () => {
  const stub = stubFetch(() =>
    json({
      user: { id: 'u1', name: 'Ana', email: 'ana@salon.mx' },
      salons: [{ id: 's1', slug: 'aurelia', name: 'Aurelia', role: 'owner' }],
    }),
  )
  try {
    setToken('jwt-me')
    const me = await fetchMe()
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/me`)
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-me')
    assert.equal(me.user.id, 'u1')
    assert.equal(me.salons[0]?.slug, 'aurelia')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchMe surfaces the server message when the identity call fails', async () => {
  clearToken()
  const stub = stubFetch(() => json({ error: 'sesión no válida' }, 401))
  try {
    setToken('stale')
    await assert.rejects(
      () => fetchMe(),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 401)
        assert.equal(err.message, 'sesión no válida')
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchStaff reads the salon team and keeps the API order', async () => {
  const stub = stubFetch(() =>
    json({
      staff: [
        { id: 'u1', name: 'Ana', role: 'owner' },
        { id: 'u2', name: 'Beto', role: 'professional' },
      ],
    }),
  )
  try {
    setToken('jwt-staff')
    const staff = await fetchStaff('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/staff`)
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-staff')
    assert.deepEqual(
      staff.map((member) => member.id),
      ['u1', 'u2'],
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchPublicSalon reads services without an Authorization header', async () => {
  const stub = stubFetch(() =>
    json({
      salon: { id: 's1', slug: 'aurelia', name: 'Aurelia' },
      services: [{ id: 'svc-1', name: 'Corte', duration_min: 45, price_cents: 38000 }],
    }),
  )
  try {
    clearToken()
    const salon = await fetchPublicSalon('aurelia')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/public/salons/aurelia`)
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
    assert.equal(salon.services[0]?.priceCents, 38000)
  } finally {
    stub.restore()
  }
})

test('fetchPublicSalon reports a 404 with the Spanish message of the API', async () => {
  clearToken()
  const stub = stubFetch(() => json({ error: 'Salón no encontrado' }, 404))
  try {
    await assert.rejects(
      () => fetchPublicSalon('nope'),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 404)
        assert.equal(err.message, 'Salón no encontrado')
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('friendlyApiMessage explains a 403 in the UI instead of showing the raw server text', () => {
  const forbidden = friendlyApiMessage(new ApiError(403, 'sin acceso al salón'), 'No pudimos cargar.')
  assert.match(forbidden, /rol no permite/)
  assert.equal(friendlyApiMessage(new ApiError(400, 'correo inválido'), 'No pudimos cargar.'), 'correo inválido')
  assert.equal(
    friendlyApiMessage(new NetworkError('No pudimos conectar con el servidor.'), 'No pudimos cargar.'),
    'No pudimos conectar con el servidor.',
  )
  assert.equal(friendlyApiMessage('boom', 'No pudimos cargar.'), 'No pudimos cargar.')
})

test('describeApiError marks a 403 as not retryable and keeps the friendly message', () => {
  const forbidden = describeApiError(new ApiError(403, 'sin permiso'), 'No pudimos cargar.')
  assert.match(forbidden.message, /rol no permite/)
  assert.equal(forbidden.retryable, false)

  const invalid = describeApiError(new ApiError(400, 'nombre vacío'), 'No pudimos cargar.')
  assert.deepEqual(invalid, { message: 'nombre vacío', retryable: true })

  const offline = describeApiError(
    new NetworkError('Sin conexión y sin copia guardada de estos datos.'),
    'No pudimos cargar.',
  )
  assert.deepEqual(offline, {
    message: 'Sin conexión y sin copia guardada de estos datos.',
    retryable: true,
  })

  assert.deepEqual(describeApiError('boom', 'No pudimos cargar.'), {
    message: 'No pudimos cargar.',
    retryable: true,
  })
})

test('an offline JSON answer of the service worker is a network failure, not a server error', async () => {
  clearToken()
  const stub = stubFetch(
    () =>
      new Response(
        JSON.stringify({
          error: 'Sin conexión y sin copia guardada de estos datos.',
          offline: true,
        }),
        { status: 503, headers: { 'content-type': 'application/json' } },
      ),
  )
  try {
    await assert.rejects(
      () => request('/api/v1/salons/s1/bookings'),
      (err: unknown) => {
        assert.ok(err instanceof NetworkError)
        assert.equal(err instanceof ApiError, false)
        assert.equal(err.message, 'Sin conexión y sin copia guardada de estos datos.')
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('a response served from the offline cache tells the UI it is saved data', async () => {
  clearToken()
  const paths: string[] = []
  const unsubscribe = subscribeOfflineData((path) => paths.push(path))
  const stub = stubFetch(
    () =>
      new Response(JSON.stringify({ bookings: [] }), {
        status: 200,
        headers: {
          'content-type': 'application/json',
          'x-salon-offline-cache': '1',
        },
      }),
  )
  try {
    const payload = await request<{ bookings: unknown[] }>('/api/v1/salons/s1/bookings')
    assert.deepEqual(payload, { bookings: [] })
    assert.deepEqual(paths, ['/api/v1/salons/s1/bookings'])
  } finally {
    unsubscribe()
    stub.restore()
  }
})

test('createClient posts name, phone, email and notes and returns the new file', async () => {
  const stub = stubFetch(() =>
    json(
      {
        id: 'cli-1',
        salon_id: 's1',
        name: 'Lía',
        phone: '11 5555-1234',
        email: 'lia@correo.com',
        notes: 'prefiere turnos de mañana',
        created_at: '2026-12-03T09:00:00Z',
      },
      201,
    ),
  )
  try {
    setToken('jwt-clients')
    const client = await createClient('s1', {
      name: 'Lía',
      phone: '11 5555-1234',
      email: 'lia@correo.com',
      notes: 'prefiere turnos de mañana',
    })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/clients`)
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-clients')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      name: 'Lía',
      phone: '11 5555-1234',
      email: 'lia@correo.com',
      notes: 'prefiere turnos de mañana',
    })
    assert.equal(client.id, 'cli-1')
    assert.equal(client.email, 'lia@correo.com')
  } finally {
    stub.restore()
    clearToken()
  }
})
