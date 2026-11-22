import type { LandingService } from '../content'
import { ServiceCard } from './ServiceCard'

type ServicesSectionProps = {
  services: readonly LandingService[]
  onSelect: (service: LandingService) => void
}

export function ServicesSection({ services, onSelect }: ServicesSectionProps) {
  return (
    <section className="section services" id="servicios" aria-labelledby="services-title">
      <div className="container">
        <div className="section-head">
          <p className="eyebrow">Servicios y precios</p>
          <h2 id="services-title">Todo lo que tu cabello necesita, en una sola visita</h2>
          <p className="lede">
            Precios finales en dólares estadounidenses; el largo y el volumen del cabello pueden
            ajustar el costo de coloración y alisados. Te confirmamos el total antes de empezar.
          </p>
        </div>

        <div className="services__grid">
          {services.map((service) => (
            <ServiceCard key={service.id} service={service} onSelect={onSelect} />
          ))}
        </div>

        <p className="services__note">
          ¿Buscas un servicio que no aparece? Escríbenos y te decimos si lo podemos hacer y cuánto
          cuesta.
        </p>
      </div>
    </section>
  )
}
