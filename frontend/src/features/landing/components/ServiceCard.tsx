import { formatCents, formatMinutes } from '@/lib/money'
import type { LandingService } from '@/lib/publicSalon'

type ServiceCardProps = {
  service: LandingService
  onSelect: (service: LandingService) => void
  /** Seña of the service (`deposit_cents`, D11); it is printed inside the card. */
  depositCents?: number | null
}

export function ServiceCard({ service, onSelect, depositCents = null }: ServiceCardProps) {
  return (
    <article className="service-card">
      <h3 className="service-card__name">{service.name}</h3>
      <p className="service-card__meta">
        <span>{formatMinutes(service.durationMin)}</span>
      </p>
      {service.description !== null ? (
        <p className="service-card__desc">{service.description}</p>
      ) : null}
      {depositCents !== null ? (
        <p className="service-card__deposit">Seña: {formatCents(depositCents)}</p>
      ) : null}
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
