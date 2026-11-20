import test from 'node:test'
import assert from 'node:assert/strict'
import { contrastRatio, meetsWcagAa, relativeLuminance } from './contrast.ts'

// Palette values copied from src/styles/theme.css (a CSS file cannot be imported).
const INK = '#1e1418'
const CREAM = '#faf5ef'
const SURFACE = '#fffdfa'
const SHELL = '#f6efe7'
const WINE = '#6b2d4f'
const GOLD = '#c9a227'
const INK_MUTED = '#6b5a5e'

test('relative luminance follows the WCAG formula', () => {
  assert.equal(relativeLuminance('#000000'), 0)
  assert.equal(relativeLuminance('#ffffff'), 1)
  assert.ok(Math.abs(relativeLuminance('#808080') - 0.2158) < 0.001)
})

test('contrastRatio is symmetric and reaches 21:1 for black on white', () => {
  assert.ok(Math.abs(contrastRatio('#000000', '#ffffff') - 21) < 0.01)
  assert.equal(contrastRatio('#000000', '#ffffff'), contrastRatio('#ffffff', '#000000'))
  assert.equal(contrastRatio('#abcdef', '#abcdef'), 1)
})

test('meetsWcagAa checks the 4.5:1 body-text threshold', () => {
  assert.equal(meetsWcagAa(INK, CREAM), true)
  assert.equal(meetsWcagAa(GOLD, CREAM), false)
  assert.equal(meetsWcagAa('#7f7777', CREAM), false)
})

test('--ink-muted reaches AA on every light surface of the app', () => {
  for (const surface of [CREAM, SURFACE, SHELL]) {
    assert.ok(
      meetsWcagAa(INK_MUTED, surface),
      `${INK_MUTED} on ${surface} should reach 4.5:1`,
    )
  }
})

test('brand ink and wine are safe body-text colours on cream', () => {
  assert.ok(meetsWcagAa(INK, CREAM))
  assert.ok(meetsWcagAa(WINE, CREAM))
})

test('gold never passes as text on cream, so it stays a decorative colour', () => {
  assert.ok(contrastRatio(GOLD, CREAM) < 4.5)
})
