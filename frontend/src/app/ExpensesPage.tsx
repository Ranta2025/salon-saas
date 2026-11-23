import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '../lib/api'
import { TIME_ZONE, todayInTimeZone } from '../lib/datetime'
import {
  canManageExpenses,
  checkExpenseRange,
  createExpense,
  defaultExpenseRange,
  deleteExpense,
  fetchExpenses,
  updateExpense,
  type Expense,
  type ExpensePage,
  type ExpensePayload,
  type ExpenseRange,
} from '../lib/expenses'
import { subscribeOfflineData } from '../lib/offlineData'
import { activeRole } from '../lib/permissions'
import { fetchSettings } from '../lib/settings'
import { useSalon } from './SalonContext'
import { ExpenseForm } from './components/ExpenseForm'
import { ExpenseList } from './components/ExpenseList'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { ManagerOnlyNotice } from './components/ManagerOnlyNotice'
import { PageHeader } from './components/PageHeader'

/**
 * `/panel/gastos` (contract F3b): the expenses of the salon for a range of local
 * days, with a create form, an edit form and a delete that asks for confirmation.
 *
 * Money is never painted optimistically: after every write the list is read again
 * from the server, so the screen never shows an amount the server did not store.
 * The first day of the form and the latest day of the range come from the zone of
 * the SALON (`GET /settings`), never from a fixed zone.
 */
export function ExpensesPage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canManageExpenses(role)

  // Zone of the active salon; the known one is the fallback while it loads.
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const today = todayInTimeZone(timeZone)

  const [range, setRange] = useState<ExpenseRange>(() =>
    defaultExpenseRange(todayInTimeZone(TIME_ZONE)),
  )
  // Once the person picks a day, a late settings answer never moves the range.
  const rangeTouched = useRef(false)

  const [page, setPage] = useState<ExpensePage | null>(null)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [savedData, setSavedData] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)

  const [editing, setEditing] = useState<Expense | null>(null)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [formVersion, setFormVersion] = useState(0)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)

  // The worker flags a cached answer before the fetch resolves (E-10 rule).
  const cachedAnswer = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(
    () =>
      subscribeOfflineData((path) => {
        if (path.includes('/expenses')) {
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
    if (!rangeTouched.current) {
      setRange(defaultExpenseRange(today))
    }
  }, [today])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    const check = checkExpenseRange(range.from, range.to)
    if (!check.ok) {
      setPage(null)
      setLoaded(false)
      setSavedData(false)
      setError({ message: check.message, retryable: false })
      return
    }
    let active = true
    cachedAnswer.current = false
    setLoading(true)
    setError(null)
    setSavedData(false)
    fetchExpenses(salonId, { from: check.from, to: check.to })
      .then((result) => {
        if (active) {
          setPage(result)
          setLoaded(true)
          setSavedData(cachedAnswer.current)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar los gastos del rango.'))
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
  }, [salonId, allowed, range.from, range.to, reloadKey])

  const handleRangeChange = (patch: Partial<ExpenseRange>): void => {
    rangeTouched.current = true
    setRange((current) => ({ ...current, ...patch }))
  }

  const handleResetRange = (): void => {
    rangeTouched.current = false
    setRange(defaultExpenseRange(today))
  }

  const handleSubmit = async (payload: ExpensePayload): Promise<void> => {
    if (!salonId || saving) {
      return
    }
    setSaving(true)
    setFormError(null)
    setSuccess(null)
    try {
      if (editing) {
        await updateExpense(salonId, editing.id, payload)
        setSuccess('Gasto actualizado.')
        setEditing(null)
      } else {
        await createExpense(salonId, payload)
        setSuccess('Gasto guardado.')
        // The form starts empty again; its values never invent the saved row.
        setFormVersion((version) => version + 1)
      }
      // No optimistic paint: the list comes back from the server.
      reload()
    } catch (err: unknown) {
      setFormError(describeApiError(err, 'No pudimos guardar el gasto.').message)
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (expense: Expense): Promise<void> => {
    if (!salonId || deletingId !== null) {
      return
    }
    setDeletingId(expense.id)
    setActionError(null)
    setSuccess(null)
    try {
      await deleteExpense(salonId, expense.id)
      if (editing?.id === expense.id) {
        setEditing(null)
      }
      setSuccess('Gasto borrado.')
      reload()
    } catch (err: unknown) {
      setActionError(describeApiError(err, 'No pudimos borrar el gasto.').message)
      reload()
    } finally {
      setDeletingId(null)
    }
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Gastos" title="Gastos del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Gastos" title="Gastos del salón" />
        <ManagerOnlyNotice role={role} />
      </>
    )
  }

  const expenses = page?.expenses ?? []
  const truncated = page?.truncated ?? false

  return (
    <>
      <PageHeader
        eyebrow="Gastos"
        title="Gastos del salón"
        description="Alquiler, insumos, servicios y otros gastos del día a día. Cada gasto es un monto en dólares con su fecha y una descripción opcional; editar o borrar cambia la ganancia del período."
        actions={
          <button type="button" className="btn btn--quiet" onClick={reload}>
            Actualizar
          </button>
        }
      />

      <div className="panel-card">
        <div className="panel-card__head">
          <h2>Rango de fechas</h2>
          <span className="panel-card__meta">Hasta 366 días por consulta</span>
        </div>
        <div className="filters">
          <div className="field">
            <label htmlFor="expenses-from">Desde</label>
            <input
              id="expenses-from"
              className="input"
              type="date"
              max={today}
              value={range.from}
              onChange={(event) => handleRangeChange({ from: event.target.value })}
            />
          </div>
          <div className="field">
            <label htmlFor="expenses-to">Hasta</label>
            <input
              id="expenses-to"
              className="input"
              type="date"
              max={today}
              value={range.to}
              onChange={(event) => handleRangeChange({ to: event.target.value })}
              aria-describedby="expenses-range-hint"
            />
          </div>
          <button type="button" className="btn btn--quiet" onClick={handleResetRange}>
            Últimos 30 días
          </button>
        </div>
        <p className="field__hint" id="expenses-range-hint">
          Las dos fechas cuentan: el rango incluye {range.from} y {range.to}. Hoy en el salón
          es {today}.
        </p>
      </div>

      <section className="panel-card" aria-labelledby="expense-form-title">
        <div className="panel-card__head">
          <h2 id="expense-form-title">{editing ? 'Editar gasto' : 'Nuevo gasto'}</h2>
          <span className="panel-card__meta">
            {editing ? `Gasto del ${editing.spent_on}` : 'Monto en dólares'}
          </span>
        </div>
        <ExpenseForm
          key={editing ? `edit-${editing.id}` : `new-${formVersion}`}
          today={today}
          editing={editing}
          saving={saving}
          serverError={formError}
          onCancel={() => {
            setEditing(null)
            setFormError(null)
          }}
          onSubmit={(payload) => void handleSubmit(payload)}
        />
      </section>

      {success ? (
        <p className="notice notice--success" role="status">
          {success}
        </p>
      ) : null}

      {savedData ? (
        <p className="notice notice--info" role="status">
          Sin conexión: mostrando datos guardados. Los gastos pueden haber cambiado desde la
          última actualización.
        </p>
      ) : null}
      {loading && !loaded ? <LoadingState label="Cargando los gastos…" /> : null}
      {loading && loaded ? <LoadingState label="Actualizando los gastos…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="expenses-list-title">
          <ExpenseList
            expenses={expenses}
            truncated={truncated}
            deletingId={deletingId}
            error={actionError}
            onEdit={(expense) => {
              setEditing(expense)
              setFormError(null)
              setActionError(null)
              window.scrollTo({ top: 0, behavior: 'smooth' })
            }}
            onDelete={(expense) => void handleDelete(expense)}
          />
        </section>
      ) : null}
    </>
  )
}
