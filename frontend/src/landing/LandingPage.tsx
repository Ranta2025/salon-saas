import { useState } from 'react'
import type { LandingService } from './content'
import { TRUST_POINTS } from './content'
import { BookingCta } from './components/BookingCta'
import { GallerySection } from './components/GallerySection'
import { Hero } from './components/Hero'
import { ServicesSection } from './components/ServicesSection'
import { SiteFooter } from './components/SiteFooter'
import { SiteHeader } from './components/SiteHeader'
import { TestimonialsSection } from './components/TestimonialsSection'
import { WhyUsSection } from './components/WhyUsSection'

/** Public landing page, shared by `/` and `/salon/:slug`. */
export function LandingPage() {
  const [preselected, setPreselected] = useState<LandingService | null>(null)

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
        <ServicesSection onSelect={setPreselected} />
        <GallerySection />
        <WhyUsSection />
        <TestimonialsSection />
        <BookingCta preselected={preselected} />
      </main>
      <SiteFooter />
    </>
  )
}
