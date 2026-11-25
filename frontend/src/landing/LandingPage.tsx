import { useCallback, useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { fetchPublicSalon } from '../lib/api'
import { isBrowserOnline, subscribeOnlineStatus } from '../lib/connectivity'
import {
  buildLanding,
  landingFailure,
  landingStateAfterFailure,
  LANDING_ERROR_MESSAGE,
  LANDING_NOT_FOUND_MESSAGE,
  LANDING_OFFLINE_NOTICE,
  type LandingService,
  type PublicSalon,
} from '../lib/publicSalon'
import { BookingCta } from './components/BookingCta'
import { GallerySection } from './components/GallerySection'
import { Hero } from './components/Hero'
import { PackagesSection } from './components/PackagesSection'
import { ServicesSection } from './components/ServicesSection'
import { SiteFooter } from './components/SiteFooter'
import { SiteHeader } from './components/SiteHeader'
import { TeamSection } from './components/TeamSection'

/**
 * Slug the public page shows at `/` when the URL has none: a deployment setting
 * (`VITE_SALON_SLUG`), not salon data. Everything printed comes from the API.
 */
const DEFAULT_SLUG =
  (import.meta.env.VITE_SALON_SLUG as string | undefined)?.trim() || 'aurelia'

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; salon: PublicSalon }
  | { kind: 'not_found' }
  | { kind: 'error' }

/** `true` while the browser reports a connection (D6 shows a notice otherwise). */
function useOnline(): boolean {
  const [online, setOnline] = useState(isBrowserOnline)
  useEffect(() => subscribeOnlineStatus(setOnline), [])
  return online
}

/** Offline notice (D6): the data on screen may be old, and the page says so. */
function OfflineNotice() {
  return (
    <p className="landing-offline" role="status">
      {LANDING_OFFLINE_NOTICE}
    </p>
  )
}

/** Public landing page, shared by `/` and `/salon/:slug`. Every datum comes from the API. */
export function LandingPage() {
  const { slug: routeSlug } = useParams<{ slug?: string }>()
  const slug = routeSlug ?? DEFAULT_SLUG
  const online = useOnline()
  const [state, setState] = useState<LoadState>({ kind: 'loading' })
  const [preselected, setPreselected] = useState<LandingService | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const retry = useCallback(() => {
    setState({ kind: 'loading' })
    setReloadKey((key) => key + 1)
  }, [])

  // A new salon starts from the loading state: never show another salon's data.
  useEffect(() => {
    setState({ kind: 'loading' })
    setPreselected(null)
  }, [slug])

  useEffect(() => {
    let active = true
    fetchPublicSalon(slug)
      .then((salon) => {
        if (active) {
          setState({ kind: 'ready', salon })
        }
      })
      .catch((error: unknown) => {
        // The data on screen (if any) is kept only when it is not a 404: a salon
        // that stopped existing must not keep showing.
        if (active) {
          setState((previous) => {
            const kind = landingStateAfterFailure(previous.kind, landingFailure(error))
            return kind === 'ready' ? previous : { kind }
          })
        }
      })
    return () => {
      active = false
    }
  }, [slug, reloadKey])

  // Back online: read the salon again so the page stops showing data that may be old.
  const [wasOffline, setWasOffline] = useState(false)
  useEffect(() => {
    if (!online) {
      setWasOffline(true)
      return
    }
    if (wasOffline) {
      setWasOffline(false)
      setReloadKey((key) => key + 1)
    }
  }, [online, wasOffline])

  const model = useMemo(
    () => (state.kind === 'ready' ? buildLanding(state.salon) : null),
    [state],
  )

  useEffect(() => {
    if (model !== null) {
      document.title = model.name
    }
  }, [model])

  if (state.kind !== 'ready' || model === null) {
    return (
      <main className="landing-state" id="contenido" aria-busy={state.kind === 'loading'}>
        {!online ? <OfflineNotice /> : null}
        {state.kind === 'loading' ? (
          <p className="loading" role="status">
            <span className="loading__dot" aria-hidden="true" />
            Cargando…
          </p>
        ) : null}
        {state.kind === 'not_found' ? (
          <div className="landing-state__card" role="alert">
            <h1>{LANDING_NOT_FOUND_MESSAGE}</h1>
          </div>
        ) : null}
        {state.kind === 'error' ? (
          <div className="landing-state__card" role="alert">
            <h1>{LANDING_ERROR_MESSAGE}</h1>
            <button type="button" className="btn btn--wine" onClick={retry}>
              Reintentar
            </button>
          </div>
        ) : null}
      </main>
    )
  }

  return (
    <>
      <a className="skip-link" href="#contenido">
        Saltar al contenido
      </a>
      {!online ? <OfflineNotice /> : null}
      <SiteHeader model={model} />
      <main id="contenido">
        <Hero model={model} />
        {model.sections.services ? (
          <ServicesSection
            services={model.services}
            whatsapp={model.phone?.whatsapp ?? null}
            onSelect={setPreselected}
          />
        ) : null}
        {model.sections.team ? <TeamSection team={model.team} /> : null}
        {model.sections.packages ? <PackagesSection packages={model.packages} /> : null}
        {model.sections.gallery ? (
          <GallerySection photos={model.gallery} salonName={model.name} />
        ) : null}
        <BookingCta slug={slug} services={model.services} preselected={preselected} model={model} />
      </main>
      <SiteFooter model={model} />
    </>
  )
}
