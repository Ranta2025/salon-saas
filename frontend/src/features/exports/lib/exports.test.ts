import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, NetworkError, UNAUTHORIZED_EVENT } from '../../../lib/api.ts'
import { todayInTimeZone } from '../../../lib/datetime.ts'
import { clearToken, getToken, setToken } from '../../../lib/session.ts'
import {
  canExportCsv,
  checkExportRange,
  CLIENTS_PRIVACY_NOTICE,
  createExportGate,
  defaultExportRange,
  downloadExportCsv,
  EXPORT_BUSY_MESSAGE,
  EXPORT_FALLBACK_FILENAMES,
  EXPORT_KIND_LABELS,
  exportErrorMessage,
  fetchExportCsv,
  INVALID_RANGE_MESSAGE,
  loadExportContext,
  MAX_EXPORT_RANGE_DAYS,
  runExport,
  type ExportCsvFile,
  type ExportKind,
} from './exports.ts'
import { EXPORT_ERROR_MESSAGE } from '../../team/lib/payrollRuns.ts'

type Call = { url: string; method: string; headers: Record<string, string> }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers: Record<string, string> = {}
    const raw = init?.headers
    if (raw && !Array.isArray(raw) && !(raw instanceof Headers)) {
      for (const [key, value] of Object.entries(raw)) {
        headers[key] = String(value)
      }
    }
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers,
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

function csv(body: string, headers: Record<string, string> = {}): Response {
  return new Response(body, {
    status: 200,
    headers: { 'content-type': 'text/csv; charset=utf-8', ...headers },
  })
}

function settings(timezone: string): Response {
  return json({ timezone, open_time: '09:00', close_time: '19:00', slot_step_min: 30 })
}

/** `document` and `URL.createObjectURL` are browser APIs: the tests stub them. */
function stubBrowser() {
  const created: Blob[] = []
  const revoked: string[] = []
  const clicks: { href: string; download: string }[] = []
  const anchor = {
    href: '',
    download: '',
    rel: '',
    click: () => clicks.push({ href: anchor.href, download: anchor.download }),
    remove: () => {},
  }
  const appended: string[] = []
  const originalCreate = URL.createObjectURL
  const originalRevoke = URL.revokeObjectURL
  const originalDocument = (globalThis as { document?: unknown }).document
  URL.createObjectURL = (blob: Blob | MediaSource) => {
    created.push(blob as Blob)
    return `blob:mock-${created.length}`
  }
  URL.revokeObjectURL = (url: string) => {
    revoked.push(url)
  }
  ;(globalThis as { document?: unknown }).document = {
    createElement: () => anchor,
    body: {
      appendChild: (node: unknown) => {
        appended.push(String((node as { href?: string }).href ?? ''))
      },
    },
  }
  return {
    created,
    revoked,
    clicks,
    appended,
    restore: () => {
      URL.createObjectURL = originalCreate
      URL.revokeObjectURL = originalRevoke
      ;(globalThis as { document?: unknown }).document = originalDocument
    },
  }
}

const RANGE = { from: '2026-11-03', to: '2026-12-02' }

// --- default range and the zone of the salon (D2) ---

test('the default range is the last 30 days ending today, today included', () => {
  assert.deepEqual(defaultExportRange('2026-12-02'), RANGE)
  // Crossing a month and a year works on the calendar, not on a fixed offset.
  assert.deepEqual(defaultExportRange('2027-01-15'), { from: '2026-12-17', to: '2027-01-15' })
})

test('loadExportContext reads the zone from GET /salons/:id/settings, never a fixed one', async () => {
  const stub = stubFetch(() => settings('Pacific/Auckland'))
  try {
    // 2026-11-09T12:30:00Z is the 10th in Auckland and still the 9th in Mexico.
    const now = new Date('2026-11-09T12:30:00Z')
    const context = await loadExportContext('s1', now)
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/settings')
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(context.timeZone, 'Pacific/Auckland')
    assert.equal(context.today, todayInTimeZone('Pacific/Auckland', now))
    assert.equal(context.today, '2026-11-10')
    assert.deepEqual(context.range, { from: '2026-10-12', to: '2026-11-10' })
  } finally {
    stub.restore()
  }
})

test('loadExportContext follows a different zone at the same instant', async () => {
  const stub = stubFetch(() => settings('America/Mexico_City'))
  try {
    const context = await loadExportContext('s1', new Date('2026-11-09T12:30:00Z'))
    assert.equal(context.timeZone, 'America/Mexico_City')
    assert.equal(context.today, '2026-11-09')
    assert.deepEqual(context.range, { from: '2026-10-11', to: '2026-11-09' })
  } finally {
    stub.restore()
  }
})

// --- client-side validation of the range (contract message, no request) ---

test('checkExportRange accepts a single day and up to 366 days', () => {
  assert.deepEqual(checkExportRange('2026-11-03', '2026-11-03'), {
    ok: true,
    from: '2026-11-03',
    to: '2026-11-03',
  })
  assert.deepEqual(checkExportRange('2026-01-01', '2026-12-31'), {
    ok: true,
    from: '2026-01-01',
    to: '2026-12-31',
  })
  assert.equal(MAX_EXPORT_RANGE_DAYS, 366)
  assert.equal(
    INVALID_RANGE_MESSAGE,
    'Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días',
  )
})

test('checkExportRange refuses to < from, more than 366 days and malformed days', () => {
  const invalid = { ok: false as const, message: INVALID_RANGE_MESSAGE }
  assert.deepEqual(checkExportRange('2026-11-30', '2026-11-01'), invalid)
  assert.deepEqual(checkExportRange('2025-01-01', '2026-01-02'), invalid)
  assert.deepEqual(checkExportRange('2026-02-30', '2026-03-01'), invalid)
  assert.deepEqual(checkExportRange('', '2026-03-01'), invalid)
  assert.deepEqual(checkExportRange('03/11/2026', '2026-11-03'), invalid)
})

test('an invalid range never sends a request and shows the contract sentence', async () => {
  const stub = stubFetch(() => csv('Fecha\r\n'))
  try {
    await assert.rejects(
      () => fetchExportCsv('s1', { kind: 'sales', range: { from: '2026-11-30', to: '2026-11-01' } }),
      (error: unknown) =>
        error instanceof ApiError && error.status === 400 && error.message === INVALID_RANGE_MESSAGE,
    )
    await assert.rejects(
      () =>
        fetchExportCsv('s1', {
          kind: 'cash-closes',
          range: { from: '2025-01-01', to: '2026-01-02' },
        }),
      (error: unknown) => error instanceof ApiError && error.message === INVALID_RANGE_MESSAGE,
    )
    assert.equal(stub.calls.length, 0)
  } finally {
    stub.restore()
  }
})

// --- the three URLs and the headers of the frozen contract ---

test('the sales export asks export/sales.csv with the inclusive range in the query', async () => {
  const stub = stubFetch(() => csv('Fecha,Venta\r\n'))
  try {
    await fetchExportCsv('s1', { kind: 'sales', range: RANGE })
    assert.equal(
      stub.calls[0]?.url,
      '/api/v1/salons/s1/export/sales.csv?from=2026-11-03&to=2026-12-02',
    )
    assert.equal(stub.calls[0]?.method, 'GET')
  } finally {
    stub.restore()
  }
})

test('the cash closes export asks export/cash-closes.csv with the same range', async () => {
  const stub = stubFetch(() => csv('Apertura,Cierre\r\n'))
  try {
    await fetchExportCsv('s1', { kind: 'cash-closes', range: RANGE })
    assert.equal(
      stub.calls[0]?.url,
      '/api/v1/salons/s1/export/cash-closes.csv?from=2026-11-03&to=2026-12-02',
    )
  } finally {
    stub.restore()
  }
})

test('the clients export asks export/clients.csv with no query at all', async () => {
  const stub = stubFetch(() => csv('Nombre,Teléfono\r\n'))
  try {
    await fetchExportCsv('s1', { kind: 'clients' })
    assert.equal(stub.calls[0]?.url, '/api/v1/salons/s1/export/clients.csv')
    assert.equal(stub.calls[0]?.url.includes('?'), false)
  } finally {
    stub.restore()
  }
})

test('every export sends the bearer token and Accept: text/csv', async () => {
  setToken('tok-123')
  const stub = stubFetch(() => csv('Fecha\r\n'))
  try {
    await fetchExportCsv('s1', { kind: 'sales', range: RANGE })
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer tok-123')
    assert.equal(stub.calls[0]?.headers['Accept'], 'text/csv')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('without a session the export travels with no Authorization header', async () => {
  clearToken()
  const stub = stubFetch(() => csv('Fecha\r\n'))
  try {
    await fetchExportCsv('s1', { kind: 'clients' })
    assert.equal(stub.calls[0]?.headers['Authorization'], undefined)
  } finally {
    stub.restore()
  }
})

// --- the name of the file (D6) ---

test('the filename comes from Content-Disposition, sanitized', async () => {
  const stub = stubFetch(() =>
    csv('Fecha\r\n', {
      'content-disposition':
        'attachment; filename="ventas-2026-11-03_2026-12-02.csv"',
    }),
  )
  try {
    const file = await fetchExportCsv('s1', { kind: 'sales', range: RANGE })
    assert.equal(file.filename, 'ventas-2026-11-03_2026-12-02.csv')
  } finally {
    stub.restore()
  }

  const hostile = stubFetch(() =>
    csv('Nombre\r\n', { 'content-disposition': 'attachment; filename="../../etc/passwd"' }),
  )
  try {
    const file = await fetchExportCsv('s1', { kind: 'clients' })
    assert.equal(file.filename, 'etc-passwd.csv')
  } finally {
    hostile.restore()
  }
})

test('a missing or unusable header falls back to the name of each export', async () => {
  assert.deepEqual(EXPORT_FALLBACK_FILENAMES, {
    sales: 'ventas.csv',
    'cash-closes': 'cierres.csv',
    clients: 'clientes.csv',
  })
  const cases: { kind: ExportKind; headers: Record<string, string>; expected: string }[] = [
    { kind: 'sales', headers: {}, expected: 'ventas.csv' },
    { kind: 'cash-closes', headers: {}, expected: 'cierres.csv' },
    { kind: 'clients', headers: {}, expected: 'clientes.csv' },
    {
      kind: 'clients',
      headers: { 'content-disposition': 'attachment; filename=""' },
      expected: 'clientes.csv',
    },
  ]
  for (const entry of cases) {
    const stub = stubFetch(() => csv('x\r\n', entry.headers))
    try {
      const request = entry.kind === 'clients' ? { kind: entry.kind } : { kind: entry.kind, range: RANGE }
      const file = await fetchExportCsv('s1', request as Parameters<typeof fetchExportCsv>[1])
      assert.equal(file.filename, entry.expected)
    } finally {
      stub.restore()
    }
  }
})

// --- the file goes straight to the download: no CSV is ever parsed ---

test('the downloaded file carries only the Blob and its name, with the bytes untouched', async () => {
  const body = '\ufeffFecha,Venta\r\n2026-11-03 10:00,s1\r\n'
  const stub = stubFetch(() => csv(body))
  try {
    const file = await fetchExportCsv('s1', { kind: 'sales', range: RANGE })
    assert.deepEqual(Object.keys(file).sort(), ['blob', 'filename', 'kind'])
    assert.equal(file.kind, 'sales')
    // Nothing parses the CSV in the client: the bytes travel as they arrived,
    // including the UTF-8 BOM the server writes.
    const bytes = new Uint8Array(await file.blob.arrayBuffer())
    assert.deepEqual([...bytes.slice(0, 3)], [0xef, 0xbb, 0xbf])
    assert.equal(await file.blob.text(), body.slice(1))
  } finally {
    stub.restore()
  }
})

test('downloadExportCsv hands the Blob to the browser and revokes the object URL', () => {
  const browser = stubBrowser()
  try {
    const file: ExportCsvFile = {
      kind: 'sales',
      filename: 'ventas-2026-11-03_2026-12-02.csv',
      blob: new Blob([]),
    }
    downloadExportCsv(file)
    assert.equal(browser.created.length, 1)
    assert.equal(browser.created[0], file.blob)
    assert.deepEqual(browser.clicks, [
      { href: 'blob:mock-1', download: 'ventas-2026-11-03_2026-12-02.csv' },
    ])
    assert.deepEqual(browser.revoked, ['blob:mock-1'])
  } finally {
    browser.restore()
  }
})

// --- one request at a time: the double click sends one ---

test('the gate lets one export run and is free again when it finishes', () => {
  const gate = createExportGate()
  assert.equal(gate.busy(), null)
  assert.equal(gate.start('sales'), true)
  assert.equal(gate.busy(), 'sales')
  assert.equal(gate.start('sales'), false)
  assert.equal(gate.start('clients'), false)
  assert.equal(gate.finish(), true)
  assert.equal(gate.busy(), null)
  assert.equal(gate.start('clients'), true)
  gate.finish()
})

test('a double click on the same button sends exactly one request', async () => {
  const gate = createExportGate()
  const saved: ExportCsvFile[] = []
  let release: () => void = () => {}
  const gateOfFetch = new Promise<void>((resolve) => {
    release = resolve
  })
  const stub = stubFetch(async () => {
    await gateOfFetch
    return csv('Fecha\r\n')
  })
  try {
    const first = runExport(gate, 's1', { kind: 'sales', range: RANGE }, (file) => saved.push(file))
    const second = runExport(gate, 's1', { kind: 'sales', range: RANGE }, (file) => saved.push(file))
    // The second press answers «busy» before the first request resolves.
    assert.deepEqual(await second, { status: 'busy', message: EXPORT_BUSY_MESSAGE })
    release()
    assert.deepEqual(await first, { status: 'done', filename: 'ventas.csv' })
    assert.equal(stub.calls.length, 1)
    assert.equal(saved.length, 1)
  } finally {
    stub.restore()
  }
})

test('the button works again after a 400 and after a 403, showing the sentence verbatim', async () => {
  const gate = createExportGate()
  const saved: ExportCsvFile[] = []
  const badRange = stubFetch(() =>
    json({ error: 'El archivo tiene más de 50000 filas: elegí un rango más corto' }, 400),
  )
  try {
    assert.deepEqual(
      await runExport(gate, 's1', { kind: 'sales', range: RANGE }, (file) => saved.push(file)),
      { status: 'failed', message: 'El archivo tiene más de 50000 filas: elegí un rango más corto' },
    )
  } finally {
    badRange.restore()
  }
  const forbidden = stubFetch(() => json({ error: 'sin permiso' }, 403))
  try {
    assert.deepEqual(
      await runExport(gate, 's1', { kind: 'clients' }, (file) => saved.push(file)),
      { status: 'failed', message: 'sin permiso' },
    )
  } finally {
    forbidden.restore()
  }
  // Nothing was saved and the gate is free for the next press.
  assert.equal(saved.length, 0)
  assert.equal(gate.busy(), null)

  const ok = stubFetch(() => csv('Nombre\r\n', { 'content-disposition': 'attachment; filename="clientes.csv"' }))
  try {
    assert.deepEqual(
      await runExport(gate, 's1', { kind: 'clients' }, (file) => saved.push(file)),
      { status: 'done', filename: 'clientes.csv' },
    )
    assert.equal(saved.length, 1)
  } finally {
    ok.restore()
  }
})

test('an invalid range through runExport fails without a request', async () => {
  const gate = createExportGate()
  const stub = stubFetch(() => csv('Fecha\r\n'))
  try {
    const outcome = await runExport(gate, 's1', {
      kind: 'sales',
      range: { from: '2026-11-30', to: '2026-11-01' },
    })
    assert.deepEqual(outcome, { status: 'failed', message: INVALID_RANGE_MESSAGE })
    assert.equal(stub.calls.length, 0)
    assert.equal(gate.busy(), null)
  } finally {
    stub.restore()
  }
})

// --- failures: a 401 clears the token like the payroll download does ---

test('a 401 of an export clears the token and signals the shell', async () => {
  setToken('tok-123')
  const events: string[] = []
  const originalWindow = (globalThis as { window?: unknown }).window
  ;(globalThis as { window?: unknown }).window = {
    dispatchEvent: (event: Event) => {
      events.push(event.type)
      return true
    },
  }
  const stub = stubFetch(() => json({ error: 'sesión vencida' }, 401))
  try {
    await assert.rejects(
      () => fetchExportCsv('s1', { kind: 'clients' }),
      (error: unknown) => error instanceof ApiError && error.status === 401,
    )
    assert.equal(getToken(), null)
    assert.deepEqual(events, [UNAUTHORIZED_EVENT])
  } finally {
    stub.restore()
    ;(globalThis as { window?: unknown }).window = originalWindow
    clearToken()
  }
})

test('a network failure is explained with the copy of the payroll download', async () => {
  const stub = stubFetch(() => {
    throw new TypeError('fetch failed')
  })
  try {
    await assert.rejects(
      () => fetchExportCsv('s1', { kind: 'sales', range: RANGE }),
      (error: unknown) => error instanceof NetworkError && error.message === EXPORT_ERROR_MESSAGE,
    )
    assert.equal(
      exportErrorMessage(new NetworkError(EXPORT_ERROR_MESSAGE)),
      EXPORT_ERROR_MESSAGE,
    )
    assert.equal(exportErrorMessage(new ApiError(403, 'sin permiso')), 'sin permiso')
    assert.equal(exportErrorMessage(new Error('boom')), 'boom')
    assert.equal(exportErrorMessage(null), EXPORT_ERROR_MESSAGE)
  } finally {
    stub.restore()
  }
})

// --- copy of the screen and the gate by role (D1) ---

test('the privacy notice of the clients export is the sentence of the contract', () => {
  assert.equal(
    CLIENTS_PRIVACY_NOTICE,
    'Incluye teléfonos, emails y notas de tus clientas, que pueden contener datos de salud. Guardá el archivo con cuidado.',
  )
  assert.deepEqual(EXPORT_KIND_LABELS, {
    sales: 'Ventas',
    'cash-closes': 'Cierres de caja',
    clients: 'Clientes',
  })
})

test('only the owner and the administration export: reception and professionals do not', () => {
  assert.equal(canExportCsv('owner'), true)
  assert.equal(canExportCsv('admin'), true)
  assert.equal(canExportCsv('reception'), false)
  assert.equal(canExportCsv('professional'), false)
  assert.equal(canExportCsv(null), false)
})
