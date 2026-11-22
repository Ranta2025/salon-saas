/**
 * Hero preload of the public landing.
 *
 * `index.html` no longer preloads `hero-atwork.webp`: the panel renders on every
 * other route, and a preload nobody uses makes Chrome warn "preload not used".
 * The landing asks for it on mount — and `main.tsx` asks for it before React
 * renders when the current URL is already a public one.
 */

export const HERO_PRELOAD_HREF = '/img/hero-atwork.webp'

const PRELOAD_SELECTOR = 'link[data-landing-preload="hero"]'

type PreloadLink = {
  rel: string
  as: string
  href: string
  fetchPriority: string
  dataset: Record<string, string>
  remove: () => void
}

type PreloadDocument = {
  querySelector: (selector: string) => unknown
  createElement: (tagName: string) => PreloadLink
  head: { appendChild: (link: PreloadLink) => unknown }
}

function documentOrNull(doc?: Document | null): PreloadDocument | null {
  const candidate = doc ?? (typeof document === 'undefined' ? null : document)
  return (candidate as unknown as PreloadDocument) ?? null
}

/** `/` and `/salon/:slug` are the only routes that render the hero. */
export function isLandingPath(pathname: string): boolean {
  const path = pathname.trim().replace(/\/+$/, '')
  if (path === '') {
    return true
  }
  return /^\/salon\/[^/]+$/.test(path)
}

/** Injects the hero preload once; a second call is a no-op. */
export function preloadLandingHero(doc?: Document | null): void {
  const target = documentOrNull(doc)
  if (!target) {
    return
  }
  if (target.querySelector(PRELOAD_SELECTOR)) {
    return
  }
  const link = target.createElement('link')
  link.rel = 'preload'
  link.as = 'image'
  link.href = HERO_PRELOAD_HREF
  link.fetchPriority = 'high'
  link.dataset['landingPreload'] = 'hero'
  target.head.appendChild(link)
}

/** Removes the preload (unmount of the landing, tests). */
export function releaseLandingHero(doc?: Document | null): void {
  const target = documentOrNull(doc)
  const link = target?.querySelector(PRELOAD_SELECTOR) as PreloadLink | null | undefined
  link?.remove()
}
