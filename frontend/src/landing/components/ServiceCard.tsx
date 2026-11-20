import { formatCents, formatMinutes } from '../../lib/money'
import type { LandingService } from '../content'

type ServiceCardProps = {
  service: LandingService
  onSelect: (service: LandingService) => void
}

export function ServiceCard({ service, onSelect }: ServiceCardProps) {
  return (
    <article className="service-card">
      <h3 className="service-card__name">{service.name}</h3>
      <p className="service-card__meta">
        <span>{formatMinutes(service.durationMin)}</span>
        {service.tag ? <span className="tag">{service.tag}</span> : null}
      </p>
      <p className="service-card__desc">{service.description}</p>
      <div className="service-card__foot">
        <p className="service-card__price">
          <span className="visually-hidden">Precio: </span>
          {formatCents(service.priceCents)}
        </p>
        <a className="service-card__link" href="#reservar" onClick={() => onSelect(service)}>
          Reservar<span aria-hidden="true"> →</span>
        </a>
      </div>
    </article>
  )
}
