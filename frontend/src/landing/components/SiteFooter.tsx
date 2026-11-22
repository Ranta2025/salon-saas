import { Link } from 'react-router-dom'
import { ATTRIBUTION, NAV_LINKS, PUBLIC_HOURS_NOTE, SALON } from '../content'

/** Footer with salon contact details and the licence attribution of every photo. */
export function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="container site-footer__grid">
        <div className="site-footer__brand">
          <p className="site-footer__logo">
            {SALON.name} <span aria-hidden="true">·</span> {SALON.tagline}
          </p>
          <address className="site-footer__address">
            {SALON.address}
            <br />
            <a href={SALON.phoneHref}>{SALON.phone}</a>
            <br />
            <a href={`mailto:${SALON.email}`}>{SALON.email}</a>
          </address>
          <ul className="site-footer__hours">
            {SALON.hours.map((slot) => (
              <li key={slot.days}>
                <span>{slot.days}</span>
                <span>{slot.time}</span>
              </li>
            ))}
          </ul>
          <p className="site-footer__hours-note">
            {PUBLIC_HOURS_NOTE.charAt(0).toUpperCase() + PUBLIC_HOURS_NOTE.slice(1)}: confirma tu
            horario al reservar.
          </p>
        </div>

        <nav className="site-footer__nav" aria-label="Enlaces del pie de página">
          <h2 className="site-footer__title">Explorar</h2>
          <ul>
            {NAV_LINKS.map((link) => (
              <li key={link.href}>
                <a href={link.href}>{link.label}</a>
              </li>
            ))}
            <li>
              <a href="#reservar">Reservar cita</a>
            </li>
            <li>
              <Link to="/login">Acceso del equipo</Link>
            </li>
          </ul>
        </nav>

        <section className="site-footer__credits" aria-labelledby="credits-title">
          <h2 className="site-footer__title" id="credits-title">
            Créditos de fotografía
          </h2>
          <p className="site-footer__credits-note">
            Fotografías de Wikimedia Commons con licencias libres para uso comercial. Se muestran
            tal cual, sin personas del salón.
          </p>
          <ul className="attribution">
            {ATTRIBUTION.map((photo) => (
              <li key={photo.src}>
                <a href={photo.commons} target="_blank" rel="noreferrer">
                  {photo.title}
                </a>{' '}
                — {photo.author}, {photo.license}
              </li>
            ))}
          </ul>
        </section>
      </div>

      <div className="container site-footer__bottom">
        <p>
          © {new Date().getFullYear()} {SALON.name} Estudio de belleza. Todos los derechos
          reservados.
        </p>
        <p>
          Hecho con cariño en {SALON.district} · <a href="#inicio">Volver arriba</a>
        </p>
      </div>
    </footer>
  )
}
