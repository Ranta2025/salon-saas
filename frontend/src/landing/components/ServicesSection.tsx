import type { LandingService } from '../../lib/publicSalon'
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
            <ServiceCard key={service.id} service={service} onSelect={onSelect} />
          ))}
        </div>

        {whatsapp !== null ? (
          <p className="services__note">
            ¿Buscas un servicio que no aparece?{' '}
            <a href={whatsapp} target="_blank" rel="noreferrer">
              Escríbenos por WhatsApp
            </a>{' '}
            y te decimos si lo podemos hacer.
          </p>
        ) : null}
      </div>
    </section>
  )
}
