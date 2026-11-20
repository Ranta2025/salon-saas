import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeStaff } from './staff.ts'

test('normalizeStaff reads id, name and role of the salon team', () => {
  const staff = normalizeStaff({
    staff: [
      { id: 'u1', name: 'Ana', role: 'owner' },
      { id: 'u2', name: 'Beto', role: 'professional' },
    ],
  })
  assert.deepEqual(staff, [
    { id: 'u1', name: 'Ana', role: 'owner' },
    { id: 'u2', name: 'Beto', role: 'professional' },
  ])
})

test('normalizeStaff accepts a bare array and skips rows without id or name', () => {
  const staff = normalizeStaff([
    { id: 'u1', name: 'Ana', role: 'admin' },
    { id: 'u2' },
    { name: 'Sin id' },
    null,
    'x',
  ])
  assert.deepEqual(staff, [{ id: 'u1', name: 'Ana', role: 'admin' }])
})

test('normalizeStaff falls back to a neutral role and never throws on bad payloads', () => {
  assert.deepEqual(normalizeStaff(null), [])
  assert.deepEqual(normalizeStaff({ staff: 'nope' }), [])
  const staff = normalizeStaff({ staff: [{ id: 'u1', name: 'Ana', role: 'superstar' }] })
  assert.equal(staff[0]?.role, 'professional')
})
