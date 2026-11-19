import test from 'node:test'
import assert from 'node:assert/strict'
import {
  addItem,
  changeQty,
  emptyCart,
  itemCount,
  removeItem,
  totalCents,
} from './posCart.ts'

const corte = { id: 'svc-corte', name: 'Corte', price_cents: 35000 }
const tinte = { id: 'svc-tinte', name: 'Coloración', price_cents: 89000 }

test('adding the same service merges it instead of duplicating the line', () => {
  let cart = emptyCart()
  cart = addItem(cart, corte)
  cart = addItem(cart, corte)
  assert.equal(cart.length, 1)
  assert.equal(cart[0]?.qty, 2)
  assert.equal(itemCount(cart), 2)
})

test('totals are integer cents taken from the server service price', () => {
  let cart = addItem(addItem(emptyCart(), corte), tinte)
  cart = changeQty(cart, corte.id, 2)
  assert.equal(totalCents(cart), 35000 * 2 + 89000)
  assert.equal(itemCount(cart), 3)
})

test('quantity can never drop below one and zero removes nothing implicitly', () => {
  const cart = addItem(emptyCart(), corte)
  assert.equal(changeQty(cart, corte.id, 0)[0]?.qty, 1)
  assert.equal(changeQty(cart, corte.id, -3)[0]?.qty, 1)
  assert.equal(changeQty(cart, 'missing', 5).length, 1)
})

test('removing a line empties the cart and zeroes the total', () => {
  const cart = removeItem(addItem(emptyCart(), corte), corte.id)
  assert.equal(cart.length, 0)
  assert.equal(totalCents(cart), 0)
  assert.equal(itemCount(cart), 0)
})

test('cart lines carry only what the sale endpoint accepts', () => {
  const cart = addItem(addItem(emptyCart(), corte), corte)
  assert.deepEqual(cart[0], {
    service_id: 'svc-corte',
    name: 'Corte',
    unit_price_cents: 35000,
    qty: 2,
  })
})
