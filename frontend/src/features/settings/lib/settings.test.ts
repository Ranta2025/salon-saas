import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from '../../../lib/api.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  DEFAULT_SETTINGS,
  SLOT_STEPS,
  TIME_ZONES,
  fetchSalonHours,
  fetchSettings,
  normalizeSettings,
  saveSalonHours,
  saveSettings,
  toSettingsPayload,
  validateSettings,
} from './settings.ts'

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

const validDraft = {
  timezone: 'America/Mexico_City',
  openTime: '09:00',
  closeTime: '19:00',
  slotStepMin: 30,
}

test('the time zone list includes the common Latin American and Spanish zones plus UTC', () => {
  const values = TIME_ZONES.map((zone) => zone.value)
  for (const zone of [
    'America/Mexico_City',
    'America/Bogota',
    'America/Lima',
    'America/Argentina/Buenos_Aires',
    'America/Santiago',
    'America/Sao_Paulo',
    'Europe/Madrid',
    'UTC',
  ]) {
    assert.ok(values.includes(zone), `falta ${zone}`)
  }
  assert.ok(TIME_ZONES.every((zone) => zone.label.trim() !== ''))
  assert.deepEqual([...SLOT_STEPS], [15, 30, 60])
})

test('normalizeSettings reads the snake_case body of the API and falls back to the defaults', () => {
  assert.deepEqual(
    normalizeSettings({ timezone: 'America/Bogota', open_time: '08:00', close_time: '18:00', slot_step_min: 60 }),
    { timezone: 'America/Bogota', openTime: '08:00', closeTime: '18:00', slotStepMin: 60 },
  )
  assert.deepEqual(normalizeSettings(null), DEFAULT_SETTINGS)
  assert.deepEqual(normalizeSettings({ timezone: '' }), DEFAULT_SETTINGS)
  assert.equal(normalizeSettings({ ...validDraft, timezone: 'America/Lima', open_time: '08:00', close_time: '18:00', slot_step_min: 45 })?.slotStepMin, 30)
})

test('validateSettings accepts a complete draft and returns the normalised settings', () => {
  const result = validateSettings({ ...validDraft, openTime: ' 08:30 ' })
  assert.equal(result.ok, true)
  assert.deepEqual(result.ok ? result.settings : null, { ...validDraft, openTime: '08:30' })
})

test('validateSettings reports the field and a Spanish message for a bad time', () => {
  const result = validateSettings({ ...validDraft, openTime: '9:00' })
  assert.equal(result.ok, false)
  assert.deepEqual(result.ok ? null : { field: result.field }, { field: 'openTime' })
  assert.match(result.ok ? '' : result.message, /HH:MM/)
})

test('validateSettings rejects a closing time that is not after the opening one', () => {
  const equal = validateSettings({ ...validDraft, openTime: '19:00', closeTime: '19:00' })
  assert.equal(equal.ok, false)
  assert.equal(equal.ok ? null : equal.field, 'closeTime')
  const before = validateSettings({ ...validDraft, openTime: '19:00', closeTime: '09:00' })
  assert.equal(before.ok ? null : before.field, 'closeTime')
})

test('validateSettings only accepts the allowed slot steps', () => {
  for (const step of SLOT_STEPS) {
    assert.equal(validateSettings({ ...validDraft, slotStepMin: step }).ok, true)
  }
  const invalid = validateSettings({ ...validDraft, slotStepMin: 45 })
  assert.equal(invalid.ok, false)
  assert.equal(invalid.ok ? null : invalid.field, 'slotStepMin')
})

test('validateSettings rejects an empty or unknown time zone', () => {
  const empty = validateSettings({ ...validDraft, timezone: '' })
  assert.equal(empty.ok ? null : empty.field, 'timezone')
  const unknown = validateSettings({ ...validDraft, timezone: 'Marte/Olympus' })
  assert.equal(unknown.ok, false)
  assert.equal(unknown.ok ? null : unknown.field, 'timezone')
})

test('toSettingsPayload sends the snake_case body the API expects', () => {
  assert.deepEqual(toSettingsPayload(validDraft), {
    timezone: 'America/Mexico_City',
    open_time: '09:00',
    close_time: '19:00',
    slot_step_min: 30,
  })
})

test('fetchSettings reads the configuration of the salon', async () => {
  const stub = stubFetch(() =>
    json({ timezone: 'America/Lima', open_time: '10:00', close_time: '20:00', slot_step_min: 15 }),
  )
  try {
    setToken('jwt-settings')
    const settings = await fetchSettings('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/settings`)
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-settings')
    assert.deepEqual(settings, {
      timezone: 'America/Lima',
      openTime: '10:00',
      closeTime: '20:00',
      slotStepMin: 15,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('saveSettings PUTs the whole configuration and returns what the server stored', async () => {
  const stub = stubFetch(() =>
    json({ timezone: 'America/Bogota', open_time: '08:00', close_time: '18:00', slot_step_min: 60 }),
  )
  try {
    setToken('jwt-settings')
    const saved = await saveSettings('s1', { ...validDraft, timezone: 'America/Bogota' })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/settings`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      timezone: 'America/Bogota',
      open_time: '09:00',
      close_time: '19:00',
      slot_step_min: 30,
    })
    assert.equal(saved.timezone, 'America/Bogota')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('saveSettings surfaces the server message when the API rejects the change', async () => {
  const stub = stubFetch(() => json({ error: 'solo owner o admin pueden cambiar la configuración' }, 403))
  try {
    setToken('jwt-settings')
    await assert.rejects(
      () => saveSettings('s1', validDraft),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 403)
        assert.match(err.message, /owner o admin/)
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchSalonHours reads the weekly hours and the custom flag', async () => {
  const stub = stubFetch(() =>
    json({
      days: [
        { weekday: 1, open: '09:00', close: '19:00' },
        { weekday: 6, open: '09:00', close: '14:00' },
      ],
      custom: true,
    }),
  )
  try {
    setToken('jwt-hours')
    const hours = await fetchSalonHours('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/hours`)
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-hours')
    assert.deepEqual(hours, {
      days: [
        { weekday: 1, open: '09:00', close: '19:00' },
        { weekday: 6, open: '09:00', close: '14:00' },
      ],
      custom: true,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('saveSalonHours PUTs the open days and an empty list resets the single window', async () => {
  const stub = stubFetch(() => json({ days: [], custom: false }))
  try {
    setToken('jwt-hours')
    await saveSalonHours('s1', [{ weekday: 1, open: '09:00', close: '19:00' }])
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/hours`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      days: [{ weekday: 1, open: '09:00', close: '19:00' }],
    })
    assert.deepEqual(await saveSalonHours('s1', []), { days: [], custom: false })
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), { days: [] })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('saveSalonHours surfaces the server sentence verbatim', async () => {
  const stub = stubFetch(() =>
    json({ error: 'Los tramos de un mismo día no pueden solaparse' }, 400),
  )
  try {
    setToken('jwt-hours')
    await assert.rejects(
      () => saveSalonHours('s1', [{ weekday: 1, open: '09:00', close: '19:00' }]),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, 'Los tramos de un mismo día no pueden solaparse')
        return true
      },
    )
  } finally {
    stub.restore()
    clearToken()
  }
})
