import type { LandingModel } from '../../lib/publicSalon'

type HeroProps = {
  model: LandingModel
}

/**
 * Hero: the salon name, its description and, when the gallery has photos, the
 * first one behind a colour overlay. Nothing here is written by hand for a salon.
 */
export function Hero({ model }: HeroProps) {
  const photo = model.heroPhoto
  return (
    <section className="hero on-dark" id="inicio" aria-labelledby="hero-title">
      {photo ? (
        <img
          className="hero__photo"
          src={photo.url}
          alt={photo.caption !== '' ? photo.caption : `Foto de ${model.name}`}
          loading="eager"
          fetchPriority="high"
          decoding="async"
        />
      ) : null}
      <div className="hero__scrim" aria-hidden="true" />
      <div className="container hero__inner">
        <p className="eyebrow">Reservá tu cita en línea</p>
        <h1 id="hero-title">{model.name}</h1>
        {model.description !== null ? <p className="hero__lede">{model.description}</p> : null}
        <div className="hero__actions">
          {model.canBook ? (
            <a className="btn btn--gold" href="#reservar">
              Reservar mi cita
            </a>
          ) : null}
          {model.sections.services ? (
            <a className="btn btn--ghost" href="#servicios">
              Ver servicios y precios
            </a>
          ) : null}
        </div>
      </div>
    </section>
  )
}
