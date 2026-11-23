import test from 'node:test'
import assert from 'node:assert/strict'
import type { SalonMember } from './members.ts'
import {
  DEACTIVATE_CONFIRM_NOTE,
  DEACTIVATE_SUCCESS,
  activeMembers,
  applyMemberEndedAt,
  deactivateConfirmQuestion,
  endedOnLabel,
  formerMembers,
  formerTeamLabel,
  isActiveMember,
  reactivateConfirmQuestion,
  reactivatedSuccess,
  restoreMember,
} from './teamStatus.ts'

const ZONE = 'America/Mexico_City'
const NOW = new Date('2026-11-30T18:00:00.000Z')

function member(
  over: Partial<SalonMember> & Pick<SalonMember, 'id' | 'role'>,
): SalonMember {
  return {
    name: over.id,
    email: `${over.id}@salon.mx`,
    attends: false,
    trade: '',
    service_ids: [],
    active: true,
    ended_at: null,
    ...over,
  }
}

// --- who is still in the team (F2g2) ---

test('a member counts as active only without a date of leave', () => {
  assert.equal(isActiveMember(member({ id: 'u1', role: 'owner' })), true)
  assert.equal(isActiveMember(member({ id: 'u2', role: 'professional', ended_at: null })), true)
  assert.equal(
    isActiveMember(
      member({ id: 'u3', role: 'professional', active: false, ended_at: '2026-11-29T18:00:00.000Z' }),
    ),
    false,
  )
  assert.equal(isActiveMember(member({ id: 'u4', role: 'admin', active: false })), false)
  // An inconsistent payload with a date of leave is a leave: the date wins.
  assert.equal(
    isActiveMember(member({ id: 'u5', role: 'admin', active: true, ended_at: '2026-11-29T18:00:00.000Z' })),
    false,
  )
})

test('the split keeps the order of the team and lists the last leave first', () => {
  const list = [
    member({ id: 'u1', role: 'owner' }),
    member({ id: 'u2', role: 'professional', active: false, ended_at: '2026-09-01T18:00:00.000Z' }),
    member({ id: 'u3', role: 'professional' }),
    member({ id: 'u4', role: 'reception', active: false, ended_at: '2026-11-29T18:00:00.000Z' }),
    member({ id: 'u5', role: 'admin', active: false }),
  ]

  assert.deepEqual(
    activeMembers(list).map((row) => row.id),
    ['u1', 'u3'],
  )
  // Most recent leave first; a leave without a date goes last.
  assert.deepEqual(
    formerMembers(list).map((row) => row.id),
    ['u4', 'u2', 'u5'],
  )
  assert.deepEqual(activeMembers([]), [])
  assert.deepEqual(formerMembers([]), [])
})

test('the collapsible block of the ex-team carries its count', () => {
  assert.equal(formerTeamLabel(0), 'Ex equipo (0)')
  assert.equal(formerTeamLabel(1), 'Ex equipo (1)')
  assert.equal(formerTeamLabel(4), 'Ex equipo (4)')
})

// --- "Dio de baja el 29 de noviembre" ---

test('the date of leave is written in Spanish with the day of the salon zone', () => {
  const options = { timeZone: ZONE, now: NOW }
  assert.equal(
    endedOnLabel('2026-11-29T18:00:00.000Z', options),
    'Dio de baja el 29 de noviembre',
  )
  // 03:00 UTC of the 30th is still the 29th in Mexico City.
  assert.equal(
    endedOnLabel('2026-11-30T03:00:00.000Z', options),
    'Dio de baja el 29 de noviembre',
  )
})

test('the date of leave adds the year when it is not the current one', () => {
  assert.equal(
    endedOnLabel('2025-11-29T18:00:00.000Z', { timeZone: ZONE, now: NOW }),
    'Dio de baja el 29 de noviembre de 2025',
  )
})

test('an unusable date of leave prints nothing instead of an invented day', () => {
  const options = { timeZone: ZONE, now: NOW }
  assert.equal(endedOnLabel(null, options), null)
  assert.equal(endedOnLabel('', options), null)
  assert.equal(endedOnLabel('ayer', options), null)
})

// --- copy of the confirmation and of the notices ---

test('the confirmation of a leave says what is kept', () => {
  assert.equal(deactivateConfirmQuestion('Ana'), '¿Dar de baja a Ana?')
  assert.equal(
    DEACTIVATE_CONFIRM_NOTE,
    'Conserva su historial y puede volver a entrar cuando quieras',
  )
  assert.equal(
    DEACTIVATE_SUCCESS,
    'La persona ya no tiene acceso al salón. Su historial se conserva.',
  )
})

test('the confirmation of a reactivation names the person and keeps the role', () => {
  assert.equal(reactivateConfirmQuestion('Ana'), '¿Reactivar a Ana?')
  assert.equal(reactivatedSuccess('Ana'), 'Ana vuelve al equipo.')
  assert.equal(reactivatedSuccess('  '), 'La persona vuelve al equipo.')
})

// --- optimistic updates with an exact rollback ---

test('the optimistic leave moves one row and never mutates the list', () => {
  const list = [member({ id: 'u1', role: 'owner' }), member({ id: 'u2', role: 'professional' })]
  const endedAt = '2026-11-29T18:00:00.000Z'
  const after = applyMemberEndedAt(list, 'u2', endedAt)

  assert.deepEqual(
    after.map((row) => ({ id: row.id, active: row.active, ended_at: row.ended_at })),
    [
      { id: 'u1', active: true, ended_at: null },
      { id: 'u2', active: false, ended_at: endedAt },
    ],
  )
  // The previous list is intact, so the rollback has something exact to restore.
  assert.deepEqual(
    list.map((row) => ({ id: row.id, active: row.active, ended_at: row.ended_at })),
    [
      { id: 'u1', active: true, ended_at: null },
      { id: 'u2', active: true, ended_at: null },
    ],
  )
})

test('reactivating optimistically clears the date of leave', () => {
  const endedAt = '2026-11-29T18:00:00.000Z'
  const list = [
    member({ id: 'u1', role: 'owner' }),
    member({ id: 'u2', role: 'professional', active: false, ended_at: endedAt }),
  ]
  const after = applyMemberEndedAt(list, 'u2', null)
  assert.deepEqual(
    after.map((row) => ({ id: row.id, active: row.active, ended_at: row.ended_at })),
    [
      { id: 'u1', active: true, ended_at: null },
      { id: 'u2', active: true, ended_at: null },
    ],
  )
  // An id that is not in the list changes nothing but still returns a copy.
  assert.deepEqual(applyMemberEndedAt(list, 'nope', null), list)
  assert.notEqual(applyMemberEndedAt(list, 'nope', null), list)
})

test('the rollback restores the exact previous row after a refused change', () => {
  const previous = member({
    id: 'u2',
    role: 'professional',
    active: false,
    ended_at: '2026-11-29T18:00:00.000Z',
    trade: 'Barbero',
    service_ids: ['s1'],
  })
  const list = [member({ id: 'u1', role: 'owner' }), previous]
  const optimistic = applyMemberEndedAt(list, 'u2', null)

  const rolledBack = restoreMember(optimistic, previous)
  assert.deepEqual(rolledBack, list)
  assert.equal(rolledBack[1], previous)
  // The list it rolls back is not mutated.
  assert.equal(optimistic[1]?.ended_at, null)
})
