/** Browser connectivity helpers shared by the shell, the POS and the queue. */

/** True unless the browser explicitly reports being offline. */
export function isBrowserOnline(): boolean {
  if (typeof navigator === 'undefined') {
    return true
  }
  return navigator.onLine !== false
}

/**
 * Calls `handler` on every `online`/`offline` transition and returns the
 * cleanup. Without a window (tests, SSR) nothing is registered.
 */
export function subscribeOnlineStatus(handler: (online: boolean) => void): () => void {
  if (typeof window === 'undefined') {
    return () => undefined
  }
  const onOnline = () => handler(true)
  const onOffline = () => handler(false)
  window.addEventListener('online', onOnline)
  window.addEventListener('offline', onOffline)
  return () => {
    window.removeEventListener('online', onOnline)
    window.removeEventListener('offline', onOffline)
  }
}
