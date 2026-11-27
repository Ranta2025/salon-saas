import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import type { LandingModel } from '@/lib/publicSalon'

type SiteHeaderProps = {
  model: LandingModel
}

/** Sticky public site header with a mobile menu; every link comes from the salon data. */
export function SiteHeader({ model }: SiteHeaderProps) {
  const [open, setOpen] = useState(false)

  // The mobile panel is a disclosure: close it with Escape.
  useEffect(() => {
    if (!open) {
      return
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open])

  return (
    <header className="site-header">
      <div className="container site-header__inner">
        <a className="brand" href="#inicio" aria-label={`${model.name}, ir al inicio`}>
          <span className="brand__name" title={model.name}>
            {model.name}
          </span>
        </a>

        <nav className="site-nav" aria-label="Secciones del sitio">
          <ul>
            {model.nav.map((link) => (
              <li key={link.href}>
                <a href={link.href}>{link.label}</a>
              </li>
            ))}
          </ul>
        </nav>

        <div className="site-header__actions">
          {model.phone !== null && model.phone.tel !== null ? (
            <a className="header-phone" href={model.phone.tel}>
              {model.phone.display}
            </a>
          ) : null}
          {model.canBook ? (
            <a className="btn btn--gold btn--sm" href="#reservar">
              Reservar
            </a>
          ) : null}
          <button
            type="button"
            className="menu-toggle"
            aria-expanded={open}
            aria-controls="mobile-nav"
            onClick={() => setOpen((value) => !value)}
          >
            <span className="visually-hidden">{open ? 'Cerrar menú' : 'Abrir menú'}</span>
            <span className="menu-toggle__bar" aria-hidden="true" data-open={open} />
            <span className="menu-toggle__bar" aria-hidden="true" data-open={open} />
            <span className="menu-toggle__bar" aria-hidden="true" data-open={open} />
          </button>
        </div>
      </div>

      <div id="mobile-nav" className="mobile-nav" hidden={!open}>
        <ul>
          {model.nav.map((link) => (
            <li key={link.href}>
              <a href={link.href} onClick={() => setOpen(false)}>
                {link.label}
              </a>
            </li>
          ))}
          {model.canBook ? (
            <li>
              <a href="#reservar" onClick={() => setOpen(false)}>
                Reservar mi cita
              </a>
            </li>
          ) : null}
          <li>
            <Link to="/login" onClick={() => setOpen(false)}>
              Acceso del equipo
            </Link>
          </li>
        </ul>
      </div>
    </header>
  )
}
