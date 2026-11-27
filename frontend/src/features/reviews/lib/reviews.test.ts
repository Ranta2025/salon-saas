import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError } from '../../../lib/api.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  applyReviewHidden,
  commentCounterText,
  createReviewGate,
  describeReviewLinkError,
  draftFromReviewSettings,
  fetchReviewLink,
  fetchReviewSettings,
  fetchReviews,
  MAX_REVIEW_COMMENT_LENGTH,
  mergeReviews,
  normalizePanelReview,
  normalizeReviewLink,
  normalizeReviewPage,
  normalizeReviewSettings,
  readFragmentToken,
  REVIEW_MESSAGES,
  REVIEWS_PAGE_SIZE,
  reviewCreatedText,
  reviewDayLabel,
  reviewDelayLabel,
  reviewLinkPath,
  reviewRatingLabel,
  reviewSettingsPath,
  reviewsPath,
  reviewStarsText,
  sanitizeReviewComment,
  saveReviewSettings,
  setReviewHidden,
  submitReview,
  validateReviewDraft,
  validateReviewSettingsDraft,
  type PanelReview,
  type ReviewSettings,
} from './reviews.ts'

type Call = { url: string; method: string; body: string | undefined; headers: Record<string, string> }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
      headers: (init?.headers ?? {}) as Record<string, string>,
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

const RAW_LINK = {
  salon_name: 'Luna Studio',
  service_name: 'Corte y peinado',
  starts_at: '2026-11-03T18:00:00Z',
}

const RAW_REVIEW = {
  id: 'rv1',
  rating: 5,
  comment: 'Excelente atención',
  author_name: 'Lía G.',
  hidden: false,
  created_at: '2026-11-04T12:00:00Z',
  service_name: 'Corte y peinado',
}

function review(over: Partial<PanelReview> = {}): PanelReview {
  return {
    id: 'rv1',
    rating: 5,
    comment: 'Excelente atención',
    author_name: 'Lía G.',
    hidden: false,
    created_at: '2026-11-04T12:00:00Z',
    service_name: 'Corte y peinado',
    ...over,
  }
}

// --- the token of the fragment (same rule as the other public token pages) ---

test('the review token is read from the fragment of /resena and the address bar is cleaned', () => {
  const read = readFragmentToken('https://salon.test/resena#tok_ABCdef0123456789xyz')
  assert.equal(read.token, 'tok_ABCdef0123456789xyz')
  assert.equal(read.url, 'https://salon.test/resena')
  // Junk in the fragment is never used as a token, and the URL still loses it.
  assert.equal(readFragmentToken('https://salon.test/resena#').token, null)
  assert.equal(readFragmentToken('https://salon.test/resena#a b').token, null)
  assert.equal(readFragmentToken('https://salon.test/resena').url, 'https://salon.test/resena')
  // A token in the query is not ours: it is neither read nor touched.
  assert.deepEqual(readFragmentToken('https://salon.test/resena?token=x#tok_ABCdef0123456789xyz'), {
    token: 'tok_ABCdef0123456789xyz',
    url: 'https://salon.test/resena?token=x',
  })
})

// --- D4: the public link of one booking ---

test('normalizeReviewLink reads {salon_name, service_name, starts_at}', () => {
  assert.deepEqual(normalizeReviewLink(RAW_LINK), {
    salonName: 'Luna Studio',
    serviceName: 'Corte y peinado',
    startsAt: '2026-11-03T18:00:00Z',
  })
  assert.deepEqual(normalizeReviewLink({ ...RAW_LINK, service_name: '  ' }), {
    salonName: 'Luna Studio',
    serviceName: '',
    startsAt: '2026-11-03T18:00:00Z',
  })
})

test('normalizeReviewLink returns null when the salon or the date is unusable (never invented)', () => {
  for (const bad of [
    null,
    'nope',
    [],
    {},
    { service_name: 'Corte', starts_at: RAW_LINK.starts_at },
    { salon_name: '   ', service_name: 'Corte', starts_at: RAW_LINK.starts_at },
    { salon_name: 'Luna', service_name: 'Corte' },
    { salon_name: 'Luna', service_name: 'Corte', starts_at: 'no-es-fecha' },
    { salon_name: 5, service_name: 'Corte', starts_at: RAW_LINK.starts_at },
  ]) {
    assert.equal(normalizeReviewLink(bad), null, JSON.stringify(bad))
  }
})

test('reviewLinkPath encodes the token and keeps it out of the path segments', () => {
  assert.equal(reviewLinkPath('tok/1 2'), '/api/v1/public/reviews/tok%2F1%202')
})

test('fetchReviewLink reads the public endpoint without a session and returns the link', async () => {
  const stub = stubFetch(() => json(RAW_LINK))
  try {
    setToken('jwt-must-not-travel')
    const link = await fetchReviewLink('tok-abc')
    assert.equal(link?.salonName, 'Luna Studio')
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.url.replace(/^.*\/api\/v1/, ''), '/public/reviews/tok-abc')
    // A public page never sends the stored session of whoever is browsing.
    assert.equal(stub.calls[0]?.headers['Authorization'], undefined)
    assert.equal(stub.calls[0]?.body, undefined)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a 404 of the link keeps the uniform server sentence verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'Enlace no válido' }, 404))
  try {
    await assert.rejects(
      () => fetchReviewLink('tok-abc'),
      (error: unknown) =>
        error instanceof ApiError && error.status === 404 && error.message === 'Enlace no válido',
    )
  } finally {
    stub.restore()
  }
})

test('describeReviewLinkError tells an invalid/used link from any other failure', () => {
  assert.deepEqual(describeReviewLinkError(new ApiError(404, 'Enlace no válido'), 'x'), {
    kind: 'invalid',
    message: 'Enlace no válido',
  })
  assert.deepEqual(describeReviewLinkError(new ApiError(500, 'Error interno'), 'x'), {
    kind: 'error',
    message: 'Error interno',
  })
  assert.deepEqual(describeReviewLinkError(new TypeError('fetch failed'), 'No pudimos cargar el enlace.'), {
    kind: 'error',
    message: 'No pudimos cargar el enlace.',
  })
})

// --- D4: the rating, the comment and the counter ---

test('reviewRatingLabel names every star and reviewStarsText draws five of them', () => {
  assert.deepEqual(
    [1, 2, 3, 4, 5].map((rating) => reviewRatingLabel(rating)),
    ['Muy mala', 'Mala', 'Regular', 'Buena', 'Excelente'],
  )
  assert.equal(reviewStarsText(4), '★★★★☆')
  assert.equal(reviewStarsText(1), '★☆☆☆☆')
  assert.equal(reviewStarsText(5), '★★★★★')
})

test('commentCounterText counts characters against the 500 of the contract', () => {
  assert.equal(MAX_REVIEW_COMMENT_LENGTH, 500)
  assert.equal(commentCounterText(0), '0/500')
  assert.equal(commentCounterText(12), '12/500')
  assert.equal(commentCounterText(500), '500/500')
  assert.equal(commentCounterText(501), '501/500')
})

test('sanitizeReviewComment removes NUL and every control character a person can paste', () => {
  assert.equal(sanitizeReviewComment('Hola\u0000 mundo'), 'Hola mundo')
  assert.equal(sanitizeReviewComment('uno\rdos\ntres'), 'unodostres')
  assert.equal(sanitizeReviewComment('a\u0007b\u007fc'), 'abc')
  assert.equal(sanitizeReviewComment('ok \u2028 dos'), 'ok  dos')
  // Accents and emoji are NOT touched: they are valid text, not control characters.
  assert.equal(sanitizeReviewComment('Atención ñ 💅'), 'Atención ñ 💅')
})

test('validateReviewDraft asks for a whole rating from 1 to 5', () => {
  const missing = validateReviewDraft({ rating: 0, comment: '' })
  assert.deepEqual(missing, { ok: false, field: 'rating', message: REVIEW_MESSAGES.ratingRequired })
  for (const rating of [6, 4.5, -1, Number.NaN]) {
    assert.equal(validateReviewDraft({ rating, comment: '' }).ok, false, String(rating))
  }
  for (const rating of [1, 2, 3, 4, 5]) {
    const checked = validateReviewDraft({ rating, comment: '' })
    assert.equal(checked.ok, true)
    if (checked.ok) {
      assert.deepEqual(checked.payload, { rating, comment: '' })
    }
  }
})

test('validateReviewDraft accepts a comment of 500 characters and rejects 501, control characters included', () => {
  const exact = 'a'.repeat(500)
  assert.equal(validateReviewDraft({ rating: 5, comment: exact }).ok, true)
  assert.deepEqual(validateReviewDraft({ rating: 5, comment: `${exact}a` }), {
    ok: false,
    field: 'comment',
    message: REVIEW_MESSAGES.commentTooLong,
  })
  for (const bad of ['Hola\u0000 mundo', 'uno\ndos', 'uno\rdos', 'a\u0007b', 'x\u007f']) {
    assert.deepEqual(validateReviewDraft({ rating: 5, comment: bad }), {
      ok: false,
      field: 'comment',
      message: REVIEW_MESSAGES.invalidText,
    })
  }
  // The body trims the comment; nothing else changes.
  assert.deepEqual(validateReviewDraft({ rating: 4, comment: '  Muy bien  ' }), {
    ok: true,
    payload: { rating: 4, comment: 'Muy bien' },
  })
})

// --- D4: POST of the review ---

test('submitReview sends {rating, comment} to the frozen route and reports the thanks state', async () => {
  const stub = stubFetch(() => json({ ok: true }, 201))
  try {
    const outcome = await submitReview(
      createReviewGate(),
      'tok/1',
      { rating: 5, comment: ' Excelente ' },
      () => true,
    )
    assert.deepEqual(outcome, { status: 'sent' })
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.equal(stub.calls[0]?.url.replace(/^.*\/api\/v1/, ''), '/public/reviews/tok%2F1')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { rating: 5, comment: 'Excelente' })
    assert.equal(stub.calls[0]?.headers['Authorization'], undefined)
  } finally {
    stub.restore()
  }
})

test('submitReview never sends an invalid rating or comment', async () => {
  const stub = stubFetch(() => json({ ok: true }, 201))
  try {
    const gate = createReviewGate()
    const noRating = await submitReview(gate, 'tok', { rating: 0, comment: 'hola' }, () => true)
    assert.equal(noRating.status, 'invalid')
    const tooLong = await submitReview(gate, 'tok', { rating: 5, comment: 'a'.repeat(501) }, () => true)
    assert.equal(tooLong.status, 'invalid')
    const control = await submitReview(gate, 'tok', { rating: 5, comment: 'a\u0000b' }, () => true)
    assert.equal(control.status, 'invalid')
    assert.equal(stub.calls.length, 0)
  } finally {
    stub.restore()
  }
})

test('submitReview sends nothing when the browser is offline', async () => {
  const stub = stubFetch(() => json({ ok: true }, 201))
  try {
    const outcome = await submitReview(createReviewGate(), 'tok', { rating: 5, comment: '' }, () => false)
    assert.deepEqual(outcome, { status: 'offline', message: REVIEW_MESSAGES.offline })
    assert.equal(stub.calls.length, 0)
  } finally {
    stub.restore()
  }
})

test('a 404 of the POST means the link was used or expired; a 400 keeps the server sentence', async () => {
  const notFound = stubFetch(() => json({ error: 'Enlace no válido' }, 404))
  try {
    const outcome = await submitReview(createReviewGate(), 'tok', { rating: 5, comment: '' }, () => true)
    assert.deepEqual(outcome, { status: 'used', message: 'Enlace no válido' })
  } finally {
    notFound.restore()
  }
  const badRequest = stubFetch(() => json({ error: 'El comentario no puede superar los 500 caracteres' }, 400))
  try {
    const outcome = await submitReview(createReviewGate(), 'tok', { rating: 5, comment: 'ok' }, () => true)
    assert.deepEqual(outcome, {
      status: 'failed',
      message: 'El comentario no puede superar los 500 caracteres',
    })
  } finally {
    badRequest.restore()
  }
})

test('a second press while the first request runs does not send twice', async () => {
  let release: (value: Response) => void = () => undefined
  const stub = stubFetch(
    () =>
      new Promise<Response>((resolve) => {
        release = resolve
      }),
  )
  try {
    const gate = createReviewGate()
    const first = submitReview(gate, 'tok', { rating: 5, comment: '' }, () => true)
    const second = await submitReview(gate, 'tok', { rating: 5, comment: '' }, () => true)
    assert.deepEqual(second, { status: 'busy', message: REVIEW_MESSAGES.busy })
    release(json({ ok: true }, 201))
    assert.deepEqual(await first, { status: 'sent' })
    assert.equal(stub.calls.length, 1)
  } finally {
    stub.restore()
  }
})

// --- D6: the panel list and its cursor ---

test('normalizePanelReview reads a row of the panel and drops what cannot be shown', () => {
  assert.deepEqual(normalizePanelReview(RAW_REVIEW), {
    id: 'rv1',
    rating: 5,
    comment: 'Excelente atención',
    author_name: 'Lía G.',
    hidden: false,
    created_at: '2026-11-04T12:00:00Z',
    service_name: 'Corte y peinado',
  })
  // `hidden` only means true when the server says so.
  assert.equal(normalizePanelReview({ ...RAW_REVIEW, hidden: 'yes' })?.hidden, false)
  for (const bad of [
    null,
    'nope',
    {},
    { ...RAW_REVIEW, id: '' },
    { ...RAW_REVIEW, rating: 0 },
    { ...RAW_REVIEW, rating: 6 },
    { ...RAW_REVIEW, rating: 4.5 },
    { ...RAW_REVIEW, created_at: '' },
    { ...RAW_REVIEW, created_at: 'no-es-fecha' },
  ]) {
    assert.equal(normalizePanelReview(bad), null, JSON.stringify(bad))
  }
})

test('normalizeReviewPage reads {items, next_cursor} and a body of another shape is null', () => {
  const page = normalizeReviewPage({ items: [RAW_REVIEW, { id: 'bad' }], next_cursor: 'c2' })
  assert.ok(page)
  assert.equal(page.items.length, 1)
  assert.equal(page.next_cursor, 'c2')
  assert.equal(normalizeReviewPage({ items: [], next_cursor: null })?.next_cursor, null)
  assert.equal(normalizeReviewPage({ items: [], next_cursor: '  ' })?.next_cursor, null)
  for (const bad of [null, 'nope', [], {}, { items: 'nope' }, { next_cursor: 'c2' }]) {
    assert.equal(normalizeReviewPage(bad), null, JSON.stringify(bad))
  }
})

test('mergeReviews appends a page without repeating a row and keeps the server order', () => {
  const current = [review({ id: 'a' }), review({ id: 'b' })]
  const incoming = [review({ id: 'b', hidden: true }), review({ id: 'c' })]
  const merged = mergeReviews(current, incoming)
  assert.deepEqual(
    merged.map((item) => item.id),
    ['a', 'b', 'c'],
  )
  // The copy already on screen wins (an optimistic hide is not overwritten by an old page).
  assert.equal(merged[1]?.hidden, false)
  // Inputs are not mutated.
  assert.equal(current.length, 2)
})

test('reviewsPath is salon scoped, always carries the limit and the cursor only when set', () => {
  assert.equal(reviewsPath('s 1', {}), `/api/v1/salons/s%201/reviews?limit=${REVIEWS_PAGE_SIZE}`)
  assert.equal(
    reviewsPath('s1', { before: 'c/2' }),
    `/api/v1/salons/s1/reviews?limit=${REVIEWS_PAGE_SIZE}&before=c%2F2`,
  )
  assert.equal(
    reviewsPath('s1', { before: '   ' }),
    `/api/v1/salons/s1/reviews?limit=${REVIEWS_PAGE_SIZE}`,
  )
})

test('fetchReviews reads the panel route and rejects a body of another shape', async () => {
  const stub = stubFetch(() => json({ items: [RAW_REVIEW], next_cursor: null }))
  try {
    setToken('jwt-reviews')
    const page = await fetchReviews('s1')
    assert.equal(page.items[0]?.id, 'rv1')
    assert.equal(page.next_cursor, null)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(
      stub.calls[0]?.url.replace(/^.*\/api\/v1/, ''),
      `/salons/s1/reviews?limit=${REVIEWS_PAGE_SIZE}`,
    )
  } finally {
    stub.restore()
    clearToken()
  }
  const weird = stubFetch(() => json({ nope: true }))
  try {
    setToken('jwt-reviews')
    await assert.rejects(
      () => fetchReviews('s1'),
      (error: unknown) =>
        error instanceof ApiError && error.message === 'La API no devolvió las reseñas.',
    )
  } finally {
    weird.restore()
    clearToken()
  }
})

test('applyReviewHidden flips one row and leaves the rest untouched (the rollback of the panel)', () => {
  const items = [review({ id: 'a' }), review({ id: 'b' }), review({ id: 'c', hidden: true })]
  const hidden = applyReviewHidden(items, 'b', true)
  assert.deepEqual(
    hidden.map((item) => [item.id, item.hidden]),
    [
      ['a', false],
      ['b', true],
      ['c', true],
    ],
  )
  // Rolling back is the same operation with the previous value.
  assert.deepEqual(
    applyReviewHidden(hidden, 'b', false).map((item) => [item.id, item.hidden]),
    items.map((item) => [item.id, item.hidden]),
  )
  // An unknown id changes nothing (no row is invented).
  assert.deepEqual(applyReviewHidden(items, 'zz', true), items)
  assert.equal(items[0]?.hidden, false)
})

test('setReviewHidden posts to /hide and /unhide and accepts {ok:true}', async () => {
  const stub = stubFetch(() => json({ ok: true }))
  try {
    setToken('jwt-reviews')
    await setReviewHidden('s 1', 'rv/1', true)
    await setReviewHidden('s 1', 'rv/1', false)
    assert.deepEqual(
      stub.calls.map((call) => `${call.method} ${call.url.replace(/^.*\/api\/v1/, '')}`),
      ['POST /salons/s%201/reviews/rv%2F1/hide', 'POST /salons/s%201/reviews/rv%2F1/unhide'],
    )
  } finally {
    stub.restore()
    clearToken()
  }
  const failing = stubFetch(() => json({ error: 'Reseña no encontrada' }, 404))
  try {
    setToken('jwt-reviews')
    await assert.rejects(
      () => setReviewHidden('s1', 'rv1', true),
      (error: unknown) =>
        error instanceof ApiError && error.status === 404 && error.message === 'Reseña no encontrada',
    )
  } finally {
    failing.restore()
    clearToken()
  }
})

// --- D6: review settings ---

test('normalizeReviewSettings reads {enabled, delay_hours} and rejects a delay outside 1..72', () => {
  assert.deepEqual(normalizeReviewSettings({ enabled: true, delay_hours: 24 }), {
    enabled: true,
    delay_hours: 24,
  })
  assert.deepEqual(normalizeReviewSettings({ enabled: false, delay_hours: 1 }), {
    enabled: false,
    delay_hours: 1,
  })
  assert.deepEqual(normalizeReviewSettings({ enabled: true, delay_hours: 72 }), {
    enabled: true,
    delay_hours: 72,
  })
  for (const bad of [
    null,
    'nope',
    {},
    { enabled: true },
    { delay_hours: 24 },
    { enabled: true, delay_hours: 0 },
    { enabled: true, delay_hours: 73 },
    { enabled: true, delay_hours: 24.5 },
    { enabled: true, delay_hours: '24' },
  ]) {
    assert.equal(normalizeReviewSettings(bad), null, JSON.stringify(bad))
  }
})

test('draftFromReviewSettings keeps what the server stored and a saved switch', () => {
  assert.deepEqual(draftFromReviewSettings(null), { enabled: false, delayHours: '' })
  assert.deepEqual(
    draftFromReviewSettings({ enabled: true, delay_hours: 6 } satisfies ReviewSettings),
    { enabled: true, delayHours: '6' },
  )
})

test('validateReviewSettingsDraft requires a whole delay between 1 and 72 hours', () => {
  const base = { enabled: true }
  assert.deepEqual(validateReviewSettingsDraft({ ...base, delayHours: '' }), {
    ok: false,
    field: 'delay',
    message: REVIEW_MESSAGES.delayRange,
  })
  for (const text of ['0', '73', '-1', '24.5', 'abc', '2 4']) {
    assert.equal(validateReviewSettingsDraft({ ...base, delayHours: text }).ok, false, text)
  }
  assert.deepEqual(validateReviewSettingsDraft({ ...base, delayHours: '1' }), {
    ok: true,
    payload: { enabled: true, delay_hours: 1 },
  })
  assert.deepEqual(validateReviewSettingsDraft({ enabled: false, delayHours: ' 72 ' }), {
    ok: true,
    payload: { enabled: false, delay_hours: 72 },
  })
})

test('the review settings PUT sends BOTH keys and the GET uses the frozen route', async () => {
  const stub = stubFetch((call) => {
    if (call.method === 'PUT') {
      return json({ enabled: false, delay_hours: 48 })
    }
    return json({ enabled: true, delay_hours: 24 })
  })
  try {
    setToken('jwt-reviews')
    const settings = await fetchReviewSettings('s 1')
    assert.deepEqual(settings, { enabled: true, delay_hours: 24 })
    const saved = await saveReviewSettings('s 1', { enabled: false, delay_hours: 48 })
    assert.deepEqual(saved, { enabled: false, delay_hours: 48 })
    assert.deepEqual(
      stub.calls.map((call) => `${call.method} ${call.url.replace(/^.*\/api\/v1/, '')}`),
      ['GET /salons/s%201/review-settings', 'PUT /salons/s%201/review-settings'],
    )
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), { enabled: false, delay_hours: 48 })
    assert.equal(reviewSettingsPath('s 1'), '/api/v1/salons/s%201/review-settings')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('reviewDelayLabel says the demora in plain Spanish', () => {
  assert.equal(reviewDelayLabel(1), '1 hora después del turno')
  assert.equal(reviewDelayLabel(24), '24 horas después del turno')
  assert.equal(reviewDelayLabel(72), '72 horas después del turno')
})

// --- presentation of the public link and of the panel rows ---

test('reviewDayLabel shows the day as dd/mm/yyyy and nothing when it is not a date', () => {
  assert.equal(reviewDayLabel('2026-11-04'), '04/11/2026')
  assert.equal(reviewDayLabel('2026-11-04T12:00:00Z'), '04/11/2026')
  assert.equal(reviewDayLabel('  '), '')
  assert.equal(reviewDayLabel('no-es-fecha'), '')
})

test('reviewCreatedText shows the date and the time in the zone of the salon (24 h)', () => {
  assert.equal(reviewCreatedText('2026-11-04T12:00:00Z', 'America/Mexico_City'), '2026-11-04 06:00')
  assert.equal(reviewCreatedText('no-es-fecha', 'America/Mexico_City'), '—')
})
