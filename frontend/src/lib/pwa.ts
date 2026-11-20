/** PWA glue: registers the vanilla service worker and purges cached API data. */

export const SERVICE_WORKER_URL = '/sw.js'

/**
 * Registers `/sw.js` on production builds only. In dev Vite serves the raw
 * files and a stale worker would hide edits, so the dev server stays clean.
 * Failures are swallowed: the app works without a service worker.
 */
export function registerServiceWorker(): void {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) {
    return
  }
  if (!import.meta.env.PROD) {
    return
  }
  const register = (): void => {
    void navigator.serviceWorker
      .register(SERVICE_WORKER_URL, { updateViaCache: 'none' })
      .catch(() => undefined)
  }
  if (document.readyState === 'complete') {
    register()
    return
  }
  window.addEventListener('load', register, { once: true })
}

/** Removes cached API responses (called on logout so another account never reads them). */
export function purgeOfflineApiCache(): void {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) {
    return
  }
  navigator.serviceWorker.controller?.postMessage({ type: 'salon:clear-api-cache' })
}
