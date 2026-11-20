import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { fetchPublicSalon } from '../lib/api'
import { toLandingServices } from '../lib/publicSalon'
import type { LandingService } from './content'
import { SERVICES, TRUST_POINTS } from './content'
import { BookingCta } from './components/BookingCta'
import { GallerySection } from './components/GallerySection'
import { Hero } from './components/Hero'
import { ServicesSection } from './components/ServicesSection'
import { SiteFooter } from './components/SiteFooter'
import { SiteHeader } from './components/SiteHeader'
import { TestimonialsSection } from './components/TestimonialsSection'
import { WhyUsSection } from './components/WhyUsSection'

/** Salon shown by the public page: the route slug, or the configured default. */
const DEFAULT_SLUG =
  (import.meta.env.VITE_SALON_SLUG as string | undefined)?.trim() || 'aurelia'

/** Public landing page, shared by `/` and `/salon/:slug`. */
export function LandingPage() {
  const { slug } = useParams<{ slug?: string }>()
  const [preselected, setPreselected] = useState<LandingService | null>(null)
  const [services, setServices] = useState<readonly LandingService[]>(SERVICES)

  useEffect(() => {
    let active = true
    // The bundled catalogue is the fallback for a slow or unavailable API.
    setServices(SERVICES)
    fetchPublicSalon(slug ?? DEFAULT_SLUG)
      .then((salon) => {
        if (active) {
          setServices(toLandingServices(salon.services, SERVICES))
        }
      })
      .catch(() => {
        // Keep the static catalogue: the landing never depends on the API.
      })
    return () => {
      active = false
    }
  }, [slug])

  return (
    <>
      <a className="skip-link" href="#contenido">
        Saltar al contenido
      </a>
      <SiteHeader />
      <main id="contenido">
        <Hero />
        <div className="trust-strip">
          <div className="container">
            <ul>
              {TRUST_POINTS.map((point) => (
                <li key={point}>{point}</li>
              ))}
            </ul>
          </div>
        </div>
        <ServicesSection services={services} onSelect={setPreselected} />
        <GallerySection />
        <WhyUsSection />
        <TestimonialsSection />
        <BookingCta services={services} preselected={preselected} />
      </main>
      <SiteFooter />
    </>
  )
}
