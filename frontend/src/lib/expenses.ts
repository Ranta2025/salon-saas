/**
 * Expenses of the salon (contract F3b, «Contrato F3b (gastos y ganancia neta)»).
 * Pure rules plus the thin API client: `/panel/gastos` renders what these return
 * and the server stays the authority on amounts, dates and permissions.
 *
 * Money is integer cents everywhere: the text a person types is turned into cents
 * with integer arithmetic only, so `12.50` never passes through a float. The
 * response shapes of the contract are read defensively, so a missing field never
 * breaks the page nor invents a number.
 */

import { ApiError, request } from './api.ts'
import { addDaysToDateValue } from './datetime.ts'
import { amountInputFromCents, isValidPayrollDate } from './payroll.ts'
import { isManager, type Role } from './permissions.ts'

/** Fixed categories of the contract (D3); the order is the one of the selector. */
export type ExpenseCategory = 'rent' | 'supplies' | 'utilities' | 'other'

export const EXPENSE_CATEGORIES: readonly ExpenseCategory[] = [
  'rent',
  'supplies',
  'utilities',
  'other',
]

export const EXPENSE_CATEGORY_LABELS: Record<ExpenseCategory, string> = {
  rent: 'Alquiler',
  supplies: 'Insumos',
  utilities: 'Servicios',
  other: 'Otros',
}

/** Cap of one amount in cents (CHECK of the base, D3). */
export const MAX_EXPENSE_CENTS = 2_000_000_000

/** First day an expense may have (D3). */
export const MIN_EXPENSE_DATE = '2000-01-01'

/** Characters of a description, counted per character and not per byte (D3). */
export const MAX_DESCRIPTION_LENGTH = 200

/** Days of the default range of the list: today and the 29 days before it. */
export const DEFAULT_RANGE_DAYS = 30

/** Longest range the list accepts (contract of `GET /expenses`). */
export const MAX_RANGE_DAYS = 366

/** Sentence of the API when an amount is not positive, reused before sending it. */
export const ZERO_AMOUNT_MESSAGE = 'El monto debe ser mayor a cero'

/** Sentence of the API when an amount passes the cap of the base. */
export const MAX_AMOUNT_MESSAGE = 'El monto no puede superar los 2000000000 centavos'

/** Category outside the four of the contract. */
export const INVALID_CATEGORY_MESSAGE =
  'Categoría inválida: usá alquiler, insumos, servicios u otros'

/** A date that is not a real `YYYY-MM-DD` local day. */
export const INVALID_DATE_MESSAGE = 'Fecha inválida: usá AAAA-MM-DD'

/** An expense dated after today in the zone of the salon. */
export const FUTURE_DATE_MESSAGE = 'La fecha del gasto no puede ser futura'

/** An expense dated before 2000-01-01. */
export const OLD_DATE_MESSAGE = 'La fecha del gasto es demasiado antigua'

/** Description longer than 200 characters. */
export const LONG_DESCRIPTION_MESSAGE = 'La descripción no puede superar los 200 caracteres'

/** Sentence of the API when a create lacks one of the three mandatory keys. */
export const MISSING_FIELDS_CREATE_MESSAGE = 'Faltan datos del gasto: categoría, monto y fecha'

/** Sentence of the API when a replace lacks one of the four keys. */
export const MISSING_FIELDS_UPDATE_MESSAGE =
  'Faltan datos del gasto: categoría, monto, fecha y descripción'

/** Sentence of the API when the range of the list is unusable. */
export const INVALID_RANGE_MESSAGE =
  'Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días'

/** Copy of the list when the server cut it at 500 rows. */
export const TRUNCATED_NOTICE =
  'Se muestran los 500 gastos más recientes del rango: acota las fechas para ver el resto.'

/** Client-side sentence for text the parser cannot read as an amount. */
export const INVALID_AMOUNT_MESSAGE = 'Escribí el monto en dólares, por ejemplo 12.50'

/** The confirmation of a delete says it cannot be undone (contract F3b). */
export const DELETE_EXPENSE_QUESTION = '¿Borrar este gasto?'
export const DELETE_EXPENSE_NOTE = 'Se borra del todo y esta acción no se puede deshacer.'

/** One expense of the salon (`expense` of the frozen shape). */
export type Expense = {
  id: string
  category: ExpenseCategory
  /** Integer cents, 1..`MAX_EXPENSE_CENTS`. */
  amount_cents: number
  /** Local day of the salon (`YYYY-MM-DD`, D2/D3). */
  spent_on: string
  description: string
  created_at: string | null
  created_by: string
}

/** Body of a create (`description` optional) and of a replace (all four keys). */
export type ExpensePayload = {
  category: ExpenseCategory
  amount_cents: number
  spent_on: string
  description: string
}

/** `GET /expenses?from=&to=`: the rows of the range and whether it was cut. */
export type ExpensePage = {
  expenses: Expense[]
  truncated: boolean
}

/** Inclusive range of local days of the list. */
export type ExpenseRange = { from: string; to: string }

export type ExpenseField = 'category' | 'amount' | 'spent_on' | 'description'

/** What the form holds: text as typed, numbers still unparsed. */
export type ExpenseDraft = {
  category: string
  amount: string
  spent_on: string
  description: string
}

export type ExpenseDraftCheck =
  | { ok: true; payload: ExpensePayload }
  | { ok: false; field: ExpenseField; message: string }

export type ExpenseRangeCheck =
  | { ok: true; from: string; to: string }
  | { ok: false; message: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Non-negative integer cents or `0` (a malformed amount is never guessed). */
function unsignedCents(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
}

/** Real `YYYY-MM-DD` local day as typed by the server, or `null`. */
function localDate(value: unknown): string | null {
  const raw = text(value)
  return isValidPayrollDate(raw) ? raw : null
}

export function isExpenseCategory(value: unknown): value is ExpenseCategory {
  return EXPENSE_CATEGORIES.includes(value as ExpenseCategory)
}

/**
 * Amount typed by a person → integer cents, without a float in between.
 * Accepts `12`, `12,5`, `12.50` and `$1 250` (a single separator with one or two
 * decimals) and refuses everything else: an empty field, `abc`, a negative
 * number, more than two decimals, thousands separators or two separators.
 */
export function parseExpenseAmountToCents(input: string): number | null {
  const cleaned =
    typeof input === 'string'
      ? input
          .replace(/\$/g, '')
          .trim()
      : ''
  const match = /^(\d+)(?:[.,](\d{1,2}))?$/.exec(cleaned)
  if (!match) {
    return null
  }
  const units = Number(match[1])
  if (!Number.isSafeInteger(units)) {
    return null
  }
  const fraction = (match[2] ?? '').padEnd(2, '0')
  const cents = units * 100 + Number(fraction === '' ? '0' : fraction)
  return Number.isSafeInteger(cents) ? cents : null
}

/**
 * Value of the amount field for an edit form (`125050` → `1250.50`), built with
 * the integer arithmetic of the payroll inputs instead of `cents / 100`.
 */
export function expenseAmountInputFromCents(value: number): string {
  return amountInputFromCents(value)
}

/**
 * Reads the form before sending it, so the person sees the sentence of the API on
 * the field that is wrong instead of a request that would 400. `today` is the
 * current local day of the SALON (see `todayInTimeZone`); an empty one skips the
 * future check and leaves it to the server.
 */
export function validateExpenseDraft(draft: ExpenseDraft, today: string): ExpenseDraftCheck {
  const category = draft.category.trim()
  const amountText = draft.amount.trim()
  const spentOn = draft.spent_on.trim()
  const description = draft.description.trim()

  if (category === '' || amountText === '' || spentOn === '') {
    const field: ExpenseField =
      category === '' ? 'category' : amountText === '' ? 'amount' : 'spent_on'
    return { ok: false, field, message: MISSING_FIELDS_CREATE_MESSAGE }
  }
  if (!isExpenseCategory(category)) {
    return { ok: false, field: 'category', message: INVALID_CATEGORY_MESSAGE }
  }
  const amount = parseExpenseAmountToCents(amountText)
  if (amount === null) {
    return { ok: false, field: 'amount', message: INVALID_AMOUNT_MESSAGE }
  }
  if (amount <= 0) {
    return { ok: false, field: 'amount', message: ZERO_AMOUNT_MESSAGE }
  }
  if (amount > MAX_EXPENSE_CENTS) {
    return { ok: false, field: 'amount', message: MAX_AMOUNT_MESSAGE }
  }
  if (!isValidPayrollDate(spentOn)) {
    return { ok: false, field: 'spent_on', message: INVALID_DATE_MESSAGE }
  }
  if (spentOn < MIN_EXPENSE_DATE) {
    return { ok: false, field: 'spent_on', message: OLD_DATE_MESSAGE }
  }
  const todayValue = today.trim()
  if (todayValue !== '' && spentOn > todayValue) {
    return { ok: false, field: 'spent_on', message: FUTURE_DATE_MESSAGE }
  }
  if (description.length > MAX_DESCRIPTION_LENGTH) {
    return { ok: false, field: 'description', message: LONG_DESCRIPTION_MESSAGE }
  }

  return {
    ok: true,
    payload: { category, amount_cents: amount, spent_on: spentOn, description },
  }
}

/** Body of `POST /expenses`: the description travels only when it has text. */
export function expenseCreateBody(payload: ExpensePayload): {
  category: ExpenseCategory
  amount_cents: number
  spent_on: string
  description?: string
} {
  const body: {
    category: ExpenseCategory
    amount_cents: number
    spent_on: string
    description?: string
  } = {
    category: payload.category,
    amount_cents: unsignedCents(payload.amount_cents),
    spent_on: payload.spent_on,
  }
  const description = payload.description.trim()
  if (description !== '') {
    body.description = description
  }
  return body
}

/**
 * Body of `PUT /expenses/:eid`: the endpoint REPLACES the row, so the four keys
 * travel even when the description is empty (contract F3b).
 */
export function expenseUpdateBody(payload: ExpensePayload): {
  category: ExpenseCategory
  amount_cents: number
  spent_on: string
  description: string
} {
  return {
    category: payload.category,
    amount_cents: unsignedCents(payload.amount_cents),
    spent_on: payload.spent_on,
    description: payload.description.trim(),
  }
}

/** Default range of the list: today and the 29 local days before it. */
export function defaultExpenseRange(today: string): ExpenseRange {
  const to = today.trim()
  return { from: addDaysToDateValue(to, -(DEFAULT_RANGE_DAYS - 1)), to }
}

function dayCount(from: string, to: string): number {
  const start = Date.parse(`${from}T00:00:00Z`)
  const end = Date.parse(`${to}T00:00:00Z`)
  if (Number.isNaN(start) || Number.isNaN(end)) {
    return Number.NaN
  }
  return Math.round((end - start) / 86_400_000) + 1
}

/** Inclusive range of the list: two real days, `to` not before `from`, max 366. */
export function checkExpenseRange(from: string, to: string): ExpenseRangeCheck {
  const start = from.trim()
  const end = to.trim()
  if (!isValidPayrollDate(start) || !isValidPayrollDate(end)) {
    return { ok: false, message: INVALID_RANGE_MESSAGE }
  }
  const days = dayCount(start, end)
  if (!Number.isFinite(days) || days < 1 || days > MAX_RANGE_DAYS) {
    return { ok: false, message: INVALID_RANGE_MESSAGE }
  }
  return { ok: true, from: start, to: end }
}

/** Reads one `expense`; a row without id, category or a real `spent_on` is dropped. */
export function normalizeExpense(raw: unknown): Expense | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  if (id === '') {
    return null
  }
  if (!isExpenseCategory(raw['category'])) {
    return null
  }
  const spentOn = localDate(raw['spent_on'])
  if (spentOn === null) {
    return null
  }
  const createdAt = text(raw['created_at'])
  return {
    id,
    category: raw['category'],
    amount_cents: unsignedCents(raw['amount_cents']),
    spent_on: spentOn,
    description: text(raw['description']),
    created_at: createdAt === '' ? null : createdAt,
    created_by: text(raw['created_by']),
  }
}

/**
 * Reads `{expenses:[…], truncated}` (or a bare list) and orders it the way the
 * contract promises — `spent_on` descending, then `created_at` descending — so a
 * malformed order never shows an old expense on top. The sort is stable.
 */
export function normalizeExpenses(raw: unknown): ExpensePage {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['expenses'])
      ? (raw['expenses'] as unknown[])
      : []
  const expenses: Expense[] = []
  for (const row of rows) {
    const expense = normalizeExpense(row)
    if (expense) {
      expenses.push(expense)
    }
  }
  const ordered = expenses
    .map((expense, index) => ({ expense, index }))
    .sort((a, b) => {
      if (a.expense.spent_on !== b.expense.spent_on) {
        return a.expense.spent_on < b.expense.spent_on ? 1 : -1
      }
      const aCreated = a.expense.created_at ?? ''
      const bCreated = b.expense.created_at ?? ''
      if (aCreated !== bCreated) {
        return aCreated < bCreated ? 1 : -1
      }
      return a.index - b.index
    })
    .map((entry) => entry.expense)
  return {
    expenses: ordered,
    truncated: isRecord(raw) && raw['truncated'] === true,
  }
}

/** Owner and administration manage expenses; the rest gets 403 from the server (D5). */
export function canManageExpenses(role: Role | null | undefined): boolean {
  return isManager(role)
}

// --- API client (frozen shapes of the F3b contract) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function expensePath(salonId: string, expenseId: string): string {
  return salonPath(salonId, `/expenses/${encodeURIComponent(expenseId)}`)
}

function requireExpense(raw: unknown, message: string): Expense {
  const expense = normalizeExpense(raw)
  if (!expense) {
    throw new ApiError(200, message)
  }
  return expense
}

/** `GET /expenses?from=&to=`: inclusive range, newest first. */
export async function fetchExpenses(
  salonId: string,
  range: ExpenseRange,
): Promise<ExpensePage> {
  const query = `?from=${encodeURIComponent(range.from)}&to=${encodeURIComponent(range.to)}`
  return normalizeExpenses(await request<unknown>(salonPath(salonId, `/expenses${query}`)))
}

/** `POST /expenses` (201). A 400 keeps the Spanish sentence verbatim. */
export async function createExpense(
  salonId: string,
  payload: ExpensePayload,
): Promise<Expense> {
  return requireExpense(
    await request<unknown>(salonPath(salonId, '/expenses'), {
      method: 'POST',
      body: expenseCreateBody(payload),
    }),
    'El servidor guardó el gasto pero no devolvió sus datos.',
  )
}

/** `PUT /expenses/:eid`: replaces the row, so the four keys always travel. */
export async function updateExpense(
  salonId: string,
  expenseId: string,
  payload: ExpensePayload,
): Promise<Expense> {
  return requireExpense(
    await request<unknown>(expensePath(salonId, expenseId), {
      method: 'PUT',
      body: expenseUpdateBody(payload),
    }),
    'El servidor guardó el gasto pero no devolvió sus datos.',
  )
}

/** `DELETE /expenses/:eid`: 204, or a 404 «Gasto no encontrado» kept verbatim. */
export async function deleteExpense(salonId: string, expenseId: string): Promise<void> {
  await request<unknown>(expensePath(salonId, expenseId), { method: 'DELETE' })
}
