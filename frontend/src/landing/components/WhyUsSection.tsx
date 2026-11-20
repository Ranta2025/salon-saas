import { PHOTOS, REASONS } from '../content'

export function WhyUsSection() {
  return (
    <section className="section why" id="nosotros" aria-labelledby="why-title">
      <div className="container why__grid">
        <figure className="why__media">
          <img
            src={PHOTOS.interior.src}
            alt={PHOTOS.interior.alt}
            width={PHOTOS.interior.width}
            height={PHOTOS.interior.height}
            loading="lazy"
            decoding="async"
          />
          <figcaption>
            {PHOTOS.interior.caption} · {PHOTOS.interior.author} ({PHOTOS.interior.license})
          </figcaption>
        </figure>

        <div className="why__body">
          <p className="eyebrow">Por qué elegirnos</p>
          <h2 id="why-title">Un salón que te explica lo que hace</h2>
          <p className="lede">
            Trabajamos con cita, sin sobreventa de horarios. Cada servicio empieza escuchando qué
            quieres y qué ha pasado con tu cabello en los últimos meses.
          </p>
          <ol className="why__list">
            {REASONS.map((reason, index) => (
              <li key={reason.title} className="why__item">
                <span className="why__num" aria-hidden="true">
                  {String(index + 1).padStart(2, '0')}
                </span>
                <div>
                  <h3>{reason.title}</h3>
                  <p>{reason.body}</p>
                </div>
              </li>
            ))}
          </ol>
        </div>
      </div>
    </section>
  )
}
