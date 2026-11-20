import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { SALON } from '../landing/content'
import { activeRole, isManager } from '../lib/permissions'
import { purgeOfflineApiCache } from '../lib/pwa'
import { clearSalonId, clearToken, clearUserId } from '../lib/session'
import { NotificationCenter } from './components/NotificationCenter'
import { OfflineBanner, SyncStatusChip } from './components/SyncStatus'
import { NotificationsProvider } from './NotificationsContext'
import { OfflineProvider, offlineQueue } from './OfflineContext'
import { RealtimeProvider } from './RealtimeContext'
import { SalonProvider, useSalon } from './SalonContext'

type NavEntry = {
  to: string
  label: string
  end: boolean
  /** Only owner/admin see it; the page itself also guards the role. */
  managerOnly?: boolean
}

const NAV: readonly NavEntry[] = [
  { to: '/panel', label: 'Panel', end: true },
  { to: '/panel/calendario', label: 'Calendario', end: false },
  { to: '/panel/pos', label: 'Ventas', end: false },
  { to: '/panel/servicios', label: 'Servicios', end: false },
  { to: '/panel/clientes', label: 'Clientes', end: false },
  { to: '/panel/avisos', label: 'Avisos', end: false },
  { to: '/panel/equipo', label: 'Equipo', end: false, managerOnly: true },
  { to: '/panel/ajustes', label: 'Ajustes', end: false, managerOnly: true },
]

function SalonSwitcher() {
  const { salonId, salons, loading, error, updateSalonId, refresh } = useSalon()

  if (loading) {
    return (
      <span className="shell__salon shell__salon--static" role="status">
        Cargando salón…
      </span>
    )
  }

  if (salons.length > 1) {
    return (
      <label className="shell__salon shell__salon--select">
        <span className="shell__salon-label">Salón</span>
        <select
          className="select shell__salon-select"
          value={salonId ?? ''}
          onChange={(event) => updateSalonId(event.target.value)}
          aria-label="Salón activo"
        >
          {salonId === null ? <option value="">Elige un salón</option> : null}
          {salons.map((salon) => (
            <option key={salon.id} value={salon.id}>
              {salon.name}
            </option>
          ))}
        </select>
      </label>
    )
  }

  const active = salons.find((salon) => salon.id === salonId) ?? salons[0]
  if (active) {
    return (
      <span className="shell__salon shell__salon--static">
        <span className="shell__salon-label">Salón</span>
        <span className="shell__salon-value">{active.name}</span>
      </span>
    )
  }

  return (
    <button
      type="button"
      className="shell__salon shell__salon--static"
      onClick={refresh}
      title={error ?? undefined}
    >
      Sin salón asignado · Reintentar
    </button>
  )
}

function ShellChrome() {
  const navigate = useNavigate()
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  // While `/me` is unknown (loading or failed) the entries stay visible; the page
  // behind them guards the role and explains a 403 instead of crashing.
  const showManagerEntries = isManager(role) || role === null
  const navItems = NAV.filter((item) => !item.managerOnly || showManagerEntries)

  const handleLogout = () => {
    clearToken()
    clearUserId()
    clearSalonId()
    // The queue of this account is never synced by the next one that signs in.
    offlineQueue.clear()
    offlineQueue.setScope(null)
    offlineQueue.clear()
    purgeOfflineApiCache()
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
            {navItems.map((item) => (
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
          <div className="shell__topbar-main">
            <SalonSwitcher />
            <p className="shell__hint">Los montos se muestran en pesos mexicanos (MXN).</p>
          </div>
          <div className="shell__topbar-actions">
            <SyncStatusChip />
            <NotificationCenter />
          </div>
        </header>
        <OfflineBanner />
        <main className="shell__content" id="contenido-panel">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

/**
 * Providers of the internal app: the active salon, the notification stream and
 * the offline queue/connectivity state. Order matters: notifications need the
 * salon id, the offline queue is independent.
 */
export function AppShell() {
  return (
    <SalonProvider>
      <RealtimeProvider>
        <NotificationsProvider>
          <OfflineProvider>
            <ShellChrome />
          </OfflineProvider>
        </NotificationsProvider>
      </RealtimeProvider>
    </SalonProvider>
  )
}
