/**
 * Theme preference of the salon UI: `light` or `dark`.
 *
 * The pure functions of this module never touch the DOM unless they receive a
 * document, so the rules (stored value wins, otherwise the OS preference, and an
 * invalid stored value is ignored) are tested with node:test. The inline script
 * of index.html mirrors `resolveTheme` with plain JavaScript before the first
 * paint; this module then keeps the DOM and the meta tags in sync.
 */

export type Theme = 'light' | 'dark'

/** `localStorage` key. Absent means «follow the operating system». */
export const THEME_STORAGE_KEY = 'salon-theme'

/** `matchMedia` query of the OS preference. */
export const DARK_QUERY = '(prefers-color-scheme: dark)'

/** Browser chrome colour per theme (the same values index.html writes inline). */
export const THEME_COLORS: Record<Theme, string> = {
  light: '#3B1E33',
  dark: '#140C10',
}

type ThemeStorage = Pick<Storage, 'getItem' | 'setItem'>

type ThemeListener = (theme: Theme) => void

const listeners = new Set<ThemeListener>()

function defaultStorage(): ThemeStorage | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null
  } catch {
    // Access to localStorage can throw (Safari private mode, blocked cookies).
    return null
  }
}

function defaultDocument(): Document | null {
  return typeof document !== 'undefined' ? document : null
}

/** Narrows an untrusted value (storage, query string) to a valid theme. */
export function isTheme(value: unknown): value is Theme {
  return value === 'light' || value === 'dark'
}

/**
 * The explicitly chosen theme, or `null` when the person never chose one. An
 * invalid stored value is ignored exactly like an absent one; a storage that is
 * unavailable or throws never breaks the page.
 */
export function readStoredTheme(storage: ThemeStorage | null = defaultStorage()): Theme | null {
  if (!storage) {
    return null
  }
  try {
    const value = storage.getItem(THEME_STORAGE_KEY)
    return isTheme(value) ? value : null
  } catch {
    return null
  }
}

/** Persists the explicit choice. A full/blocked storage is silently ignored. */
export function writeStoredTheme(theme: Theme, storage: ThemeStorage | null = defaultStorage()): void {
  if (!storage) {
    return
  }
  try {
    storage.setItem(THEME_STORAGE_KEY, theme)
  } catch {
    // The theme still applies for this page; the choice simply is not remembered.
  }
}

/** The theme the OS asks for. */
export function systemTheme(prefersDark: boolean): Theme {
  return prefersDark ? 'dark' : 'light'
}

/** Explicit choice when there is one; otherwise the OS preference. */
export function resolveTheme(stored: Theme | null, prefersDark: boolean): Theme {
  return stored ?? systemTheme(prefersDark)
}

/** The other theme (the toggle). */
export function nextTheme(theme: Theme): Theme {
  return theme === 'dark' ? 'light' : 'dark'
}

/** The theme currently painted on the document (the inline script got there first). */
export function currentTheme(doc: Document | null = defaultDocument()): Theme {
  return doc?.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'
}

/**
 * Paints a theme: the `data-theme` attribute drives every token override, the
 * `color-scheme` property styles the native controls and the two meta tags keep
 * the browser UI (address bar, form controls) in the same theme.
 */
export function applyTheme(theme: Theme, doc: Document | null = defaultDocument()): void {
  if (!doc) {
    return
  }
  const root = doc.documentElement
  root.dataset.theme = theme
  root.style.colorScheme = theme
  doc
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute('content', THEME_COLORS[theme])
  doc
    .querySelector('meta[name="color-scheme"]')
    ?.setAttribute('content', theme === 'dark' ? 'dark' : 'light')
}

/** Subscribes to theme changes so every toggle on the page stays in sync. */
export function subscribeTheme(listener: ThemeListener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

function notifyTheme(theme: Theme): void {
  for (const listener of [...listeners]) {
    listener(theme)
  }
}

/**
 * Applies a theme and remembers an explicit choice. `persist: false` is used for
 * the OS preference: it follows the system without writing anything.
 */
export function setTheme(
  theme: Theme,
  options: { persist?: boolean; document?: Document | null } = {},
): void {
  const { persist = true, document: doc = defaultDocument() } = options
  applyTheme(theme, doc)
  if (persist) {
    writeStoredTheme(theme)
  }
  notifyTheme(theme)
}
