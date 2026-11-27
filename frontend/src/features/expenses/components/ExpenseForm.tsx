import { useEffect, useState, type FormEvent } from 'react'
import {
  EXPENSE_CATEGORIES,
  EXPENSE_CATEGORY_LABELS,
  MAX_DESCRIPTION_LENGTH,
  expenseAmountInputFromCents,
  validateExpenseDraft,
  type Expense,
  type ExpenseDraft,
  type ExpenseField,
  type ExpensePayload,
} from '../lib/expenses'

const EMPTY_DRAFT: ExpenseDraft = { category: '', amount: '', spent_on: '', description: '' }

/** The form as it opens: empty for a new expense, filled when one is edited. */
function draftOf(expense: Expense | null, today: string): ExpenseDraft {
  if (!expense) {
    return { ...EMPTY_DRAFT, spent_on: today }
  }
  return {
    category: expense.category,
    amount: expenseAmountInputFromCents(expense.amount_cents),
    spent_on: expense.spent_on,
    description: expense.description,
  }
}

type ExpenseFormProps = {
  /** Today in the zone of the salon: the latest day an expense may have (D3). */
  today: string
  /** The expense being replaced, or `null` to create a new one. */
  editing: Expense | null
  saving: boolean
  /** Spanish sentence of the server (400/404), shown verbatim. */
  serverError: string | null
  onCancel: () => void
  onSubmit: (payload: ExpensePayload) => void
}

/**
 * Form of an expense (contract F3b): category, amount typed in dollars, day and
 * description. The amount is parsed to integer cents with integer arithmetic, and
 * the draft is checked with the sentences of the API before a request is sent, so
 * the person sees the message on the field instead of a rejected POST.
 */
export function ExpenseForm({
  today,
  editing,
  saving,
  serverError,
  onCancel,
  onSubmit,
}: ExpenseFormProps) {
  const [draft, setDraft] = useState<ExpenseDraft>(() => draftOf(editing, today))
  const [error, setError] = useState<{ field: ExpenseField; message: string } | null>(null)

  // Editing another row (or a new expense) always starts from that row's values.
  useEffect(() => {
    setDraft(draftOf(editing, today))
    setError(null)
  }, [editing, today])

  const idBase = editing ? `expense-edit-${editing.id}` : 'expense-new'
  const fieldError = (field: ExpenseField): string | null =>
    error?.field === field ? error.message : null

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    const check = validateExpenseDraft(draft, today)
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      return
    }
    setError(null)
    onSubmit(check.payload)
  }

  return (
    <form className="service-form" onSubmit={handleSubmit} noValidate>
      <div className="service-form__grid">
        <div className="field">
          <label htmlFor={`${idBase}-category`}>Categoría</label>
          <select
            id={`${idBase}-category`}
            className="select"
            value={draft.category}
            onChange={(event) => setDraft({ ...draft, category: event.target.value })}
            aria-invalid={fieldError('category') ? true : undefined}
            aria-describedby={fieldError('category') ? `${idBase}-category-error` : undefined}
            required
          >
            <option value="">Elegí una categoría</option>
            {EXPENSE_CATEGORIES.map((category) => (
              <option key={category} value={category}>
                {EXPENSE_CATEGORY_LABELS[category]}
              </option>
            ))}
          </select>
          {fieldError('category') ? (
            <p className="field__error" id={`${idBase}-category-error`} role="alert">
              {fieldError('category')}
            </p>
          ) : null}
        </div>

        <div className="field">
          <label htmlFor={`${idBase}-amount`}>Monto en dólares</label>
          <input
            id={`${idBase}-amount`}
            className="input"
            type="text"
            inputMode="decimal"
            autoComplete="off"
            placeholder="1250.00"
            value={draft.amount}
            onChange={(event) => setDraft({ ...draft, amount: event.target.value })}
            aria-invalid={fieldError('amount') ? true : undefined}
            aria-describedby={
              fieldError('amount') ? `${idBase}-amount-error` : `${idBase}-amount-hint`
            }
            required
          />
          {fieldError('amount') ? (
            <p className="field__error" id={`${idBase}-amount-error`} role="alert">
              {fieldError('amount')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-amount-hint`}>
              Hasta dos decimales: 12, 12.50 o 12,50. Sin separador de miles.
            </p>
          )}
        </div>

        <div className="field">
          <label htmlFor={`${idBase}-spent-on`}>Fecha del gasto</label>
          <input
            id={`${idBase}-spent-on`}
            className="input"
            type="date"
            max={today}
            value={draft.spent_on}
            onChange={(event) => setDraft({ ...draft, spent_on: event.target.value })}
            aria-invalid={fieldError('spent_on') ? true : undefined}
            aria-describedby={
              fieldError('spent_on') ? `${idBase}-spent-on-error` : `${idBase}-spent-on-hint`
            }
            required
          />
          {fieldError('spent_on') ? (
            <p className="field__error" id={`${idBase}-spent-on-error`} role="alert">
              {fieldError('spent_on')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-spent-on-hint`}>
              Hoy en la zona horaria del salón: {today}.
            </p>
          )}
        </div>

        <div className="field service-form__wide">
          <label htmlFor={`${idBase}-description`}>Descripción (opcional)</label>
          <textarea
            id={`${idBase}-description`}
            className="textarea"
            rows={2}
            value={draft.description}
            onChange={(event) => setDraft({ ...draft, description: event.target.value })}
            aria-invalid={fieldError('description') ? true : undefined}
            aria-describedby={
              fieldError('description') ? `${idBase}-description-error` : `${idBase}-description-hint`
            }
          />
          {fieldError('description') ? (
            <p className="field__error" id={`${idBase}-description-error`} role="alert">
              {fieldError('description')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-description-hint`}>
              Hasta {MAX_DESCRIPTION_LENGTH} caracteres.
            </p>
          )}
        </div>
      </div>

      {serverError ? (
        <p className="notice notice--error" role="alert">
          {serverError}
        </p>
      ) : null}

      <div className="page-head__actions">
        <button type="submit" className="btn btn--wine" disabled={saving}>
          {saving ? 'Guardando…' : editing ? 'Guardar cambios' : 'Guardar gasto'}
        </button>
        {editing ? (
          <button type="button" className="btn btn--quiet" onClick={onCancel} disabled={saving}>
            Cancelar
          </button>
        ) : null}
      </div>
    </form>
  )
}
