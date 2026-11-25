import test from 'node:test'
import assert from 'node:assert/strict'
import {
  buildLanding,
  instagramUrl,
  landingFailure,
  landingStateAfterFailure,
  LANDING_ERROR_MESSAGE,
  LANDING_NOT_FOUND_MESSAGE,
  LANDING_OFFLINE_NOTICE,
  loyaltySentence,
  normalizePublicSalon,
  telHref,
  toLandingServices,
  whatsappUrl,
  type PublicSalon,
} from './publicSalon.ts'

const payload = {
  salon: { id: 's1', slug: 'aurelia', name: 'Aurelia' },
  services: [
    { id: 'svc-1', name: 'Corte y peinado', duration_min: 45, price_cents: 42000 },
    { id: 'svc-2', name: 'Keratina', duration_min: 120, price_cents: 150000 },
  ],
}

test('normalizePublicSalon reads the salon and its active services', () => {
  const salon = normalizePublicSalon(payload)
  assert.ok(salon)
  assert.deepEqual(salon.salon, { id: 's1', slug: 'aurelia', name: 'Aurelia' })
  assert.equal(salon.services.length, 2)
  assert.deepEqual(salon.services[0], {
    id: 'svc-1',
    name: 'Corte y peinado',
    durationMin: 45,
    priceCents: 42000,
  })
})

test('normalizePublicSalon tolerates a missing salon block and bad rows', () => {
  const salon = normalizePublicSalon({
    services: [
      { name: 'Sin id', duration_min: 30, price_cents: 1000 },
      { id: 'svc-1', name: 'Corte', duration_min: 0, price_cents: 1000 },
      { id: 'svc-2', name: 'Tinte', duration_min: 90, price_cents: -5 },
      { id: 'svc-3', name: 'Peinado', duration_min: 60, price_cents: 75000 },
    ],
  })
  assert.ok(salon)
  assert.equal(salon.salon.slug, '')
  assert.deepEqual(
    salon.services.map((service) => service.id),
    ['svc-3'],
  )
})

test('normalizePublicSalon returns null when nothing usable came back', () => {
  assert.equal(normalizePublicSalon(null), null)
  assert.equal(normalizePublicSalon({ services: [] }), null)
  assert.equal(normalizePublicSalon({ services: [{ id: 'x' }] }), null)
})

// F16 (D5): the static catalogue and the copy matched by service name are gone.
// These two tests asserted that removed hard-coded content; they now pin the
// replacement behaviour (description comes from the API, nothing is invented).
test('toLandingServices keeps the API name, price and description and invents nothing', () => {
  const salon = normalizePublicSalon({
    ...payload,
    services: [
      { id: 'svc-1', name: 'Corte y peinado', duration_min: 45, price_cents: 42000, description: 'Con lavado' },
      { id: 'svc-2', name: 'Keratina', duration_min: 120, price_cents: 150000, description: null },
    ],
  })
  assert.ok(salon)
  const services = toLandingServices(salon.services)
  assert.equal(services.length, 2)
  assert.equal(services[0]?.id, 'svc-1')
  assert.equal(services[0]?.priceCents, 42000)
  assert.equal(services[0]?.description, 'Con lavado')
  assert.equal(services[1]?.name, 'Keratina')
  assert.equal(services[1]?.description, null)
  assert.equal('tag' in (services[1] ?? {}), false)
})

test('toLandingServices returns an empty list when the API sends no service (no fallback catalogue)', () => {
  assert.deepEqual(toLandingServices([]), [])
})

test('normalizePublicSalon reads the public hours of the week', () => {
  const salon = normalizePublicSalon({
    ...payload,
    hours: [
      { weekday: 1, open: '09:00', close: '19:00' },
      { weekday: 6, open: '09:00', close: '14:00' },
      { weekday: 9, open: '09:00', close: '19:00' },
    ],
  })
  assert.ok(salon)
  assert.deepEqual(salon.hours, [
    { weekday: 1, open: '09:00', close: '19:00' },
    { weekday: 6, open: '09:00', close: '14:00' },
  ])
})

test('normalizePublicSalon leaves hours null when the field is missing or unusable', () => {
  assert.equal(normalizePublicSalon(payload)?.hours, null)
  assert.equal(normalizePublicSalon({ ...payload, hours: [] })?.hours, null)
  assert.equal(normalizePublicSalon({ ...payload, hours: 'nope' })?.hours, null)
  assert.equal(
    normalizePublicSalon({ ...payload, hours: [{ weekday: 9, open: '09:00', close: '19:00' }] })
      ?.hours,
    null,
  )
})

// ---------- F16: public payload, sections and states ----------

const full = {
  salon: {
    id: 's1',
    slug: 'luna',
    name: 'Luna Studio',
    description: 'Cortes y color con cita.',
    address: 'Calle 1 #23, Centro',
    phone: '+53 5555 1234',
    email: 'hola@luna.test',
    instagram: 'luna.studio',
    timezone: 'America/Havana',
  },
  services: [
    { id: 'svc-1', name: 'Corte', duration_min: 45, price_cents: 3800, description: 'Corte a medida' },
    { id: 'svc-2', name: 'Tinte', duration_min: 90, price_cents: 9000, description: null },
  ],
  team: [
    { name: 'Ana', trade: 'Estilista' },
    { name: 'Beto', trade: '' },
  ],
  gallery: [
    { id: 'p1', url: '/api/v1/public/salons/luna/gallery/p1', caption: 'Frente' },
    { id: 'p2', url: '/api/v1/public/salons/luna/gallery/p2', caption: null },
  ],
  packages: [
    { id: 'k1', name: '5 cortes', service_name: 'Corte', sessions: 5, price_cents: 15000, validity_days: 90 },
    { id: 'k2', name: '3 tintes', service_name: 'Tinte', sessions: 3, price_cents: 24000, validity_days: null },
  ],
  hours: [{ weekday: 1, open: '09:00', close: '19:00' }],
}

function parsed(raw: unknown, base = ''): PublicSalon {
  const salon = normalizePublicSalon(raw, base)
  assert.ok(salon)
  return salon
}

test('normalizePublicSalon reads the whole D4 payload', () => {
  const salon = parsed(full, 'https://api.test')
  assert.deepEqual(salon.salon, full.salon)
  assert.deepEqual(salon.services[0], {
    id: 'svc-1',
    name: 'Corte',
    durationMin: 45,
    priceCents: 3800,
    description: 'Corte a medida',
  })
  assert.equal('description' in (salon.services[1] ?? {}), false)
  assert.deepEqual(salon.team, [{ name: 'Ana', trade: 'Estilista' }, { name: 'Beto', trade: '' }])
  assert.deepEqual(salon.gallery, [
    { id: 'p1', url: 'https://api.test/api/v1/public/salons/luna/gallery/p1', caption: 'Frente' },
    { id: 'p2', url: 'https://api.test/api/v1/public/salons/luna/gallery/p2', caption: '' },
  ])
  assert.deepEqual(salon.packages[1], {
    id: 'k2',
    name: '3 tintes',
    serviceName: 'Tinte',
    sessions: 3,
    priceCents: 24000,
    validityDays: null,
  })
})

test('normalizePublicSalon drops null text fields and unusable rows without inventing anything', () => {
  const salon = parsed({
    salon: { id: 's1', slug: 'x', name: 'X', description: null, address: null, phone: null, email: null, instagram: null, timezone: null },
    services: [{ id: 'a', name: 'A', duration_min: 30, price_cents: 100 }],
    team: [{ name: '', trade: 'x' }, 'nope', null],
    gallery: [{ id: '', url: '/x' }, { id: 'p', url: '' }, { id: 'ok', url: '/ok', caption: 'c' }],
    packages: [
      { id: 'k', name: 'K', service_name: 'S', sessions: 0, price_cents: 100, validity_days: 5 },
      { id: 'k', name: 'K', service_name: 'S', sessions: 2, price_cents: 1.5, validity_days: 5 },
      { id: 'k', name: 'K', service_name: 'S', sessions: 2, price_cents: -1, validity_days: 5 },
    ],
  })
  assert.deepEqual(salon.salon, { id: 's1', slug: 'x', name: 'X' })
  assert.deepEqual(salon.team, [])
  assert.deepEqual(salon.gallery, [{ id: 'ok', url: '/ok', caption: 'c' }])
  assert.deepEqual(salon.packages, [])
})

test('normalizePublicSalon with an old payload (no team, gallery, packages) gives empty lists', () => {
  const salon = parsed(payload)
  assert.deepEqual(salon.team, [])
  assert.deepEqual(salon.gallery, [])
  assert.deepEqual(salon.packages, [])
})

test('normalizePublicSalon accepts a salon with zero services (no static fallback)', () => {
  const salon = normalizePublicSalon({ salon: full.salon, services: [] })
  assert.ok(salon)
  assert.deepEqual(salon.services, [])
})

test('whatsappUrl is built from the phone digits only when the salon has a usable phone', () => {
  assert.equal(whatsappUrl('+53 5555 1234'), 'https://wa.me/5355551234')
  assert.equal(whatsappUrl('(53) 5555-1234', 'Hola Luna'), 'https://wa.me/5355551234?text=Hola%20Luna')
  assert.equal(whatsappUrl(null), null)
  assert.equal(whatsappUrl(''), null)
  assert.equal(whatsappUrl('   '), null)
  assert.equal(whatsappUrl('abc'), null)
  assert.equal(whatsappUrl('12345'), null)
  assert.equal(whatsappUrl('+1234567890123456'), null)
})

test('telHref and instagramUrl are built from the data or are null', () => {
  assert.equal(telHref('+53 5555 1234'), 'tel:+5355551234')
  assert.equal(telHref('5555 1234'), 'tel:55551234')
  assert.equal(telHref(null), null)
  assert.equal(telHref('---'), null)
  assert.equal(instagramUrl('luna.studio'), 'https://instagram.com/luna.studio')
  assert.equal(instagramUrl(null), null)
  assert.equal(instagramUrl(''), null)
})

test('buildLanding with full data renders every section from the API and nothing else', () => {
  const model = buildLanding(parsed(full))
  assert.equal(model.name, 'Luna Studio')
  assert.equal(model.description, 'Cortes y color con cita.')
  assert.equal(model.address, 'Calle 1 #23, Centro')
  assert.deepEqual(model.phone, {
    display: '+53 5555 1234',
    tel: 'tel:+5355551234',
    whatsapp: 'https://wa.me/5355551234',
  })
  assert.equal(model.email, 'hola@luna.test')
  assert.deepEqual(model.instagram, { handle: 'luna.studio', url: 'https://instagram.com/luna.studio' })
  assert.equal(model.timezone, 'America/Havana')
  assert.deepEqual(model.sections, {
    services: true,
    team: true,
    packages: true,
    loyalty: false,
    gallery: true,
  })
  assert.equal(model.canBook, true)
  assert.equal(model.heroPhoto?.id, 'p1')
  assert.deepEqual(
    model.nav.map((link) => link.label),
    ['Servicios', 'Equipo', 'Paquetes', 'Galería'],
  )
  assert.deepEqual(
    model.nav.map((link) => link.href),
    ['#servicios', '#equipo', '#paquetes', '#galeria'],
  )
  assert.equal(model.packages[0]?.detail, '5 sesiones de Corte · vigencia 90 días')
  assert.equal(model.packages[1]?.detail, '3 sesiones de Tinte · sin vencimiento')
  assert.equal(model.team[1]?.trade, null)
})

test('buildLanding with empty optional data hides every section that has no data', () => {
  const model = buildLanding(
    parsed({
      salon: { id: 's1', slug: 'x', name: 'Solo Nombre' },
      services: [{ id: 'a', name: 'A', duration_min: 30, price_cents: 100 }],
    }),
  )
  assert.equal(model.name, 'Solo Nombre')
  assert.equal(model.description, null)
  assert.equal(model.address, null)
  assert.equal(model.phone, null)
  assert.equal(model.email, null)
  assert.equal(model.instagram, null)
  assert.equal(model.heroPhoto, null)
  assert.equal(model.timezone, null)
  assert.deepEqual(model.sections, {
    services: true,
    team: false,
    packages: false,
    loyalty: false,
    gallery: false,
  })
  assert.deepEqual(
    model.nav.map((link) => link.label),
    ['Servicios'],
  )
})

test('buildLanding with no services cannot book and offers no services link', () => {
  const model = buildLanding(parsed({ salon: full.salon, services: [] }))
  assert.equal(model.canBook, false)
  assert.equal(model.sections.services, false)
  assert.deepEqual(model.nav, [])
})

test('buildLanding keeps a phone without a usable WhatsApp number as text with no wa.me link', () => {
  const model = buildLanding(parsed({ ...full, salon: { ...full.salon, phone: '12345' } }))
  assert.equal(model.phone?.display, '12345')
  assert.equal(model.phone?.whatsapp, null)
})

test('the landing carries no hard-coded salon identity, stats, testimonials or claims', () => {
  const text = JSON.stringify(buildLanding(parsed({ salon: { id: 's1', slug: 'x', name: 'Zeta' }, services: [{ id: 'a', name: 'A', duration_min: 30, price_cents: 100 }] })))
  for (const forbidden of ['Aurelia', 'Roma Norte', '5552', 'hola@aurelia', 'Especialistas certificadas', 'anticipo', '4 800', 'Mariana']) {
    assert.equal(text.includes(forbidden), false, forbidden)
  }
})

test('landingFailure tells a missing salon from any other failure', () => {
  assert.equal(landingFailure({ status: 404 }), 'not_found')
  assert.equal(landingFailure({ status: 500 }), 'error')
  assert.equal(landingFailure(new Error('x')), 'error')
  assert.equal(landingFailure(null), 'error')
})

test('the state copy is verbatim', () => {
  assert.equal(LANDING_ERROR_MESSAGE, 'No pudimos cargar los datos del salón. Probá de nuevo en un momento.')
  assert.equal(LANDING_NOT_FOUND_MESSAGE, 'No encontramos este salón.')
  assert.equal(LANDING_OFFLINE_NOTICE, 'Sin conexión: los datos pueden no estar al día.')
})

// ---------- D6: the service worker never caches the public API ----------

import { readFileSync } from 'node:fs'
import vm from 'node:vm'

type FetchEvent = {
  request: { method: string; url: string; mode: string; headers: { get: () => null } }
  respondWith: (response: unknown) => void
}

/** Loads the worker in a sandbox and returns its `fetch` listener. */
function loadWorker(source: string): (event: FetchEvent) => void {
  const listeners: Record<string, (event: never) => void> = {}
  const sandbox = {
    self: {
      location: { origin: 'https://salon.test' },
      addEventListener: (type: string, listener: (event: never) => void) => {
        listeners[type] = listener
      },
      skipWaiting: () => Promise.resolve(),
      clients: { claim: () => Promise.resolve() },
    },
    caches: { open: async () => ({ put: async () => undefined, match: async () => undefined }) },
    URL,
    Request,
    Response,
    Headers,
    fetch: async () => new Response('{}'),
  }
  vm.runInNewContext(source, sandbox)
  const handler = listeners['fetch']
  assert.ok(handler)
  return handler as unknown as (event: FetchEvent) => void
}

function intercepted(handler: (event: FetchEvent) => void, url: string, method = 'GET'): boolean {
  let handled = false
  handler({
    request: { method, url, mode: 'cors', headers: { get: () => null } },
    respondWith: () => {
      handled = true
    },
  })
  return handled
}

const WORKER_SOURCE = readFileSync(new URL('../../public/sw.js', import.meta.url), 'utf8')

test('the service worker leaves GET /api/v1/public/ to the network (no cache, no stale prices)', () => {
  const worker = loadWorker(WORKER_SOURCE)
  assert.equal(intercepted(worker, 'https://salon.test/api/v1/public/salons/luna'), false)
  assert.equal(intercepted(worker, 'https://salon.test/api/v1/public/salons/luna/gallery/p1'), false)
  assert.equal(intercepted(worker, 'https://salon.test/api/v1/public/salons/luna/availability?date=2026-01-01'), false)
})

test('the service worker keeps its network-first cache for the private API and the shell', () => {
  const worker = loadWorker(WORKER_SOURCE)
  assert.equal(intercepted(worker, 'https://salon.test/api/v1/salons/s1/services'), true)
  assert.equal(intercepted(worker, 'https://salon.test/api/v1/publication'), true)
  assert.equal(intercepted(worker, 'https://salon.test/api/v1/ws'), false)
  assert.equal(intercepted(worker, 'https://salon.test/api/v1/salons/s1/services', 'POST'), false)
  assert.equal(intercepted(worker, 'https://salon.test/assets/app.js'), true)
})

test('a failed reload keeps the salon already on screen unless it is a 404', () => {
  // Transient failure on reconnect: the page that works stays.
  assert.equal(landingStateAfterFailure('ready', 'error'), 'ready')
  // A salon that stopped existing must not keep showing.
  assert.equal(landingStateAfterFailure('ready', 'not_found'), 'not_found')
  // Nothing to keep when the first load fails or after pressing retry.
  assert.equal(landingStateAfterFailure('loading', 'error'), 'error')
  assert.equal(landingStateAfterFailure('error', 'error'), 'error')
  assert.equal(landingStateAfterFailure('loading', 'not_found'), 'not_found')
})

// ---------- F9 (D8): public loyalty program ----------

test('loyaltySentence is the verbatim public sentence «Cada N visitas, <servicio> de regalo»', () => {
  assert.equal(loyaltySentence(10, 'Corte'), 'Cada 10 visitas, Corte de regalo')
  assert.equal(loyaltySentence(2, 'Manicura semipermanente'), 'Cada 2 visitas, Manicura semipermanente de regalo')
})

test('normalizePublicSalon reads `loyalty` {visits_required, reward_service_name}', () => {
  const salon = parsed({ ...full, loyalty: { visits_required: 10, reward_service_name: ' Corte ' } })
  assert.deepEqual(salon.loyalty, { visitsRequired: 10, rewardServiceName: 'Corte' })
})

test('normalizePublicSalon leaves `loyalty` null when it is null, missing or unusable (never invented)', () => {
  assert.equal(parsed({ ...full, loyalty: null }).loyalty, null)
  assert.equal(parsed(full).loyalty, null)
  assert.equal(parsed(payload).loyalty, null)
  for (const bad of [
    'nope',
    [],
    {},
    { visits_required: 10 },
    { visits_required: 10, reward_service_name: '' },
    { visits_required: 10, reward_service_name: '   ' },
    { visits_required: 10, reward_service_name: 5 },
    { visits_required: 1, reward_service_name: 'Corte' },
    { visits_required: 51, reward_service_name: 'Corte' },
    { visits_required: 2.5, reward_service_name: 'Corte' },
    { visits_required: '10', reward_service_name: 'Corte' },
    { visits_required: null, reward_service_name: 'Corte' },
  ]) {
    assert.equal(parsed({ ...full, loyalty: bad }).loyalty, null, JSON.stringify(bad))
  }
  // The borders of the contract (2..50) are valid.
  assert.equal(parsed({ ...full, loyalty: { visits_required: 2, reward_service_name: 'A' } }).loyalty?.visitsRequired, 2)
  assert.equal(parsed({ ...full, loyalty: { visits_required: 50, reward_service_name: 'A' } }).loyalty?.visitsRequired, 50)
})

test('buildLanding shows the loyalty section ONLY from the payload `loyalty`, wired into sections and nav', () => {
  const model = buildLanding(parsed({ ...full, loyalty: { visits_required: 10, reward_service_name: 'Corte' } }))
  assert.equal(model.sections.loyalty, true)
  assert.deepEqual(model.loyalty, {
    visitsRequired: 10,
    rewardServiceName: 'Corte',
    sentence: 'Cada 10 visitas, Corte de regalo',
  })
  assert.deepEqual(
    model.nav.map((link) => link.label),
    ['Servicios', 'Equipo', 'Paquetes', 'Fidelidad', 'Galería'],
  )
  assert.deepEqual(model.nav.find((link) => link.label === 'Fidelidad'), {
    href: '#fidelidad',
    label: 'Fidelidad',
  })
})

test('buildLanding without `loyalty` renders no loyalty section and no nav link', () => {
  for (const raw of [full, { ...full, loyalty: null }]) {
    const model = buildLanding(parsed(raw))
    assert.equal(model.sections.loyalty, false)
    assert.equal(model.loyalty, null)
    assert.equal(model.nav.some((link) => link.href === '#fidelidad'), false)
    assert.equal(JSON.stringify(model).includes('de regalo'), false)
  }
})

test('the loyalty section does not depend on any other section (a salon with only a program)', () => {
  const model = buildLanding(
    parsed({ salon: full.salon, services: [], loyalty: { visits_required: 5, reward_service_name: 'Peinado' } }),
  )
  assert.equal(model.sections.services, false)
  assert.equal(model.sections.loyalty, true)
  assert.deepEqual(model.nav.map((link) => link.href), ['#fidelidad'])
})
