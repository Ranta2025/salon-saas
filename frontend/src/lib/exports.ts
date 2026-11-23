/**
 * Exports of the salon — «Exportar» (contract F4, `odd/tasks/fase6-roadmap.md`).
 *
 * Three CSV files leave the app through authenticated requests: sales, cash closes
 * (both for an inclusive range of LOCAL days of the salon) and clients (no range).
 * The download machinery is the one of the payroll export (F3.3 D8) and is
 * REUSED, not re-implemented: `csvDownloadFilename` reads the name from
 * `Content-Disposition` and `savePayrollRunCsv` hands the Blob to the browser.
 *
 * Nothing here parses the CSV: the body travels as a Blob straight to the
 * download, so no row is ever rendered, logged or turned into state.
 *
 * The server is the authority on permissions, the dates and the row cap; these
 * helpers only decide what the screen offers and keep a request from being sent
 * with a range the API would reject.
 */

import {
  ApiError,
  API_BASE,
  NetworkError,
  UNAUTHORIZED_EVENT,
} from './api.ts'
import { todayInTimeZone } from './datetime.ts'
import {
  checkExpenseRange,
  defaultExpenseRange,
  INVALID_RANGE_MESSAGE as EXPENSE_INVALID_RANGE_MESSAGE,
  MAX_RANGE_DAYS,
  type ExpenseRange,
} from './expenses.ts'
import {
  csvDownloadFilename,
  EXPORT_ERROR_MESSAGE,
  savePayrollRunCsv,
} from './payrollRuns.ts'
import { isManager, type Role } from './permissions.ts'
import { fetchSettings } from './settings.ts'
import { clearToken, getToken } from './session.ts'

/** The three files of the contract. */
export type ExportKind = 'sales' | 'cash-closes' | 'clients'

export const EXPORT_KIND_LABELS: Record<ExportKind, string> = {
  sales: 'Ventas',
  'cash-closes': 'Cierres de caja',
  clients: 'Clientes',
}

/**
 * Name used when the response carries no usable `Content-Disposition` (the
 * server names the file `ventas-<from>_<to>.csv`, `cierres-…`, `clientes-<day>.csv`).
 */
export const EXPORT_FALLBACK_FILENAMES: Record<ExportKind, string> = {
  sales: 'ventas.csv',
  'cash-closes': 'cierres.csv',
  clients: 'clientes.csv',
}

/** Longest range an export accepts (same rule as the API, D2). */
export const MAX_EXPORT_RANGE_DAYS = MAX_RANGE_DAYS

/** Sentence of the API when the range is unusable; also the client-side copy. */
export const INVALID_RANGE_MESSAGE = EXPENSE_INVALID_RANGE_MESSAGE

/**
 * Warning next to the clients export (contract F4): the file is a mass output of
 * personal data, and the free-text notes may contain health data.
 */
export const CLIENTS_PRIVACY_NOTICE =
  'Incluye teléfonos, emails y notas de tus clientas, que pueden contener datos de salud. Guardá el archivo con cuidado.'

/** Answer of a press while another file is still downloading. */
export const EXPORT_BUSY_MESSAGE =
  'Ya se está descargando un archivo. Esperá a que termine para pedir otro.'

/** Inclusive range of LOCAL days of the salon (`YYYY-MM-DD`, D2). */
export type ExportRange = ExpenseRange

/** What one press asks for: the two range exports need the range, clients do not. */
export type ExportRequest =
  | { kind: 'sales'; range: ExportRange }
  | { kind: 'cash-closes'; range: ExportRange }
  | { kind: 'clients' }

/** The downloaded CSV plus its safe name; nothing of the body is read here. */
export type ExportCsvFile = {
  kind: ExportKind
  blob: Blob
  filename: string
}

export type ExportRangeCheck =
  | { ok: true; from: string; to: string }
  | { ok: false; message: string }

/** Zone of the salon, its current local day and the default range of the page. */
export type ExportContext = {
  timeZone: string
  today: string
  range: ExportRange
}

/** Outcome of one press, rendered as is by the page. */
export type ExportOutcome =
  | { status: 'done'; filename: string }
  | { status: 'failed'; message: string }
  | { status: 'busy'; message: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

// --- range of the two date exports (D2) ---

/** Default range: today and the 29 local days before it, both inclusive. */
export function defaultExportRange(today: string): ExportRange {
  return defaultExpenseRange(today)
}

/**
 * Reads the range before sending it: two real days, `to` not before `from` and at
 * most 366 days. It reuses the rule and the sentence of the expenses list, which
 * the contract of F4 repeats verbatim.
 */
export function checkExportRange(from: string, to: string): ExportRangeCheck {
  return checkExpenseRange(from, to)
}

/**
 * Zone and today of the salon: `GET /salons/:id/settings` carries
 * `salons.timezone`, so the default range ends on today in that zone and never in
 * a fixed one (the F3b lesson of H14). A failure propagates: the page keeps its
 * known zone as the fallback, like Gastos and Ganancias.
 */
export async function loadExportContext(
  salonId: string,
  now: Date = new Date(),
): Promise<ExportContext> {
  const settings = await fetchSettings(salonId)
  const timeZone = settings.timezone
  const today = todayInTimeZone(timeZone, now)
  return { timeZone, today, range: defaultExportRange(today) }
}

// --- permission gate (D1) ---

/**
 * The three exports are for the owner and the administration only (D1): a mass
 * output, not a query. Reception, professionals and clients receive 403 even
 * though some of those lists are visible on screen today.
 */
export function canExportCsv(role: Role | null | undefined): boolean {
  return isManager(role)
}

// --- authenticated download (Bearer + Blob, like the payroll CSV) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function exportPath(salonId: string, kind: ExportKind): string {
  return salonPath(salonId, `/export/${kind}.csv`)
}

/**
 * URL of one export of the frozen contract. The range travels in the query with
 * the same names the API reads; the clients export carries no query at all.
 */
function exportUrl(salonId: string, request: ExportRequest): string {
  const path = exportPath(salonId, request.kind)
  if (request.kind === 'clients') {
    return path
  }
  const range = checkExportRange(request.range.from, request.range.to)
  if (!range.ok) {
    // Refused before the request: an invalid range never leaves the browser.
    throw new ApiError(400, range.message)
  }
  return `${path}?from=${encodeURIComponent(range.from)}&to=${encodeURIComponent(range.to)}`
}

/** Sentence of a failed export: the 400/403 of the API travels verbatim. */
export function exportErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message
  }
  if (error instanceof NetworkError) {
    return error.message
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return EXPORT_ERROR_MESSAGE
}

async function serverErrorMessage(response: Response): Promise<string> {
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
 * Downloads one export with the bearer token: the endpoint needs the
 * Authorization header, so it is fetched and handed back as a Blob instead of
 * opening a plain link. The name comes from `Content-Disposition`, sanitized,
 * with the name of the export as fallback. The body is never read as text here:
 * a CSV is a file, not data for the screen.
 */
export async function fetchExportCsv(
  salonId: string,
  request: ExportRequest,
): Promise<ExportCsvFile> {
  const url = exportUrl(salonId, request)
  const headers: Record<string, string> = { Accept: 'text/csv' }
  const token = getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  let response: Response
  try {
    response = await fetch(`${API_BASE}${url}`, { method: 'GET', headers })
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
    throw new ApiError(response.status, await serverErrorMessage(response))
  }
  return {
    kind: request.kind,
    blob: await response.blob(),
    filename: csvDownloadFilename(
      response.headers.get('content-disposition'),
      EXPORT_FALLBACK_FILENAMES[request.kind],
    ),
  }
}

/** Hands the Blob to the browser through the download of the payroll export. */
export function downloadExportCsv(file: ExportCsvFile): void {
  savePayrollRunCsv({ blob: file.blob, filename: file.filename })
}

// --- one press at a time (a double click must not download twice) ---

export type ExportGate = {
  /** The export in flight, or `null`. */
  busy: () => ExportKind | null
  /** Claims the gate; `false` when another export is already running. */
  start: (kind: ExportKind) => boolean
  /** Releases the gate; `true` when it was busy. */
  finish: () => boolean
}

/**
 * Synchronous gate of the page. React state alone is not enough for a double
 * click: both presses can read the same render, so the guard lives here and the
 * state only paints the disabled button.
 */
export function createExportGate(): ExportGate {
  let running: ExportKind | null = null
  return {
    busy: () => running,
    start: (kind: ExportKind) => {
      if (running !== null) {
        return false
      }
      running = kind
      return true
    },
    finish: () => {
      const wasBusy = running !== null
      running = null
      return wasBusy
    },
  }
}

/**
 * One press of a download button: claims the gate, downloads, saves and frees the
 * gate again, whatever happens. A 400/403 is handed back verbatim so the page
 * shows the sentence of the server, and the button is usable again afterwards.
 */
export async function runExport(
  gate: ExportGate,
  salonId: string,
  request: ExportRequest,
  save: (file: ExportCsvFile) => void = downloadExportCsv,
): Promise<ExportOutcome> {
  if (!gate.start(request.kind)) {
    return { status: 'busy', message: EXPORT_BUSY_MESSAGE }
  }
  try {
    const file = await fetchExportCsv(salonId, request)
    save(file)
    return { status: 'done', filename: file.filename }
  } catch (error: unknown) {
    return { status: 'failed', message: exportErrorMessage(error) }
  } finally {
    gate.finish()
  }
}

/** Copy of the range hint of the page, in the zone of the salon. */
export function exportRangeHint(range: ExportRange, today: string): string {
  return `Las dos fechas cuentan: el archivo incluye desde ${range.from} hasta ${range.to}, en días locales del salón. Hoy en el salón es ${today}.`
}
