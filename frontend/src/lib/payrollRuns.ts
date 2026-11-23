/**
 * Payroll runs — «Liquidaciones» (contract F3, D6/D8 and S-F3.3-5).
 *
 * The shapes are frozen in `odd/tasks/fase6-roadmap.md`, section «Formas de F3.3»:
 * a run freezes the amounts of a finished period, an open run can be adjusted
 * line by line, paying it makes it immutable and the CSV is downloaded with the
 * bearer token (never a plain link, because the endpoint needs the Authorization
 * header).
 *
 * Everything here is pure and testable: money is integer cents (the adjustment
 * is signed), a malformed field is dropped to a safe value instead of being
 * guessed, and the server remains the authority on amounts, status and
 * permissions.
 */

import {
  ApiError,
  API_BASE,
  NetworkError,
  UNAUTHORIZED_EVENT,
  request,
} from './api.ts'
import { formatCents, parseAmountToCents } from './money.ts'
import {
  isPayPeriodKind,
  PAY_PERIOD_KIND_LABELS,
  payPeriodLabel,
  type PayPeriod,
  type PayPeriodKind,
} from './payroll.ts'
import { isManager, type Role } from './permissions.ts'
import { clearToken, getToken } from './session.ts'

/** Lifecycle of a run (D6): open runs can change, paid runs cannot. */
export type PayrollRunStatus = 'open' | 'paid'

export const RUN_STATUS_LABELS: Record<PayrollRunStatus, string> = {
  open: 'Abierta',
  paid: 'Pagada',
}

/** Label of the button that freezes the previewed period (S-F3.3-5). */
export const LIQUIDATE_ACTION = 'Liquidar este período'

/** Sentence of the API when the period is still running (POST /payroll/runs). */
export const PERIOD_NOT_CLOSED_MESSAGE =
  'El período todavía no terminó: liquidá cuando haya pasado'

/** Reason shown when there is no previewed period to liquidate. */
export const NO_PERIOD_MESSAGE = 'Elegí un período para liquidar'

/** Notice of a paid run: the contract makes it immutable. */
export const PAID_RUN_NOTICE =
  'Esta liquidación está pagada: no se puede editar ni borrar.'

/** Confirmation before paying (S-F3.3-5: it says the run becomes frozen). */
export const PAY_RUN_CONFIRM_QUESTION = '¿Marcar esta liquidación como pagada?'
export const PAY_RUN_CONFIRM_NOTE =
  'La liquidación queda congelada: no se va a poder editar ni borrar.'

/** Confirmation before deleting an open run. */
export const DELETE_RUN_CONFIRM_QUESTION = '¿Borrar esta liquidación?'
export const DELETE_RUN_CONFIRM_NOTE =
  'Se liberan los días del período para poder liquidarlo de nuevo. Esta acción no se puede deshacer.'

/** Client-side refusal of an amount the amount parser cannot read. */
export const INVALID_ADJUSTMENT_MESSAGE =
  'Escribe un ajuste válido en dólares, por ejemplo -500.00'

/** Polite copy of a failed download (S-F3.3-5). */
export const EXPORT_ERROR_MESSAGE =
  'No pudimos descargar el CSV. Revisa tu conexión e intenta de nuevo.'

/** Copy of the export button and of the empty list of runs. */
export const EXPORT_ACTION = 'Exportar CSV'
export const EMPTY_RUNS_MESSAGE =
  'Todavía no hay liquidaciones. Liquidá un período terminado para congelar sus montos.'

/** One line of a run: the frozen amounts plus the signed adjustment (D6). */
export type PayrollRunLine = {
  user_id: string
  name: string
  active: boolean
  sales_count: number
  sales_total_cents: number
  commission_cents: number
  fixed_cents: number
  adjustment_cents: number
  adjustment_note: string
  total_cents: number
  estimated: boolean
}

export type PayrollRunTotals = {
  commission_cents: number
  fixed_cents: number
  adjustment_cents: number
  total_cents: number
}

/** `GET /salons/:id/payroll/runs/:rid` and the answer of every mutation. */
export type PayrollRun = {
  id: string
  kind: PayPeriodKind
  /** First local day of the period, inclusive (`YYYY-MM-DD`). */
  from: string
  /** Last local day of the period, inclusive. */
  to: string
  status: PayrollRunStatus
  created_at: string | null
  paid_at: string | null
  totals: PayrollRunTotals
  lines: PayrollRunLine[]
}

/** One row of `GET /salons/:id/payroll/runs`. */
export type PayrollRunSummary = {
  id: string
  kind: PayPeriodKind
  from: string
  to: string
  status: PayrollRunStatus
  created_at: string | null
  paid_at: string | null
  lines_count: number
  totals: PayrollRunTotals
}

/** The downloaded CSV plus the safe name of the file. */
export type PayrollRunCsv = {
  blob: Blob
  filename: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Non-negative integer cents or `0` (commissions, fixed amounts, sold totals). */
function cents(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
}

/** Signed integer cents: the adjustment can be negative, a fraction cannot. */
function signedCents(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : 0
}

/** Non-negative integer count or `0` (sales, sales count, lines count). */
function count(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
}

function optionalText(value: unknown): string | null {
  const raw = text(value)
  return raw === '' ? null : raw
}

/** A real `YYYY-MM-DD` local day, or `null` (same rule as the preview). */
function localDate(value: unknown): string | null {
  const raw = text(value)
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(raw)
  if (!match) {
    return null
  }
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const check = new Date(Date.UTC(year, month - 1, day))
  if (
    check.getUTCFullYear() !== year ||
    check.getUTCMonth() !== month - 1 ||
    check.getUTCDate() !== day
  ) {
    return null
  }
  return raw
}

/**
 * Unknown or missing status is read as `paid`: read-only is the safe side, so a
 * malformed payload never offers an edit the server would reject.
 */
function runStatus(value: unknown): PayrollRunStatus {
  return value === 'open' ? 'open' : 'paid'
}

function readTotals(raw: unknown): PayrollRunTotals | null {
  if (!isRecord(raw)) {
    return null
  }
  const commission = raw['commission_cents']
  const fixed = raw['fixed_cents']
  const adjustment = raw['adjustment_cents']
  const total = raw['total_cents']
  const unsigned = (value: unknown): value is number =>
    typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
  const signed = (value: unknown): value is number =>
    typeof value === 'number' && Number.isSafeInteger(value)
  if (!unsigned(commission) || !unsigned(fixed) || !signed(adjustment) || !unsigned(total)) {
    return null
  }
  return {
    commission_cents: commission,
    fixed_cents: fixed,
    adjustment_cents: adjustment,
    total_cents: total,
  }
}

function normalizeLine(value: unknown): PayrollRunLine | null {
  if (!isRecord(value)) {
    return null
  }
  const userId = text(value['user_id'])
  if (userId === '') {
    return null
  }
  const commission = cents(value['commission_cents'])
  const fixed = cents(value['fixed_cents'])
  const adjustment = signedCents(value['adjustment_cents'])
  const total = cents(value['total_cents'])
  const computed = commission + fixed + adjustment
  return {
    user_id: userId,
    name: text(value['name']) || userId,
    active: value['active'] !== false,
    sales_count: count(value['sales_count']),
    sales_total_cents: cents(value['sales_total_cents']),
    commission_cents: commission,
    fixed_cents: fixed,
    adjustment_cents: adjustment,
    adjustment_note: text(value['adjustment_note']),
    // A missing or unusable total is its three concepts in integer cents.
    total_cents: total > 0 || computed === 0 ? total : computed,
    estimated: value['estimated'] === true,
  }
}

/** Total of one line: commission + fixed + signed adjustment (D6). */
export function lineTotalCents(line: PayrollRunLine): number {
  return cents(line.commission_cents) + cents(line.fixed_cents) + signedCents(line.adjustment_cents)
}

/** Integer sums of every concept of a set of lines; never a float. */
export function runTotalsFromLines(lines: readonly PayrollRunLine[]): PayrollRunTotals {
  let commission = 0
  let fixed = 0
  let adjustment = 0
  let total = 0
  for (const line of lines) {
    commission += cents(line.commission_cents)
    fixed += cents(line.fixed_cents)
    adjustment += signedCents(line.adjustment_cents)
    total += lineTotalCents(line)
  }
  return {
    commission_cents: commission,
    fixed_cents: fixed,
    adjustment_cents: adjustment,
    total_cents: total,
  }
}

/**
 * Reads a run of `GET /payroll/runs/:rid`. A run without id, with a kind outside
 * the three of the contract or with an unusable `from`/`to` is dropped instead of
 * inventing a period; every amount is normalised to signed or unsigned integer
 * cents and the totals fall back to the sum of the lines when untrustworthy.
 */
export function normalizePayrollRun(raw: unknown): PayrollRun | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const kind = raw['kind']
  const from = localDate(raw['from'])
  const to = localDate(raw['to'])
  if (id === '' || !isPayPeriodKind(kind) || from === null || to === null) {
    return null
  }
  const rows = Array.isArray(raw['lines']) ? raw['lines'] : []
  const lines: PayrollRunLine[] = []
  for (const row of rows) {
    const line = normalizeLine(row)
    if (line) {
      lines.push(line)
    }
  }
  return {
    id,
    kind,
    from,
    to,
    status: runStatus(raw['status']),
    created_at: optionalText(raw['created_at']),
    paid_at: optionalText(raw['paid_at']),
    totals: readTotals(raw['totals']) ?? runTotalsFromLines(lines),
    lines,
  }
}

function normalizeSummary(raw: unknown): PayrollRunSummary | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const kind = raw['kind']
  const from = localDate(raw['from'])
  const to = localDate(raw['to'])
  if (id === '' || !isPayPeriodKind(kind) || from === null || to === null) {
    return null
  }
  return {
    id,
    kind,
    from,
    to,
    status: runStatus(raw['status']),
    created_at: optionalText(raw['created_at']),
    paid_at: optionalText(raw['paid_at']),
    lines_count: count(raw['lines_count']),
    // A summary carries no lines, so untrustworthy totals fall back to zeros.
    totals: readTotals(raw['totals']) ?? {
      commission_cents: 0,
      fixed_cents: 0,
      adjustment_cents: 0,
      total_cents: 0,
    },
  }
}

/** Exported for the tests of the defensive read of one row. */
export function normalizePayrollRunSummary(raw: unknown): PayrollRunSummary | null {
  return normalizeSummary(raw)
}

/** Reads `{runs:[…]}` (or a bare list), drops rows without id and sorts newest first. */
export function normalizePayrollRuns(raw: unknown): PayrollRunSummary[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['runs'])
      ? (raw['runs'] as unknown[])
      : []
  const runs: PayrollRunSummary[] = []
  for (const row of rows) {
    const summary = normalizeSummary(row)
    if (summary) {
      runs.push(summary)
    }
  }
  // The server lists newest first by `from` desc; sorting again keeps a
  // malformed order from showing an old run on top. The sort is stable.
  return runs
    .map((summary, index) => ({ summary, index }))
    .sort((a, b) => {
      if (a.summary.from !== b.summary.from) {
        return a.summary.from < b.summary.from ? 1 : -1
      }
      const aCreated = a.summary.created_at ?? ''
      const bCreated = b.summary.created_at ?? ''
      if (aCreated !== bCreated) {
        return aCreated < bCreated ? 1 : -1
      }
      return a.index - b.index
    })
    .map((entry) => entry.summary)
}

// --- signed amounts: the adjustment of a line, always integer cents ---

/**
 * Value of the adjustment field for an `<input>`: `-1250` → `-12.50`, built with
 * integer arithmetic (never `cents / 100`). A fraction or a NaN is `0.00`.
 */
export function signedAmountInputFromCents(value: number): string {
  const safe = signedCents(value)
  const abs = Math.abs(safe)
  const units = Math.trunc(abs / 100)
  const fraction = abs % 100
  return `${safe < 0 ? '-' : ''}${units}.${String(fraction).padStart(2, '0')}`
}

/**
 * Reads a signed amount typed by a person (`-500`, `+12.5`, `$1,234.55`) with the
 * existing amount parser for the numeric part. Returns `null` instead of guessing.
 */
export function parseSignedAmountToCents(input: string): number | null {
  const cleaned = input.trim().replace(/\$/g, '').replace(/\s/g, '').replace(/,/g, '')
  const match = /^([+-]?)(\d+(?:\.\d{1,2})?)$/.exec(cleaned)
  if (!match) {
    return null
  }
  const parsed = parseAmountToCents(match[2] ?? '')
  if (parsed === null || !Number.isSafeInteger(parsed)) {
    return null
  }
  return match[1] === '-' ? -parsed : parsed
}

// --- optimistic adjustment with exact rollback ---

/**
 * Paints the new adjustment of one line (S-F3.3-5). The line total and the run
 * totals are recomputed in integer cents for the optimistic preview, and the run
 * it receives is never mutated: the caller keeps it as the exact rollback until
 * the server answers.
 */
export function applyRunLineAdjustment(
  run: PayrollRun,
  userId: string,
  input: { adjustment_cents: number; adjustment_note: string },
): PayrollRun {
  const adjustment = signedCents(input.adjustment_cents)
  const note = text(input.adjustment_note)
  const lines = run.lines.map((line) => {
    if (line.user_id !== userId) {
      return line
    }
    const next: PayrollRunLine = {
      ...line,
      adjustment_cents: adjustment,
      adjustment_note: note,
    }
    return { ...next, total_cents: lineTotalCents(next) }
  })
  return { ...run, lines, totals: runTotalsFromLines(lines) }
}

/** Exact rollback: the snapshot the caller kept comes back unchanged. */
export function restorePayrollRun(previous: PayrollRun): PayrollRun {
  return previous
}

// --- gates: what an open run allows and when the period can be liquidated ---

/** Only an open run accepts edits; a paid one is immutable (D6). */
export function canEditRunLines(run: Pick<PayrollRun, 'status'>): boolean {
  return run.status === 'open'
}

/** Only an open run can be deleted (it releases its days). */
export function canDeleteRun(run: Pick<PayrollRun, 'status'>): boolean {
  return run.status === 'open'
}

/** Only an open run can be paid: paying again is a 409 of the server. */
export function canPayRun(run: Pick<PayrollRun, 'status'>): boolean {
  return run.status === 'open'
}

/**
 * Reason that keeps «Liquidar este período» disabled, or `null` when the previewed
 * period is finished and can be frozen (D5: `to` earlier than today).
 */
export function liquidateBlockReason(period: PayPeriod | null): string | null {
  if (period === null) {
    return NO_PERIOD_MESSAGE
  }
  return period.closed ? null : PERIOD_NOT_CLOSED_MESSAGE
}

/** Owner and administration manage runs; professionals and reception do not (D7). */
export function canManagePayrollRuns(role: Role | null | undefined): boolean {
  return isManager(role)
}

// --- labels in Spanish (kind, dates, status) ---

export function runKindLabel(kind: PayPeriodKind): string {
  return PAY_PERIOD_KIND_LABELS[kind]
}

export function runStatusLabel(status: PayrollRunStatus): string {
  return RUN_STATUS_LABELS[status]
}

/** «Semana del 3 al 9 de noviembre»: kind plus the inclusive local dates. */
export function runLabel(run: { kind: PayPeriodKind; from: string; to: string }): string {
  return payPeriodLabel(run)
}

/** Signed adjustment formatted for reading (`-$5.00`, `$12.50`). */
export function formatSignedCents(value: number): string {
  return formatCents(signedCents(value))
}

// --- file name of the CSV (D8) ---

/** Name of the file the server would send for this run; also the client fallback. */
export function runCsvFilename(run: { kind: PayPeriodKind; from: string; to: string }): string {
  return sanitizeFilename(`nomina-${run.kind}-${run.from}-${run.to}.csv`) || 'nomina.csv'
}

/**
 * Safe file name: allowlist of letters, digits, dot, dash and underscore, with the
 * path removed and the `.csv` extension kept. An empty or unusable candidate is
 * discarded in favour of the fallback, so a malformed header never writes outside
 * the downloads of the browser.
 */
function sanitizeFilename(raw: string): string {
  const base = raw.trim()
  const safe = base
    .replace(/[^A-Za-z0-9._-]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^[-.]+/, '')
    .replace(/[-.]+$/, '')
  if (safe === '') {
    return ''
  }
  const capped = safe.length > 120 ? safe.slice(0, 120).replace(/[-.]+$/, '') : safe
  if (capped === '') {
    return ''
  }
  return /\.csv$/i.test(capped) ? capped : `${capped}.csv`
}

function filenameFromHeader(header: string | null | undefined): string {
  if (typeof header !== 'string' || header.trim() === '') {
    return ''
  }
  // RFC 5987 (`filename*=UTF-8''…`) wins over the plain parameter.
  const extended = /filename\*\s*=\s*([^;]+)/i.exec(header)
  if (extended) {
    const raw = (extended[1] ?? '').trim().replace(/^"(.*)"$/, '$1')
    const parts = /^([\w-]*)'[^']*'(.*)$/.exec(raw)
    const value = parts ? (parts[2] ?? '') : raw
    try {
      return decodeURIComponent(value)
    } catch {
      return value
    }
  }
  const plain = /filename\s*=\s*("([^"]*)"|([^;]*))/i.exec(header)
  if (plain) {
    return (plain[2] ?? plain[3] ?? '').trim()
  }
  return ''
}

/** Safe name of the CSV, from `Content-Disposition` with a fallback. */
export function csvDownloadFilename(
  header: string | null | undefined,
  fallback: string,
): string {
  const fromHeader = sanitizeFilename(filenameFromHeader(header))
  if (fromHeader !== '') {
    return fromHeader
  }
  const safeFallback = sanitizeFilename(fallback)
  return safeFallback === '' ? 'nomina.csv' : safeFallback
}

// --- API client (frozen shapes of «Formas de F3.3») ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function runPath(salonId: string, runId: string, suffix = ''): string {
  return salonPath(salonId, `/payroll/runs/${encodeURIComponent(runId)}${suffix}`)
}

function requireRun(raw: unknown, message: string): PayrollRun {
  const run = normalizePayrollRun(raw)
  if (!run) {
    throw new ApiError(200, message)
  }
  return run
}

/** `POST /payroll/runs {kind,date}`: freezes the finished period (201). */
export async function createPayrollRun(
  salonId: string,
  input: { kind: PayPeriodKind; date: string },
): Promise<PayrollRun> {
  return requireRun(
    await request<unknown>(salonPath(salonId, '/payroll/runs'), {
      method: 'POST',
      body: { kind: input.kind, date: input.date },
    }),
    'El servidor creó la liquidación pero no devolvió sus datos.',
  )
}

/** `GET /payroll/runs`: the list, newest first. */
export async function fetchPayrollRuns(salonId: string): Promise<PayrollRunSummary[]> {
  return normalizePayrollRuns(await request<unknown>(salonPath(salonId, '/payroll/runs')))
}

/** `GET /payroll/runs/:rid`: the frozen run with its lines. */
export async function fetchPayrollRun(salonId: string, runId: string): Promise<PayrollRun> {
  return requireRun(
    await request<unknown>(runPath(salonId, runId)),
    'El servidor devolvió una liquidación ilegible.',
  )
}

/**
 * `PATCH /payroll/runs/:rid/lines/:uid` with the whole body (the contract refuses
 * a partial one). A 400/409 keeps the Spanish sentence of the server.
 */
export async function updateRunLineAdjustment(
  salonId: string,
  runId: string,
  userId: string,
  input: { adjustment_cents: number; adjustment_note: string },
): Promise<PayrollRun> {
  return requireRun(
    await request<unknown>(runPath(salonId, runId, `/lines/${encodeURIComponent(userId)}`), {
      method: 'PATCH',
      body: {
        adjustment_cents: signedCents(input.adjustment_cents),
        adjustment_note: text(input.adjustment_note),
      },
    }),
    'El servidor guardó el ajuste pero no devolvió la liquidación.',
  )
}

/** `POST /payroll/runs/:rid/pay`: atomic, and a paid run is a 409 afterwards. */
export async function payPayrollRun(salonId: string, runId: string): Promise<PayrollRun> {
  return requireRun(
    await request<unknown>(runPath(salonId, runId, '/pay'), { method: 'POST' }),
    'El servidor pagó la liquidación pero no devolvió sus datos.',
  )
}

/** `DELETE /payroll/runs/:rid`: only while open. */
export async function deletePayrollRun(salonId: string, runId: string): Promise<void> {
  await request<unknown>(runPath(salonId, runId), { method: 'DELETE' })
}

async function exportErrorMessage(response: Response): Promise<string> {
  const fallback = `El servidor respondió ${response.status}. Intenta nuevamente.`
  try {
    const body = await response.text()
    if (body.trim() === '') {
      return fallback
    }
    const parsed: unknown = JSON.parse(body)
    if (isRecord(parsed)) {
      const message = parsed['error'] ?? parsed['message']
      if (typeof message === 'string' && message.trim() !== '') {
        return message
      }
    }
    return fallback
  } catch {
    return fallback
  }
}

/**
 * `GET /payroll/runs/:rid/export.csv` with the bearer token (D8): the endpoint
 * needs the Authorization header, so it is fetched and handed back as a Blob
 * instead of opening a plain link. The name comes from `Content-Disposition`,
 * sanitized, with the name of the run as fallback.
 */
export async function fetchPayrollRunCsv(
  salonId: string,
  runId: string,
  fallbackFilename = 'nomina.csv',
): Promise<PayrollRunCsv> {
  const headers: Record<string, string> = { Accept: 'text/csv' }
  const token = getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  let response: Response
  try {
    response = await fetch(`${API_BASE}${runPath(salonId, runId, '/export.csv')}`, {
      method: 'GET',
      headers,
    })
  } catch {
    throw new NetworkError(EXPORT_ERROR_MESSAGE)
  }
  if (response.status === 401) {
    clearToken()
    if (typeof window !== 'undefined') {
      window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
    }
  }
  if (!response.ok) {
    throw new ApiError(response.status, await exportErrorMessage(response))
  }
  return {
    blob: await response.blob(),
    filename: csvDownloadFilename(response.headers.get('content-disposition'), fallbackFilename),
  }
}

/** Hands the Blob to the browser and revokes the object URL when it is done. */
export function savePayrollRunCsv(file: PayrollRunCsv): void {
  const url = URL.createObjectURL(file.blob)
  try {
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = file.filename
    anchor.rel = 'noopener'
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}
