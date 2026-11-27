/**
 * Net profit of the salon (contract F3b, «Contrato F3b (gastos y ganancia neta)»).
 * `/panel/ganancias` renders what these helpers return: the cards of revenue,
 * expenses, paid payroll and net profit, the bars of every local day and the
 * breakdown of the expenses by category.
 *
 * Money is integer cents everywhere and the net can be negative. The response
 * shape is read defensively: a day the payload left empty becomes a zero day, a
 * net that does not add up is derived from its components and the totals fall
 * back to the exact sum of the days, so the footer and the bars always agree.
 */

import { request } from '../../../lib/api.ts'
import { EXPENSE_CATEGORY_LABELS, type ExpenseCategory } from './expenses.ts'
import { formatCents } from '../../../lib/money.ts'
import {
  INVALID_PERIOD_MESSAGE,
  isValidPayrollDate,
  payPeriodLabel,
  type PayPeriodKind,
} from '../../team/lib/payroll.ts'
import { isManager, type Role } from '../../../lib/permissions.ts'

/** Periods the server derives in the zone of the salon (D7: `day` is new). */
export type ProfitKind = 'day' | 'week' | 'month'

/** The three kinds in the order the selector shows them. */
export const PROFIT_KINDS: readonly ProfitKind[] = ['day', 'week', 'month']

/** Spanish labels of the selector. */
export const PROFIT_KIND_LABELS: Record<ProfitKind, string> = {
  day: 'Día',
  week: 'Semana',
  month: 'Mes',
}

/**
 * Fixed notice of the report (D1): the profit counts payroll only when a run is
 * PAID, on the local day of `paid_at`; open runs and unscheduled commissions do
 * not count.
 */
export const PROFIT_PAYROLL_NOTICE =
  'Incluye la nómina ya pagada; las liquidaciones abiertas y las comisiones sin liquidar no cuentan'

/** Sentence of the API when `kind` or `date` is not valid, shared with the payroll. */
export const INVALID_PROFIT_PERIOD_MESSAGE = INVALID_PERIOD_MESSAGE

/** Function name of every concept of the report; the net can be negative. */
export type ProfitTotals = {
  revenue_cents: number
  expenses_cents: number
  payroll_cents: number
  net_cents: number
}

/** One local day of the period; `days` carries them all, ascending, with zeros. */
export type ProfitDay = ProfitTotals & { date: string }

/** `{category, total_cents}`: only categories with a total greater than zero. */
export type ProfitCategoryTotal = {
  category: string
  total_cents: number
}

/** The period the server derived from `kind` + `date` (inclusive local days). */
export type ProfitPeriod = {
  kind: ProfitKind
  from: string
  to: string
}

/** `GET /salons/:id/profit?kind=&date=`. */
export type ProfitReport = {
  period: ProfitPeriod | null
  totals: ProfitTotals
  days: ProfitDay[]
  expenses_by_category: ProfitCategoryTotal[]
}

export type ProfitInputCheck =
  | { ok: true; kind: ProfitKind; date: string }
  | { ok: false; message: string }

/** One bar of the daily chart: height in percent, never money. */
export type ProfitDayBar = {
  date: string
  /** Short label of the day («2 nov»). */
  label: string
  /** Height of the bar, 0..100, relative to the best day. */
  height: number
  revenue_cents: number
  net_cents: number
}

/** One row of the breakdown by category. */
export type ProfitCategoryRow = {
  category: string
  label: string
  total_cents: number
  /** Width of the bar, 0..100, relative to the biggest category. */
  share: number
}

const MONTHS = [
  'enero',
  'febrero',
  'marzo',
  'abril',
  'mayo',
  'junio',
  'julio',
  'agosto',
  'septiembre',
  'octubre',
  'noviembre',
  'diciembre',
] as const

const MONTHS_SHORT = [
  'ene',
  'feb',
  'mar',
  'abr',
  'may',
  'jun',
  'jul',
  'ago',
  'sep',
  'oct',
  'nov',
  'dic',
] as const

type DateParts = { year: number; month: number; day: number }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Non-negative integer cents or `0`: negative, fractional and text values go. */
function unsignedCents(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : 0
}

/** Signed integer cents or `0`: the net of a day may be negative. */
function signedCents(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : 0
}

/** Real `YYYY-MM-DD` local day, or `null` (same rule as the payroll dates). */
function localDate(value: unknown): string | null {
  const raw = text(value)
  return isValidPayrollDate(raw) ? raw : null
}

function dateParts(value: string): DateParts | null {
  if (!isValidPayrollDate(value)) {
    return null
  }
  const [year = '', month = '', day = ''] = value.split('-')
  return { year: Number(year), month: Number(month), day: Number(day) }
}

export function isProfitKind(value: unknown): value is ProfitKind {
  return value === 'day' || value === 'week' || value === 'month'
}

/**
 * Kind + date of the selector. Both come from the form, so an unusable one is
 * refused with the sentence of the API instead of a request that would 400.
 */
export function checkProfitInput(kind: unknown, date: string): ProfitInputCheck {
  const value = typeof date === 'string' ? date.trim() : ''
  if (!isProfitKind(kind) || !isValidPayrollDate(value)) {
    return { ok: false, message: INVALID_PROFIT_PERIOD_MESSAGE }
  }
  return { ok: true, kind, date: value }
}

function normalizePeriod(raw: unknown): ProfitPeriod | null {
  if (!isRecord(raw) || !isProfitKind(raw['kind'])) {
    return null
  }
  const from = localDate(raw['from'])
  const to = localDate(raw['to'])
  if (from === null || to === null) {
    return null
  }
  return { kind: raw['kind'], from, to }
}

/**
 * One day of the curve. A missing or unusable component is `0` instead of a
 * guess; the net keeps its sign and, when the payload did not send a usable one,
 * it is derived as `revenue - expenses - payroll` (the identity of the contract).
 */
function normalizeDay(raw: unknown): ProfitDay | null {
  if (!isRecord(raw)) {
    return null
  }
  const date = localDate(raw['date'])
  if (date === null) {
    return null
  }
  const revenue = unsignedCents(raw['revenue_cents'])
  const expenses = unsignedCents(raw['expenses_cents'])
  const payroll = unsignedCents(raw['payroll_cents'])
  const net = raw['net_cents']
  return {
    date,
    revenue_cents: revenue,
    expenses_cents: expenses,
    payroll_cents: payroll,
    // A net of exactly zero is a real value: only a missing or unusable one is derived.
    net_cents: typeof net === 'number' && Number.isSafeInteger(net) ? net : revenue - expenses - payroll,
  }
}

/** Exact integer sum of a set of days; never a float. */
export function profitTotalsFromDays(days: readonly ProfitDay[]): ProfitTotals {
  let revenue = 0
  let expenses = 0
  let payroll = 0
  let net = 0
  for (const day of days) {
    revenue += unsignedCents(day.revenue_cents)
    expenses += unsignedCents(day.expenses_cents)
    payroll += unsignedCents(day.payroll_cents)
    net += signedCents(day.net_cents)
  }
  return {
    revenue_cents: revenue,
    expenses_cents: expenses,
    payroll_cents: payroll,
    net_cents: net,
  }
}

function readTotals(raw: unknown): ProfitTotals | null {
  if (!isRecord(raw)) {
    return null
  }
  const unsigned = (value: unknown): value is number =>
    typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
  const signed = (value: unknown): value is number =>
    typeof value === 'number' && Number.isSafeInteger(value)
  if (
    !unsigned(raw['revenue_cents']) ||
    !unsigned(raw['expenses_cents']) ||
    !unsigned(raw['payroll_cents']) ||
    !signed(raw['net_cents'])
  ) {
    return null
  }
  return {
    revenue_cents: raw['revenue_cents'],
    expenses_cents: raw['expenses_cents'],
    payroll_cents: raw['payroll_cents'],
    net_cents: raw['net_cents'],
  }
}

/**
 * Reads the report. A payload that is not an object is `null` (the page explains
 * it); everything else keeps its figures: days ascending without impossible
 * dates, totals of the server when they are integers and otherwise the exact sum
 * of the days, and only the categories with a positive total.
 */
export function normalizeProfit(raw: unknown): ProfitReport | null {
  if (!isRecord(raw)) {
    return null
  }
  const days: ProfitDay[] = []
  for (const row of Array.isArray(raw['days']) ? raw['days'] : []) {
    const day = normalizeDay(row)
    if (day) {
      days.push(day)
    }
  }
  days.sort((a, b) => (a.date === b.date ? 0 : a.date < b.date ? -1 : 1))

  const categories: ProfitCategoryTotal[] = []
  for (const row of Array.isArray(raw['expenses_by_category']) ? raw['expenses_by_category'] : []) {
    if (!isRecord(row)) {
      continue
    }
    const category = text(row['category'])
    const total = unsignedCents(row['total_cents'])
    if (category !== '' && total > 0) {
      categories.push({ category, total_cents: total })
    }
  }
  categories.sort((a, b) => (a.category === b.category ? 0 : a.category < b.category ? -1 : 1))

  return {
    period: normalizePeriod(raw['period']),
    totals: readTotals(raw['totals']) ?? profitTotalsFromDays(days),
    days,
    expenses_by_category: categories,
  }
}

/** `negative` when the salon lost money, `zero` when it broke even. */
export function profitNetSign(
  totals: Pick<ProfitTotals, 'net_cents'>,
): 'negative' | 'zero' | 'positive' {
  const net = signedCents(totals.net_cents)
  if (net < 0) {
    return 'negative'
  }
  return net === 0 ? 'zero' : 'positive'
}

/**
 * Net profit for reading: a loss always carries its sign (`-$450.00`) so it is
 * never mistaken for a win. Every amount goes through `formatCents`.
 */
export function formatProfitNet(value: number): string {
  return formatCents(signedCents(value))
}

/** «10 de noviembre de 2026» for a day; the payroll label for week and month. */
export function profitPeriodLabel(period: ProfitPeriod): string {
  if (period.kind === 'day') {
    const parts = dateParts(period.from)
    if (!parts) {
      return 'Período'
    }
    return `${parts.day} de ${MONTHS[parts.month - 1] ?? ''} de ${parts.year}`
  }
  return payPeriodLabel({ kind: period.kind as PayPeriodKind, from: period.from, to: period.to })
}

/** «2 nov»: the label under a bar. */
export function shortDateLabel(date: string): string {
  const parts = dateParts(date)
  if (!parts) {
    return ''
  }
  return `${parts.day} ${MONTHS_SHORT[parts.month - 1] ?? ''}`.trim()
}

/**
 * Percent of `value` against `max` (0..100), used for the height of a bar and the
 * width of a category row. It is a geometry number, not money: the amounts stay
 * integer cents.
 */
export function barShare(value: number, max: number): number {
  const top = unsignedCents(max)
  if (top <= 0) {
    return 0
  }
  const part = Math.min(unsignedCents(value), top)
  return Math.round((part * 1000) / top) / 10
}

/** Label of a category of the contract; an unknown code stays readable. */
export function profitDayBars(report: Pick<ProfitReport, 'days'>): ProfitDayBar[] {
  let max = 0
  for (const day of report.days) {
    const revenue = unsignedCents(day.revenue_cents)
    if (revenue > max) {
      max = revenue
    }
  }
  return report.days.map((day) => ({
    date: day.date,
    label: shortDateLabel(day.date),
    height: barShare(day.revenue_cents, max),
    revenue_cents: unsignedCents(day.revenue_cents),
    net_cents: signedCents(day.net_cents),
  }))
}

/**
 * Accessible name of one bar: the day written out plus both amounts, so the chart
 * is readable with the keyboard and with a screen reader without a tooltip.
 */
export function profitDayBarLabel(bar: ProfitDayBar): string {
  const parts = dateParts(bar.date)
  const day = parts ? `${parts.day} de ${MONTHS[parts.month - 1] ?? ''}` : bar.date
  return `${day}: ingresos ${formatCents(bar.revenue_cents)}, ganancia neta ${formatProfitNet(bar.net_cents)}`
}

/**
 * One bar per local day of the period, in the order of the server. A day without
 * movements keeps its zero height, so the curve never hides a day.
 */
export function profitCategoryLabel(category: string): string {
  return EXPENSE_CATEGORY_LABELS[category as ExpenseCategory] ?? category
}

/** Breakdown rows in the order the server sent them, with their bar width. */
export function profitCategoryRows(
  report: Pick<ProfitReport, 'expenses_by_category'>,
): ProfitCategoryRow[] {
  let max = 0
  for (const row of report.expenses_by_category) {
    const total = unsignedCents(row.total_cents)
    if (total > max) {
      max = total
    }
  }
  return report.expenses_by_category.map((row) => ({
    category: row.category,
    label: profitCategoryLabel(row.category),
    total_cents: unsignedCents(row.total_cents),
    share: barShare(row.total_cents, max),
  }))
}

/** Owner and administration see the profit; the rest gets 403 from the server (D5). */
export function canViewProfit(role: Role | null | undefined): boolean {
  return isManager(role)
}

// --- API client (frozen shape of GET /profit) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

/** `GET /salons/:id/profit?kind=&date=`: `date` is any day inside the period. */
export async function fetchProfit(
  salonId: string,
  input: { kind: ProfitKind; date: string },
): Promise<ProfitReport | null> {
  const query = `?kind=${encodeURIComponent(input.kind)}&date=${encodeURIComponent(input.date)}`
  return normalizeProfit(await request<unknown>(salonPath(salonId, `/profit${query}`)))
}
