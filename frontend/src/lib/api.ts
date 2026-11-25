/**
 * Single API client for the salon backend.
 * - one place for the base URL (`VITE_API_URL`, default http://localhost:8080)
 * - one place for the Authorization header
 * - one place for error handling (`ApiError` / `NetworkError`)
 */

import { clearToken, getToken } from './session.ts'
import { normalizeMe, type Me } from './me.ts'
import { normalizeStaff, type StaffMember } from './staff.ts'
import { normalizePublicSalon, type PublicSalon } from './publicSalon.ts'
import { isCachedResponse, noteOfflineData } from './offlineData.ts'
import { normalizeDashboard, type DashboardMetrics } from './metrics.ts'
import {
  normalizeNotifications,
  type Notification,
} from './notifications.ts'
import { normalizeServiceIds } from './tradeServices.ts'
import type { QueuedOperation } from './offlineQueue.ts'
import type {
  Booking,
  BookingInput,
  Client,
  ClientInput,
  ClientPhoto,
  Sale,
  SaleCreated,
  SaleInput,
  Service,
  ServiceInput,
  ServiceUpdateInput,
} from '../types'

const DEFAULT_BASE_URL = ''

/**
 * Base URL of the API. `VITE_API_URL` wins (production build arg); without it the
 * app uses same-origin requests, which the Vite dev server proxies to the API so
 * local development does not depend on CORS.
 */
function resolveBaseUrl(): string {
  const env = (import.meta as ImportMeta & { env?: Record<string, string | undefined> }).env
  const configured = env?.['VITE_API_URL']?.trim()
  const base = configured && configured !== '' ? configured : DEFAULT_BASE_URL
  return base.replace(/\/+$/, '')
}

/** A request that hangs falls back to the offline queue instead of blocking the POS. */
export const DEFAULT_REQUEST_TIMEOUT_MS = 10_000

/** Base URL of the API (no trailing slash). */
export const API_BASE = resolveBaseUrl()

/** Emitted when the server answers 401 so the shell can send the user to /login. */
export const UNAUTHORIZED_EVENT = 'salon:unauthorized'

export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export class NetworkError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'NetworkError'
  }
}

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export type RequestOptions = {
  method?: HttpMethod
  body?: unknown
  signal?: AbortSignal
  /** Milliseconds before the request aborts as a network failure (0 disables it). */
  timeoutMs?: number
  /** `none` sends the request without the Authorization header (public endpoints). */
  auth?: 'auto' | 'none'
  /**
   * A 401 of this call is NOT an expired session (a wrong password or code, F10): the
   * stored token is kept and the app does not send the person to /login. The error
   * still reaches the caller with the server sentence.
   */
  keepSessionOn401?: boolean
  /** Fetch implementation (tests inject a stub; production keeps the global one). */
  fetchImpl?: typeof fetch
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

async function errorDetail(
  response: Response,
): Promise<{ message: string; offline: boolean }> {
  const fallback = `El servidor respondió ${response.status}. Intenta nuevamente.`
  try {
    const text = await response.text()
    if (text.trim() === '') {
      return { message: fallback, offline: false }
    }
    const parsed: unknown = JSON.parse(text)
    if (isRecord(parsed)) {
      const message = parsed['error'] ?? parsed['message']
      // The service worker answers an unreachable API with this flag: it is a
      // connectivity failure, not a server error the person can retry forever.
      const offline = parsed['offline'] === true
      if (typeof message === 'string' && message.trim() !== '') {
        return { message, offline }
      }
      return { message: fallback, offline }
    }
    return { message: fallback, offline: false }
  } catch {
    return { message: fallback, offline: false }
  }
}

/**
 * Copy for a failed request. A 403 is a permission problem the person cannot fix
 * by retrying, so it gets an explanation instead of the raw server text; every
 * other status keeps the Spanish message of the API.
 */
export function friendlyApiMessage(error: unknown, fallback: string): string {
  return describeApiError(error, fallback).message
}

export type ApiErrorNotice = {
  /** Spanish copy the screen shows. */
  message: string
  /** False when retrying cannot help (a permission problem, for example). */
  retryable: boolean
}

/**
 * Copy plus retry policy of a failed request. A 403 is a permission problem the
 * person cannot fix by retrying, so it gets an explanation and no Retry button;
 * every other status keeps the Spanish message of the API.
 */
export function describeApiError(error: unknown, fallback: string): ApiErrorNotice {
  if (error instanceof ApiError) {
    if (error.status === 403) {
      return {
        message:
          'Tu rol no permite esta acción. Pídele a la administración del salón que revise tu acceso.',
        retryable: false,
      }
    }
    return { message: error.message, retryable: true }
  }
  if (error instanceof NetworkError) {
    return { message: error.message, retryable: true }
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return { message: error.message, retryable: true }
  }
  return { message: fallback, retryable: true }
}

function signalUnauthorized(): void {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
  }
}

/** Performs one request and returns the parsed JSON body. */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const token = options.auth === 'none' ? null : getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  const controller = new AbortController()
  let timedOut = false
  const timeoutMs = options.timeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS
  const timer =
    timeoutMs > 0
      ? globalThis.setTimeout(() => {
          timedOut = true
          controller.abort()
        }, timeoutMs)
      : null
  const external = options.signal
  const onExternalAbort = (): void => controller.abort()
  if (external) {
    if (external.aborted) {
      controller.abort()
    } else {
      external.addEventListener('abort', onExternalAbort, { once: true })
    }
  }
  const cleanup = (): void => {
    if (timer !== null) {
      globalThis.clearTimeout(timer)
    }
    external?.removeEventListener('abort', onExternalAbort)
  }

  const init: RequestInit = {
    method: options.method ?? 'GET',
    headers,
    signal: controller.signal,
  }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(options.body)
  }

  const doFetch = options.fetchImpl ?? fetch
  let response: Response
  try {
    response = await doFetch(`${API_BASE}${path}`, init)
  } catch (error) {
    cleanup()
    if (external?.aborted) {
      throw error
    }
    if (timedOut) {
      throw new NetworkError(
        'El servidor tardó demasiado en responder. Revisa tu conexión o intenta de nuevo.',
      )
    }
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw error
    }
    throw new NetworkError(
      'No pudimos conectar con el servidor. Revisa tu conexión o la URL de la API.',
    )
  }
  cleanup()

  if (response.status === 401 && !options.keepSessionOn401) {
    clearToken()
    signalUnauthorized()
  }
  if (!response.ok) {
    const detail = await errorDetail(response)
    if (detail.offline) {
      throw new NetworkError(detail.message)
    }
    throw new ApiError(response.status, detail.message)
  }
  if (isCachedResponse(response)) {
    // A cached API answer is not live data: the screen says so instead of
    // showing it as if it had just arrived from the server.
    noteOfflineData(path)
  }
  if (response.status === 204) {
    return undefined as T
  }
  const text = await response.text()
  if (text.trim() === '') {
    return undefined as T
  }
  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError(response.status, 'El servidor devolvió una respuesta ilegible.')
  }
}

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

/**
 * `resource_type_id` of a service body (S-F2e-3): a trimmed type id, `null` for
 * "no equipment" and `undefined` when the client did not send the key at all.
 */
function normalizeResourceTypeId(value: unknown): string | null | undefined {
  if (value === undefined) {
    return undefined
  }
  if (typeof value !== 'string') {
    return null
  }
  const id = value.trim()
  return id === '' ? null : id
}

// --- auth ---

/**
 * What `POST /auth/login` can answer (contract F10, D8): a session token (the
 * answer of every account without 2FA, byte for byte as before) or, for an
 * account with 2FA, a short-lived challenge that is NOT a session.
 */
export type LoginResult =
  | { kind: 'token'; token: string }
  | { kind: 'challenge'; challenge: string }

/**
 * Narrows a login answer. `two_factor_required` wins over a stray `token`: a
 * pending challenge must never become a stored session. Anything else that is
 * neither a token nor a challenge is `null` (the caller reports an error).
 */
export function narrowLoginResponse(raw: unknown): LoginResult | null {
  if (!isRecord(raw)) {
    return null
  }
  if (raw['two_factor_required'] === true) {
    const challenge = raw['challenge']
    return typeof challenge === 'string' && challenge !== ''
      ? { kind: 'challenge', challenge }
      : null
  }
  const token = raw['token']
  return typeof token === 'string' && token !== '' ? { kind: 'token', token } : null
}

/** Message of a login answer that carried neither a token nor a challenge. */
export const LOGIN_NO_TOKEN_MESSAGE = 'La respuesta de inicio de sesión no incluyó un token.'

/**
 * Logs in. The caller decides what to do with the result: it stores a token, or
 * moves to the second step with the challenge. Nothing is stored here.
 */
export async function login(email: string, password: string): Promise<LoginResult> {
  const payload = await request<unknown>('/api/v1/auth/login', {
    method: 'POST',
    body: { email, password },
  })
  const result = narrowLoginResponse(payload)
  if (!result) {
    throw new ApiError(200, LOGIN_NO_TOKEN_MESSAGE)
  }
  return result
}

// --- identity and directory ---

export async function fetchMe(): Promise<Me> {
  const me = normalizeMe(await request<unknown>('/api/v1/me'))
  if (!me) {
    throw new ApiError(200, 'La API no devolvió los datos de tu cuenta.')
  }
  return me
}

/** Team of the salon; the POS uses these ids instead of a free-text field. */
export async function fetchStaff(salonId: string): Promise<StaffMember[]> {
  const payload = await request<unknown>(salonPath(salonId, '/staff'))
  return normalizeStaff(payload)
}

/**
 * Public salon (F16, D4): identity, contact, services, team, gallery, packages and
 * hours. No Authorization header because the endpoint is anonymous. A payload with
 * no salon name is not a usable salon: the landing shows its error state.
 */
export async function fetchPublicSalon(slug: string): Promise<PublicSalon> {
  const salon = normalizePublicSalon(
    await request<unknown>(`/api/v1/public/salons/${encodeURIComponent(slug)}`, {
      auth: 'none',
    }),
    API_BASE,
  )
  if (!salon || salon.salon.name === '') {
    throw new ApiError(200, 'La API no devolvió los datos del salón.')
  }
  return salon
}

// --- dashboard ---

export async function fetchDashboard(salonId: string): Promise<DashboardMetrics> {
  return normalizeDashboard(await request<unknown>(salonPath(salonId, '/dashboard')))
}

// --- services ---

export async function fetchServices(
  salonId: string,
  includeInactive = false,
): Promise<Service[]> {
  const suffix = includeInactive ? '/services?include_inactive=true' : '/services'
  const payload = await request<{ services?: Service[] }>(salonPath(salonId, suffix))
  return Array.isArray(payload?.services) ? payload.services : []
}

export async function createService(salonId: string, input: ServiceInput): Promise<void> {
  // S-F2c-5: nobody is assigned unless the body names them; the optional list is
  // normalised and omitted when empty so an older server sees the same body.
  const professionalIds = normalizeServiceIds(input.professional_ids)
  const body: Record<string, unknown> = { ...input }
  if (professionalIds.length > 0) {
    body['professional_ids'] = professionalIds
  } else {
    delete body['professional_ids']
  }
  // S-F2e-3: the equipment type travels as a type id or `null` for none; an
  // absent key keeps the request of an older client untouched.
  const resourceTypeId = normalizeResourceTypeId(input.resource_type_id)
  if (resourceTypeId === undefined) {
    delete body['resource_type_id']
  } else {
    body['resource_type_id'] = resourceTypeId
  }
  await request<unknown>(salonPath(salonId, '/services'), { method: 'POST', body })
}

export async function updateService(
  salonId: string,
  serviceId: string,
  input: ServiceUpdateInput,
): Promise<void> {
  const body: Record<string, unknown> = { ...input }
  const resourceTypeId = normalizeResourceTypeId(input.resource_type_id)
  if (resourceTypeId === undefined) {
    delete body['resource_type_id']
  } else {
    body['resource_type_id'] = resourceTypeId
  }
  await request<unknown>(salonPath(salonId, `/services/${encodeURIComponent(serviceId)}`), {
    method: 'PUT',
    body,
  })
}

// --- bookings ---

export async function fetchBookings(
  salonId: string,
  from: string,
  to: string,
  signal?: AbortSignal,
): Promise<Booking[]> {
  const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
  const payload = await request<{ bookings?: Booking[] }>(salonPath(salonId, `/bookings${query}`), {
    signal,
  })
  return Array.isArray(payload?.bookings) ? payload.bookings : []
}

/** Creates a booking. The offline queue flushes these with their `client_op_id`. */
export async function createBooking(salonId: string, input: BookingInput): Promise<void> {
  await request<unknown>(salonPath(salonId, '/bookings'), { method: 'POST', body: input })
}

// --- sales ---

export async function createSale(salonId: string, input: SaleInput): Promise<SaleCreated> {
  return request<SaleCreated>(salonPath(salonId, '/sales'), { method: 'POST', body: input })
}

export async function fetchSales(
  salonId: string,
  from: string,
  to: string,
  signal?: AbortSignal,
): Promise<Sale[]> {
  const query = `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`
  const payload = await request<{ sales?: Sale[] }>(salonPath(salonId, `/sales${query}`), { signal })
  return Array.isArray(payload?.sales) ? payload.sales : []
}

// --- notifications ---

export async function fetchNotifications(
  salonId: string,
  signal?: AbortSignal,
): Promise<Notification[]> {
  return normalizeNotifications(await request<unknown>(salonPath(salonId, '/notifications'), { signal }))
}

export async function markNotificationRead(notificationId: string): Promise<void> {
  await request<unknown>(`/api/v1/notifications/${encodeURIComponent(notificationId)}/read`, {
    method: 'POST',
  })
}

// --- clients ---

function optionalText(value: unknown): string | null {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : null
}

function optionalNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

/** Photos may come back as absolute URLs or as a path relative to the API. */
function resolveAssetUrl(url: string): string {
  if (/^(?:https?:|data:|blob:)/i.test(url)) {
    return url
  }
  return `${API_BASE}${url.startsWith('/') ? '' : '/'}${url}`
}

export function normalizeClient(raw: unknown): Client | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = optionalText(raw['id'])
  if (!id) {
    return null
  }
  return {
    id,
    name: optionalText(raw['name']) ?? optionalText(raw['full_name']) ?? 'Cliente sin nombre',
    phone: optionalText(raw['phone']),
    email: optionalText(raw['email']),
    created_at: optionalText(raw['created_at']),
  }
}

export function normalizeClientPhoto(raw: unknown): ClientPhoto | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = optionalText(raw['id'])
  const url = optionalText(raw['url'])
  if (!id || !url) {
    return null
  }
  return {
    id,
    url: resolveAssetUrl(url),
    // The POST contract uses w/h; the list endpoint may spell them out.
    width: optionalNumber(raw['w']) ?? optionalNumber(raw['width']),
    height: optionalNumber(raw['h']) ?? optionalNumber(raw['height']),
    bytes: optionalNumber(raw['bytes']),
    created_at: optionalText(raw['created_at']),
  }
}

function rowsOf(raw: unknown, key: string): unknown[] {
  if (Array.isArray(raw)) {
    return raw
  }
  if (isRecord(raw) && Array.isArray(raw[key])) {
    return raw[key] as unknown[]
  }
  return []
}

export async function fetchClients(salonId: string, signal?: AbortSignal): Promise<Client[]> {
  const payload = await request<unknown>(salonPath(salonId, '/clients'), { signal })
  const clients: Client[] = []
  for (const row of rowsOf(payload, 'clients')) {
    const client = normalizeClient(row)
    if (client) {
      clients.push(client)
    }
  }
  return clients.sort((a, b) => a.name.localeCompare(b.name, 'es'))
}

/**
 * Creates a client file by hand (`POST /salons/:id/clients`). The server accepts
 * name, phone, notes; `email` is sent too because the mirrored endpoint persists
 * it (and the public booking flow fills it in).
 */
export async function createClient(salonId: string, input: ClientInput): Promise<Client> {
  const payload = await request<unknown>(salonPath(salonId, '/clients'), {
    method: 'POST',
    body: {
      name: input.name.trim(),
      phone: input.phone.trim(),
      email: input.email.trim().toLowerCase(),
      notes: input.notes.trim(),
    },
  })
  const client = normalizeClient(payload)
  if (!client) {
    throw new ApiError(201, 'El servidor creó la ficha pero no devolvió sus datos.')
  }
  return client
}

export async function fetchClientPhotos(
  salonId: string,
  clientId: string,
  signal?: AbortSignal,
): Promise<ClientPhoto[]> {
  const payload = await request<unknown>(
    salonPath(salonId, `/clients/${encodeURIComponent(clientId)}/photos`),
    { signal },
  )
  const photos: ClientPhoto[] = []
  for (const row of rowsOf(payload, 'photos')) {
    const photo = normalizeClientPhoto(row)
    if (photo) {
      photos.push(photo)
    }
  }
  return photos
}

/** Multipart upload; the server expects the file in the `photo` field. */
export async function uploadClientPhoto(
  salonId: string,
  clientId: string,
  file: File,
): Promise<ClientPhoto> {
  const form = new FormData()
  form.append('photo', file, file.name)
  const headers: Record<string, string> = { Accept: 'application/json' }
  const token = getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  let response: Response
  try {
    response = await fetch(
      `${API_BASE}${salonPath(salonId, `/clients/${encodeURIComponent(clientId)}/photos`)}`,
      { method: 'POST', headers, body: form },
    )
  } catch {
    throw new NetworkError(
      'No pudimos conectar con el servidor. Revisa tu conexión o la URL de la API.',
    )
  }

  if (response.status === 401) {
    clearToken()
    signalUnauthorized()
  }
  if (!response.ok) {
    const detail = await errorDetail(response)
    if (detail.offline) {
      throw new NetworkError(detail.message)
    }
    throw new ApiError(response.status, detail.message)
  }
  const text = await response.text()
  let parsed: unknown = null
  try {
    parsed = text.trim() === '' ? null : JSON.parse(text)
  } catch {
    throw new ApiError(response.status, 'El servidor devolvió una respuesta ilegible.')
  }
  const photo = isRecord(parsed) ? normalizeClientPhoto(parsed['photo']) : null
  if (!photo) {
    throw new ApiError(response.status, 'El servidor no devolvió la foto subida.')
  }
  return photo
}

export async function deleteClientPhoto(
  salonId: string,
  clientId: string,
  photoId: string,
): Promise<void> {
  await request<unknown>(
    salonPath(
      salonId,
      `/clients/${encodeURIComponent(clientId)}/photos/${encodeURIComponent(photoId)}`,
    ),
    { method: 'DELETE' },
  )
}

/**
 * Multipart POST (a file upload). Same session and error handling as `request`,
 * but the browser sets the `Content-Type` with the boundary, so it is never set
 * here. Returns the parsed JSON body (`undefined` for an empty one).
 */
export async function requestMultipart<T>(path: string, form: FormData): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const token = getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  let response: Response
  try {
    response = await fetch(`${API_BASE}${path}`, { method: 'POST', headers, body: form })
  } catch {
    throw new NetworkError(
      'No pudimos conectar con el servidor. Revisa tu conexión o la URL de la API.',
    )
  }
  if (response.status === 401) {
    clearToken()
    signalUnauthorized()
  }
  if (!response.ok) {
    const detail = await errorDetail(response)
    if (detail.offline) {
      throw new NetworkError(detail.message)
    }
    throw new ApiError(response.status, detail.message)
  }
  const body = await response.text()
  if (body.trim() === '') {
    return undefined as T
  }
  try {
    return JSON.parse(body) as T
  } catch {
    throw new ApiError(response.status, 'El servidor devolvió una respuesta ilegible.')
  }
}

// --- offline queue transport ---

/**
 * Sends one queued operation. The `client_op_id` of the queue travels inside the
 * body so a retry or a reload never books the same sale twice.
 */
export async function submitQueuedOperation(operation: QueuedOperation): Promise<void> {
  if (operation.kind === 'sale') {
    await createSale(operation.salon_id, {
      ...(operation.payload as SaleInput),
      client_op_id: operation.client_op_id,
    })
    return
  }
  await createBooking(operation.salon_id, {
    ...(operation.payload as BookingInput),
    client_op_id: operation.client_op_id,
  })
}
