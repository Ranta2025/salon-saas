import test from 'node:test'
import assert from 'node:assert/strict'
import {
  ATTENDS_NOTICE,
  applyAttends,
  attendsControl,
  memberAttends,
  nobodyAttends,
} from './attends.ts'
import type { SalonMember } from './members.ts'

function member(over: Partial<SalonMember> & Pick<SalonMember, 'id' | 'role'>): SalonMember {
  return { name: over.id, email: '', attends: false, ...over }
}

test('a professional always attends clients, even with the flag stored as false', () => {
  assert.equal(memberAttends(member({ id: 'u2', role: 'professional', attends: false })), true)
  assert.equal(memberAttends(member({ id: 'u2', role: 'professional', attends: true })), true)
})

test('owner and admin attend only with the flag; reception never does', () => {
  assert.equal(memberAttends(member({ id: 'u1', role: 'owner', attends: true })), true)
  assert.equal(memberAttends(member({ id: 'u1', role: 'owner', attends: false })), false)
  assert.equal(memberAttends(member({ id: 'u3', role: 'admin', attends: true })), true)
  assert.equal(memberAttends(member({ id: 'u3', role: 'admin', attends: false })), false)
  assert.equal(memberAttends(member({ id: 'u4', role: 'reception', attends: true })), false)
  assert.equal(memberAttends(member({ id: 'u4', role: 'reception', attends: false })), false)
})

test('the switch is offered only on owner and admin members, and only to a manager', () => {
  assert.equal(attendsControl('owner', 'owner'), 'switch')
  assert.equal(attendsControl('owner', 'admin'), 'switch')
  assert.equal(attendsControl('admin', 'owner'), 'switch')
  assert.equal(attendsControl('admin', 'admin'), 'switch')
})

test('professionals get a fixed label and reception gets nothing', () => {
  assert.equal(attendsControl('owner', 'professional'), 'fixed')
  assert.equal(attendsControl('admin', 'professional'), 'fixed')
  assert.equal(attendsControl('owner', 'reception'), 'none')
  assert.equal(attendsControl('admin', 'reception'), 'none')
})

test('a role that cannot manage the team never gets a switch', () => {
  assert.equal(attendsControl('professional', 'owner'), 'none')
  assert.equal(attendsControl('reception', 'admin'), 'none')
  assert.equal(attendsControl(null, 'owner'), 'none')
  assert.equal(attendsControl(null, 'professional'), 'fixed')
})

test('the notice appears only when the team is not empty and nobody attends', () => {
  assert.equal(nobodyAttends([]), false)
  assert.equal(
    nobodyAttends([
      member({ id: 'u1', role: 'owner', attends: false }),
      member({ id: 'u4', role: 'reception', attends: true }),
    ]),
    true,
  )
  assert.equal(
    nobodyAttends([
      member({ id: 'u1', role: 'owner', attends: false }),
      member({ id: 'u2', role: 'professional' }),
    ]),
    false,
  )
  assert.equal(nobodyAttends([member({ id: 'u1', role: 'owner', attends: true })]), false)
})

test('the notice copy is the one of the contract, in Spanish', () => {
  assert.equal(
    ATTENDS_NOTICE,
    "Nadie atiende todavía: activá 'Atiende clientes' en alguien para recibir turnos",
  )
})

test('applyAttends changes one member, never mutates and rolls back to the previous state', () => {
  const before: SalonMember[] = [
    member({ id: 'u1', role: 'owner', attends: false }),
    member({ id: 'u2', role: 'admin', attends: true }),
  ]
  const optimistic = applyAttends(before, 'u1', true)
  assert.deepEqual(
    optimistic.map((row) => [row.id, row.attends]),
    [
      ['u1', true],
      ['u2', true],
    ],
  )
  assert.notEqual(optimistic, before)
  assert.deepEqual(before.map((row) => row.attends), [false, true])

  const rolledBack = applyAttends(optimistic, 'u1', before[0]?.attends ?? false)
  assert.deepEqual(rolledBack, before)
})

test('applyAttends leaves the list untouched when the person is not in it', () => {
  const before = [member({ id: 'u1', role: 'owner', attends: false })]
  assert.deepEqual(applyAttends(before, 'u9', true), before)
})
