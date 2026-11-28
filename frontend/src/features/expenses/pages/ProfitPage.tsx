import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '@/lib/api'
import { TIME_ZONE, todayInTimeZone } from '@/lib/datetime'
import { formatCents } from '@/lib/money'
import { subscribeOfflineData } from '@/lib/offlineData'
import { activeRole } from '@/lib/permissions'
import {
  canViewProfit,
  checkProfitInput,
  fetchProfit,
  formatProfitNet,
  PROFIT_KINDS,
  PROFIT_KIND_LABELS,
  PROFIT_PAYROLL_NOTICE,
  profitCategoryRows,
  profitDayBars,
  profitNetSign,
  profitPeriodLabel,
  type ProfitKind,
  type ProfitReport,
} from '../lib/profit'
import { fetchSettings } from '@/features/settings/lib/settings'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { ManagerOnlyNotice } from '@/app/components/ManagerOnlyNotice'
import { PageHeader } from '@/app/components/PageHeader'
import { ProfitCategoryBreakdown } from '../components/ProfitCategoryBreakdown'
import { ProfitDayBars } from '../components/ProfitDayBars'

/** Same token as `.field__error`: a loss is never painted as a win (contract F3b). */
const NEGATIVE_COLOR = 'var(--error-text)'

/**
 * `/panel/ganancias` (contract F3b): revenue, expenses, paid payroll and net profit
 * of a day, a week or a month, with the bars of every local day and the breakdown
 * of the expenses by category.
 *
 * The payroll counts only when a run is PAID, on the local day of `paid_at` (D1);
 * the fixed notice says it. The report is derived by the server in the zone of the
 * salon, so the default date is today in that zone and not a fixed one (D2).
 */
export function ProfitPage() {
  const { salonId, salons, loading: meLoading, refresh: refreshMe } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canViewProfit(role)

  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const today = todayInTimeZone(timeZone)

  const [kind, setKind] = useState<ProfitKind>('week')
  const [date, setDate] = useState(() => todayInTimeZone(TIME_ZONE))
  // The person may want to look at another day: a late settings answer never moves it.
  const dateTouched = useRef(false)

  const [report, setReport] = useState<ProfitReport | null>(null)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [savedData, setSavedData] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  const cachedAnswer = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(
    () =>
      subscribeOfflineData((path) => {
        if (path.includes('/profit')) {
          cachedAnswer.current = true
        }
      }),
    [],
  )

  // The zone comes from the settings of the salon; a failure keeps the known one.
  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setTimeZone(settings.timezone)
        }
      })
      .catch(() => {
        // TIME_ZONE stays as the fallback.
      })
    return () => {
      active = false
    }
  }, [salonId])

  useEffect(() => {
    if (!dateTouched.current) {
      setDate(today)
    }
  }, [today])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    const check = checkProfitInput(kind, date)
    if (!check.ok) {
      setReport(null)
      setLoaded(false)
      setSavedData(false)
      setError({ message: check.message, retryable: false })
      return
    }
    let active = true
    cachedAnswer.current = false
    // A new period never shows the figures of the previous one.
    setReport(null)
    setLoaded(false)
    setLoading(true)
    setSavedData(false)
    setError(null)
    fetchProfit(salonId, { kind: check.kind, date: check.date })
      .then((result) => {
        if (active) {
          setReport(result)
          setLoaded(true)
          setSavedData(cachedAnswer.current)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar la ganancia del período.'))
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
        <PageHeader eyebrow="Ganancias" title="Ganancias del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Ganancias" title="Ganancias del salón" />
        <ManagerOnlyNotice
          role={role}
          what="las ganancias del salón"
          loading={meLoading}
          onRetry={refreshMe}
        />
      </>
    )
  }

  const period = report?.period ?? null
  const totals = report?.totals ?? null
  const sign = totals ? profitNetSign(totals) : 'zero'
  const bars = report ? profitDayBars(report) : []
  const categories = report ? profitCategoryRows(report) : []

  return (
    <>
      <PageHeader
        eyebrow="Ganancias"
        title="Ganancias del salón"
        description="Ingresos de las ventas menos gastos y nómina pagada. El servidor arma el día, la semana (lunes a domingo) o el mes calendario en la zona horaria del salón."
        actions={
          <button type="button" className="btn btn--quiet" onClick={reload}>
            Actualizar
          </button>
        }
      />

      <div className="panel-card">
        <div className="filters">
          <div className="field">
            <label htmlFor="profit-kind">Período</label>
            <select
              id="profit-kind"
              className="select"
              value={kind}
              onChange={(event) => setKind(event.target.value as ProfitKind)}
            >
              {PROFIT_KINDS.map((option) => (
                <option key={option} value={option}>
                  {PROFIT_KIND_LABELS[option]}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="profit-date">Fecha dentro del período</label>
            <input
              id="profit-date"
              className="input"
              type="date"
              value={date}
              onChange={(event) => {
                dateTouched.current = true
                setDate(event.target.value)
              }}
              aria-describedby="profit-date-hint"
            />
            <p className="field__hint" id="profit-date-hint">
              Cualquier día del período; hoy en el salón es {today}.
            </p>
          </div>
        </div>
        {period ? (
          <p className="panel-card__meta" role="status">
            {profitPeriodLabel(period)}
          </p>
        ) : null}
      </div>

      <p className="notice notice--info">{PROFIT_PAYROLL_NOTICE}</p>

      {savedData ? (
        <p className="notice notice--info" role="status">
          Sin conexión: mostrando datos guardados. Las cifras pueden haber cambiado desde la
          última actualización.
        </p>
      ) : null}
      {loading && !loaded ? <LoadingState label="Cargando la ganancia…" /> : null}
      {loading && loaded ? <LoadingState label="Actualizando la ganancia…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded && report && !period ? (
        <p className="notice notice--error" role="alert">
          El servidor devolvió la ganancia sin un período válido. Revisá la fecha y volvé a
          intentar.
        </p>
      ) : null}

      {loaded && totals ? (
        <section className="kpi-grid" aria-label="Resumen del período">
          <article className="kpi kpi--gold">
            <p className="kpi__label">Ingresos</p>
            <p className="kpi__value">{formatCents(totals.revenue_cents)}</p>
            <p className="kpi__foot">Ventas del período, en la zona del salón</p>
          </article>
          <article className="kpi">
            <p className="kpi__label">Gastos</p>
            <p className="kpi__value">{formatCents(totals.expenses_cents)}</p>
            <p className="kpi__foot">Alquiler, insumos, servicios y otros</p>
          </article>
          <article className="kpi">
            <p className="kpi__label">Nómina pagada</p>
            <p className="kpi__value">{formatCents(totals.payroll_cents)}</p>
            <p className="kpi__foot">Solo liquidaciones pagadas, en su día de pago</p>
          </article>
          <article className="kpi kpi--plum">
            <p className="kpi__label">Ganancia neta</p>
            <p
              className="kpi__value"
              style={sign === 'negative' ? { color: NEGATIVE_COLOR } : undefined}
              data-sign={sign}
            >
              {formatProfitNet(totals.net_cents)}
            </p>
            <p className="kpi__foot">
              {sign === 'negative'
                ? 'Pérdida: los gastos y la nómina superan los ingresos'
                : sign === 'zero'
                  ? 'Sin ganancia ni pérdida en el período'
                  : 'Ingresos menos gastos y nómina pagada'}
            </p>
          </article>
        </section>
      ) : null}

      {loaded && report && !error ? (
        <>
          <section className="panel-card" aria-labelledby="profit-bars-title">
            <div className="panel-card__head">
              <h2 id="profit-bars-title">Ingresos por día</h2>
              <span className="panel-card__meta">Montos en dólares</span>
            </div>
            {bars.length === 0 ? (
              <p className="empty-state">Este período no tiene días: probá con otra fecha.</p>
            ) : (
              <ProfitDayBars bars={bars} />
            )}
          </section>

          <section className="panel-card" aria-labelledby="profit-categories-title">
            <div className="panel-card__head">
              <h2 id="profit-categories-title">Gastos por categoría</h2>
              <span className="panel-card__meta">Montos en dólares</span>
            </div>
            <ProfitCategoryBreakdown rows={categories} />
          </section>
        </>
      ) : null}
    </>
  )
}
