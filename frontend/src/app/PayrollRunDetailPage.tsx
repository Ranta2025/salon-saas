import { Fragment, useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { describeApiError, type ApiErrorNotice } from '../lib/api'
import { formatCents } from '../lib/money'
import {
  ESTIMATED_BADGE,
  ESTIMATED_NOTICE,
  FORMER_MEMBER_LABEL,
  payrollSalesTotals,
} from '../lib/payroll'
import {
  applyRunLineAdjustment,
  canDeleteRun,
  canEditRunLines,
  canManagePayrollRuns,
  canPayRun,
  DELETE_RUN_CONFIRM_NOTE,
  DELETE_RUN_CONFIRM_QUESTION,
  deletePayrollRun,
  EXPORT_ACTION,
  EXPORT_ERROR_MESSAGE,
  fetchPayrollRun,
  fetchPayrollRunCsv,
  formatSignedCents,
  INVALID_ADJUSTMENT_MESSAGE,
  PAID_RUN_NOTICE,
  PAY_RUN_CONFIRM_NOTE,
  PAY_RUN_CONFIRM_QUESTION,
  parseSignedAmountToCents,
  payPayrollRun,
  restorePayrollRun,
  runCsvFilename,
  runLabel,
  runStatusLabel,
  savePayrollRunCsv,
  signedAmountInputFromCents,
  updateRunLineAdjustment,
  type PayrollRun,
  type PayrollRunLine,
} from '../lib/payrollRuns'
import { activeRole } from '../lib/permissions'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { ManagerOnlyNotice } from './components/ManagerOnlyNotice'
import { PageHeader } from './components/PageHeader'

type LineError = { userId: string; message: string }

/**
 * `/panel/nomina/liquidaciones/:rid` (S-F3.3-5): the frozen run with its lines.
 * While the run is open each line's adjustment can be edited (signed amount plus
 * a note) with an optimistic paint and an exact rollback, the run can be paid or
 * deleted after a confirmation, and the CSV is downloaded with the bearer token.
 * A paid run is read-only: the same page only shows its figures and the export.
 */
export function PayrollRunDetailPage() {
  const { rid } = useParams<{ rid: string }>()
  const { salonId, salons } = useSalon()
  const navigate = useNavigate()
  const role = activeRole(salons, salonId)
  const allowed = canManagePayrollRuns(role)

  const [run, setRun] = useState<PayrollRun | null>(null)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const [editing, setEditing] = useState<string | null>(null)
  const [draftAmount, setDraftAmount] = useState('0.00')
  const [draftNote, setDraftNote] = useState('')
  const [savingId, setSavingId] = useState<string | null>(null)
  const [lineError, setLineError] = useState<LineError | null>(null)

  const [confirm, setConfirm] = useState<'pay' | 'delete' | null>(null)
  const [actionPending, setActionPending] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const [exporting, setExporting] = useState(false)
  const [exportError, setExportError] = useState<string | null>(null)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed || !rid) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchPayrollRun(salonId, rid)
      .then((result) => {
        if (active) {
          setRun(result)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar la liquidación.'))
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
  }, [salonId, allowed, rid, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Liquidaciones" title="Liquidación" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Liquidaciones" title="Liquidación" />
        <ManagerOnlyNotice role={role} />
      </>
    )
  }

  if (!rid) {
    return (
      <>
        <PageHeader eyebrow="Liquidaciones" title="Liquidación" />
        <p className="empty-state">No encontramos esa liquidación.</p>
      </>
    )
  }

  const open = run !== null && canEditRunLines(run)
  const editable = open && savingId === null

  const startEdit = (line: PayrollRunLine): void => {
    setEditing(line.user_id)
    setDraftAmount(signedAmountInputFromCents(line.adjustment_cents))
    setDraftNote(line.adjustment_note)
    setLineError(null)
  }

  const cancelEdit = (): void => {
    setEditing(null)
    setLineError(null)
  }

  const saveAdjustment = async (
    line: PayrollRunLine,
    event: FormEvent<HTMLFormElement>,
  ): Promise<void> => {
    event.preventDefault()
    if (!salonId || !run || savingId !== null) {
      return
    }
    const cents = parseSignedAmountToCents(draftAmount)
    if (cents === null || !Number.isSafeInteger(cents)) {
      setLineError({ userId: line.user_id, message: INVALID_ADJUSTMENT_MESSAGE })
      return
    }
    const previous = run
    setLineError(null)
    setSavingId(line.user_id)
    // Optimistic paint: the line and the totals move before the server answers.
    setRun(
      applyRunLineAdjustment(previous, line.user_id, {
        adjustment_cents: cents,
        adjustment_note: draftNote,
      }),
    )
    try {
      const saved = await updateRunLineAdjustment(salonId, previous.id, line.user_id, {
        adjustment_cents: cents,
        adjustment_note: draftNote,
      })
      // The answer of the server replaces the optimistic preview.
      setRun(saved)
      setEditing(null)
    } catch (err: unknown) {
      // Exact rollback: the snapshot comes back untouched and the sentence of the
      // server is shown verbatim next to the line.
      setRun(restorePayrollRun(previous))
      setLineError({
        userId: line.user_id,
        message: describeApiError(err, 'No pudimos guardar el ajuste.').message,
      })
    } finally {
      setSavingId(null)
    }
  }

  const handlePay = async (): Promise<void> => {
    if (!salonId || !run || actionPending) {
      return
    }
    setActionPending(true)
    setActionError(null)
    try {
      const paid = await payPayrollRun(salonId, run.id)
      setRun(paid)
      setEditing(null)
      setConfirm(null)
    } catch (err: unknown) {
      setActionError(describeApiError(err, 'No pudimos marcar la liquidación como pagada.').message)
    } finally {
      setActionPending(false)
    }
  }

  const handleDelete = async (): Promise<void> => {
    if (!salonId || !run || actionPending) {
      return
    }
    setActionPending(true)
    setActionError(null)
    try {
      await deletePayrollRun(salonId, run.id)
      navigate('/panel/nomina')
    } catch (err: unknown) {
      setActionError(describeApiError(err, 'No pudimos borrar la liquidación.').message)
    } finally {
      setActionPending(false)
    }
  }

  const handleExport = async (): Promise<void> => {
    if (!salonId || !run || exporting) {
      return
    }
    setExporting(true)
    setExportError(null)
    try {
      const file = await fetchPayrollRunCsv(salonId, run.id, runCsvFilename(run))
      savePayrollRunCsv(file)
    } catch (err: unknown) {
      setExportError(describeApiError(err, EXPORT_ERROR_MESSAGE).message)
    } finally {
      setExporting(false)
    }
  }

  const title = run ? runLabel(run) : 'Liquidación'
  const sales = payrollSalesTotals(run?.lines ?? [])

  return (
    <>
      <PageHeader
        eyebrow="Liquidaciones"
        title={title}
        description={
          run
            ? 'Montos congelados: no cambian aunque se edite el catálogo o un modelo de pago.'
            : undefined
        }
        actions={
          <Link className="btn btn--quiet" to="/panel/nomina">
            Volver a Nómina
          </Link>
        }
      />

      {loading && !loaded ? <LoadingState label="Cargando la liquidación…" /> : null}
      {loading && loaded ? <LoadingState label="Actualizando la liquidación…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {run ? (
        <>
          <section className="panel-card run-head" aria-label="Datos de la liquidación">
            <p className="run-head__meta">
              <span className={`pill ${run.status === 'paid' ? 'pill--on' : 'pill--pending'}`}>
                {runStatusLabel(run.status)}
              </span>
              <span>
                {run.lines.length} {run.lines.length === 1 ? 'línea' : 'líneas'}
              </span>
              <span className="run-head__total">
                Total de la liquidación: {formatCents(run.totals.total_cents)}
              </span>
            </p>

            {run.status === 'paid' ? (
              <p className="notice notice--info" role="status">
                {PAID_RUN_NOTICE}
              </p>
            ) : null}

            <div className="run-head__actions">
              <button
                type="button"
                className="btn btn--quiet"
                onClick={() => void handleExport()}
                disabled={exporting}
              >
                {exporting ? 'Descargando…' : EXPORT_ACTION}
              </button>
              {canPayRun(run) ? (
                <button
                  type="button"
                  className="btn btn--wine"
                  onClick={() => {
                    setConfirm('pay')
                    setActionError(null)
                  }}
                  disabled={actionPending}
                >
                  Marcar como pagada
                </button>
              ) : null}
              {canDeleteRun(run) ? (
                <button
                  type="button"
                  className="btn btn--quiet"
                  onClick={() => {
                    setConfirm('delete')
                    setActionError(null)
                  }}
                  disabled={actionPending}
                >
                  Borrar liquidación
                </button>
              ) : null}
            </div>

            {exportError ? (
              <div className="notice notice--error" role="alert">
                <p>{exportError}</p>
              </div>
            ) : null}

            {confirm === 'pay' ? (
              <span className="team-confirm" role="group" aria-label="Confirmar pago de la liquidación">
                <span className="team-confirm__question">{PAY_RUN_CONFIRM_QUESTION}</span>
                <span className="team-confirm__note">{PAY_RUN_CONFIRM_NOTE}</span>
                <span className="team-confirm__buttons">
                  <button
                    type="button"
                    className="btn btn--wine"
                    onClick={() => void handlePay()}
                    disabled={actionPending}
                  >
                    {actionPending ? 'Marcando como pagada…' : 'Sí, marcar como pagada'}
                  </button>
                  <button
                    type="button"
                    className="btn btn--quiet"
                    onClick={() => {
                      setConfirm(null)
                      setActionError(null)
                    }}
                    disabled={actionPending}
                  >
                    Volver
                  </button>
                </span>
                {actionError ? (
                  <span className="team-confirm__error" role="alert">
                    {actionError}
                  </span>
                ) : null}
              </span>
            ) : null}

            {confirm === 'delete' ? (
              <span className="team-confirm" role="group" aria-label="Confirmar borrado de la liquidación">
                <span className="team-confirm__question">{DELETE_RUN_CONFIRM_QUESTION}</span>
                <span className="team-confirm__note">{DELETE_RUN_CONFIRM_NOTE}</span>
                <span className="team-confirm__buttons">
                  <button
                    type="button"
                    className="btn btn--wine"
                    onClick={() => void handleDelete()}
                    disabled={actionPending}
                  >
                    {actionPending ? 'Borrando…' : 'Sí, borrar liquidación'}
                  </button>
                  <button
                    type="button"
                    className="btn btn--quiet"
                    onClick={() => {
                      setConfirm(null)
                      setActionError(null)
                    }}
                    disabled={actionPending}
                  >
                    Volver
                  </button>
                </span>
                {actionError ? (
                  <span className="team-confirm__error" role="alert">
                    {actionError}
                  </span>
                ) : null}
              </span>
            ) : null}
          </section>

          {run.lines.length === 0 ? (
            <p className="empty-state">Esta liquidación no tiene líneas.</p>
          ) : (
            <section className="panel-card" aria-labelledby="run-lines-title">
              <div className="panel-card__head">
                <h2 id="run-lines-title">Líneas de la liquidación</h2>
                <span className="panel-card__meta">Montos en dólares</span>
              </div>
              <div className="table-wrap">
                <table className="table table--stack run-table">
                  <thead>
                    <tr>
                      <th scope="col">Profesional</th>
                      <th scope="col" className="table__num">
                        Ventas
                      </th>
                      <th scope="col" className="table__num">
                        Vendido
                      </th>
                      <th scope="col" className="table__num">
                        Comisión
                      </th>
                      <th scope="col" className="table__num">
                        Fijo
                      </th>
                      <th scope="col" className="table__num">
                        Ajuste
                      </th>
                      <th scope="col">Nota</th>
                      <th scope="col" className="table__num">
                        Total
                      </th>
                      {open ? (
                        <th scope="col" className="table__actions">
                          Acciones
                        </th>
                      ) : null}
                    </tr>
                  </thead>
                  <tbody>
                    {run.lines.map((line) => (
                      <Fragment key={line.user_id}>
                        <tr data-inactive={line.active ? undefined : 'true'}>
                          <td data-label="Profesional" className="run-table__person">
                            <span className="run-table__name">{line.name}</span>
                            {line.active ? null : (
                              <span className="pill pill--off">{FORMER_MEMBER_LABEL}</span>
                            )}
                            {line.estimated ? (
                              <span className="payroll-badge" title={ESTIMATED_NOTICE}>
                                {ESTIMATED_BADGE}
                              </span>
                            ) : null}
                          </td>
                          <td data-label="Ventas" className="table__num">
                            {line.sales_count}
                          </td>
                          <td data-label="Vendido" className="table__num">
                            {formatCents(line.sales_total_cents)}
                          </td>
                          <td data-label="Comisión" className="table__num">
                            {formatCents(line.commission_cents)}
                          </td>
                          <td data-label="Fijo" className="table__num">
                            {formatCents(line.fixed_cents)}
                          </td>
                          <td data-label="Ajuste" className="table__num">
                            {formatSignedCents(line.adjustment_cents)}
                          </td>
                          <td data-label="Nota" className="run-table__note">
                            {line.adjustment_note === '' ? '—' : line.adjustment_note}
                          </td>
                          <td data-label="Total" className="table__num run-table__total">
                            {formatCents(line.total_cents)}
                          </td>
                          {open ? (
                            <td data-label="Acciones" className="table__actions">
                              <button
                                type="button"
                                className="btn btn--quiet"
                                onClick={() => startEdit(line)}
                                disabled={!editable}
                                aria-expanded={editing === line.user_id}
                                aria-label={`Editar el ajuste de ${line.name}`}
                              >
                                Editar ajuste
                              </button>
                            </td>
                          ) : null}
                        </tr>
                        {editing === line.user_id ? (
                          <tr className="run-line-editor">
                            <td
                              colSpan={open ? 9 : 8}
                              className="run-line-editor__cell"
                              data-label="Editar ajuste"
                            >
                              <form
                                className="run-line-form"
                                aria-label={`Ajuste de ${line.name}`}
                                onSubmit={(event) => void saveAdjustment(line, event)}
                              >
                                <div className="field">
                                  <label htmlFor={`run-adjustment-amount-${line.user_id}`}>
                                    Ajuste en dólares
                                  </label>
                                  <input
                                    id={`run-adjustment-amount-${line.user_id}`}
                                    className="input"
                                    inputMode="decimal"
                                    autoComplete="off"
                                    value={draftAmount}
                                    onChange={(event) => setDraftAmount(event.target.value)}
                                    aria-describedby={`run-adjustment-hint-${line.user_id}`}
                                  />
                                  <p className="field__hint" id={`run-adjustment-hint-${line.user_id}`}>
                                    Puede ser negativo. Si no es 0, la nota es obligatoria.
                                  </p>
                                </div>
                                <div className="field">
                                  <label htmlFor={`run-adjustment-note-${line.user_id}`}>
                                    Nota del ajuste
                                  </label>
                                  <input
                                    id={`run-adjustment-note-${line.user_id}`}
                                    className="input"
                                    type="text"
                                    maxLength={200}
                                    autoComplete="off"
                                    value={draftNote}
                                    onChange={(event) => setDraftNote(event.target.value)}
                                  />
                                </div>
                                <div className="run-line-form__buttons">
                                  <button
                                    type="submit"
                                    className="btn btn--wine"
                                    disabled={savingId !== null}
                                  >
                                    {savingId === line.user_id ? 'Guardando…' : 'Guardar ajuste'}
                                  </button>
                                  <button
                                    type="button"
                                    className="btn btn--quiet"
                                    onClick={cancelEdit}
                                    disabled={savingId !== null}
                                  >
                                    Cancelar
                                  </button>
                                </div>
                                {lineError !== null && lineError.userId === line.user_id ? (
                                  <p className="run-line-form__error" role="alert">
                                    {lineError.message}
                                  </p>
                                ) : null}
                              </form>
                            </td>
                          </tr>
                        ) : null}
                      </Fragment>
                    ))}
                  </tbody>
                  <tfoot>
                    <tr>
                      <td data-label="Totales" className="run-table__total-label">
                        Totales
                      </td>
                      <td data-label="Ventas" className="table__num">
                        {sales.sales_count}
                      </td>
                      <td data-label="Vendido" className="table__num">
                        {formatCents(sales.sales_total_cents)}
                      </td>
                      <td data-label="Comisión" className="table__num">
                        {formatCents(run.totals.commission_cents)}
                      </td>
                      <td data-label="Fijo" className="table__num">
                        {formatCents(run.totals.fixed_cents)}
                      </td>
                      <td data-label="Ajuste" className="table__num">
                        {formatSignedCents(run.totals.adjustment_cents)}
                      </td>
                      <td data-label="Nota" />
                      <td data-label="Total" className="table__num run-table__total">
                        {formatCents(run.totals.total_cents)}
                      </td>
                      {open ? <td data-label="Acciones" /> : null}
                    </tr>
                  </tfoot>
                </table>
              </div>
            </section>
          )}
        </>
      ) : null}
    </>
  )
}
