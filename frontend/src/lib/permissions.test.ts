import test from 'node:test'
import assert from 'node:assert/strict'
import {
  activeRole,
  canAssignRole,
  canEditSettings,
  canManageBooking,
  canManageMembers,
  isManager,
  roleLabel,
} from './permissions.ts'

test('roleLabel names every salon role in Spanish', () => {
  assert.equal(roleLabel('owner'), 'Dueña o dueño')
  assert.equal(roleLabel('admin'), 'Administración')
  assert.equal(roleLabel('professional'), 'Profesional')
  assert.equal(roleLabel(null), 'Sin rol')
})

test('isManager is true only for owner and admin', () => {
  assert.equal(isManager('owner'), true)
  assert.equal(isManager('admin'), true)
  assert.equal(isManager('professional'), false)
  assert.equal(isManager(null), false)
})

test('members and settings are managed by owner and admin only', () => {
  assert.equal(canManageMembers('owner'), true)
  assert.equal(canManageMembers('admin'), true)
  assert.equal(canManageMembers('professional'), false)
  assert.equal(canEditSettings('owner'), true)
  assert.equal(canEditSettings('admin'), true)
  assert.equal(canEditSettings('professional'), false)
  assert.equal(canEditSettings(null), false)
})

test('a professional only acts on their own bookings', () => {
  assert.equal(canManageBooking('owner', 'u1', 'u9'), true)
  assert.equal(canManageBooking('admin', 'u1', 'u9'), true)
  assert.equal(canManageBooking('professional', 'u2', 'u2'), true)
  assert.equal(canManageBooking('professional', 'u2', 'u9'), false)
  assert.equal(canManageBooking('professional', null, 'u2'), false)
  assert.equal(canManageBooking(null, 'u1', 'u1'), false)
})

test('only the owner may add another admin', () => {
  assert.deepEqual(canAssignRole('owner', 'admin'), true)
  assert.deepEqual(canAssignRole('owner', 'professional'), true)
  assert.deepEqual(canAssignRole('owner', 'owner'), true)
  assert.deepEqual(canAssignRole('admin', 'admin'), false)
  assert.deepEqual(canAssignRole('admin', 'professional'), true)
  assert.deepEqual(canAssignRole('professional', 'professional'), false)
})

test('activeRole reads the role of the active salon and never guesses another one', () => {
  const salons = [
    { id: 's1', role: 'owner' as const },
    { id: 's2', role: 'professional' as const },
  ]
  assert.equal(activeRole(salons, 's2'), 'professional')
  assert.equal(activeRole(salons, 's9'), null)
  assert.equal(activeRole(salons, null), null)
  assert.equal(activeRole([], 's1'), null)
})
