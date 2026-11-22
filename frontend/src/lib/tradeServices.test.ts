import test from 'node:test'
import assert from 'node:assert/strict'
import type { SalonMember } from './members.ts'
import {
  applyMemberServices,
  canEditServices,
  canEditTrade,
  NOBODY_DOES_SERVICE_NOTICE,
  NO_SERVICES_HINT,
  nobodyDoesService,
  normalizeServiceIds,
  normalizeTrade,
  professionalCount,
  professionalsCountLabel,
  professionalsForService,
  TRADE_MAX_LENGTH,
} from './tradeServices.ts'

function member(overrides: Partial<SalonMember> & Pick<SalonMember, 'id' | 'role'>): SalonMember {
  return {
    name: overrides.id,
    email: `${overrides.id}@salon.mx`,
    attends: false,
    trade: '',
    service_ids: [],
    ...overrides,
  }
}

test('normalizeTrade trims, removes line breaks and stops at 40 chars', () => {
  assert.equal(normalizeTrade('  Barbero '), 'Barbero')
  assert.equal(normalizeTrade('Colorista\r\n'), 'Colorista')
  assert.equal(normalizeTrade('Estilista\n  y colorista'), 'Estilista y colorista')
  assert.equal(normalizeTrade(''), '')
  assert.equal(TRADE_MAX_LENGTH, 40)
  assert.equal(normalizeTrade('x'.repeat(60)).length, 40)
  assert.equal(normalizeTrade(`  ${'y'.repeat(39)}  `), 'y'.repeat(39))
})

test('normalizeServiceIds keeps unique trimmed strings and drops anything else', () => {
  assert.deepEqual(normalizeServiceIds([' s1 ', 's1', '', 's2', 3, null]), ['s1', 's2'])
  assert.deepEqual(normalizeServiceIds(undefined), [])
  assert.deepEqual(normalizeServiceIds('s1'), [])
})

test('only owner/admin edit the trade or the services, and only of who attends', () => {
  const professional = member({ id: 'p1', role: 'professional' })
  const ownerWhoAttends = member({ id: 'o1', role: 'owner', attends: true })
  const ownerWhoDoesNot = member({ id: 'o2', role: 'owner', attends: false })
  const reception = member({ id: 'r1', role: 'reception' })

  assert.equal(canEditTrade('owner', professional), true)
  assert.equal(canEditTrade('admin', ownerWhoAttends), true)
  assert.equal(canEditServices('owner', professional), true)
  assert.equal(canEditServices('admin', ownerWhoAttends), true)

  assert.equal(canEditTrade('owner', ownerWhoDoesNot), false)
  assert.equal(canEditServices('owner', ownerWhoDoesNot), false)
  assert.equal(canEditTrade('owner', reception), false)
  assert.equal(canEditServices('owner', reception), false)
  assert.equal(canEditTrade('professional', professional), false)
  assert.equal(canEditServices('professional', professional), false)
  assert.equal(canEditTrade(null, professional), false)
})

test('the count of professionals per service comes from the members list', () => {
  const members = [
    member({ id: 'p1', role: 'professional', service_ids: ['s1', 's2'] }),
    member({ id: 'p2', role: 'professional', service_ids: ['s1'] }),
    member({ id: 'o1', role: 'owner', attends: true, service_ids: ['s2'] }),
    // Stale ids of someone who does not attend never count.
    member({ id: 'o2', role: 'owner', attends: false, service_ids: ['s1'] }),
    member({ id: 'r1', role: 'reception', service_ids: ['s1'] }),
  ]

  assert.deepEqual(
    professionalsForService(members, 's1').map((row) => row.id),
    ['p1', 'p2'],
  )
  assert.equal(professionalCount(members, 's1'), 2)
  assert.equal(professionalCount(members, 's2'), 2)
  assert.equal(professionalCount(members, 's3'), 0)
  assert.equal(professionalCount(members, ''), 0)
})

test('the "nobody does this service" notice follows the count', () => {
  const members = [member({ id: 'p1', role: 'professional', service_ids: ['s1'] })]
  assert.equal(nobodyDoesService(members, 's1'), false)
  assert.equal(nobodyDoesService(members, 's2'), true)
  assert.equal(
    NOBODY_DOES_SERVICE_NOTICE,
    'Nadie hace este servicio: no se ofrecen turnos',
  )
  assert.equal(NO_SERVICES_HINT, 'Todavía no hace ningún servicio: no recibirá turnos')
})

test('professionalsCountLabel follows the Spanish plural', () => {
  assert.equal(professionalsCountLabel(0), 'Ningún profesional lo hace')
  assert.equal(professionalsCountLabel(1), '1 profesional lo hace')
  assert.equal(professionalsCountLabel(3), '3 profesionales lo hacen')
})

test('applyMemberServices is optimistic and comes back to the saved list', () => {
  const before = [
    member({ id: 'p1', role: 'professional', service_ids: ['s1'] }),
    member({ id: 'p2', role: 'professional', service_ids: ['s2'] }),
  ]
  const after = applyMemberServices(before, 'p1', ['s2', 's2', 's3'])
  assert.deepEqual(after[0]?.service_ids, ['s2', 's3'])
  assert.deepEqual(after[1]?.service_ids, ['s2'])
  // The original list is untouched, so replaying the old ids is the exact rollback.
  assert.deepEqual(before[0]?.service_ids, ['s1'])
  const rolledBack = applyMemberServices(after, 'p1', before[0]?.service_ids ?? [])
  assert.deepEqual(
    rolledBack.map((row) => row.service_ids),
    [['s1'], ['s2']],
  )
  const cleared = applyMemberServices(before, 'p1', [])
  assert.deepEqual(cleared[0]?.service_ids, [])
})
