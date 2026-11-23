import { useState } from 'react'
import {
  DELETE_EXPENSE_NOTE,
  DELETE_EXPENSE_QUESTION,
  EXPENSE_CATEGORY_LABELS,
  TRUNCATED_NOTICE,
  type Expense,
} from '../../lib/expenses'
import { formatCents, sumCents } from '../../lib/money'

type ExpenseListProps = {
  expenses: readonly Expense[]
  /** The server cut the range at 500 rows: the total of the list is not the total. */
  truncated: boolean
  deletingId: string | null
  /** Spanish sentence of a failed delete, shown verbatim. */
  error: string | null
  onEdit: (expense: Expense) => void
  onDelete: (expense: Expense) => void
}

/**
 * List of the expenses of the chosen range (contract F3b), newest first. Every
 * row can be edited or deleted; deleting first asks for a confirmation that says
 * the action cannot be undone. Amounts are printed with `formatCents` and the
 * total is only shown when the server did not cut the list.
 */
export function ExpenseList({
  expenses,
  truncated,
  deletingId,
  error,
  onEdit,
  onDelete,
}: ExpenseListProps) {
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const total = expenses.length > 0 ? sumCents(expenses.map((expense) => expense.amount_cents)) : 0

  if (expenses.length === 0) {
    return <p className="empty-state">No hay gastos cargados en este rango de fechas.</p>
  }

  return (
    <>
      <div className="panel-card__head">
        <h2 id="expenses-list-title">Gastos del rango</h2>
        <span className="panel-card__meta">
          {expenses.length === 1 ? '1 gasto' : `${expenses.length} gastos`}
          {truncated ? '' : ` · Total ${formatCents(total)}`}
        </span>
      </div>

      {truncated ? (
        <p className="notice notice--info" role="status">
          {TRUNCATED_NOTICE}
        </p>
      ) : null}

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}

      <div className="table-wrap">
        <table className="table table--stack">
          <thead>
            <tr>
              <th scope="col">Fecha</th>
              <th scope="col">Categoría</th>
              <th scope="col">Descripción</th>
              <th scope="col" className="table__num">
                Monto
              </th>
              <th scope="col">
                Acciones
              </th>
            </tr>
          </thead>
          <tbody>
            {expenses.map((expense) => {
              const deleting = deletingId === expense.id
              return (
                <tr key={expense.id}>
                  <td data-label="Fecha">
                    <time dateTime={expense.spent_on}>{expense.spent_on}</time>
                  </td>
                  <td data-label="Categoría">{EXPENSE_CATEGORY_LABELS[expense.category]}</td>
                  <td data-label="Descripción">
                    {expense.description === '' ? (
                      <span className="muted">Sin descripción</span>
                    ) : (
                      expense.description
                    )}
                  </td>
                  <td data-label="Monto" className="table__num">
                    {formatCents(expense.amount_cents)}
                  </td>
                  <td data-label="Acciones" className="table__actions">
                    {confirmId === expense.id ? (
                      <div className="team-confirm">
                        <p className="team-confirm__question">{DELETE_EXPENSE_QUESTION}</p>
                        <p className="team-confirm__note">{DELETE_EXPENSE_NOTE}</p>
                        <div className="team-confirm__buttons">
                          <button
                            type="button"
                            className="btn btn--wine"
                            disabled={deleting}
                            onClick={() => {
                              setConfirmId(null)
                              onDelete(expense)
                            }}
                          >
                            {deleting ? 'Borrando…' : 'Sí, borrar'}
                          </button>
                          <button
                            type="button"
                            className="btn btn--quiet"
                            onClick={() => setConfirmId(null)}
                          >
                            Cancelar
                          </button>
                        </div>
                      </div>
                    ) : (
                      <div className="team-confirm__buttons">
                        <button
                          type="button"
                          className="btn btn--quiet"
                          onClick={() => onEdit(expense)}
                        >
                          Editar
                        </button>
                        <button
                          type="button"
                          className="btn btn--quiet"
                          disabled={deleting}
                          onClick={() => setConfirmId(expense.id)}
                        >
                          Borrar
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </>
  )
}
