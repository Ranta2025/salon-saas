import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from './api.ts'
import {
  CLIENT_HISTORY_UNREADABLE_MESSAGE,
  EMPTY_HISTORY_TEXT,
  NO_LAST_VISIT_TEXT,
  NO_MONEY_HINT,
  NO_NEXT_VISIT_TEXT,
  clientHistoryMoney,
  fetchClientHistory,
  formatHistoryInstant,
  hasLinkedBookings,
  normalizeClientHistory,
  spendText,
  summaryCards,
} from './clientHistory.ts'
import { formatCents } from './money.ts'

type Call = { url: string; method: string; body: string | undefined }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
    })
    return handler({ url: String(input), method: init?.method ?? 'GET', body: undefined })
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

/** Frozen shape of `GET /salons/:id/clients/:cid/history`. */
function historyRaw(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    client_id: 'c1',
    summary: {
      visits: 3,
      no_shows: 1,
      cancelled: 2,
      upcoming: 1,
      last_visit: '2026-10-02',
      next_visit: '2026-11-20T21:30:00Z',
      timezone: 'America/Mexico_City',
    },
    bookings: [
      { id: 'b1', starts_at: '2026-10-02T16:00:00Z', status: 'done', service_name: 'Corte', professional_name: 'Ana' },
      { id: 'b2', starts_at: '2026-11-20T21:30:00Z', status: 'booked', service_name: 'Color', professional_name: 'Bea' },
    ],
    spend: { sales_count: 2, total_cents: 125050 },
    sales: [
      { id: 'sa1', created_at: '2026-10-02T16:30:00Z', total_cents: 90050, method: 'cash' },
      { id: 'sa2', created_at: '2026-11-01T17:00:00Z', total_cents: 35000, method: 'card' },
    ],
    ...over,
  }
}

function historyOf(over: Record<string, unknown> = {}) {
  const history = normalizeClientHistory(historyRaw(over))
  assert.ok(history, 'the frozen shape should always be readable')
  return history
}

// --- la forma congelada ---

test('normalizeClientHistory reads the frozen shape of the history', () => {
  const history = historyOf()
  assert.equal(history.client_id, 'c1')
  assert.deepEqual(history.summary, {
    visits: 3,
    no_shows: 1,
    cancelled: 2,
    upcoming: 1,
    last_visit: '2026-10-02',
    next_visit: '2026-11-20T21:30:00Z',
    timezone: 'America/Mexico_City',
  })
  assert.deepEqual(history.spend, { sales_count: 2, total_cents: 125050 })
  assert.equal(history.sales?.length, 2)
  // Each list arrives newest first and is pinned that way.
  assert.deepEqual(
    history.bookings.map((booking) => booking.id),
    ['b2', 'b1'],
  )
  assert.deepEqual(
    history.sales?.map((sale) => sale.id),
    ['sa2', 'sa1'],
  )
})

test('normalizeClientHistory never invents a count or a date it did not receive', () => {
  const history = historyOf({
    summary: { visits: 'muchas', last_visit: 'ayer', timezone: 'America/Mexico_City' },
    bookings: [{ id: 'b1', starts_at: 'no es una fecha', status: 'done' }],
    spend: null,
    sales: null,
  })
  assert.deepEqual(history.summary, {
    visits: 0,
    no_shows: 0,
    cancelled: 0,
    upcoming: 0,
    last_visit: null,
    next_visit: null,
    timezone: 'America/Mexico_City',
  })
  assert.deepEqual(history.bookings, [])
  assert.equal(history.spend, null)
  assert.equal(history.sales, null)
  assert.equal(normalizeClientHistory(null), null)
  assert.equal(normalizeClientHistory({ summary: {} }), null)
})

// --- el dinero solo cuando la API lo manda (D6) ---

test('a professional history has no spend and no sales at all', () => {
  const professional = historyOf({ spend: null, sales: null })
  assert.equal(clientHistoryMoney(professional), null)
  const cards = summaryCards(professional)
  const money = JSON.stringify(cards)
  assert.equal(money.includes('$'), false)
})

test('a client_id spent leak is dropped: no spend means no sales', () => {
  const leaky = historyOf({ spend: null })
  assert.equal(leaky.spend, null)
  assert.equal(leaky.sales, null)
  assert.equal(clientHistoryMoney(leaky), null)
})

test('with spend the money card and the sales list are the ones of the server', () => {
  const money = clientHistoryMoney(historyOf())
  assert.ok(money)
  assert.equal(money.spend.total_cents, 125050)
  assert.equal(money.spend.sales_count, 2)
  assert.equal(money.sales.length, 2)
  assert.equal(spendText(money.spend), `$1,250.50 en 2 ventas vinculadas`)
  assert.equal(spendText({ sales_count: 1, total_cents: 35000 }), '$350.00 en 1 venta vinculada')
  assert.equal(spendText({ sales_count: 0, total_cents: 0 }), '$0.00 en 0 ventas vinculadas')
  // Every amount goes through formatCents, never a division.
  for (const sale of money.sales) {
    assert.equal(Number.isInteger(sale.total_cents), true)
    assert.equal(typeof formatCents(sale.total_cents), 'string')
  }
})

test('a cents value that is not an integer is never painted as money', () => {
  const floatSpend = historyOf({
    spend: { sales_count: 1, total_cents: 12.5 },
    sales: [{ id: 'sa1', created_at: '2026-10-02T16:30:00Z', total_cents: 12.5, method: 'cash' }],
  })
  assert.equal(floatSpend.spend, null)
  assert.equal(floatSpend.sales, null)
  assert.equal(clientHistoryMoney(floatSpend), null)

  const floatRow = historyOf({
    spend: { sales_count: 1, total_cents: 90050 },
    sales: [
      { id: 'sa1', created_at: '2026-10-02T16:30:00Z', total_cents: 90050, method: 'cash' },
      { id: 'sa2', created_at: '2026-11-01T17:00:00Z', total_cents: 100.5, method: 'card' },
    ],
  })
  assert.deepEqual(
    floatRow.sales?.map((sale) => sale.id),
    ['sa1'],
  )
})

// --- tarjetas de resumen ---

test('the summary cards show visits, no-shows, next and last visit', () => {
  const cards = summaryCards(historyOf())
  assert.deepEqual(
    cards.map((card) => card.label),
    ['Visitas', 'No asistió', 'Próxima visita', 'Última visita'],
  )
  assert.deepEqual(
    cards.map((card) => card.value),
    ['3', '1', '2026-11-20 15:30', '2026-10-02'],
  )
})

test('the last visit is the day the server sent and the next visit is local to the salon', () => {
  const cards = summaryCards(
    historyOf({
      summary: {
        visits: 0,
        no_shows: 0,
        cancelled: 0,
        upcoming: 0,
        last_visit: null,
        next_visit: null,
        timezone: 'America/Mexico_City',
      },
    }),
  )
  assert.equal(cards[2]?.value, NO_NEXT_VISIT_TEXT)
  assert.equal(cards[3]?.value, NO_LAST_VISIT_TEXT)
  assert.equal(NO_NEXT_VISIT_TEXT, 'Sin turno agendado')
  assert.equal(NO_LAST_VISIT_TEXT, 'Sin visitas registradas')

  // Same instant, two zones: the day depends on the salon, never on the browser.
  assert.equal(formatHistoryInstant('2026-11-20T02:30:00Z', 'America/Mexico_City'), '2026-11-19 20:30')
  assert.equal(formatHistoryInstant('2026-11-20T02:30:00Z', 'Europe/Madrid'), '2026-11-20 03:30')
  assert.equal(formatHistoryInstant('', 'America/Mexico_City'), '')
  assert.equal(formatHistoryInstant('ayer', 'America/Mexico_City'), '')
  // An unusable zone falls back to the known one instead of throwing.
  assert.equal(formatHistoryInstant('2026-11-20T21:30:00Z', 'no/es-zona'), '2026-11-20 15:30')
})

// --- estado vacío y textos fijos ---

test('the empty history text is exactly the one of the contract', () => {
  assert.equal(
    EMPTY_HISTORY_TEXT,
    'Todavía no hay turnos vinculados a esta clienta. Los turnos nuevos se vinculan solos; los anteriores, cuando el teléfono coincide con una sola ficha. Las ventas anteriores no se atribuyen.',
  )
  assert.equal(hasLinkedBookings(historyOf()), true)
  assert.equal(hasLinkedBookings(historyOf({ bookings: [] })), false)
})

test('the notice for a role without money has no figure in it', () => {
  assert.equal(NO_MONEY_HINT.includes('$'), false)
  assert.equal(CLIENT_HISTORY_UNREADABLE_MESSAGE.includes('$'), false)
})

// --- GET /salons/:id/clients/:cid/history ---

test('fetchClientHistory asks GET /salons/:id/clients/:cid/history', async () => {
  const stub = stubFetch(() => json(historyRaw()))
  try {
    const history = await fetchClientHistory('s1', 'c1')
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1/history`)
    assert.equal(history.summary.visits, 3)
  } finally {
    stub.restore()
  }
})

test('a 404 of the history keeps «Ficha no encontrada» verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'Ficha no encontrada' }, 404))
  try {
    await assert.rejects(
      () => fetchClientHistory('s1', 'c1'),
      (error: unknown) => {
        assert.ok(error instanceof ApiError)
        assert.equal(error.status, 404)
        assert.equal(error.message, 'Ficha no encontrada')
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('an unreadable answer is reported instead of an empty history', async () => {
  const stub = stubFetch(() => json({ ok: true }))
  try {
    await assert.rejects(
      () => fetchClientHistory('s1', 'c1'),
      (error: unknown) => {
        assert.ok(error instanceof ApiError)
        assert.equal(error.message, CLIENT_HISTORY_UNREADABLE_MESSAGE)
        return true
      },
    )
  } finally {
    stub.restore()
  }
})
