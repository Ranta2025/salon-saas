/**
 * Signal that a screen is showing *saved* data: an API GET answered by the
 * service worker from its cache because the server could not be reached.
 *
 * The worker marks those responses (`X-Salon-Offline-Cache: 1`) and the API
 * client broadcasts them here, so a page can say "Sin conexión: mostrando datos
 * guardados" instead of pretending a cached list is live data.
 */

export const OFFLINE_DATA_EVENT = 'salon:offline-data'

/** Responses the worker labels as a cached copy (never a live API answer). */
export const OFFLINE_CACHE_HEADER = 'x-salon-offline-cache'

type EventTargetLike = {
  addEventListener: (type: string, listener: EventListener) => void
  removeEventListener: (type: string, listener: EventListener) => void
  dispatchEvent: (event: Event) => boolean
}

/** Fallback bus for runtimes without a global event target (node --test). */
const localBus: EventTargetLike | null =
  typeof EventTarget === 'function' ? (new EventTarget() as unknown as EventTargetLike) : null

function eventTarget(): EventTargetLike | null {
  const candidate = globalThis as unknown as Partial<EventTargetLike>
  return typeof candidate.addEventListener === 'function' &&
    typeof candidate.dispatchEvent === 'function'
    ? (candidate as EventTargetLike)
    : localBus
}

/** Tells every mounted screen that `path` was answered from the offline cache. */
export function noteOfflineData(path: string): void {
  const target = eventTarget()
  if (!target || typeof CustomEvent !== 'function') {
    return
  }
  target.dispatchEvent(new CustomEvent(OFFLINE_DATA_EVENT, { detail: { path } }))
}

/** Subscribes to cached answers; returns the cleanup. */
export function subscribeOfflineData(handler: (path: string) => void): () => void {
  const target = eventTarget()
  if (!target) {
    return () => undefined
  }
  const listener = (event: Event): void => {
    const detail = (event as CustomEvent<{ path?: unknown }>).detail
    const path = typeof detail?.path === 'string' ? detail.path : ''
    handler(path)
  }
  target.addEventListener(OFFLINE_DATA_EVENT, listener)
  return () => target.removeEventListener(OFFLINE_DATA_EVENT, listener)
}

/** True when the response of the worker is a cached copy instead of live data. */
export function isCachedResponse(response: Response): boolean {
  return response.headers.get(OFFLINE_CACHE_HEADER) === '1'
}
