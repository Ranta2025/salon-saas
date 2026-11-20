import { useEffect } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useNavigate } from 'react-router-dom'
import { AppShell } from './app/AppShell'
import { CalendarPage } from './app/CalendarPage'
import { ClientDetailPage } from './app/ClientDetailPage'
import { ClientsPage } from './app/ClientsPage'
import { DashboardPage } from './app/DashboardPage'
import { LoginPage } from './app/LoginPage'
import { NotificationsPage } from './app/NotificationsPage'
import { PosPage } from './app/PosPage'
import { RequireAuth } from './app/RequireAuth'
import { ServicesPage } from './app/ServicesPage'
import { LandingPage } from './landing/LandingPage'
import { UNAUTHORIZED_EVENT } from './lib/api'
import { purgeOfflineApiCache } from './lib/pwa'

/** A 401 anywhere in the app sends the user back to /login. */
function UnauthorizedRedirect() {
  const navigate = useNavigate()
  useEffect(() => {
    const onUnauthorized = () => {
      // Another account must never read cached salon responses of this session.
      purgeOfflineApiCache()
      navigate('/login', { replace: true })
    }
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [navigate])
  return null
}

function NotFound() {
  return (
    <main className="not-found">
      <p className="eyebrow">404</p>
      <h1>No encontramos esta página</h1>
      <p className="lede">Puede que el enlace haya cambiado. Vuelve al inicio o entra al panel.</p>
      <p className="not-found__actions">
        <a className="btn btn--wine" href="/">
          Ir al sitio
        </a>
        <a className="btn btn--quiet" href="/panel">
          Ir al panel
        </a>
      </p>
    </main>
  )
}

export function App() {
  return (
    <BrowserRouter>
      <UnauthorizedRedirect />
      <Routes>
        <Route path="/" element={<LandingPage />} />
        <Route path="/salon/:slug" element={<LandingPage />} />
        <Route path="/login" element={<LoginPage />} />

        <Route
          path="/panel"
          element={
            <RequireAuth>
              <AppShell />
            </RequireAuth>
          }
        >
          <Route index element={<DashboardPage />} />
          <Route path="calendario" element={<CalendarPage />} />
          <Route path="pos" element={<PosPage />} />
          <Route path="servicios" element={<ServicesPage />} />
          <Route path="clientes" element={<ClientsPage />} />
          <Route path="clientes/:clientId" element={<ClientDetailPage />} />
          <Route path="avisos" element={<NotificationsPage />} />
        </Route>

        {/* Old shell routes from the scaffold keep working. */}
        <Route path="/dashboard" element={<Navigate to="/panel" replace />} />
        <Route path="/calendar" element={<Navigate to="/panel/calendario" replace />} />
        <Route path="/pos" element={<Navigate to="/panel/pos" replace />} />
        <Route path="/clients" element={<Navigate to="/panel/clientes" replace />} />

        <Route path="*" element={<NotFound />} />
      </Routes>
    </BrowserRouter>
  )
}
