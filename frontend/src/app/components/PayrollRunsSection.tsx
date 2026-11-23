import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { describeApiError, type ApiErrorNotice } from '../../lib/api'
import { formatCents } from '../../lib/money'
import type { PayPeriod } from '../../lib/payroll'
import {
  createPayrollRun,
  EMPTY_RUNS_MESSAGE,
  fetchPayrollRuns,
  formatSignedCents,
  LIQUIDATE_ACTION,
  liquidateBlockReason,
  runLabel,
  runStatusLabel,
  type PayrollRunSummary,
} from '../../lib/payrollRuns'
import { useSalon } from '../SalonContext'
import { ErrorNotice, LoadingState } from './Feedback'

function detailPath(runId: string): string {
  return `/panel/nomina/liquidaciones/${encodeURIComponent(runId)}`
}

/**
 * Block «Liquidaciones» of `/panel/nomina` (S-F3.3-5): freezes the previewed
 * period and lists the runs newest first. The button only works on a finished
 * period; while the period is still in course it explains why instead of being a
 * dead control.
 */
export function PayrollRunsSection({ period }: { period: PayPeriod | null }) {
  const { salonId } = useSalon()
  const navigate = useNavigate()

  const [runs, setRuns] = useState<PayrollRunSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchPayrollRuns(salonId)
      .then((rows) => {
        if (active) {
          setRuns(rows)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar las liquidaciones.'))
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

  const blockReason = liquidateBlockReason(period)
  const showReason = period !== null && blockReason !== null

  const handleCreate = async (): Promise<void> => {
    if (!salonId || creating || period === null || blockReason !== null) {
      return
    }
    setCreating(true)
    setCreateError(null)
    try {
      const created = await createPayrollRun(salonId, {
        kind: period.kind,
        date: period.from,
      })
      navigate(detailPath(created.id))
    } catch (err: unknown) {
      setCreateError(describeApiError(err, 'No pudimos liquidar el período.').message)
      // The rejected run usually means the list is stale: read it again.
      reload()
    } finally {
      setCreating(false)
    }
  }

  return (
    <section className="panel-card runs" aria-labelledby="runs-title">
      <div className="panel-card__head">
        <h2 id="runs-title">Liquidaciones</h2>
        <span className="panel-card__meta">Una liquidación congela los montos del período</span>
      </div>

      <div className="runs__actions">
        <button
          type="button"
          className="btn btn--wine"
          onClick={() => void handleCreate()}
          disabled={creating || blockReason !== null}
          aria-describedby={showReason ? 'runs-blocked-reason' : undefined}
        >
          {creating ? 'Liquidando…' : LIQUIDATE_ACTION}
        </button>
        {showReason ? (
          <p className="field__hint" id="runs-blocked-reason">
            {blockReason}
          </p>
        ) : null}
      </div>

      {createError ? (
        <div className="notice notice--error" role="alert">
          <p>{createError}</p>
        </div>
      ) : null}

      {loading && !loaded ? <LoadingState label="Cargando las liquidaciones…" /> : null}
      {loading && loaded ? <LoadingState label="Actualizando las liquidaciones…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded && !error && runs.length === 0 ? <p className="empty-state">{EMPTY_RUNS_MESSAGE}</p> : null}

      {runs.length > 0 ? (
        <ul className="run-list">
          {runs.map((run) => (
            <li key={run.id} className="run-card">
              <div className="run-card__head">
                <Link className="run-card__label" to={detailPath(run.id)}>
                  {runLabel(run)}
                </Link>
                <span className={`pill ${run.status === 'paid' ? 'pill--on' : 'pill--pending'}`}>
                  {runStatusLabel(run.status)}
                </span>
              </div>
              <p className="run-card__meta">
                <span>
                  {run.lines_count} {run.lines_count === 1 ? 'línea' : 'líneas'}
                </span>
                <span>Comisión {formatCents(run.totals.commission_cents)}</span>
                <span>Fijo {formatCents(run.totals.fixed_cents)}</span>
                <span>Ajuste {formatSignedCents(run.totals.adjustment_cents)}</span>
                <span className="run-card__total">
                  Total {formatCents(run.totals.total_cents)}
                </span>
              </p>
              <Link className="run-card__open" to={detailPath(run.id)}>
                Ver detalle
              </Link>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  )
}
