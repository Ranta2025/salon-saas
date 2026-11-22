import test from 'node:test'
import assert from 'node:assert/strict'
import {
  CURRENCY,
  CURRENCY_LABEL,
  formatCents,
  parseAmountToCents,
  sumCents,
  percentOf,
  formatMinutes,
} from './money.ts'

test('the salon works in US dollars but keeps the `$1,234.55` shape', () => {
  assert.equal(CURRENCY, 'USD')
  assert.equal(CURRENCY_LABEL, 'USD')
  assert.equal(formatCents(123455), '$1,234.55')
})

test('formatCents renders salon currency without float drift', () => {
  assert.equal(formatCents(35000), '$350.00')
  assert.equal(formatCents(0), '$0.00')
  assert.equal(formatCents(123455), '$1,234.55')
  assert.equal(formatCents(5), '$0.05')
  // 19.99 * 3 must never show a floating point artefact
  assert.equal(sumCents([1999, 1999, 1999]), 5997)
  assert.equal(formatCents(sumCents([1999, 1999, 1999])), '$59.97')
  assert.equal(formatCents(sumCents([10, 20])), '$0.30')
})

test('formatCents rejects non integer cents instead of rounding silently', () => {
  assert.throws(() => formatCents(10.5), /centavos/)
  assert.throws(() => formatCents(Number.NaN), /centavos/)
})

test('parseAmountToCents accepts the price formats a receptionist types', () => {
  assert.equal(parseAmountToCents('350'), 35000)
  assert.equal(parseAmountToCents('350.5'), 35050)
  assert.equal(parseAmountToCents('350.55'), 35055)
  assert.equal(parseAmountToCents('$1,234.55'), 123455)
  assert.equal(parseAmountToCents(' 0.05 '), 5)
  assert.equal(parseAmountToCents(''), null)
  assert.equal(parseAmountToCents('abc'), null)
  assert.equal(parseAmountToCents('-5'), null)
  assert.equal(parseAmountToCents('1.999'), null)
})

test('percentOf keeps commission and occupancy math in integers', () => {
  assert.equal(percentOf(35000, 10), 3500)
  assert.equal(percentOf(35000, 12.5), 4375)
  assert.equal(percentOf(0, 10), 0)
})

test('formatMinutes renders service durations in Spanish', () => {
  assert.equal(formatMinutes(45), '45 min')
  assert.equal(formatMinutes(60), '1 h')
  assert.equal(formatMinutes(90), '1 h 30 min')
  assert.equal(formatMinutes(120), '2 h')
})
