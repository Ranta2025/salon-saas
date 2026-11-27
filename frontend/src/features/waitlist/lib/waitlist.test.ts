import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, NetworkError } from '../../../lib/api.ts'
import { EMAIL_INVALID_MESSAGE } from '../../../lib/email.ts'
import { RATE_LIMIT_MESSAGE } from '../../../lib/publicBooking.ts'
import { canViewWaitlist } from '../../../lib/permissions.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  WAITLIST_ANY_PROFESSIONAL,
  WAITLIST_EMAIL_MISSING,
  WAITLIST_FORM_TITLE,
  WAITLIST_LEAVE_ACTION,
  WAITLIST_LEAVE_DONE,
  WAITLIST_NAME_MISSING,
  WAITLIST_REMOVE_QUESTION,
  WAITLIST_SUCCESS,
  describeWaitlistError,
  fetchWaitlist,
  isWaitlistDate,
  joinWaitlist,
  leaveWaitlist,
  normalizeWaitlistEntries,
  professionalLabel,
  readWaitlistHash,
  removeWaitlistEntry,
  sortWaitlist,
  takeWaitlistToken,
  validateWaitlistDraft,
  waitlistCountLabel,
  waitlistDayLabel,
  waitlistPayload,
  waitlistStatusLabel,
  type WaitlistEntry,
} from './waitlist.ts'

// A real token is 32 random bytes in base64url: 43 characters.
const TOKEN = 'AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcde'

// --- the copy of the contract, verbatim (D9) ------------------------------

test('the copy of the form is the one of the contract, verbatim', () => {
  assert.equal(WAITLIST_FORM_TITLE, 'Avisame si se libera un turno')
  assert.equal(WAITLIST_SUCCESS, 'Listo, te avisamos por email si se libera un turno.')
  assert.equal(WAITLIST_LEAVE_ACTION, 'Dejar de recibir avisos')
  // The day of a staff row is the day the person asked for: "Cualquiera" when
  // no professional was chosen (D2).
  assert.equal(WAITLIST_ANY_PROFESSIONAL, 'Cualquiera')
})

test('the leave page confirms the baja without promising anything else', () => {
  assert.match(WAITLIST_LEAVE_DONE, /ya no/)
})

// --- the form: the rules the API applies to name, email and phone (D4) ---

const draft = {
  name: 'Ana Pérez',
  email: 'Ana@Mail.com ',
  phone: '+5355528595',
  date: '2026-03-14',
}

test('a complete draft passes and is normalized the way the API stores it', () => {
  const result = validateWaitlistDraft(draft)
  assert.equal(result.ok, true)
  if (!result.ok) {
    return
  }
  assert.equal(result.value.name, 'Ana Pérez')
  assert.equal(result.value.email, 'ana@mail.com')
  assert.equal(result.value.phone, '+5355528595')
})

test('the name is required and cannot be blank', () => {
  const result = validateWaitlistDraft({ ...draft, name: '   ' })
  assert.equal(result.ok, false)
  if (result.ok) {
    return
  }
  assert.equal(result.field, 'name')
  assert.equal(result.message, WAITLIST_NAME_MISSING)
})

test('the name has the 120 character cap of the migration', () => {
  const atCap = validateWaitlistDraft({ ...draft, name: 'a'.repeat(120) })
  assert.equal(atCap.ok, true)
  const overCap = validateWaitlistDraft({ ...draft, name: 'a'.repeat(121) })
  assert.equal(overCap.ok, false)
  if (overCap.ok) {
    return
  }
  assert.equal(overCap.field, 'name')
})

test('a NUL in the name is rejected instead of reaching the server', () => {
  const result = validateWaitlistDraft({ ...draft, name: 'Ana\u0000Pérez' })
  assert.equal(result.ok, false)
  if (result.ok) {
    return
  }
  assert.equal(result.field, 'name')
})

test('the email is the channel of the aviso, so it is required here', () => {
  const result = validateWaitlistDraft({ ...draft, email: '  ' })
  assert.equal(result.ok, false)
  if (result.ok) {
    return
  }
  assert.equal(result.field, 'email')
  assert.equal(result.message, WAITLIST_EMAIL_MISSING)
})

test('a filled email must pass the same validator of the public booking', () => {
  for (const email of ['ana', 'ana@', '@mail.com', 'ana mail@test.com']) {
    const result = validateWaitlistDraft({ ...draft, email })
    assert.equal(result.ok, false, email)
    if (result.ok) {
      continue
    }
    assert.equal(result.field, 'email')
    assert.equal(result.message, EMAIL_INVALID_MESSAGE)
  }
})

test('the phone is optional: empty is fine and sends no phone at all', () => {
  const result = validateWaitlistDraft({ ...draft, phone: '   ' })
  assert.equal(result.ok, true)
  if (!result.ok) {
    return
  }
  assert.equal(result.value.phone, '')
  const payload = waitlistPayload({ ...result.value, serviceId: 'srv-1', date: draft.date })
  assert.equal('phone' in payload, false)
})

test('a filled phone must be a usable number', () => {
  const result = validateWaitlistDraft({ ...draft, phone: '+531' })
  assert.equal(result.ok, false)
  if (result.ok) {
    return
  }
  assert.equal(result.field, 'phone')
  assert.ok(result.message.length > 0)
})

test('a day is only a real local calendar day', () => {
  assert.equal(isWaitlistDate('2026-03-14'), true)
  assert.equal(isWaitlistDate('2026-02-31'), false)
  assert.equal(isWaitlistDate('14/03/2026'), false)
  assert.equal(isWaitlistDate(''), false)
})

test('the body of the union carries only what the contract names', () => {
  assert.deepEqual(
    waitlistPayload({
      serviceId: ' srv-1 ',
      date: '2026-03-14',
      name: '  Ana Pérez ',
      email: ' Ana@Mail.com',
      phone: ' +5355528595 ',
    }),
    {
      service_id: 'srv-1',
      date: '2026-03-14',
      name: 'Ana Pérez',
      email: 'ana@mail.com',
      phone: '+5355528595',
    },
  )
  // No professional chosen = "cualquiera": the key is absent, never an empty string.
  const withoutOptional = waitlistPayload({
    serviceId: 'srv-1',
    date: '2026-03-14',
    name: 'Ana',
    email: 'ana@mail.com',
  })
  assert.equal('professional_id' in withoutOptional, false)
  assert.equal('phone' in withoutOptional, false)
  assert.deepEqual(
    waitlistPayload({
      serviceId: 'srv-1',
      professionalId: 'pro-9',
      date: '2026-03-14',
      name: 'Ana',
      email: 'ana@mail.com',
    }).professional_id,
    'pro-9',
  )
})

// --- the union against the public API (D4) -------------------------------

type Call = { url: string; method: string; headers: Record<string, string>; body?: string }

function stubFetch(handler: (call: Call) => Response): {
  calls: Call[]
  restore: () => void
} {
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

const joined = { status: 'waiting', date: '2026-03-14', service_id: 'srv-1' }

test('joinWaitlist posts the draft to the public route without a session', async () => {
  setToken('jwt-1')
  const stub = stubFetch(() => json(joined, 201))
  try {
    const result = await joinWaitlist('aurelia', {
      serviceId: 'srv-1',
      date: '2026-03-14',
      name: 'Ana Pérez',
      email: 'ana@mail.com',
    })
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/public\/salons\/aurelia\/waitlist$/)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      service_id: 'srv-1',
      date: '2026-03-14',
      name: 'Ana Pérez',
      email: 'ana@mail.com',
    })
    // A public route: it never carries the session of whoever is logged in.
    assert.equal(stub.calls[0]?.headers['Authorization'], undefined)
    assert.deepEqual(result, { status: 'waiting', date: '2026-03-14', service_id: 'srv-1' })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('joinWaitlist accepts the 200 of an identical aviso that already exists', async () => {
  const stub = stubFetch(() => json(joined, 200))
  try {
    const result = await joinWaitlist('aurelia', {
      serviceId: 'srv-1',
      date: '2026-03-14',
      name: 'Ana',
      email: 'ana@mail.com',
    })
    assert.equal(result.status, 'waiting')
  } finally {
    stub.restore()
  }
})

test('joinWaitlist rejects an answer that is not a waiting entry', async () => {
  const stub = stubFetch(() => json({ status: 'other' }, 201))
  try {
    await assert.rejects(
      () =>
        joinWaitlist('aurelia', {
          serviceId: 'srv-1',
          date: '2026-03-14',
          name: 'Ana',
          email: 'ana@mail.com',
        }),
      (error: unknown) => error instanceof ApiError && error.status === 200,
    )
  } finally {
    stub.restore()
  }
})

test('the three 409 of the contract are shown as the server sends them', async () => {
  const sentences = [
    'Todavía hay horarios libres ese día',
    'Ya tenés el máximo de avisos activos',
    'La lista de espera de este salón está completa',
  ]
  for (const sentence of sentences) {
    const stub = stubFetch(() => json({ error: sentence }, 409))
    try {
      await assert.rejects(
        () =>
          joinWaitlist('aurelia', {
            serviceId: 'srv-1',
            date: '2026-03-14',
            name: 'Ana',
            email: 'ana@mail.com',
          }),
        (error: unknown) => {
          const failure = describeWaitlistError(error, 'otra cosa')
          assert.equal(failure.kind, 'conflict')
          assert.equal(failure.message, sentence)
          return error instanceof ApiError && error.message === sentence
        },
      )
    } finally {
      stub.restore()
    }
  }
})

test('a 429 uses the rate-limit sentence the public booking already has', () => {
  const failure = describeWaitlistError(new ApiError(429, 'demasiadas'), 'otra cosa')
  assert.equal(failure.kind, 'rate_limited')
  assert.equal(failure.message, RATE_LIMIT_MESSAGE)
})

test('describeWaitlistError keeps the server sentence of the other statuses', () => {
  assert.deepEqual(describeWaitlistError(new ApiError(400, 'Email inválido'), 'x'), {
    kind: 'validation',
    message: 'Email inválido',
  })
  assert.deepEqual(describeWaitlistError(new ApiError(404, 'Aviso no encontrado'), 'x'), {
    kind: 'not_found',
    message: 'Aviso no encontrado',
  })
  assert.deepEqual(describeWaitlistError(new ApiError(500, 'Boom'), 'x'), {
    kind: 'unknown',
    message: 'Boom',
  })
  assert.deepEqual(describeWaitlistError(new NetworkError('Sin conexión'), 'x'), {
    kind: 'network',
    message: 'Sin conexión',
  })
  assert.deepEqual(describeWaitlistError(new Error(''), 'genérico'), {
    kind: 'unknown',
    message: 'genérico',
  })
})

// --- the baja of the link (D5) -------------------------------------------

test('readWaitlistHash takes the token out of the fragment and keeps the rest', () => {
  assert.deepEqual(readWaitlistHash(`https://salon.test/lista-de-espera#${TOKEN}`), {
    token: TOKEN,
    url: 'https://salon.test/lista-de-espera',
  })
  assert.deepEqual(readWaitlistHash(`http://localhost:5173/lista-de-espera?x=1#${TOKEN}`), {
    token: TOKEN,
    url: 'http://localhost:5173/lista-de-espera?x=1',
  })
})

test('readWaitlistHash leaves a URL without a fragment untouched', () => {
  assert.deepEqual(readWaitlistHash('https://salon.test/lista-de-espera'), {
    token: null,
    url: 'https://salon.test/lista-de-espera',
  })
})

test('readWaitlistHash never reads a malformed fragment as a token and cleans it', () => {
  for (const hash of ['#', '#corto', '#con espacio', '#abc=', '#a%20b', `#${'a'.repeat(200)}`]) {
    const read = readWaitlistHash(`https://salon.test/lista-de-espera${hash}`)
    assert.equal(read.token, null, hash)
    assert.equal(read.url, 'https://salon.test/lista-de-espera', hash)
  }
})

test('takeWaitlistToken wipes the token from the address bar before any request', () => {
  const replaced: string[] = []
  const history = {
    replaceState: (_data: unknown, _unused: string, url?: string | null) => {
      replaced.push(url ?? '')
    },
  }
  const token = takeWaitlistToken(`https://salon.test/lista-de-espera#${TOKEN}`, history)
  assert.equal(token, TOKEN)
  assert.deepEqual(replaced, ['https://salon.test/lista-de-espera'])

  // A URL without a token is left alone: the page must not touch the history.
  assert.equal(takeWaitlistToken('https://salon.test/lista-de-espera', history), null)
  assert.equal(replaced.length, 1)
})

test('leaveWaitlist posts the token in the body and keeps a 404 verbatim', async () => {
  const stub = stubFetch(() => json({ status: 'left' }))
  try {
    await leaveWaitlist(TOKEN)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/public\/waitlist\/leave$/)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { token: TOKEN })
    // The token never travels in the URL and never in a log-visible header.
    assert.equal(stub.calls[0]?.url.includes(TOKEN), false)
    assert.equal(stub.calls[0]?.headers['Authorization'], undefined)
  } finally {
    stub.restore()
  }

  const missing = stubFetch(() => json({ error: 'Aviso no encontrado' }, 404))
  try {
    await assert.rejects(
      () => leaveWaitlist(TOKEN),
      (error: unknown) => {
        const failure = describeWaitlistError(error, 'x')
        assert.equal(failure.kind, 'not_found')
        assert.equal(failure.message, 'Aviso no encontrado')
        return error instanceof ApiError && error.status === 404
      },
    )
  } finally {
    missing.restore()
  }
})

test('leaveWaitlist accepts the idempotent 200 of a token that was already left', async () => {
  const stub = stubFetch(() => json({ status: 'left' }))
  try {
    await leaveWaitlist(TOKEN)
    await leaveWaitlist(TOKEN)
    assert.equal(stub.calls.length, 2)
  } finally {
    stub.restore()
  }
})

// --- the staff list (D8) --------------------------------------------------

function entry(over: Partial<WaitlistEntry> = {}): WaitlistEntry {
  return {
    id: 'e1',
    service_id: 'srv-1',
    service_name: 'Corte y peinado',
    professional_id: null,
    professional_name: null,
    date: '2026-03-14',
    name: 'Ana Pérez',
    email: 'ana@mail.com',
    phone: null,
    status: 'waiting',
    created_at: '2026-03-10T15:00:00.000Z',
    notified_at: null,
    ...over,
  }
}

test('normalizeWaitlistEntries reads the shape of the contract and drops junk', () => {
  const rows = normalizeWaitlistEntries({
    entries: [
      {
        id: 'e1',
        service_id: 'srv-1',
        service_name: 'Corte',
        professional_id: null,
        professional_name: null,
        date: '2026-03-14',
        name: 'Ana',
        email: 'ana@mail.com',
        phone: null,
        status: 'waiting',
        created_at: '2026-03-10T15:00:00.000Z',
        notified_at: null,
      },
      {
        id: 'e2',
        service_id: 'srv-2',
        service_name: 'Color',
        professional_id: 'pro-1',
        professional_name: 'Cata',
        date: '2026-03-15',
        name: 'Bea',
        email: 'bea@mail.com',
        phone: '+5355528595',
        status: 'notified',
        created_at: '2026-03-11T15:00:00.000Z',
        notified_at: '2026-03-12T15:00:00.000Z',
      },
      // No id: the row cannot be removed later, so it is not shown.
      { service_name: 'Corte', date: '2026-03-14' },
      'basura',
      null,
    ],
  })
  assert.equal(rows.length, 2)
  assert.deepEqual(rows[1], {
    id: 'e2',
    service_id: 'srv-2',
    service_name: 'Color',
    professional_id: 'pro-1',
    professional_name: 'Cata',
    date: '2026-03-15',
    name: 'Bea',
    email: 'bea@mail.com',
    phone: '+5355528595',
    status: 'notified',
    created_at: '2026-03-11T15:00:00.000Z',
    notified_at: '2026-03-12T15:00:00.000Z',
  })
  // A status outside the two of the contract is read as still waiting.
  assert.equal(rows[0]?.status, 'waiting')
})

test('normalizeWaitlistEntries tolerates a bare array and an empty answer', () => {
  assert.equal(normalizeWaitlistEntries([entry()]).length, 1)
  assert.deepEqual(normalizeWaitlistEntries({}), [])
  assert.deepEqual(normalizeWaitlistEntries(null), [])
})

test('the list is ordered by day and then by the moment the person joined', () => {
  const sorted = sortWaitlist([
    entry({ id: 'c', date: '2026-03-15', created_at: '2026-03-01T10:00:00.000Z' }),
    entry({ id: 'a', date: '2026-03-14', created_at: '2026-03-02T10:00:00.000Z' }),
    entry({ id: 'b', date: '2026-03-14', created_at: '2026-03-01T10:00:00.000Z' }),
  ])
  assert.deepEqual(
    sorted.map((row) => row.id),
    ['b', 'a', 'c'],
  )
})

test('fetchWaitlist asks the list of the salon with the session and normalizes it', async () => {
  setToken('jwt-1')
  const stub = stubFetch(() => json({ entries: [entry()] }))
  try {
    const rows = await fetchWaitlist('s1')
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/waitlist$/)
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer jwt-1')
    assert.equal(rows.length, 1)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('removeWaitlistEntry deletes one aviso and keeps the 404 of the contract', async () => {
  setToken('jwt-1')
  const stub = stubFetch(() => new Response(null, { status: 204 }))
  try {
    await removeWaitlistEntry('s1', 'e1')
    assert.equal(stub.calls[0]?.method, 'DELETE')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/waitlist\/e1$/)
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer jwt-1')
  } finally {
    stub.restore()
    clearToken()
  }

  const missing = stubFetch(() => json({ error: 'Aviso no encontrado' }, 404))
  try {
    await assert.rejects(
      () => removeWaitlistEntry('s1', 'e1'),
      (error: unknown) => {
        assert.equal(describeWaitlistError(error, 'x').message, 'Aviso no encontrado')
        return error instanceof ApiError && error.status === 404
      },
    )
  } finally {
    missing.restore()
    clearToken()
  }
})

// --- what each row says ---------------------------------------------------

test('the status of a row is said in Spanish', () => {
  assert.equal(waitlistStatusLabel('waiting'), 'Esperando')
  assert.equal(waitlistStatusLabel('notified'), 'Avisada')
})

test('a row without professional says "Cualquiera", never an empty cell', () => {
  assert.equal(professionalLabel(null), WAITLIST_ANY_PROFESSIONAL)
  assert.equal(professionalLabel('   '), WAITLIST_ANY_PROFESSIONAL)
  assert.equal(professionalLabel(' Cata '), 'Cata')
})

test('the day of a row is the day of the salon, never shifted by the visitor zone', () => {
  // Same label whatever the time zone of the machine reading the table.
  assert.equal(waitlistDayLabel('2026-03-14'), 'sábado, 14 de marzo de 2026')
  assert.equal(waitlistDayLabel('2026-01-01'), 'jueves, 1 de enero de 2026')
  // An unusable value is printed as it came instead of inventing a date.
  assert.equal(waitlistDayLabel(''), '')
  assert.equal(waitlistDayLabel('14/03/2026'), '14/03/2026')
})

test('the count of the list is a Spanish sentence in singular and plural', () => {
  assert.equal(waitlistCountLabel(0), '0 avisos')
  assert.equal(waitlistCountLabel(1), '1 aviso')
  assert.equal(waitlistCountLabel(3), '3 avisos')
})

test('the confirmation of "Quitar" asks before removing the aviso', () => {
  assert.match(WAITLIST_REMOVE_QUESTION, /^\¿Quitar/)
})

// --- who sees the page (D8) ----------------------------------------------

test('owner, admin and reception see the waiting list; nobody else does', () => {
  assert.equal(canViewWaitlist('owner'), true)
  assert.equal(canViewWaitlist('admin'), true)
  assert.equal(canViewWaitlist('reception'), true)
  assert.equal(canViewWaitlist('professional'), false)
  assert.equal(canViewWaitlist(null), false)
})
