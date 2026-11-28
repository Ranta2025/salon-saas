import type { LandingService } from '@/lib/publicSalon'
import { ServiceCard } from './ServiceCard'

type ServicesSectionProps = {
  services: readonly LandingService[]
  /** `wa.me` link of the salon phone; the note below only shows when there is one. */
  whatsapp: string | null
  onSelect: (service: LandingService) => void
}

/** Rendered only when the salon has at least one service. */
export function ServicesSection({ services, whatsapp, onSelect }: ServicesSectionProps) {
  return (
    <section className="section services" id="servicios" aria-labelledby="services-title">
      <div className="container">
        <div className="section-head">
          <p className="eyebrow">Servicios y precios</p>
          <h2 id="services-title">Servicios</h2>
          <p className="lede">Precios finales en dólares estadounidenses.</p>
        </div>

        <div className="services__grid">
          {services.map((service) => (
            <div key={service.id} className="services__item">
              {/* F11 (D11): the amount is the `deposit_cents` of the server, never a
                  percentage recomputed in the browser. */}
              <ServiceCard
                service={service}
                depositCents={service.depositCents}
                onSelect={onSelect}
              />
            </div>
          ))}
        </div>

        {whatsapp !== null ? (
          <p className="services__note">
            ¿Buscás un servicio que no aparece?{' '}
            <a href={whatsapp} target="_blank" rel="noreferrer">
              Escribinos por WhatsApp
            </a>{' '}
            y te decimos si lo podemos hacer.
          </p>
        ) : null}
      </div>
    </section>
  )
}
