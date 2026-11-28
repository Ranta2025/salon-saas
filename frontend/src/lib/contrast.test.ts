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

// Dark theme values copied from `[data-theme='dark']` in src/styles/theme.css.
const DARK = {
  page: '#140c10',
  shell: '#160d12',
  surface: '#22131b',
  raised: '#2b1924',
  alt: '#1b1016',
  head: '#2e1b26',
  footer: '#120a0e',
  ink: '#f3e9e3',
  inkSoft: '#cdbdb7',
  inkMuted: '#b3a09b',
  wine: '#e39ab4',
  accentSolid: '#8f3d66',
  cream: '#faf5ef',
  plum: '#3b1e33',
  goldTint: '#3a2b16',
  roseTint: '#3a2028',
  okBg: '#1e3328',
  okText: '#a7e3c0',
  badBg: '#3a1a22',
  badBand: '#361922',
  badText: '#ffa9ba',
  warningBg: '#33280f',
  warningInk: '#f2d492',
  bannerBg: '#4d3a00',
  bannerText: '#ffe9a8',
  // rgba(243, 233, 227, 0.4) over #22131b: the border of an input in dark.
  lineStrong: '#7a6d6f',
} as const

const DARK_SURFACES = [
  DARK.page,
  DARK.shell,
  DARK.surface,
  DARK.raised,
  DARK.alt,
  DARK.head,
] as const

test('dark theme: every text token reaches AA on every dark surface', () => {
  for (const text of [DARK.ink, DARK.inkSoft, DARK.inkMuted]) {
    for (const surface of DARK_SURFACES) {
      assert.ok(
        meetsWcagAa(text, surface),
        `${text} on ${surface} should reach 4.5:1`,
      )
    }
  }
})

test('dark theme: the wine accent is readable as text on surfaces and tints', () => {
  for (const surface of [...DARK_SURFACES, DARK.goldTint, DARK.roseTint]) {
    assert.ok(
      meetsWcagAa(DARK.wine, surface),
      `${DARK.wine} on ${surface} should reach 4.5:1`,
    )
  }
})

test('dark theme: status text is readable on its own tinted background', () => {
  assert.ok(meetsWcagAa(DARK.okText, DARK.okBg))
  assert.ok(meetsWcagAa(DARK.badText, DARK.badBg))
  assert.ok(meetsWcagAa(DARK.badText, DARK.badBand))
  assert.ok(meetsWcagAa(DARK.warningInk, DARK.warningBg))
  assert.ok(meetsWcagAa(DARK.bannerText, DARK.bannerBg))
})

test('dark theme: cream text stays readable on the brand solids', () => {
  assert.ok(meetsWcagAa(DARK.cream, DARK.plum))
  assert.ok(meetsWcagAa(DARK.cream, DARK.accentSolid))
  assert.ok(meetsWcagAa(DARK.inkSoft, DARK.footer))
  assert.ok(meetsWcagAa(DARK.cream, DARK.footer))
})

test('dark theme: UI borders and focus rings reach 3:1', () => {
  for (const surface of DARK_SURFACES) {
    assert.ok(contrastRatio(DARK.lineStrong, surface) >= 3)
    assert.ok(contrastRatio(DARK.wine, surface) >= 3)
  }
})

test('dark theme is dark: its page and card surfaces are not a light palette', () => {
  for (const surface of DARK_SURFACES) {
    assert.ok(relativeLuminance(surface) < 0.06, `${surface} should be dark`)
  }
})
