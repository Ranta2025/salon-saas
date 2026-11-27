/*
 * Salon · service worker (vanilla, no dependencies).
 *
 * - precaches the app shell and the hashed build assets referenced by it
 * - network-first for API GETs, with a cached copy as fallback
 * - EXCEPT GET /api/v1/public/: the public page (prices, hours, gallery) goes
 *   straight to the network and is never cached, so it can never show old
 *   prices without saying so (F16, D6); the page shows its own offline notice
 * - EXCEPT the money paths of F11 (D13-d): /api/v1/simbank/ (the simulated bank),
 *   /api/v1/payments/ (the callback of the gateway) and /api/v1/public/deposits/
 *   (the state of a payment) are never cached nor answered from a cache, with any
 *   method: a payment is online only and a stale answer would lie about money
 * - EXCEPT the public review link of F13 (D8): /api/v1/public/reviews/ carries a
 *   single-use token, so it is never intercepted nor cached with any method: a
 *   cached GET or a replayed POST would lie about a review already sent
 * - never intercepts or caches non-GET requests: mutations are queued by the app
 *   (`src/lib/offlineQueue.ts`) so the server keeps deduplicating by client_op_id
 * - cache-first for same-origin static assets (JS/CSS/fonts/images)
 */

// Bump this value on every deploy that changes the worker or the precache list:
// `activate` deletes every cache that is not part of the current version.
const VERSION = 'salon-v6'
const SHELL_CACHE = `${VERSION}-shell`
const STATIC_CACHE = `${VERSION}-static`
const API_CACHE = `${VERSION}-api`
const KEEP = new Set([SHELL_CACHE, STATIC_CACHE, API_CACHE])

const SHELL_URLS = [
  '/',
  '/index.html',
  '/manifest.webmanifest',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
  '/icons/icon-maskable-512.png',
  '/icons/apple-touch-icon.png',
  '/icons/favicon.svg',
]

self.addEventListener('install', (event) => {
  event.waitUntil(precacheShell().then(() => self.skipWaiting()))
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys()
      await Promise.all(
        names.filter((name) => !KEEP.has(name)).map((name) => caches.delete(name)),
      )
      await self.clients.claim()
    })(),
  )
})

async function precacheShell() {
  const cache = await caches.open(SHELL_CACHE)
  await Promise.all(
    SHELL_URLS.map(async (url) => {
      try {
        const response = await fetch(new Request(url, { cache: 'reload' }))
        if (response.ok) {
          await cache.put(url, response)
        }
      } catch {
        // offline install: keep whatever was already available
      }
    }),
  )

  // Warm the hashed assets of the current build (index.html references them).
  try {
    const response = await fetch('/index.html', { cache: 'reload' })
    if (!response.ok) {
      return
    }
    await cache.put('/index.html', response.clone())
    const html = await response.text()
    const assets = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map(
      (match) => match[1],
    )
    await Promise.all(
      assets.map(async (asset) => {
        try {
          const assetResponse = await fetch(new Request(asset, { cache: 'reload' }))
          if (assetResponse.ok) {
            await cache.put(asset, assetResponse)
          }
        } catch {
          // a missing asset must not break the install
        }
      }),
    )
  } catch {
    // no shell to warm
  }
}

self.addEventListener('fetch', (event) => {
  const { request } = event

  let url
  try {
    url = new URL(request.url)
  } catch {
    return
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return
  }

  // Money of F11 (D13-d): never intercepted, with ANY method. Neither a cached GET
  // nor a replay of a payment is acceptable: these pages are online only.
  if (isNeverCachedApiPath(url.pathname)) {
    return
  }

  // Mutations always go straight to the network; the offline queue owns retries.
  if (request.method !== 'GET') {
    return
  }

  if (url.pathname === '/api/v1/ws') {
    return
  }

  // Public API (F16, D6): no respondWith, so the browser goes to the network
  // and nothing is stored. A failed request reaches the page, which says so.
  if (isPublicApiPath(url.pathname)) {
    return
  }

  if (url.pathname.startsWith('/api/v1/')) {
    event.respondWith(networkFirstApi(request))
    return
  }
  if (request.mode === 'navigate') {
    event.respondWith(networkFirstShell(request))
    return
  }
  if (url.origin === self.location.origin) {
    event.respondWith(cacheFirstStatic(request))
  }
})

/** `GET /api/v1/public/...`: anonymous salon data that must never come from a cache. */
function isPublicApiPath(pathname) {
  return pathname === '/api/v1/public' || pathname.startsWith('/api/v1/public/')
}

/**
 * Money paths (F11, D13-d) and the public review link (F13, D8): never cached, never
 * answered from a cache and never intercepted, whatever the method
 * (`/api/v1/public/deposits/` and `/api/v1/public/reviews/` are also covered by
 * `isPublicApiPath`, and they are named here on purpose: a review carries a
 * single-use token and a payment is online only).
 */
function isNeverCachedApiPath(pathname) {
  return (
    pathname.startsWith('/api/v1/simbank/') ||
    pathname.startsWith('/api/v1/payments/') ||
    pathname.startsWith('/api/v1/public/deposits/') ||
    pathname.startsWith('/api/v1/public/reviews/')
  )
}

async function networkFirstApi(request) {
  const cache = await caches.open(API_CACHE)
  const key = apiCacheKey(request)
  try {
    const response = await fetch(request)
    if (response.status === 401) {
      // A rejected session must not leave private responses behind.
      await caches.delete(API_CACHE)
      return response
    }
    const noStore = (response.headers.get('Cache-Control') || '').includes('no-store')
    if (response.ok && !noStore) {
      await cache.put(key, response.clone())
    }
    return response
  } catch {
    const cached = await cache.match(key)
    if (cached) {
      // The flag lets the app say "sin conexión: mostrando datos guardados"
      // instead of presenting a cached list as live data.
      return withOfflineFlag(cached)
    }
    return offlineJSON()
  }
}

/** A cached API answer, marked so the UI knows it is not live data. */
function withOfflineFlag(response) {
  const headers = new Headers(response.headers)
  headers.set('X-Salon-Offline-Cache', '1')
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  })
}

/**
 * An API GET that neither the network nor the cache can answer. It is a JSON
 * body with the `offline` flag (the app turns it into a quiet connectivity
 * state, not a server error) and a 503 status; answering with a rejected fetch
 * would only print a console error for an expected offline situation.
 */
function offlineJSON() {
  return new Response(
    JSON.stringify({
      error:
        'Sin conexión y sin copia guardada de estos datos. Reintenta cuando vuelva la red.',
      offline: true,
    }),
    { status: 503, headers: { 'Content-Type': 'application/json' } },
  )
}

async function networkFirstShell(request) {
  const cache = await caches.open(SHELL_CACHE)
  try {
    const response = await fetch(request)
    if (response.ok) {
      // Every SPA route is served by the same index.html.
      await cache.put('/index.html', response.clone())
    }
    return response
  } catch {
    const cached = await cache.match('/index.html')
    if (cached) {
      return cached
    }
    return new Response('<h1>Sin conexión</h1><p>Vuelve a intentar cuando recuperes la red.</p>', {
      status: 503,
      headers: { 'Content-Type': 'text/html; charset=utf-8' },
    })
  }
}

async function cacheFirstStatic(request) {
  const cache = await caches.open(STATIC_CACHE)
  const cached = await cache.match(request)
  if (cached) {
    return cached
  }
  const response = await fetch(request)
  if (response.ok && response.type === 'basic') {
    await cache.put(request, response.clone())
  }
  return response
}

/**
 * The cache key includes two independent hashes of the Authorization header so
 * another account cannot read these responses and a hash collision is
 * practically impossible. The raw token is never stored in the cache name.
 */
function apiCacheKey(request) {
  const url = new URL(request.url)
  const authorization = request.headers.get('Authorization') || 'anon'
  url.searchParams.set('__auth', hashToken(authorization))
  return new Request(url.toString(), { method: 'GET' })
}

function hashToken(value) {
  let first = 5381
  let second = 52711
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    first = ((first << 5) + first + code) >>> 0
    second = ((second << 5) - second + code) >>> 0
  }
  return `u${first.toString(36)}${second.toString(36)}`
}

self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'salon:clear-api-cache') {
    event.waitUntil(caches.delete(API_CACHE))
  }
})
