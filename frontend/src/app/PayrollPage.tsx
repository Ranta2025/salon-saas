import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '../lib/api'
import { formatCents } from '../lib/money'
import { subscribeOfflineData } from '../lib/offlineData'
import { activeRole, roleLabel } from '../lib/permissions'
import {
  checkPayrollInput,
  ESTIMATED_BADGE,
  ESTIMATED_NOTICE,
  FORMER_MEMBER_LABEL,
  OPEN_PERIOD_NOTICE,
  PAY_PERIOD_KINDS,
  PAY_PERIOD_KIND_LABELS,
  canManagePayroll,
  payPeriodLabel,
  payrollSalesTotals,
  previewPayroll,
  type PayPeriodKind,
  type PayrollLine,
  type PayrollPreview,
} from '../lib/payroll'
import { toDateInputValue } from '../lib/datetime'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'

/**
 * `/panel/nomina` (S-F3.2-5): preview of the payroll of a period for owner and
 * admin. The server derives the period from the kind and any date inside it and
 * answers the lines, the totals and whether the period is still open and whether
 * some commission was estimated with the current rate of the service.
 */
export function PayrollPage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canManagePayroll(role)

  const [kind, setKind] = useState<PayPeriodKind>('week')
  const [date, setDate] = useState(() => toDateInputValue(new Date()))
  const [preview, setPreview] = useState<PayrollPreview | null>(null)
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
        if (path.includes('/payroll/preview')) {
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
      setPreview(null)
      setLoaded(false)
      setSavedData(false)
      setError({ message: check.message, retryable: false })
      return
    }
    let active = true
    cachedAnswer.current = false
    // A new selection never shows the lines of the previous period: an error of
    // the new one must not leave stale figures under the new selector.
    setPreview(null)
    setLoaded(false)
    setLoading(true)
    setSavedData(false)
    setError(null)
    previewPayroll(salonId, { kind: check.kind, date: check.date })
      .then((result) => {
        if (active) {
          setPreview(result)
          setLoaded(true)
          setSavedData(cachedAnswer.current)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar la nómina del período.'))
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
        <PageHeader eyebrow="Nómina" title="Nómina del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Nómina" title="Nómina del salón" />
        <div className="empty-state">
          <h2>Esta sección es de la administración</h2>
          <p>
            Tu rol ({roleLabel(role)}) no puede ver la nómina ni las comisiones del equipo. Si
            necesitas acceso, pídeselo a la dueña o a la administración.
          </p>
        </div>
      </>
    )
  }

  const period = preview?.period ?? null
  const lines = preview?.lines ?? []
  const totals = preview?.totals
  const sales = payrollSalesTotals(lines)
  const anyEstimated = lines.some((line) => line.estimated)

  return (
    <>
      <PageHeader
        eyebrow="Nómina"
        title="Nómina del salón"
        description="Lo que se pagaría por comisiones y montos fijos en el período. El servidor calcula la semana (lunes a domingo), la quincena o el mes en la zona del salón; el monto fijo se paga solo a quien tiene ese período fijo."
        actions={
          <button type="button" className="btn btn--quiet" onClick={reload}>
            Actualizar
          </button>
        }
      />

      <div className="panel-card payroll-filters">
        <div className="filters">
          <div className="field">
            <label htmlFor="payroll-kind">Período</label>
            <select
              id="payroll-kind"
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
            <label htmlFor="payroll-date">Fecha dentro del período</label>
            <input
              id="payroll-date"
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
          Sin conexión: mostrando datos guardados. La nómina puede haber cambiado desde la última
          actualización.
        </p>
      ) : null}
      {loading && !loaded ? <LoadingState label="Cargando la nómina…" /> : null}
      {loading && loaded ? <LoadingState label="Actualizando la nómina…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {period && !period.closed ? (
        <p className="notice notice--info" role="status">
          {OPEN_PERIOD_NOTICE}
        </p>
      ) : null}
      {anyEstimated ? (
        <p className="notice notice--info" role="status">
          {ESTIMATED_NOTICE}
        </p>
      ) : null}

      {loaded && preview && lines.length === 0 ? (
        <p className="empty-state">
          No hay ventas ni montos fijos en este período. Prueba con otro período o espera a que se
          registren ventas.
        </p>
      ) : null}

      {loaded && lines.length > 0 && totals ? (
        <section className="panel-card" aria-labelledby="payroll-table-title">
          <div className="panel-card__head">
            <h2 id="payroll-table-title">Por profesional</h2>
            <span className="panel-card__meta">Montos en dólares</span>
          </div>
          <div className="table-wrap">
            <table className="table table--stack payroll-table">
              <thead>
                <tr>
                  <th scope="col">Profesional</th>
                  <th scope="col" className="table__num">
                    Ventas
                  </th>
                  <th scope="col" className="table__num">
                    Total vendido
                  </th>
                  <th scope="col" className="table__num">
                    Comisión
                  </th>
                  <th scope="col" className="table__num">
                    Fijo
                  </th>
                  <th scope="col" className="table__num">
                    Total
                  </th>
                </tr>
              </thead>
              <tbody>
                {lines.map((line) => (
                  <PayrollRow key={line.user_id} line={line} />
                ))}
              </tbody>
              <tfoot>
                <tr>
                  <td data-label="Totales" className="payroll-table__total-label">
                    Totales
                  </td>
                  <td data-label="Ventas" className="table__num">
                    {sales.sales_count}
                  </td>
                  <td data-label="Total vendido" className="table__num">
                    {formatCents(sales.sales_total_cents)}
                  </td>
                  <td data-label="Comisión" className="table__num">
                    {formatCents(totals.commission_cents)}
                  </td>
                  <td data-label="Fijo" className="table__num">
                    {formatCents(totals.fixed_cents)}
                  </td>
                  <td data-label="Total" className="table__num payroll-table__total">
                    {formatCents(totals.total_cents)}
                  </td>
                </tr>
              </tfoot>
            </table>
          </div>
        </section>
      ) : null}
    </>
  )
}

function PayrollRow({ line }: { line: PayrollLine }) {
  return (
    <tr data-inactive={line.active ? undefined : 'true'}>
      <td data-label="Profesional" className="payroll-table__person">
        <span className="payroll-table__name">{line.name}</span>
        {line.active ? null : <span className="pill pill--off">{FORMER_MEMBER_LABEL}</span>}
        {line.estimated ? (
          <span className="payroll-badge" title={ESTIMATED_NOTICE}>
            {ESTIMATED_BADGE}
          </span>
        ) : null}
      </td>
      <td data-label="Ventas" className="table__num">
        {line.sales_count}
      </td>
      <td data-label="Total vendido" className="table__num">
        {formatCents(line.sales_total_cents)}
      </td>
      <td data-label="Comisión" className="table__num">
        {formatCents(line.commission_cents)}
      </td>
      <td data-label="Fijo" className="table__num">
        {formatCents(line.fixed_cents)}
      </td>
      <td data-label="Total" className="table__num payroll-table__total">
        {formatCents(line.total_cents)}
      </td>
    </tr>
  )
}
