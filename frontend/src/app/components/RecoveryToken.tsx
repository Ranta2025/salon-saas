import { useEffect, useState } from 'react'
import { readFragmentToken } from '../../lib/recovery'

/**
 * Token of the emailed link (F15, D11), read from the FRAGMENT.
 *
 * The read is PURE (it only looks at `location.href`), so React StrictMode's
 * double render gets the same token both times; the state keeps it in memory for
 * the life of the page (never storage, never the console). The address bar is
 * wiped in an effect with `history.replaceState`, which runs before the page's own
 * effects (hooks run in call order) and so before any network call, and is
 * idempotent (the second StrictMode run finds the URL already clean).
 */
export function useFragmentToken(): string | null {
  const [token] = useState(() => readFragmentToken(window.location.href).token)
  useEffect(() => {
    const { url } = readFragmentToken(window.location.href)
    if (url !== window.location.href) {
      window.history.replaceState(null, '', url)
    }
  }, [])
  return token
}
