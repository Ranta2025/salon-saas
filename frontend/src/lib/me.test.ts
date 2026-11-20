import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeMe, pickSalonId } from './me.ts'

const payload = {
  user: { id: 'u1', name: 'Ana', email: 'ana@salon.mx' },
  salons: [
    { id: 's1', slug: 'aurelia', name: 'Aurelia', role: 'owner' },
    { id: 's2', slug: 'otro', name: 'Otro salón', role: 'professional' },
  ],
}

test('normalizeMe reads the user and every salon membership', () => {
  const me = normalizeMe(payload)
  assert.ok(me)
  assert.deepEqual(me.user, { id: 'u1', name: 'Ana', email: 'ana@salon.mx' })
  assert.equal(me.salons.length, 2)
  assert.deepEqual(me.salons[0], { id: 's1', slug: 'aurelia', name: 'Aurelia', role: 'owner' })
})

test('normalizeMe drops malformed users and memberships instead of breaking login', () => {
  assert.equal(normalizeMe(null), null)
  assert.equal(normalizeMe({ salons: [] }), null)
  assert.equal(normalizeMe({ user: { name: 'sin id' } }), null)

  const me = normalizeMe({
    user: { id: 'u1', name: 'Ana' },
    salons: [{ id: 's1' }, { slug: 'sin-id' }, 'x', { id: 's2', name: 'Dos', role: 'admin' }],
  })
  assert.ok(me)
  assert.equal(me.user.email, '')
  assert.deepEqual(
    me.salons.map((salon) => salon.id),
    ['s1', 's2'],
  )
  assert.equal(me.salons[0]?.role, 'professional')
  assert.equal(me.salons[1]?.name, 'Dos')
})

test('pickSalonId keeps a stored salon only while the user is a member', () => {
  const me = normalizeMe(payload)
  assert.ok(me)
  assert.equal(pickSalonId('s2', me.salons), 's2')
  assert.equal(pickSalonId('stale', me.salons), 's1')
  assert.equal(pickSalonId(null, me.salons), 's1')
})

test('pickSalonId returns null when the account has no salons', () => {
  assert.equal(pickSalonId('stale', []), null)
})
