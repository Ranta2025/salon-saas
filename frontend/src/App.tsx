import { useEffect } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { ActivityPage } from '@/features/audit'
import { AppShell } from './app/AppShell'
import { BankPage } from '@/features/bookings'
import { BookingLinkPage } from '@/features/bookings'
import { CalendarPage } from '@/features/bookings'
import { ClientDetailPage } from '@/features/clients'
import { ClientsPage } from '@/features/clients'
import { DepositsPage } from '@/features/bookings'
import { EquipmentPage } from '@/features/services'
import { ExpensesPage } from '@/features/expenses'
import { ExportsPage } from '@/features/exports'
import { ForgotPasswordPage } from '@/features/auth'
import { InventoryPage } from '@/features/products'
import { InvitationPage } from '@/features/auth'
import { LoginPage } from '@/features/auth'
import { LoyaltyPage } from '@/features/loyalty'
import { MyAgendaPage } from '@/features/bookings'
import { MyCommissionsPage } from '@/features/team'
import { NotificationsPage } from '@/features/notifications'
import { PackagesPage } from '@/features/packages'
import { PanelHome } from '@/features/dashboard'
import { PaymentResultPage } from '@/features/bookings'
import { PayrollPage } from '@/features/team'
import { PayrollRunDetailPage } from '@/features/team'
import { PosPage } from '@/features/pos'
import { ProfitPage } from '@/features/expenses'
import { RequireAuth } from './app/RequireAuth'
import { ResetPasswordPage } from '@/features/auth'
import { ReviewPage, ReviewsPage } from '@/features/reviews'
import { SalonProfilePage } from '@/features/settings'
import { SecurityPage } from '@/features/auth'
import { ServicesPage } from '@/features/services'
import { SettingsPage } from '@/features/settings'
import { TeamPage } from '@/features/team'
import { VerifyEmailPage } from '@/features/auth'
import { WaitlistLeavePage } from '@/features/waitlist'
import { WaitlistPage } from '@/features/waitlist'
import { LandingPage } from '@/features/landing'
import { UNAUTHORIZED_EVENT } from './lib/api'
import { purgeOfflineApiCache } from './lib/pwa'
import { isRecoveryPath } from '@/features/auth/lib/recovery'
import { isPaymentPath } from '@/features/bookings/lib/simbank'

/** A 401 anywhere in the app sends the user back to /login. */
function UnauthorizedRedirect() {
  const navigate = useNavigate()
  const { pathname } = useLocation()
  useEffect(() => {
    const onUnauthorized = () => {
      // The invitation page logs in with its own form: a wrong password must not
      // navigate away and lose the token it keeps only in memory (S-F2g1-6).
      if (pathname === '/invitacion') {
        return
      }
      // Recovery pages are public (F15, D11): they have no session to lose.
      if (isRecoveryPath(pathname)) {
        return
      }
      // The two pages of the simulated bank are public too (F11, D13-c): a 401 of
      // the payment must never wipe what the person is reading and send them away.
      if (isPaymentPath(pathname)) {
        return
      }
      // Another account must never read cached salon responses of this session.
      purgeOfflineApiCache()
      navigate('/login', { replace: true })
    }
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [navigate, pathname])
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
        {/* Public, no session: the review link of an attended turno (F13). The token
            travels in the FRAGMENT, like the other pages with a token. */}
        <Route path="/resena" element={<ReviewPage />} />
        {/* Public, no session: the token of the link lives in the fragment. */}
        <Route path="/invitacion" element={<InvitationPage />} />
        {/* Public, no session: password recovery and email verification (F15). */}
        <Route path="/recuperar" element={<ForgotPasswordPage />} />
        <Route path="/restablecer" element={<ResetPasswordPage />} />
        <Route path="/verificar-email" element={<VerifyEmailPage />} />
        {/* Public, no session, no menu entry: single-use booking link (F1). */}
        <Route path="/turno" element={<BookingLinkPage />} />
        {/* Public, no session: the baja link of the waiting list (F7, D5). */}
        <Route path="/lista-de-espera" element={<WaitlistLeavePage />} />
        {/* Public, no session: the simulated bank and the state of the payment
            (F11, D13-c). The charge id travels in the fragment of both. */}
        <Route path="/pagar" element={<BankPage />} />
        <Route path="/pago-resultado" element={<PaymentResultPage />} />

        <Route
          path="/panel"
          element={
            <RequireAuth>
              <AppShell />
            </RequireAuth>
          }
        >
          <Route index element={<PanelHome />} />
          <Route path="mi-agenda" element={<MyAgendaPage />} />
          <Route path="calendario" element={<CalendarPage />} />
          <Route path="pos" element={<PosPage />} />
          <Route path="servicios" element={<ServicesPage />} />
          <Route path="inventario" element={<InventoryPage />} />
          <Route path="paquetes" element={<PackagesPage />} />
          <Route path="fidelidad" element={<LoyaltyPage />} />
          <Route path="senas" element={<DepositsPage />} />
          <Route path="clientes" element={<ClientsPage />} />
          <Route path="clientes/:clientId" element={<ClientDetailPage />} />
          <Route path="avisos" element={<NotificationsPage />} />
          <Route path="equipo" element={<TeamPage />} />
          <Route path="equipamiento" element={<EquipmentPage />} />
          <Route path="nomina" element={<PayrollPage />} />
          <Route path="gastos" element={<ExpensesPage />} />
          <Route path="ganancias" element={<ProfitPage />} />
          <Route path="exportar" element={<ExportsPage />} />
          <Route path="nomina/liquidaciones/:rid" element={<PayrollRunDetailPage />} />
          <Route path="mis-comisiones" element={<MyCommissionsPage />} />
          <Route path="lista-de-espera" element={<WaitlistPage />} />
          <Route path="mi-salon" element={<SalonProfilePage />} />
          <Route path="resenas" element={<ReviewsPage />} />
          <Route path="ajustes" element={<SettingsPage />} />
          <Route path="actividad" element={<ActivityPage />} />
          <Route path="seguridad" element={<SecurityPage />} />
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
