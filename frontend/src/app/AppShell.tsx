import { useEffect, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { fetchStaff } from '../lib/api'
import { canOpenMyAgenda } from '../lib/myAgenda'
import { canViewOwnCommissions } from '../lib/payroll'
import {
  activeRole,
  canViewInventory,
  canViewPackages,
  canViewSalonProfile,
  canViewWaitlist,
  isManager,
} from '../lib/permissions'
import type { StaffMember } from '../lib/staff'
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
  /** Only whoever attends clients in the active salon sees it (S-F2b-5). */
  agendaOnly?: boolean
  /**
   * Only owner, admin and professional see their own commissions (D7); reception
   * does not, and the page itself also guards the role.
   */
  commissionsOnly?: boolean
  /**
   * Inventory (contract F6, D7): owner, admin and reception read the stock; a
   * professional does not see the entry and gets `ManagerOnlyNotice` on the page.
   */
  inventoryOnly?: boolean
  /**
   * Waiting list (contract F7, D8): owner, admin and reception read the avisos;
   * a professional does not see the entry and gets the notice on the page.
   */
  waitlistOnly?: boolean
  /**
   * Prepaid packages (contract F8, D8/D10): owner, admin and reception read the
   * catalog; a professional does not see the entry and gets the notice on the page.
   */
  packagesOnly?: boolean
  /**
   * «Mi salón» (contract F16, D7): owner, admin and reception see the entry; a
   * professional does not and gets the notice on the page.
   */
  salonProfileOnly?: boolean
}

const NAV: readonly NavEntry[] = [
  { to: '/panel', label: 'Panel', end: true },
  { to: '/panel/mi-agenda', label: 'Mi agenda', end: false, agendaOnly: true },
  { to: '/panel/calendario', label: 'Calendario', end: false },
  { to: '/panel/pos', label: 'Ventas', end: false },
  { to: '/panel/servicios', label: 'Servicios', end: false },
  { to: '/panel/inventario', label: 'Inventario', end: false, inventoryOnly: true },
  // F8: short label on purpose (three columns at 390 px); the page is titled
  // «Paquetes prepagos».
  { to: '/panel/paquetes', label: 'Paquetes', end: false, packagesOnly: true },
  // The label is short on purpose: five columns at 375 px keep every entry in
  // two rows (S-F2e-5). The page itself is titled «Equipamiento del salón».
  { to: '/panel/equipamiento', label: 'Puestos', end: false, managerOnly: true },
  // F16: «Mi salón» is the profile and gallery of the public page. Owner/admin
  // edit it and reception reads it; a professional does not see the entry. The
  // label is short on purpose (three columns at 390 px); the page is titled
  // «Mi salón».
  { to: '/panel/mi-salon', label: 'Mi salón', end: false, salonProfileOnly: true },
  { to: '/panel/clientes', label: 'Clientes', end: false },
  { to: '/panel/avisos', label: 'Avisos', end: false },
  // F7: the notices with personal data are read by owner, admin and reception.
  // Short label on purpose: the three columns at 390 px keep every entry in two
  // rows without clipping (S-F2e-5); the page is titled «Lista de espera».
  { to: '/panel/lista-de-espera', label: 'Espera', end: false, waitlistOnly: true },
  { to: '/panel/equipo', label: 'Equipo', end: false, managerOnly: true },
  { to: '/panel/nomina', label: 'Nómina', end: false, managerOnly: true },
  // F3b: expenses and net profit are money, so only owner/admin see them.
  { to: '/panel/gastos', label: 'Gastos', end: false, managerOnly: true },
  { to: '/panel/ganancias', label: 'Ganancias', end: false, managerOnly: true },
  // F4: the CSV exports include money and personal data, so only owner/admin.
  { to: '/panel/exportar', label: 'Exportar', end: false, managerOnly: true },
  // Short label on purpose: the entries keep five columns without overflow at
  // 375/390 px (S-F2e-5, S-F3.2-5 and S-F3b). The page is titled «Mis comisiones».
  { to: '/panel/mis-comisiones', label: 'Comisiones', end: false, commissionsOnly: true },
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
  const { salonId, salons, user } = useSalon()
  const role = activeRole(salons, salonId)
  // The brand of the panel is the ACTIVE salon (F16: no salon name is hard-coded).
  const brandName = salons.find((salon) => salon.id === salonId)?.name ?? 'Salón'
  // «Mi agenda» is shown only to whoever appears in /staff (the bookable team of
  // F2a); the list is re-read on every salon switch, so the entry follows it.
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [staffLoaded, setStaffLoaded] = useState(false)

  useEffect(() => {
    if (!salonId) {
      setStaff([])
      setStaffLoaded(false)
      return
    }
    let active = true
    setStaff([])
    setStaffLoaded(false)
    fetchStaff(salonId)
      .then((list) => {
        if (active) {
          setStaff(list)
          setStaffLoaded(true)
        }
      })
      .catch(() => {
        // The fallback of canOpenMyAgenda keeps a professional's agenda reachable.
      })
    return () => {
      active = false
    }
  }, [salonId])

  const canOpenMine = canOpenMyAgenda({
    staff,
    staffLoaded,
    userId: user?.id ?? null,
    role,
  })
  // While `/me` is unknown (loading or failed) the entries stay visible; the page
  // behind them guards the role and explains a 403 instead of crashing.
  const showManagerEntries = isManager(role) || role === null
  const navItems = NAV.filter((item) => {
    if (item.managerOnly && !showManagerEntries) {
      return false
    }
    if (item.commissionsOnly && role !== null && !canViewOwnCommissions(role)) {
      return false
    }
    // While `/me` is unknown the entry stays visible (role === null) and the page
    // explains the 403 instead of hiding the section from a real reader.
    if (item.inventoryOnly && role !== null && !canViewInventory(role)) {
      return false
    }
    if (item.waitlistOnly && role !== null && !canViewWaitlist(role)) {
      return false
    }
    if (item.packagesOnly && role !== null && !canViewPackages(role)) {
      return false
    }
    if (item.salonProfileOnly && role !== null && !canViewSalonProfile(role)) {
      return false
    }
    return !item.agendaOnly || canOpenMine
  })

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
          <span className="shell__brand-name">{brandName}</span>
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
            <p className="shell__hint">Los montos se muestran en dólares estadounidenses (USD).</p>
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
