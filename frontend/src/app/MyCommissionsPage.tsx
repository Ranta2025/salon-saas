import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '../lib/api'
import { toDateInputValue } from '../lib/datetime'
import { formatCents } from '../lib/money'
import { subscribeOfflineData } from '../lib/offlineData'
import { activeRole, roleLabel } from '../lib/permissions'
import {
  canViewOwnCommissions,
  checkPayrollInput,
  ESTIMATED_BADGE,
  ESTIMATED_NOTICE,
  fetchMyCommissions,
  OPEN_PERIOD_NOTICE,
  payPeriodLabel,
  PAY_PERIOD_KINDS,
  PAY_PERIOD_KIND_LABELS,
  type MyCommissions,
  type PayPeriodKind,
} from '../lib/payroll'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { KPICard } from './components/KPICard'
import { PageHeader } from './components/PageHeader'

/**
 * `/panel/mis-comisiones` (S-F3.2-5, D7): the caller's own figures of a period,
 * read from `GET /salons/:id/payroll/me`. It never asks for, renders or links the
 * figures of anybody else, nor anybody's model of payment. Reception sees the
 * role explanation instead of a page it cannot use.
 */
export function MyCommissionsPage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canViewOwnCommissions(role)

  const [kind, setKind] = useState<PayPeriodKind>('week')
  const [date, setDate] = useState(() => toDateInputValue(new Date()))
  const [mine, setMine] = useState<MyCommissions | null>(null)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [savedData, setSavedData] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  // The worker flags a cached answer before the fetch resolves (E-10 rule).
  const cachedAnswer = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(
    () =>
      subscribeOfflineData((path) => {
        if (path.includes('/payroll/me')) {
          cachedAnswer.current = true
        }
      }),
    [],
  )

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    const check = checkPayrollInput(kind, date)
    if (!check.ok) {
      setMine(null)
      setLoaded(false)
      setSavedData(false)
      setError({ message: check.message, retryable: false })
      return
    }
    let active = true
    cachedAnswer.current = false
    // Same rule as the payroll page: another period is not the previous figures.
    setMine(null)
    setLoaded(false)
    setLoading(true)
    setSavedData(false)
    setError(null)
    fetchMyCommissions(salonId, { kind: check.kind, date: check.date })
      .then((result) => {
        if (active) {
          setMine(result)
          setLoaded(true)
          setSavedData(cachedAnswer.current)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar tus comisiones del período.'))
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
  }, [salonId, allowed, kind, date, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Mis comisiones" title="Mis comisiones" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Mis comisiones" title="Mis comisiones" />
        <div className="empty-state">
          <h2>Tus comisiones no están disponibles para tu rol</h2>
          <p>
            Tu rol ({roleLabel(role)}) no puede ver comisiones. Solo la dueña o el dueño, la
            administración y cada profesional con sus propias cifras tienen acceso.
          </p>
        </div>
      </>
    )
  }

  const period = mine?.period ?? null

  return (
    <>
      <PageHeader
        eyebrow="Mis comisiones"
        title="Mis comisiones"
        description="Solo tus cifras del período. El servidor calcula la comisión con el porcentaje de cada venta; las ventas anteriores a la nómina se estiman con el porcentaje actual del servicio."
        actions={
          <button type="button" className="btn btn--quiet" onClick={reload}>
            Actualizar
          </button>
        }
      />

      <div className="panel-card payroll-filters">
        <div className="filters">
          <div className="field">
            <label htmlFor="my-commissions-kind">Período</label>
            <select
              id="my-commissions-kind"
              className="select"
              value={kind}
              onChange={(event) => setKind(event.target.value as PayPeriodKind)}
            >
              {PAY_PERIOD_KINDS.map((option) => (
                <option key={option} value={option}>
                  {PAY_PERIOD_KIND_LABELS[option]}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="my-commissions-date">Fecha dentro del período</label>
            <input
              id="my-commissions-date"
              className="input"
              type="date"
              value={date}
              onChange={(event) => setDate(event.target.value)}
            />
            <p className="field__hint">Cualquier día del período: el servidor lo deriva.</p>
          </div>
        </div>
        {period ? (
          <p className="payroll-filters__period" role="status">
            {payPeriodLabel(period)}
          </p>
        ) : null}
      </div>

      {savedData ? (
        <p className="notice notice--info" role="status">
          Sin conexión: mostrando datos guardados. Tus comisiones pueden haber cambiado desde la
          última actualización.
        </p>
      ) : null}
      {loading && !loaded ? <LoadingState label="Cargando tus comisiones…" /> : null}
      {loading && loaded ? <LoadingState label="Actualizando tus comisiones…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {period && !period.closed ? (
        <p className="notice notice--info" role="status">
          {OPEN_PERIOD_NOTICE}
        </p>
      ) : null}
      {mine?.estimated ? (
        <p className="notice notice--info" role="status">
          <span>
            <span className="payroll-badge">{ESTIMATED_BADGE}</span> {ESTIMATED_NOTICE}
          </span>
        </p>
      ) : null}

      {loaded && mine ? (
        <>
          <div className="kpi-grid">
            <KPICard
              label="Ventas del período"
              value={String(mine.sales_count)}
              hint="Ventas registradas a tu nombre"
              accent="gold"
            />
            <KPICard
              label="Total vendido"
              value={formatCents(mine.sales_total_cents)}
              hint="Antes de comisiones"
              accent="plum"
            />
            <KPICard
              label="Tu comisión"
              value={formatCents(mine.commission_cents)}
              hint="Calculada por el servidor"
              accent="wine"
            />
          </div>
          {mine.sales_count === 0 ? (
            <p className="empty-state">
              No tenés ventas en este período. Prueba con otro período o espera a que se registren
              ventas.
            </p>
          ) : null}
        </>
      ) : null}
    </>
  )
}
