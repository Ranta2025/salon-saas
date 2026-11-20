import test from 'node:test'
import assert from 'node:assert/strict'
import { formatOccupancy, occupancyFraction, normalizeTopServices } from './metrics.ts'

test('occupancyFraction accepts a ratio or a percentage from the API', () => {
  assert.equal(occupancyFraction(0), 0)
  assert.equal(occupancyFraction(0.42), 0.42)
  assert.equal(occupancyFraction(42), 0.42)
  assert.equal(occupancyFraction(1), 1)
  assert.equal(occupancyFraction(100), 1)
  assert.equal(occupancyFraction(130), 1)
  assert.equal(occupancyFraction(-5), 0)
  assert.equal(occupancyFraction(null), 0)
  assert.equal(occupancyFraction(undefined), 0)
})

test('formatOccupancy renders a readable percentage', () => {
  assert.equal(formatOccupancy(0.42), '42%')
  assert.equal(formatOccupancy(42), '42%')
  assert.equal(formatOccupancy(0), '0%')
  assert.equal(formatOccupancy(1), '100%')
  assert.equal(formatOccupancy(null), '—')
})

test('normalizeTopServices tolerates the documented and the plural field names', () => {
  assert.deepEqual(
    normalizeTopServices([
      { name: 'Coloración', count: 12 },
      { service_name: 'Keratina', bookings: 4, revenue_cents: 356000 },
    ]),
    [
      { name: 'Coloración', count: 12, revenue_cents: null },
      { name: 'Keratina', count: 4, revenue_cents: 356000 },
    ],
  )
})

test('normalizeTopServices drops unusable rows instead of rendering undefined', () => {
  assert.deepEqual(normalizeTopServices([{}, null, 'x', { name: 'Corte' }]), [
    { name: 'Corte', count: 0, revenue_cents: null },
  ])
  assert.deepEqual(normalizeTopServices(undefined), [])
  assert.deepEqual(normalizeTopServices('nope'), [])
})
