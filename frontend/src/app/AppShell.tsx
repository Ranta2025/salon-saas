import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { SALON } from '../landing/content'
import { clearToken } from '../lib/session'
import { SalonProvider, useSalon } from './SalonContext'

const NAV = [
  { to: '/panel', label: 'Panel', end: true },
  { to: '/panel/calendario', label: 'Calendario', end: false },
  { to: '/panel/pos', label: 'Ventas', end: false },
  { to: '/panel/servicios', label: 'Servicios', end: false },
] as const

function SalonSwitcher() {
  const { salonId, updateSalonId } = useSalon()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(salonId ?? '')

  if (!editing) {
    return (
      <button type="button" className="shell__salon" onClick={() => setEditing(true)}>
        <span className="shell__salon-label">Salón</span>
        <span className="shell__salon-value">{salonId ?? 'Sin definir'}</span>
      </button>
    )
  }

  return (
    <form
      className="shell__salon-form"
      onSubmit={(event) => {
        event.preventDefault()
        updateSalonId(draft)
        setEditing(false)
      }}
    >
      <label className="visually-hidden" htmlFor="salon-id">
        ID del salón
      </label>
      <input
        id="salon-id"
        className="input"
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        placeholder="ID del salón"
        autoFocus
      />
      <button type="submit" className="btn btn--wine btn--sm">
        Usar
      </button>
      <button
        type="button"
        className="btn btn--quiet btn--sm"
        onClick={() => {
          setDraft(salonId ?? '')
          setEditing(false)
        }}
      >
        Cancelar
      </button>
    </form>
  )
}

function ShellChrome() {
  const navigate = useNavigate()

  const handleLogout = () => {
    clearToken()
    navigate('/login', { replace: true })
  }

  return (
    <div className="shell">
      <a className="skip-link" href="#contenido-panel">
        Saltar al contenido
      </a>

      <aside className="shell__sidebar">
        <div className="shell__brand">
          <span className="shell__brand-name">{SALON.name}</span>
          <span className="shell__brand-kicker">Panel del salón</span>
        </div>

        <nav className="shell__nav" aria-label="Navegación del panel">
          <ul>
            {NAV.map((item) => (
              <li key={item.to}>
                <NavLink to={item.to} end={item.end}>
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>

        <div className="shell__sidebar-foot">
          <a href="/" className="shell__public-link">
            Ver sitio público ↗
          </a>
          <button type="button" className="btn btn--quiet btn--sm" onClick={handleLogout}>
            Cerrar sesión
          </button>
        </div>
      </aside>

      <div className="shell__main">
        <header className="shell__topbar">
          <SalonSwitcher />
          <p className="shell__hint">
            Los montos se muestran en pesos mexicanos (MXN), en centavos en la API.
          </p>
        </header>
        <main className="shell__content" id="contenido-panel">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function AppShell() {
  return (
    <SalonProvider>
      <ShellChrome />
    </SalonProvider>
  )
}
