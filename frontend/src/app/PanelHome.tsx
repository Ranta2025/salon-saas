import { Navigate } from 'react-router-dom'
import { activeRole, panelHomePath } from '../lib/permissions'
import { DashboardPage } from './DashboardPage'
import { LoadingState } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { useSalon } from './SalonContext'

/**
 * Index of `/panel`. A professional has no dashboard — the API answers 403 — so
 * the index sends them to the agenda. While `/me` is loading the dashboard is
 * not mounted: asking for it first would only produce a permission error.
 */
export function PanelHome() {
  const { salonId, salons, loading } = useSalon()
  const home = panelHomePath(activeRole(salons, salonId))
  if (home) {
    return <Navigate to={home} replace />
  }
  if (loading) {
    return (
      <>
        <PageHeader eyebrow="Panel" title="Resumen del día" />
        <LoadingState label="Cargando tu acceso…" />
      </>
    )
  }
  return <DashboardPage />
}
