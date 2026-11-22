import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizePublicSalon, toLandingServices } from './publicSalon.ts'
import { SERVICES, type LandingService } from '../landing/content.ts'

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

test('toLandingServices keeps the API name and price and reuses the static copy', () => {
  const salon = normalizePublicSalon(payload)
  assert.ok(salon)
  const services = toLandingServices(salon.services, SERVICES)
  assert.equal(services.length, 2)
  assert.equal(services[0]?.id, 'svc-1')
  assert.equal(services[0]?.name, 'Corte y peinado')
  assert.equal(services[0]?.priceCents, 42000)
  assert.equal(services[0]?.description, SERVICES[0]?.description)
  assert.equal(services[1]?.name, 'Keratina')
  assert.equal(services[1]?.tag, undefined)
})

test('toLandingServices falls back to the static catalogue when the API sends nothing', () => {
  const fallback: readonly LandingService[] = SERVICES
  assert.equal(toLandingServices([], fallback), fallback)
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
