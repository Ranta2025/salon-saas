import test from 'node:test'
import assert from 'node:assert/strict'
import {
  HERO_PRELOAD_HREF,
  isLandingPath,
  preloadLandingHero,
  releaseLandingHero,
} from './landingPreload.ts'

type FakeLink = {
  rel: string
  as: string
  href: string
  fetchPriority: string
  dataset: Record<string, string>
  remove: () => void
}

function fakeDocument() {
  const links: FakeLink[] = []
  const doc = {
    querySelector: (selector: string) =>
      selector === 'link[data-landing-preload="hero"]'
        ? links.find((link) => link.dataset['landingPreload'] === 'hero') ?? null
        : null,
    createElement: () => {
      const link: FakeLink = {
        rel: '',
        as: '',
        href: '',
        fetchPriority: '',
        dataset: {},
        remove: () => {
          const index = links.indexOf(link)
          if (index >= 0) {
            links.splice(index, 1)
          }
        },
      }
      return link
    },
    head: {
      appendChild: (link: FakeLink) => {
        links.push(link)
        return link
      },
    },
  }
  return { doc: doc as unknown as Document, links }
}

test('the hero is preloaded only for the public routes', () => {
  assert.equal(isLandingPath('/'), true)
  assert.equal(isLandingPath('/salon/aurelia'), true)
  assert.equal(isLandingPath('/salon/aurelia/'), true)
  assert.equal(isLandingPath('/panel'), false)
  assert.equal(isLandingPath('/panel/calendario'), false)
  assert.equal(isLandingPath('/login'), false)
  assert.equal(isLandingPath('/salon'), false)
  assert.equal(isLandingPath('/salon/aurelia/servicios'), false)
})

test('preloadLandingHero injects one high-priority image preload', () => {
  const { doc, links } = fakeDocument()
  preloadLandingHero(doc)
  preloadLandingHero(doc)

  assert.equal(links.length, 1)
  assert.equal(links[0]?.rel, 'preload')
  assert.equal(links[0]?.as, 'image')
  assert.equal(links[0]?.href, HERO_PRELOAD_HREF)
  assert.equal(links[0]?.fetchPriority, 'high')
  assert.equal(links[0]?.dataset['landingPreload'], 'hero')
})

test('releaseLandingHero removes the injected preload and tolerates a missing head', () => {
  const { doc, links } = fakeDocument()
  preloadLandingHero(doc)
  releaseLandingHero(doc)
  assert.equal(links.length, 0)
  // No document (tests, SSR) is a no-op, never a crash.
  preloadLandingHero(null)
  releaseLandingHero(null)
})
