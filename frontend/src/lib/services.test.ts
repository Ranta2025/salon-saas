import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, createService } from './api.ts'
import { clearToken, setToken } from './session.ts'

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

const INPUT = {
  name: 'Corte',
  duration_min: 45,
  price_cents: 35000,
  commission_pct: 30,
}

test('createService sends the optional professional_ids of who does it', async () => {
  const stub = stubFetch(() => json({ id: 's9' }, 201))
  try {
    setToken('jwt-services')
    await createService('s1', { ...INPUT, professional_ids: ['u1', 'u1', 'u2'] })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/services`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      ...INPUT,
      professional_ids: ['u1', 'u2'],
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('createService omits professional_ids when nobody was marked', async () => {
  const stub = stubFetch(() => json({ id: 's9' }, 201))
  try {
    setToken('jwt-services')
    await createService('s1', INPUT)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), INPUT)
  } finally {
    stub.restore()
    clearToken()
  }
})
