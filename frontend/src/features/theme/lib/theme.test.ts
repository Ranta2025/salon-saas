import test from 'node:test'
import assert from 'node:assert/strict'
import {
  THEME_STORAGE_KEY,
  applyTheme,
  currentTheme,
  isTheme,
  nextTheme,
  readStoredTheme,
  resolveTheme,
  setTheme,
  systemTheme,
  writeStoredTheme,
} from './theme.ts'

type StorageLike = {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
}

function storageWith(value: string | null): StorageLike {
  return {
    getItem: () => value,
    setItem: () => undefined,
  }
}

function fakeDocument() {
  const meta = new Map<string, string>()
  const root = {
    dataset: {} as Record<string, string | undefined>,
    style: { colorScheme: '' },
  }
  return {
    root,
    meta,
    doc: {
      documentElement: root,
      querySelector: (selector: string) => {
        const name = selector.includes('theme-color')
          ? 'theme-color'
          : selector.includes('color-scheme')
            ? 'color-scheme'
            : null
        if (name === null) {
          return null
        }
        return {
          setAttribute: (attribute: string, value: string) => {
            if (attribute === 'content') {
              meta.set(name, value)
            }
          },
        }
      },
    } as unknown as Document,
  }
}

test('only light and dark are valid theme values', () => {
  assert.equal(isTheme('light'), true)
  assert.equal(isTheme('dark'), true)
  assert.equal(isTheme('sepia'), false)
  assert.equal(isTheme(''), false)
  assert.equal(isTheme(null), false)
  assert.equal(isTheme(undefined), false)
})

test('the stored value is only honoured when it is valid', () => {
  assert.equal(readStoredTheme(storageWith('dark')), 'dark')
  assert.equal(readStoredTheme(storageWith('light')), 'light')
  // An unknown value is ignored: it means «follow the OS», never a broken theme.
  assert.equal(readStoredTheme(storageWith('sepia')), null)
  assert.equal(readStoredTheme(storageWith('')), null)
  assert.equal(readStoredTheme(storageWith(null)), null)
})

test('without storage the theme follows the OS and nothing crashes', () => {
  assert.equal(readStoredTheme(null), null)
  assert.doesNotThrow(() => writeStoredTheme('dark', null))
})

test('a storage that throws (blocked or full) is swallowed', () => {
  const throwing: StorageLike = {
    getItem: () => {
      throw new Error('SecurityError')
    },
    setItem: () => {
      throw new Error('QuotaExceededError')
    },
  }
  assert.equal(readStoredTheme(throwing), null)
  assert.doesNotThrow(() => writeStoredTheme('light', throwing))
})

test('the OS preference decides when nobody chose a theme', () => {
  assert.equal(systemTheme(true), 'dark')
  assert.equal(systemTheme(false), 'light')
  assert.equal(resolveTheme(null, true), 'dark')
  assert.equal(resolveTheme(null, false), 'light')
})

test('an explicit choice wins over the OS preference', () => {
  assert.equal(resolveTheme('light', true), 'light')
  assert.equal(resolveTheme('dark', false), 'dark')
})

test('toggling swaps the theme', () => {
  assert.equal(nextTheme('light'), 'dark')
  assert.equal(nextTheme('dark'), 'light')
})

test('applyTheme paints data-theme, color-scheme and both meta tags', () => {
  const { root, meta, doc } = fakeDocument()
  applyTheme('dark', doc)
  assert.equal(root.dataset.theme, 'dark')
  assert.equal(root.style.colorScheme, 'dark')
  assert.equal(meta.get('theme-color'), '#140C10')
  assert.equal(meta.get('color-scheme'), 'dark')

  applyTheme('light', doc)
  assert.equal(root.dataset.theme, 'light')
  assert.equal(root.style.colorScheme, 'light')
  assert.equal(meta.get('theme-color'), '#3B1E33')
  assert.equal(meta.get('color-scheme'), 'light')
  assert.equal(currentTheme(doc), 'light')
})

test('the storage key stays stable: users keep their choice across deploys', () => {
  assert.equal(THEME_STORAGE_KEY, 'salon-theme')
})

test('setTheme paints the document and the explicit choice is persisted', () => {
  const written: Array<[string, string]> = []
  const storage: StorageLike = {
    getItem: () => null,
    setItem: (key, value) => {
      written.push([key, value])
    },
  }
  const { root, doc } = fakeDocument()
  assert.doesNotThrow(() => setTheme('dark', { persist: false, document: doc }))
  assert.equal(root.dataset.theme, 'dark')

  writeStoredTheme('light', storage)
  assert.deepEqual(written, [['salon-theme', 'light']])
})
