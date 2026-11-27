import { Link } from 'react-router-dom'
import { PUBLIC_HOURS_FALLBACK } from '../content'
import type { LandingModel } from '../../lib/publicSalon'
import { visitorTimeZone } from '@/features/bookings/lib/publicBooking'
import { publicHoursRows, todayWeekday } from '@/features/team/lib/weeklyHours'

type SiteFooterProps = {
  model: LandingModel
}

/** Footer with the contact data the salon filled in; an empty field prints nothing. */
export function SiteFooter({ model }: SiteFooterProps) {
  // The salon zone (D4 sends it) decides which day is «hoy»; an old payload without
  // it falls back to the visitor's zone.
  const today = todayWeekday(new Date(), model.timezone ?? visitorTimeZone())
  const rows = model.hours === null ? [] : publicHoursRows(model.hours, today)
  const hasContact =
    model.address !== null ||
    model.phone !== null ||
    model.email !== null ||
    model.instagram !== null

  return (
    <footer className="site-footer">
      <div className="container site-footer__grid">
        <div className="site-footer__brand">
          <p className="site-footer__logo">{model.name}</p>
          {hasContact ? (
            <address className="site-footer__address">
              {model.address !== null ? <span>{model.address}</span> : null}
              {model.phone !== null ? (
                <span>
                  {model.phone.tel !== null ? (
                    <a href={model.phone.tel}>{model.phone.display}</a>
                  ) : (
                    model.phone.display
                  )}
                </span>
              ) : null}
              {model.email !== null ? (
                <span>
                  <a href={`mailto:${model.email}`}>{model.email}</a>
                </span>
              ) : null}
              {model.instagram !== null ? (
                <span>
                  <a href={model.instagram.url} target="_blank" rel="noreferrer">
                    Instagram: @{model.instagram.handle}
                  </a>
                </span>
              ) : null}
            </address>
          ) : null}
          {model.hours === null ? (
            model.phone?.whatsapp ? (
              <p className="site-footer__hours-note">
                {PUBLIC_HOURS_FALLBACK}:{' '}
                <a href={model.phone.whatsapp} target="_blank" rel="noreferrer">
                  {model.phone.display}
                </a>
              </p>
            ) : null
          ) : (
            <ul className="site-footer__hours" aria-label="Horario de atención">
              {rows.map((row) => (
                <li
                  key={row.weekday}
                  data-today={row.isToday ? 'true' : undefined}
                  data-closed={row.closed ? 'true' : undefined}
                  aria-current={row.isToday ? 'date' : undefined}
                >
                  <span>
                    {row.label}
                    {row.isToday ? <span className="site-footer__hours-today">Hoy</span> : null}
                  </span>
                  <span>{row.time}</span>
                </li>
              ))}
            </ul>
          )}
        </div>

        <nav className="site-footer__nav" aria-label="Enlaces del pie de página">
          <h2 className="site-footer__title">Explorar</h2>
          <ul>
            {model.nav.map((link) => (
              <li key={link.href}>
                <a href={link.href}>{link.label}</a>
              </li>
            ))}
            {model.canBook ? (
              <li>
                <a href="#reservar">Reservar cita</a>
              </li>
            ) : null}
            <li>
              <Link to="/login">Acceso del equipo</Link>
            </li>
          </ul>
        </nav>
      </div>

      <div className="container site-footer__bottom">
        <p>
          © {new Date().getFullYear()} {model.name}. Todos los derechos reservados.
        </p>
        <p>
          <a href="#inicio">Volver arriba</a>
        </p>
      </div>
    </footer>
  )
}
