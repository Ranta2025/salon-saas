import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError, NetworkError } from './api.ts'
import { visitorTimeZone } from './publicBooking.ts'
import {
  BOOKING_LINK_MESSAGES,
  CANCEL_ACTION,
  CANCEL_CONFIRM_NOTE,
  CANCEL_DONE,
  CHANGE_ACTION,
  INVALID_LINK_HELP,
  NEW_LINK_FALLBACK_NOTE,
  RESCHEDULE_DONE,
  bookingLinkErrorMessage,
  canCancel,
  canReschedule,
  cancelBookingLink,
  cancelConfirmQuestion,
  cutOffNotice,
  defaultPickDate,
  fetchLinkSlots,
  formatLinkDay,
  formatLinkWhen,
  formatLinkZone,
  initialLinkChange,
  isBookingLinkToken,
  isDateValue,
  isLinkTerminal,
  lookupBookingLink,
  newLinkNotice,
  normalizeLinkBooking,
  normalizeReschedule,
  readBookingLinkHash,
  reduceLinkChange,
  rescheduleBookingLink,
  rescheduleConfirmQuestion,
  salonTimeZone,
  takeBookingLinkToken,
  type BookingLinkBooking,
  type HistoryLike,
} from './bookingLinks.ts'

/** 32 random bytes in base64url are exactly 43 characters (contract F1/D1). */
const TOKEN = 'a'.repeat(43)
const TOKEN_MIXED = 'AbC-_z9' + 'x'.repeat(36)
const ZONE = 'America/Bogota'
const NOW = new Date('2026-03-10T15:00:00.000Z')

type Call = {
  url: string
  method: string
  headers: Record<string, string>
  body: string | undefined
}

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const headers: Record<string, string> = {}
    const raw = (init?.headers ?? {}) as Record<string, string>
    for (const [key, value] of Object.entries(raw)) {
      headers[key] = String(value)
    }
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers,
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

function booking(over: Partial<BookingLinkBooking> = {}): BookingLinkBooking {
  return {
    salon_name: 'Aurelia',
    service_name: 'Corte',
    professional_name: 'Ana',
    starts_at: '2026-03-14T16:00:00Z',
    ends_at: '2026-03-14T17:00:00Z',
    timezone: ZONE,
    can_change: true,
    min_notice_hours: 2,
    ...over,
  }
}

function okLookup(over: Partial<BookingLinkBooking> = {}) {
  return json({ booking: booking(over) })
}

/** Records the calls of `history.replaceState` in order with the fetch stubs. */
function fakeHistory(sequence: string[]): HistoryLike & { urls: (string | null | undefined)[] } {
  const urls: (string | null | undefined)[] = []
  return {
    urls,
    replaceState: (_data: unknown, _unused: string, url?: string | null) => {
      sequence.push('replace')
      urls.push(url)
    },
  }
}

// --- the token lives in the fragment, never in the path or the query ---------

test('readBookingLinkHash takes the 43-character token out of the first fragment', () => {
  assert.equal(TOKEN.length, 43)
  assert.deepEqual(readBookingLinkHash(`https://salon.test/turno#${TOKEN}`), {
    token: TOKEN,
    url: 'https://salon.test/turno',
  })
  assert.deepEqual(readBookingLinkHash(`http://localhost:5173/turno?x=1#${TOKEN_MIXED}`), {
    token: TOKEN_MIXED,
    url: 'http://localhost:5173/turno?x=1',
  })
})

test('readBookingLinkHash leaves a URL without a fragment untouched', () => {
  assert.deepEqual(readBookingLinkHash('https://salon.test/turno'), {
    token: null,
    url: 'https://salon.test/turno',
  })
  assert.deepEqual(readBookingLinkHash('https://salon.test/turno?x=1'), {
    token: null,
    url: 'https://salon.test/turno?x=1',
  })
})

test('readBookingLinkHash consumes malformed fragments and never reads them as a token', () => {
  const bad = [
    '',
    '#' + 'a'.repeat(42),
    '#' + 'a'.repeat(44),
    '#' + 'a'.repeat(42) + '=',
    '#' + 'a'.repeat(20),
    '#con espacio' + 'a'.repeat(31),
    '#a+b' + 'a'.repeat(40),
    '#a%20b' + 'a'.repeat(38),
    '#',
  ]
  for (const hash of bad) {
    const read = readBookingLinkHash(`https://salon.test/turno${hash}`)
    assert.equal(read.token, null, hash)
    // The link is clean even when the token is unusable: no trace is left behind.
    assert.equal(read.url, 'https://salon.test/turno', hash)
  }
})

test('readBookingLinkHash only trusts the first fragment and keeps the extra anchors', () => {
  assert.deepEqual(readBookingLinkHash(`https://salon.test/turno#${TOKEN}#paso-2`), {
    token: TOKEN,
    url: 'https://salon.test/turno#paso-2',
  })
  // A token after another fragment is not the link slot: never guessed, never kept.
  assert.deepEqual(readBookingLinkHash(`https://salon.test/turno#paso-2#${TOKEN}`), {
    token: null,
    url: 'https://salon.test/turno#paso-2',
  })
})

test('isBookingLinkToken accepts only 43 base64url characters', () => {
  assert.equal(isBookingLinkToken(TOKEN), true)
  assert.equal(isBookingLinkToken(TOKEN_MIXED), true)
  assert.equal(isBookingLinkToken('a'.repeat(42)), false)
  assert.equal(isBookingLinkToken('a'.repeat(44)), false)
  assert.equal(isBookingLinkToken('a'.repeat(42) + '='), false)
  assert.equal(isBookingLinkToken('a'.repeat(42) + '+'), false)
  assert.equal(isBookingLinkToken('a'.repeat(42) + ' '), false)
  assert.equal(isBookingLinkToken(''), false)
})

test('takeBookingLinkToken wipes the address bar and returns the token', () => {
  const sequence: string[] = []
  const history = fakeHistory(sequence)
  const token = takeBookingLinkToken(`https://salon.test/turno#${TOKEN}`, history)
  assert.equal(token, TOKEN)
  assert.deepEqual(history.urls, ['https://salon.test/turno'])

  // Nothing to clean: history is not touched at all.
  const clean = fakeHistory(sequence)
  assert.equal(takeBookingLinkToken('https://salon.test/turno', clean), null)
  assert.deepEqual(clean.urls, [])

  // A page without history (tests, a browser that refuses it) still reads the token.
  assert.equal(takeBookingLinkToken(`https://salon.test/turno#${TOKEN}`, null), TOKEN)
})

test('the fragment is wiped BEFORE the first request of the page', async () => {
  const stub = stubFetch((call) => {
    sequence.push(`fetch:${call.url}`)
    return okLookup()
  })
  const sequence: string[] = []
  const history = fakeHistory(sequence)
  try {
    // The exact composition of the page on its first render.
    const token = takeBookingLinkToken(`https://salon.test/turno#${TOKEN}`, history)
    const lookup = token === null ? null : await lookupBookingLink(token)
    assert.equal(lookup?.booking.salon_name, 'Aurelia')
    assert.deepEqual(sequence, ['replace', `fetch:${API_BASE}/api/v1/public/booking-links/lookup`])
    // The address bar is clean and the token never travels in the URL.
    assert.deepEqual(history.urls, ['https://salon.test/turno'])
    assert.equal(stub.calls[0]?.url.includes(TOKEN), false)
    assert.equal(stub.calls[0]?.url.includes('?'), false)
  } finally {
    stub.restore()
  }
})

test('an invalid fragment makes no request at all', async () => {
  const stub = stubFetch(() => okLookup())
  try {
    for (const hash of ['#nope', '#' + 'a'.repeat(42), '#%41', '']) {
      const sequence: string[] = []
      const history = fakeHistory(sequence)
      const token = takeBookingLinkToken(`https://salon.test/turno${hash}`, history)
      if (token !== null) {
        await lookupBookingLink(token)
      }
      assert.equal(token, null, hash)
      assert.equal(stub.calls.length, 0, hash)
    }
    // Not even the fallback lookup of the page ran: the invalid screen needs no API.
    assert.equal(stub.calls.length, 0)
  } finally {
    stub.restore()
  }
})

// --- request bodies of the contract ----------------------------------------

test('lookup POSTs {token} in the body and never in the query string', async () => {
  const stub = stubFetch(() => okLookup())
  try {
    const lookup = await lookupBookingLink(TOKEN)
    assert.equal(stub.calls.length, 1)
    const call = stub.calls[0]!
    assert.equal(call.url, `${API_BASE}/api/v1/public/booking-links/lookup`)
    assert.equal(call.method, 'POST')
    assert.deepEqual(JSON.parse(call.body ?? ''), { token: TOKEN })
    assert.equal(call.headers['Content-Type'], 'application/json')
    // Public route: no session header even when the browser has one.
    assert.equal(call.headers['Authorization'], undefined)
    assert.equal(lookup.booking.professional_name, 'Ana')
  } finally {
    stub.restore()
  }
})

test('slots POSTs {token, date} and reads the public availability shape', async () => {
  const stub = stubFetch(() =>
    json({
      slots: [
        { professional_id: 'u2', starts_at: '2026-03-15T15:00:00Z', ends_at: '2026-03-15T16:00:00Z' },
        { professional_id: '', starts_at: '2026-03-15T17:00:00Z', ends_at: '2026-03-15T18:00:00Z' },
        null,
      ],
    }),
  )
  try {
    const slots = await fetchLinkSlots(TOKEN, '2026-03-15')
    const call = stub.calls[0]!
    assert.equal(call.url, `${API_BASE}/api/v1/public/booking-links/slots`)
    assert.equal(call.method, 'POST')
    assert.deepEqual(JSON.parse(call.body ?? ''), { token: TOKEN, date: '2026-03-15' })
    assert.equal(call.url.includes('date'), false)
    assert.deepEqual(slots, [
      { professionalId: 'u2', startsAt: '2026-03-15T15:00:00Z', endsAt: '2026-03-15T16:00:00Z' },
    ])
  } finally {
    stub.restore()
  }
})

test('cancel POSTs {token} and reads the status of the contract', async () => {
  const stub = stubFetch(() => json({ status: 'cancelled' }))
  try {
    const result = await cancelBookingLink(TOKEN)
    const call = stub.calls[0]!
    assert.equal(call.url, `${API_BASE}/api/v1/public/booking-links/cancel`)
    assert.equal(call.method, 'POST')
    assert.deepEqual(JSON.parse(call.body ?? ''), { token: TOKEN })
    assert.equal(result, 'cancelled')
  } finally {
    stub.restore()
  }
})

test('reschedule POSTs {token, starts_at} and reads the new booking', async () => {
  const stub = stubFetch(() =>
    json({
      booking: booking({ starts_at: '2026-03-16T14:00:00Z', ends_at: '2026-03-16T15:00:00Z' }),
      new_link_sent: true,
    }),
  )
  try {
    const result = await rescheduleBookingLink(TOKEN, '2026-03-16T14:00:00Z')
    const call = stub.calls[0]!
    assert.equal(call.url, `${API_BASE}/api/v1/public/booking-links/reschedule`)
    assert.equal(call.method, 'POST')
    assert.deepEqual(JSON.parse(call.body ?? ''), {
      token: TOKEN,
      starts_at: '2026-03-16T14:00:00Z',
    })
    assert.equal(result.booking.starts_at, '2026-03-16T14:00:00Z')
    assert.equal(result.new_link_sent, true)
    // The new token NEVER travels in the answer: there is nothing else to read.
    assert.deepEqual(Object.keys(result).sort(), ['booking', 'new_link_sent'])
  } finally {
    stub.restore()
  }
})

test('the token is never written to localStorage, sessionStorage or a cookie', async () => {
  const storage = () => {
    const items = new Map<string, string>()
    return {
      items,
      setItem: (key: string, value: string) => {
        items.set(key, value)
      },
      getItem: (key: string) => items.get(key) ?? null,
      removeItem: (key: string) => {
        items.delete(key)
      },
    }
  }
  const local = storage()
  const session = storage()
  const cookies: string[] = []
  const define = (key: string, value: unknown) =>
    Object.defineProperty(globalThis, key, { value, configurable: true, writable: true })
  define('localStorage', local)
  define('sessionStorage', session)
  define('document', {
    get cookie() {
      return ''
    },
    set cookie(value: string) {
      cookies.push(value)
    },
  })
  const stub = stubFetch(() => json({ status: 'cancelled' }))
  try {
    await cancelBookingLink(TOKEN)
    await fetchLinkSlots(TOKEN, '2026-03-15')
    assert.equal(local.items.size, 0)
    assert.equal(session.items.size, 0)
    assert.deepEqual(cookies, [])
    // Every request carried the token in the JSON body and nowhere else.
    for (const call of stub.calls) {
      assert.equal(call.url.includes(TOKEN), false)
      assert.equal(call.url.includes('?'), false)
      assert.equal(JSON.parse(call.body ?? '{}')['token'], TOKEN)
    }
  } finally {
    stub.restore()
    delete (globalThis as { localStorage?: unknown }).localStorage
    delete (globalThis as { sessionStorage?: unknown }).sessionStorage
    delete (globalThis as { document?: unknown }).document
  }
})

// --- reading the lookup of the contract ------------------------------------

test('normalizeLinkBooking reads the frozen contract shape and nothing else', () => {
  const read = normalizeLinkBooking({
    booking: {
      salon_name: '  Aurelia ',
      service_name: 'Corte',
      professional_name: 'Ana',
      starts_at: '2026-03-14T16:00:00Z',
      ends_at: '2026-03-14T17:00:00Z',
      timezone: ZONE,
      can_change: true,
      min_notice_hours: 3,
      // A server must never leak these; if it does, the UI drops them.
      client_phone: '+5355528595',
      client_email: 'ana@mail.com',
      booking_id: 'b1',
    },
  })
  assert.deepEqual(read, {
    salon_name: 'Aurelia',
    service_name: 'Corte',
    professional_name: 'Ana',
    starts_at: '2026-03-14T16:00:00Z',
    ends_at: '2026-03-14T17:00:00Z',
    timezone: ZONE,
    can_change: true,
    min_notice_hours: 3,
  })
})

test('normalizeLinkBooking is defensive with a missing or unusable payload', () => {
  assert.equal(normalizeLinkBooking(null), null)
  assert.equal(normalizeLinkBooking({ booking: {} }), null)
  // No salon name or no usable instants: nothing the page can show.
  assert.equal(normalizeLinkBooking({ booking: booking({ salon_name: '  ' }) }), null)
  assert.equal(normalizeLinkBooking({ booking: booking({ starts_at: 'no-es-fecha' }) }), null)
  assert.equal(normalizeLinkBooking({ booking: booking({ ends_at: '' }) }), null)
  // `can_change` is only true when the server says so; the window falls back to 2 h.
  const plain = normalizeLinkBooking({ booking: { ...booking(), can_change: 'si', min_notice_hours: -1 } })
  assert.equal(plain?.can_change, false)
  assert.equal(plain?.min_notice_hours, 2)
})

test('salonTimeZone uses the zone of the API and never the fixed constant', () => {
  assert.equal(salonTimeZone(ZONE), ZONE)
  assert.equal(salonTimeZone('America/Mexico_City'), 'America/Mexico_City')
  // Unusable or missing: the visitor zone, never a hard-coded salon.
  assert.equal(salonTimeZone(''), visitorTimeZone())
  assert.equal(salonTimeZone('Not/AZone'), visitorTimeZone())
  assert.equal(salonTimeZone(undefined), visitorTimeZone())
})

test('lookup of an unusable answer is the ONE generic invalid sentence', async () => {
  const stub = stubFetch(() => json({ booking: { salon_name: '' } }))
  try {
    await assert.rejects(
      () => lookupBookingLink(TOKEN),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 200 &&
        error.message === BOOKING_LINK_MESSAGES.invalid,
    )
    // A 404 keeps that same sentence instead of the one of the server (D6).
    stub.restore()
    const notFound = stubFetch(() => json({ error: 'Enlace inválido o vencido' }, 404))
    try {
      await assert.rejects(
        () => lookupBookingLink(TOKEN),
        (error: unknown) =>
          error instanceof ApiError &&
          error.status === 404 &&
          bookingLinkErrorMessage(error, 'otra cosa') === BOOKING_LINK_MESSAGES.invalid,
      )
    } finally {
      notFound.restore()
    }
  } finally {
    stub.restore()
  }
})

// --- errors: generic 404, verbatim 409/400 --------------------------------

test('bookingLinkErrorMessage hides the cause of a 404 and repeats 409/400 verbatim', () => {
  assert.equal(
    bookingLinkErrorMessage(new ApiError(404, 'Enlace inválido o vencido'), 'x'),
    BOOKING_LINK_MESSAGES.invalid,
  )
  assert.equal(
    bookingLinkErrorMessage(new ApiError(409, BOOKING_LINK_MESSAGES.tooLate), 'x'),
    "Faltan menos de 2 horas para el turno: comunicate con el salón para cambiarlo",
  )
  assert.equal(
    bookingLinkErrorMessage(new ApiError(409, BOOKING_LINK_MESSAGES.slotTaken), 'x'),
    'Ese horario ya no está disponible: elegí otro',
  )
  assert.equal(
    bookingLinkErrorMessage(new ApiError(400, BOOKING_LINK_MESSAGES.sameTime), 'x'),
    'Elegí un horario distinto al actual',
  )
  assert.equal(
    bookingLinkErrorMessage(new ApiError(400, 'Fecha inválida: usá AAAA-MM-DD'), 'x'),
    'Fecha inválida: usá AAAA-MM-DD',
  )
  const network = new NetworkError('No pudimos conectar con el servidor.')
  assert.equal(bookingLinkErrorMessage(network, 'x'), 'No pudimos conectar con el servidor.')
  assert.equal(bookingLinkErrorMessage(new Error('  '), 'No pudimos cargar el turno.'), 'No pudimos cargar el turno.')
})

// --- the slot picker: one salon day at a time -----------------------------

test('defaultPickDate is the day of the salon zone, not the fixed one', () => {
  // The picker opens on the day the appointment already has, in its zone.
  assert.equal(defaultPickDate(NOW, ZONE), '2026-03-10')
  // 02:00 UTC of the 15th is still the 14th in Bogotá (UTC-5).
  assert.equal(defaultPickDate(new Date('2026-03-15T02:00:00Z'), ZONE), '2026-03-14')
  assert.equal(defaultPickDate(new Date('2026-03-15T06:00:00Z'), ZONE), '2026-03-15')
  // A zone east of UTC moves the other way.
  assert.equal(defaultPickDate(new Date('2026-03-14T22:00:00Z'), 'Europe/Madrid'), '2026-03-14')
  assert.equal(defaultPickDate(new Date('2026-03-14T23:30:00Z'), 'Europe/Madrid'), '2026-03-15')
})

test('isDateValue accepts only YYYY-MM-DD', () => {
  assert.equal(isDateValue('2026-03-14'), true)
  assert.equal(isDateValue('2026-3-14'), false)
  assert.equal(isDateValue('14/03/2026'), false)
  assert.equal(isDateValue('2026-03-14T00:00:00Z'), false)
  assert.equal(isDateValue(''), false)
})

// --- the copy of the contract ---------------------------------------------

test('can_change:false shows the notice and hides both actions', () => {
  assert.equal(
    BOOKING_LINK_MESSAGES.tooLate,
    'Faltan menos de 2 horas para el turno: comunicate con el salón para cambiarlo',
  )
  // The page renders the notice from `can_change` and disables both buttons.
  assert.equal(canCancel({ canChange: false, busy: false }), false)
  assert.equal(canReschedule({ canChange: false, busy: false, hasSlot: true }), false)
})

test('both actions are disabled while a request is in flight (double click)', () => {
  assert.equal(canCancel({ canChange: true, busy: false }), true)
  assert.equal(canCancel({ canChange: true, busy: true }), false)
  assert.equal(canReschedule({ canChange: true, busy: false, hasSlot: true }), true)
  assert.equal(canReschedule({ canChange: true, busy: true, hasSlot: true }), false)
  // Without a chosen slot there is nothing to confirm.
  assert.equal(canReschedule({ canChange: true, busy: false, hasSlot: false }), false)
})

test('the cancel confirmation says it cannot be undone', () => {
  const question = cancelConfirmQuestion('sábado, 14 de marzo de 2026, 11:00–12:00')
  assert.equal(question, '¿Cancelar tu turno del sábado, 14 de marzo de 2026, 11:00–12:00?')
  assert.match(CANCEL_CONFIRM_NOTE, /No se puede deshacer/)
  assert.equal(cancelConfirmQuestion('  '), '¿Cancelar tu turno?')
  assert.equal(
    rescheduleConfirmQuestion('domingo, 15 de marzo de 2026, 09:00–10:00'),
    '¿Mover tu turno al domingo, 15 de marzo de 2026, 09:00–10:00?',
  )
  assert.equal(rescheduleConfirmQuestion(''), '¿Mover tu turno a otro horario?')
})

test('the cut-off hint uses the hours the API sent', () => {
  assert.equal(cutOffNotice(2), 'Podés cambiar o cancelar hasta 2 horas antes del turno')
  assert.equal(cutOffNotice(1), 'Podés cambiar o cancelar hasta 1 hora antes del turno')
  assert.equal(cutOffNotice(0), 'Podés cambiar o cancelar hasta el momento del turno')
})

test('the result copy is the one of the contract', () => {
  assert.equal(CANCEL_DONE, 'Tu turno quedó cancelado')
  assert.equal(RESCHEDULE_DONE, 'Tu turno nuevo quedó reservado')
  assert.equal(CHANGE_ACTION, 'Cambiar horario')
  assert.equal(CANCEL_ACTION, 'Cancelar turno')
  assert.equal(newLinkNotice(true), 'Te mandamos un email con el nuevo enlace')
  assert.equal(newLinkNotice(false), NEW_LINK_FALLBACK_NOTE)
  assert.match(INVALID_LINK_HELP, /una sola vez/)
})

// --- the dates and times are always those of the salon zone ----------------

test('formatLinkDay and formatLinkWhen print the salon zone, not the visitor one', () => {
  // 16:00 UTC is 11:00 in Bogotá and also 10:00 in Ciudad de México.
  assert.equal(formatLinkDay('2026-03-14T16:00:00Z', ZONE), 'sábado, 14 de marzo de 2026')
  assert.equal(formatLinkWhen(booking(), ZONE), 'sábado, 14 de marzo de 2026, 11:00–12:00')
  assert.equal(
    formatLinkWhen(booking(), 'America/Mexico_City'),
    'sábado, 14 de marzo de 2026, 10:00–11:00',
  )
  // 23:30 UTC is the next day in Madrid and the same day in Bogotá.
  assert.equal(formatLinkDay('2026-03-14T23:30:00Z', 'Europe/Madrid'), 'domingo, 15 de marzo de 2026')
  assert.equal(formatLinkDay('2026-03-14T23:30:00Z', ZONE), 'sábado, 14 de marzo de 2026')
  // Unusable instants print nothing instead of an invented day.
  assert.equal(formatLinkDay('no-es-fecha', ZONE), '')
  assert.equal(formatLinkWhen(booking({ ends_at: '' }), ZONE), 'sábado, 14 de marzo de 2026, 11:00')
  assert.equal(formatLinkZone(ZONE), 'Bogotá (America/Bogota)')
})

test('the reschedule result shows the new time the API returned', () => {
  const read = normalizeReschedule({
    booking: booking({ starts_at: '2026-03-16T14:00:00Z', ends_at: '2026-03-16T15:00:00Z' }),
    new_link_sent: true,
  })
  assert.ok(read)
  assert.equal(formatLinkWhen(read.booking, read.booking.timezone), 'lunes, 16 de marzo de 2026, 09:00–10:00')
  assert.equal(newLinkNotice(read.new_link_sent), 'Te mandamos un email con el nuevo enlace')
  // Missing or unusable answer: no result the page could show.
  assert.equal(normalizeReschedule({ booking: booking(), new_link_sent: 'si' })?.new_link_sent, false)
  assert.equal(normalizeReschedule({ booking: null, new_link_sent: true }), null)
  assert.equal(normalizeReschedule(null), null)
})

// --- the state of the page: no optimistic change ---------------------------

test('initialLinkChange starts on the view with no result', () => {
  const state = initialLinkChange()
  assert.equal(state.step, 'view')
  assert.equal(state.error, null)
  assert.equal(state.cancelled, false)
  assert.equal(state.booking, null)
  assert.equal(state.newLinkSent, false)
  assert.equal(isLinkTerminal(state), false)
})

test('nothing changes state until the server confirms it', () => {
  const opened = reduceLinkChange(initialLinkChange(), { type: 'open_cancel' })
  assert.equal(opened.step, 'confirm_cancel')
  // Still not cancelled: the confirmation is not the result.
  assert.equal(opened.cancelled, false)
  assert.equal(isLinkTerminal(opened), false)

  // A cancel confirmed from the wrong step is ignored, never guessed.
  assert.equal(reduceLinkChange(initialLinkChange(), { type: 'cancelled' }).cancelled, false)

  const slots = reduceLinkChange(initialLinkChange(), { type: 'open_slots' })
  assert.equal(slots.step, 'slots')
  // A reschedule answered before asking for a slot is ignored too.
  assert.equal(
    reduceLinkChange(initialLinkChange(), {
      type: 'rescheduled',
      booking: booking(),
      newLinkSent: true,
    }).step,
    'view',
  )
})

test('a failed request keeps the page usable and shows the sentence verbatim', () => {
  const slots = reduceLinkChange(initialLinkChange(), { type: 'open_slots' })
  const failed = reduceLinkChange(slots, { type: 'failed', message: BOOKING_LINK_MESSAGES.slotTaken })
  assert.equal(failed.step, 'slots')
  assert.equal(failed.error, 'Ese horario ya no está disponible: elegí otro')
  assert.equal(failed.cancelled, false)
  assert.equal(failed.booking, null)
  // The token is still valid: another slot can be chosen right away.
  assert.equal(canReschedule({ canChange: true, busy: false, hasSlot: true }), true)
  // Choosing another time clears the previous error.
  assert.equal(reduceLinkChange(failed, { type: 'open_slots' }).error, null)
})

test('a confirmed cancel is terminal, and so is a confirmed reschedule', () => {
  const confirmed = reduceLinkChange(
    reduceLinkChange(initialLinkChange(), { type: 'open_cancel' }),
    { type: 'cancelled' },
  )
  assert.equal(confirmed.step, 'done_cancel')
  assert.equal(confirmed.cancelled, true)
  assert.equal(isLinkTerminal(confirmed), true)
  // The token was consumed: nothing else happens on that screen.
  assert.deepEqual(reduceLinkChange(confirmed, { type: 'open_slots' }), confirmed)
  assert.deepEqual(reduceLinkChange(confirmed, { type: 'failed', message: 'x' }), confirmed)

  const moved = reduceLinkChange(
    reduceLinkChange(initialLinkChange(), { type: 'open_slots' }),
    { type: 'rescheduled', booking: booking({ starts_at: '2026-03-16T14:00:00Z' }), newLinkSent: true },
  )
  assert.equal(moved.step, 'done_rescheduled')
  assert.equal(moved.booking?.starts_at, '2026-03-16T14:00:00Z')
  assert.equal(moved.newLinkSent, true)
  assert.equal(moved.cancelled, false)
  assert.equal(isLinkTerminal(moved), true)
  assert.deepEqual(reduceLinkChange(moved, { type: 'open_cancel' }), moved)
})

test('going back returns to the view without leaving a result behind', () => {
  const confirm = reduceLinkChange(initialLinkChange(), { type: 'open_cancel' })
  const back = reduceLinkChange(confirm, { type: 'back' })
  assert.equal(back.step, 'view')
  assert.equal(back.cancelled, false)
  const slots = reduceLinkChange(initialLinkChange(), { type: 'open_slots' })
  assert.equal(reduceLinkChange(slots, { type: 'back' }).step, 'view')
  // Back from the view is a no-op.
  assert.equal(reduceLinkChange(initialLinkChange(), { type: 'back' }).step, 'view')
  // The date of the picker survives within the flow.
  assert.equal(reduceLinkChange(slots, { type: 'date', date: '2026-03-20' }).date, '2026-03-20')
})
