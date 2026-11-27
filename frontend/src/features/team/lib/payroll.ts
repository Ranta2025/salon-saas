/**
 * Payroll and commissions (contract F3, S-F3.2-5). Pure rules plus the thin API
 * client: `/panel/nomina`, `/panel/mis-comisiones` and the pay model of every
 * member in `/panel/equipo` render what these return; the server stays the
 * authority on amounts and permissions.
 *
 * Money is integer cents everywhere. A malformed, negative or fractional number
 * is dropped to `0` instead of being guessed, and sums never pass through a
 * float. The three response shapes of the contract are read defensively so a
 * missing field never breaks a page nor invents a number.
 */

import { request } from '../../../lib/api.ts'
import { formatCents } from '../../../lib/money.ts'
import { isManager, type Role } from '../../../lib/permissions.ts'

/** Periods the server derives in the zone of the salon (D5). */
export type PayPeriodKind = 'week' | 'fortnight' | 'month'

/** The three kinds in the order the selector shows them. */
export const PAY_PERIOD_KINDS: readonly PayPeriodKind[] = ['week', 'fortnight', 'month']

/** Spanish labels of the selector and of the model of payment. */
export const PAY_PERIOD_KIND_LABELS: Record<PayPeriodKind, string> = {
  week: 'Semana',
  fortnight: 'Quincena',
  month: 'Mes',
}

/** Word of a period inside a sentence («Fijo $500.00 por semana»). */
const PERIOD_WORDS: Record<PayPeriodKind, string> = {
  week: 'semana',
  fortnight: 'quincena',
  month: 'mes',
}

/** Sentence of the API when the period input is not valid (contract S-F3.2-5). */
export const INVALID_PERIOD_MESSAGE = 'Período inválido'

/** Badge of a line whose commission was estimated with the current rate (D1). */
export const ESTIMATED_BADGE = 'estimado'

/** Plain explanation of the `estimated` flag, verbatim from the contract. */
export const ESTIMATED_NOTICE =
  'Incluye ventas anteriores a la nómina: su comisión se calcula con el porcentaje actual del servicio'

/** Notice of a period still in course (D5): the figures may change. */
export const OPEN_PERIOD_NOTICE = 'Período en curso: las cifras pueden cambiar'

/** Mark of a person who no longer belongs to the team (F2g2). */
export const FORMER_MEMBER_LABEL = 'Dado de baja'

/**
 * Explanation under the model of payment (S-F3.2-5): mixed is fixed + commission
 * and the fixed amount is paid only when the liquidated period kind equals the
 * fixed period of the member.
 */
export const PAY_MODEL_EXPLANATION =
  'Mixto = fijo + comisión. El monto fijo se paga una sola vez y solo cuando el período que se liquida coincide con el período fijo de la persona; la comisión se suma siempre.'

/** One member of `GET /salons/:id/pay-models` (D3). */
export type PayModel = {
  user_id: string
  name: string
  role: Role
  active: boolean
  fixed_cents: number
  fixed_period: PayPeriodKind
  commission_enabled: boolean
}

/** The period the server derived from `kind` + `date` (D5). */
export type PayPeriod = {
  kind: PayPeriodKind
  /** First local day, inclusive (`YYYY-MM-DD`). */
  from: string
  /** Last local day, inclusive (`YYYY-MM-DD`). */
  to: string
  /** `false` for the period in course, which the contract allows to preview. */
  closed: boolean
}

/** One professional of the preview (`POST /salons/:id/payroll/preview`). */
export type PayrollLine = {
  user_id: string
  name: string
  active: boolean
  sales_count: number
  sales_total_cents: number
  commission_cents: number
  fixed_cents: number
  total_cents: number
  estimated: boolean
}

export type PayrollTotals = {
  commission_cents: number
  fixed_cents: number
  total_cents: number
}

/** Sales of the preview: the count and the sold amount, both integers. */
export type PayrollSalesTotals = {
  sales_count: number
  sales_total_cents: number
}

export type PayrollPreview = {
  period: PayPeriod | null
  lines: PayrollLine[]
  totals: PayrollTotals
}

/** `GET /salons/:id/payroll/me` (D7): only the figures of the caller. */
export type MyCommissions = {
  period: PayPeriod | null
  sales_count: number
  sales_total_cents: number
  commission_cents: number
  estimated: boolean
}

/** Result of validating the selector of a period. */
export type PayrollInputCheck =
  | { ok: true; kind: PayPeriodKind; date: string }
  | { ok: false; message: string }

const ROLES: readonly Role[] = ['owner', 'admin', 'professional', 'reception']

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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Integer cents or `0`: negative, fractional, infinite and non-number values go. */
function cents(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
}

/** Integer count or `0` (sales, quantities). */
function count(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
    ? value
    : 0
}

function role(value: unknown): Role {
  return ROLES.includes(value as Role) ? (value as Role) : 'professional'
}

/** `week` / `fortnight` / `month`; anything else is not a period of the contract. */
export function isPayPeriodKind(value: unknown): value is PayPeriodKind {
  return value === 'week' || value === 'fortnight' || value === 'month'
}

function periodKind(value: unknown, fallback: PayPeriodKind = 'month'): PayPeriodKind {
  return isPayPeriodKind(value) ? value : fallback
}

type DateParts = { year: number; month: number; day: number }

/**
 * Reads a `YYYY-MM-DD` local date. Only a real calendar day is accepted: the
 * round trip through `Date.UTC` rejects `2026-02-30` instead of rolling it over.
 */
function dateParts(value: unknown): DateParts | null {
  const raw = text(value)
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(raw)
  if (!match) {
    return null
  }
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  if (month < 1 || month > 12 || day < 1 || day > 31) {
    return null
  }
  const check = new Date(Date.UTC(year, month - 1, day))
  if (
    check.getUTCFullYear() !== year ||
    check.getUTCMonth() !== month - 1 ||
    check.getUTCDate() !== day
  ) {
    return null
  }
  return { year, month, day }
}

/** True for a real `YYYY-MM-DD` local day. */
export function isValidPayrollDate(value: string): boolean {
  return dateParts(value) !== null
}

function dateValue(value: unknown): string | null {
  return dateParts(value) === null ? null : text(value)
}

/**
 * Kind + date of the selector. Both come from the form, so a bad one is refused
 * with the sentence of the API instead of a request that would 400.
 */
export function checkPayrollInput(kind: unknown, date: string): PayrollInputCheck {
  if (!isPayPeriodKind(kind) || !isValidPayrollDate(date)) {
    return { ok: false, message: INVALID_PERIOD_MESSAGE }
  }
  return { ok: true, kind, date: date.trim() }
}

// --- pay models (D3, shape of GET /pay-models and PUT /pay-model) ---

function normalizePayModelRow(value: unknown): PayModel | null {
  if (!isRecord(value)) {
    return null
  }
  const userId = text(value['user_id'])
  if (userId === '') {
    return null
  }
  return {
    user_id: userId,
    name: text(value['name']) || userId,
    role: role(value['role']),
    // A payload without the flag counts as active (an older server does not send it).
    active: value['active'] !== false,
    fixed_cents: cents(value['fixed_cents']),
    fixed_period: periodKind(value['fixed_period']),
    // No row means "only commission" (D3), so a missing flag is `true`.
    commission_enabled: value['commission_enabled'] !== false,
  }
}

/** Reads `{pay_models:[…]}` (or a bare array) and drops rows without `user_id`. */
export function normalizePayModels(raw: unknown): PayModel[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['pay_models'])
      ? raw['pay_models']
      : []
  const models: PayModel[] = []
  for (const row of rows) {
    const model = normalizePayModelRow(row)
    if (model) {
      models.push(model)
    }
  }
  return models
}

/** Reads the answer of `PUT /members/:uid/pay-model`: the object, bare or wrapped. */
export function normalizePayModel(raw: unknown): PayModel | null {
  if (!isRecord(raw)) {
    return null
  }
  return normalizePayModelRow(isRecord(raw['pay_model']) ? raw['pay_model'] : raw)
}

// --- the period (D5) ---

/** Reads `{kind, from, to, closed}` of the two payroll endpoints. */
export function normalizePayPeriod(raw: unknown): PayPeriod | null {
  if (!isRecord(raw) || !isPayPeriodKind(raw['kind'])) {
    return null
  }
  const from = dateValue(raw['from'])
  const to = dateValue(raw['to'])
  if (from === null || to === null) {
    return null
  }
  return { kind: raw['kind'], from, to, closed: raw['closed'] === true }
}

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

/**
 * Spanish label of a period (S-F3.2-5):
 * «Semana del 3 al 9 de noviembre», «Quincena del 1 al 15 de noviembre»,
 * «Noviembre de 2026». A range across months (or years) names both edges; an
 * unusable range prints «Período» instead of inventing a day.
 */
export function payPeriodLabel(period: {
  kind: PayPeriodKind
  from: string
  to: string
}): string {
  const from = dateParts(period.from)
  const to = dateParts(period.to)
  if (!from || !to) {
    return 'Período'
  }
  const kind = isPayPeriodKind(period.kind) ? period.kind : 'week'
  if (kind === 'month' && from.year === to.year && from.month === to.month) {
    return `${capitalize(MONTHS[from.month - 1] ?? '')} de ${from.year}`
  }
  const prefix = PAY_PERIOD_KIND_LABELS[kind]
  const fromMonth = MONTHS[from.month - 1] ?? ''
  const toMonth = MONTHS[to.month - 1] ?? ''
  if (from.year === to.year && from.month === to.month) {
    return `${prefix} del ${from.day} al ${to.day} de ${fromMonth}`
  }
  if (from.year === to.year) {
    return `${prefix} del ${from.day} de ${fromMonth} al ${to.day} de ${toMonth}`
  }
  return `${prefix} del ${from.day} de ${fromMonth} de ${from.year} al ${to.day} de ${toMonth} de ${to.year}`
}

// --- preview (POST /payroll/preview) ---

function normalizePayrollLine(value: unknown): PayrollLine | null {
  if (!isRecord(value)) {
    return null
  }
  const userId = text(value['user_id'])
  if (userId === '') {
    return null
  }
  const commission = cents(value['commission_cents'])
  const fixed = cents(value['fixed_cents'])
  const total = cents(value['total_cents'])
  return {
    user_id: userId,
    name: text(value['name']) || userId,
    active: value['active'] !== false,
    sales_count: count(value['sales_count']),
    sales_total_cents: cents(value['sales_total_cents']),
    commission_cents: commission,
    fixed_cents: fixed,
    // A missing or unusable total is the integer sum of its two concepts.
    total_cents: total > 0 || commission + fixed === 0 ? total : commission + fixed,
    estimated: value['estimated'] === true,
  }
}

/** Integer sums of a set of lines; never a float. */
export function payrollTotals(lines: readonly PayrollLine[]): PayrollTotals {
  let commission = 0
  let fixed = 0
  let total = 0
  for (const line of lines) {
    commission += cents(line.commission_cents)
    fixed += cents(line.fixed_cents)
    total += cents(line.total_cents)
  }
  return { commission_cents: commission, fixed_cents: fixed, total_cents: total }
}

function readTotals(raw: unknown): PayrollTotals | null {
  if (!isRecord(raw)) {
    return null
  }
  const commission = raw['commission_cents']
  const fixed = raw['fixed_cents']
  const total = raw['total_cents']
  const valid = (value: unknown): value is number =>
    typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
  if (!valid(commission) || !valid(fixed) || !valid(total)) {
    return null
  }
  return { commission_cents: commission, fixed_cents: fixed, total_cents: total }
}

/**
 * Reads the preview. The server totals win when they are integers; otherwise
 * they are recomputed from the lines so the table and its footer always agree.
 */
export function normalizePayrollPreview(raw: unknown): PayrollPreview {
  if (!isRecord(raw)) {
    return { period: null, lines: [], totals: payrollTotals([]) }
  }
  const rows = Array.isArray(raw['lines']) ? raw['lines'] : []
  const lines: PayrollLine[] = []
  for (const row of rows) {
    const line = normalizePayrollLine(row)
    if (line) {
      lines.push(line)
    }
  }
  return {
    period: normalizePayPeriod(raw['period']),
    lines,
    totals: readTotals(raw['totals']) ?? payrollTotals(lines),
  }
}

/** Integer sums of the sales of a set of lines; never a float. */
export function payrollSalesTotals(lines: readonly PayrollLine[]): PayrollSalesTotals {
  let sales = 0
  let sold = 0
  for (const line of lines) {
    sales += count(line.sales_count)
    sold += cents(line.sales_total_cents)
  }
  return { sales_count: sales, sales_total_cents: sold }
}

/**
 * Value of the fixed amount for an `<input>`: `150000` → `1500.00`, built with
 * integer arithmetic (never `cents / 100`). Negative or fractional cents are 0.
 */
export function amountInputFromCents(value: number): string {
  const safe = cents(value)
  const units = Math.trunc(safe / 100)
  const fraction = safe % 100
  return `${units}.${String(fraction).padStart(2, '0')}`
}

// --- my commissions (GET /payroll/me) ---

/** Reads the answer of `GET /payroll/me`; every missing field falls back to `0`. */
export function normalizeMyCommissions(raw: unknown): MyCommissions {
  if (!isRecord(raw)) {
    return {
      period: null,
      sales_count: 0,
      sales_total_cents: 0,
      commission_cents: 0,
      estimated: false,
    }
  }
  return {
    period: normalizePayPeriod(raw['period']),
    sales_count: count(raw['sales_count']),
    sales_total_cents: cents(raw['sales_total_cents']),
    commission_cents: cents(raw['commission_cents']),
    estimated: raw['estimated'] === true,
  }
}

// --- who sees what (D7) ---

/** Owner and admin manage pay models and see the payroll of everybody. */
export function canManagePayroll(role: Role | null | undefined): boolean {
  return isManager(role)
}

/** Owner, admin and professional see their own commissions; reception does not. */
export function canViewOwnCommissions(role: Role | null | undefined): boolean {
  return isManager(role) || role === 'professional'
}

// --- optimistic save of a pay model and its exact rollback ---

/**
 * Optimistically paints the new model of one member (S-F3.2-5). The list it
 * receives is never mutated, so the caller can keep the previous row as the
 * exact rollback.
 */
export function applyPayModel(
  models: readonly PayModel[],
  userId: string,
  patch: Partial<Pick<PayModel, 'fixed_cents' | 'fixed_period' | 'commission_enabled'>>,
): PayModel[] {
  return models.map((model) => (model.user_id === userId ? { ...model, ...patch } : model))
}

/** Exact rollback: the row goes back to the snapshot the caller kept. */
export function restorePayModel(models: readonly PayModel[], previous: PayModel): PayModel[] {
  return models.map((model) => (model.user_id === previous.user_id ? previous : model))
}

// --- copy of the model, read-only for the ex-team ---

/** «Fijo $1,500.00 por mes + comisión», «Solo comisión»… in integer cents. */
export function payModelSummary(model: PayModel): string {
  const fixed = cents(model.fixed_cents)
  const word = PERIOD_WORDS[periodKind(model.fixed_period)]
  if (fixed > 0 && model.commission_enabled) {
    return `Fijo ${formatCents(fixed)} por ${word} + comisión`
  }
  if (fixed > 0) {
    return `Fijo ${formatCents(fixed)} por ${word}, sin comisión`
  }
  if (model.commission_enabled) {
    return 'Solo comisión'
  }
  return 'Sin pago fijo ni comisión'
}

// --- API client (contract F3, shapes of the F3 contract block) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

export async function fetchPayModels(salonId: string): Promise<PayModel[]> {
  return normalizePayModels(await request<unknown>(salonPath(salonId, '/pay-models')))
}

/**
 * `PUT /salons/:id/members/:uid/pay-model` (D3). The full model travels because
 * the endpoint replaces it; a 400/404 keeps the Spanish sentence verbatim.
 */
export async function setMemberPayModel(
  salonId: string,
  userId: string,
  model: Pick<PayModel, 'fixed_cents'> &
    Partial<Pick<PayModel, 'fixed_period' | 'commission_enabled'>>,
): Promise<PayModel> {
  const body = await request<unknown>(
    salonPath(salonId, `/members/${encodeURIComponent(userId)}/pay-model`),
    {
      method: 'PUT',
      body: {
        fixed_cents: cents(model.fixed_cents),
        fixed_period: periodKind(model.fixed_period),
        commission_enabled: model.commission_enabled !== false,
      },
    },
  )
  const saved = normalizePayModel(body)
  if (!saved) {
    throw new Error('El servidor guardó el modelo de pago pero no devolvió sus datos.')
  }
  return saved
}

/** `POST /salons/:id/payroll/preview`; a 400 «Período inválido» is shown verbatim. */
export async function previewPayroll(
  salonId: string,
  input: { kind: PayPeriodKind; date: string },
): Promise<PayrollPreview> {
  return normalizePayrollPreview(
    await request<unknown>(salonPath(salonId, '/payroll/preview'), {
      method: 'POST',
      body: { kind: input.kind, date: input.date },
    }),
  )
}

/** `GET /salons/:id/payroll/me?kind=&date=`: only the figures of the caller. */
export async function fetchMyCommissions(
  salonId: string,
  input: { kind: PayPeriodKind; date: string },
): Promise<MyCommissions> {
  const query = `?kind=${encodeURIComponent(input.kind)}&date=${encodeURIComponent(input.date)}`
  return normalizeMyCommissions(
    await request<unknown>(salonPath(salonId, `/payroll/me${query}`)),
  )
}
