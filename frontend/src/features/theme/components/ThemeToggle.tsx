import { useEffect, useSyncExternalStore } from 'react'
import {
  DARK_QUERY,
  currentTheme,
  nextTheme,
  readStoredTheme,
  setTheme,
  subscribeTheme,
  systemTheme,
} from '../lib/theme'

function MoonIcon() {
  return (
    <svg
      className="theme-toggle__icon"
      viewBox="0 0 20 20"
      width="18"
      height="18"
      aria-hidden="true"
      focusable="false"
    >
      <path
        d="M16.5 12.2A6.8 6.8 0 0 1 7.8 3.5a7 7 0 1 0 8.7 8.7Z"
        fill="currentColor"
      />
    </svg>
  )
}

function SunIcon() {
  return (
    <svg
      className="theme-toggle__icon"
      viewBox="0 0 20 20"
      width="18"
      height="18"
      aria-hidden="true"
      focusable="false"
    >
      <circle cx="10" cy="10" r="3.6" fill="currentColor" />
      <path
        d="M10 1.5v2M10 16.5v2M1.5 10h2M16.5 10h2M4 4l1.4 1.4M14.6 14.6 16 16M16 4l-1.4 1.4M5.4 14.6 4 16"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
      />
    </svg>
  )
}

/**
 * Light/dark switch of the whole app. It is a real `<button>` with a 44 px hit
 * area, a visible focus ring, the state in `aria-pressed` and an action label in
 * Spanish. On a phone the text is visually hidden and the icon carries the
 * meaning (the accessible name never disappears).
 *
 * The first render reads the `data-theme` the inline script of index.html already
 * painted, so the button never disagrees with the page and there is no flash. An
 * explicit click is persisted; while nobody chose a theme, the page follows the
 * OS preference and reacts live to its changes.
 */
export function ThemeToggle() {
  const theme = useSyncExternalStore(subscribeTheme, currentTheme, currentTheme)

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return
    }
    const media = window.matchMedia(DARK_QUERY)
    const onSystemChange = (event: MediaQueryListEvent) => {
      // An explicit choice is never overridden by the OS.
      if (readStoredTheme() !== null) {
        return
      }
      setTheme(systemTheme(event.matches), { persist: false })
    }
    media.addEventListener('change', onSystemChange)
    return () => media.removeEventListener('change', onSystemChange)
  }, [])

  const isDark = theme === 'dark'
  const label = isDark ? 'Cambiar a tema claro' : 'Cambiar a tema oscuro'

  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={() => setTheme(nextTheme(theme))}
      aria-label={label}
      aria-pressed={isDark}
      title={label}
    >
      {isDark ? <SunIcon /> : <MoonIcon />}
      <span className="theme-toggle__text">{isDark ? 'Tema claro' : 'Tema oscuro'}</span>
    </button>
  )
}
