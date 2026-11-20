import { PHOTOS, SALON, STATS } from '../content'

/** Hero: headline, booking CTA and the main salon photo with a colour overlay. */
export function Hero() {
  return (
    <section className="hero on-dark" id="inicio" aria-labelledby="hero-title">
      <img
        className="hero__photo"
        src={PHOTOS.hero.src}
        alt={PHOTOS.hero.alt}
        width={PHOTOS.hero.width}
        height={PHOTOS.hero.height}
        loading="eager"
        fetchPriority="high"
        decoding="async"
      />
      <div className="hero__scrim" aria-hidden="true" />
      <div className="container hero__inner">
        <p className="eyebrow">
          {SALON.tagline} · {SALON.district}
        </p>
        <h1 id="hero-title">Cabello que se siente tan bien como se ve</h1>
        <p className="hero__lede">
          Coloración, keratina y styling en Roma Norte. Empezamos con un diagnóstico honesto de tu
          cabello y terminamos con un look que puedes repetir en casa.
        </p>
        <div className="hero__actions">
          <a className="btn btn--gold" href="#reservar">
            Reservar mi cita
          </a>
          <a className="btn btn--ghost" href="#servicios">
            Ver servicios y precios
          </a>
        </div>
        <dl className="hero__stats">
          {STATS.map((stat) => (
            <div key={stat.label}>
              <dt>{stat.label}</dt>
              <dd>{stat.value}</dd>
            </div>
          ))}
        </dl>
      </div>
    </section>
  )
}
