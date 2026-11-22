import { useCallback, useEffect, useState } from 'react'
import { describeApiError, fetchDashboard, type ApiErrorNotice } from '../lib/api'
import { formatOccupancy, type DashboardMetrics } from '../lib/metrics'
import { formatCents } from '../lib/money'
import { useRealtime } from './RealtimeContext'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { KPICard } from './components/KPICard'
import { PageHeader } from './components/PageHeader'

export function DashboardPage() {
  const { salonId } = useSalon()
  const { subscribe } = useRealtime()
  const [data, setData] = useState<DashboardMetrics | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // Live events of the salon keep the numbers current without a manual refresh.
  useEffect(() => {
    const offBooking = subscribe('booking.created', () => reload())
    const offSale = subscribe('sale.created', () => reload())
    return () => {
      offBooking()
      offSale()
    }
  }, [subscribe, reload])

  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchDashboard(salonId)
      .then((metrics) => {
        if (active) {
          // Keep the previous numbers on screen while refreshing.
          setData(metrics)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar el panel.'))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Panel" title="Resumen del día" />
        <SalonRequired />
      </>
    )
  }

  const servicesSold = data?.top_services.reduce((total, row) => total + row.count, 0) ?? 0
  const maxCount = data?.top_services.reduce((max, row) => Math.max(max, row.count), 0) ?? 0

  return (
    <>
      <PageHeader
        eyebrow="Panel"
        title="Resumen del día"
        description="Cifras calculadas por el servidor para el salón activo; se actualizan al recargar."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {loading && !data ? <LoadingState label="Cargando indicadores…" /> : null}
      {loading && data ? <LoadingState label="Actualizando indicadores…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {data ? (
        <>
          <div className="kpi-grid">
            <KPICard
              label="Ingresos de hoy"
              value={formatCents(data.revenue_today)}
              hint="Ventas registradas en el POS"
              accent="wine"
            />
            <KPICard
              label="Citas de hoy"
              value={String(data.appointments_today)}
              hint="Reservas que bloquean agenda"
              accent="gold"
            />
            <KPICard
              label="Ocupación"
              value={formatOccupancy(data.occupancy_rate)}
              hint="Sobre la jornada de referencia"
              accent="plum"
            />
            <KPICard
              label="Servicios vendidos"
              value={String(servicesSold)}
              hint="Suma del ranking de hoy"
              accent="wine"
            />
          </div>

          <section className="panel-card" aria-labelledby="top-services-title">
            <div className="panel-card__head">
              <h2 id="top-services-title">Servicios más vendidos hoy</h2>
              <span className="panel-card__meta">Montos en pesos</span>
            </div>
            {data.top_services.length === 0 ? (
              <p className="muted">
                Todavía no hay ventas registradas hoy. Registra una en el punto de venta.
              </p>
            ) : (
              <ul className="rank">
                {data.top_services.map((row) => (
                  <li key={row.name} className="rank__row">
                    <div className="rank__label">
                      <span>{row.name}</span>
                      <span className="muted">{row.count} vendidos</span>
                    </div>
                    <div className="rank__bar" aria-hidden="true">
                      <span
                        style={{ width: `${maxCount > 0 ? (row.count / maxCount) * 100 : 0}%` }}
                      />
                    </div>
                    <span className="rank__amount">
                      {row.revenue_cents === null ? '—' : formatCents(row.revenue_cents)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      ) : null}
    </>
  )
}
