/**
 * Single API client for the salon backend.
 * - one place for the base URL (`VITE_API_URL`, default http://localhost:8080)
 * - one place for the Authorization header
 * - one place for error handling (`ApiError` / `NetworkError`)
 */

import { clearToken, getToken } from './session.ts'
import { normalizeDashboard, type DashboardMetrics } from './metrics.ts'
import {
  normalizeNotifications,
  type Notification,
} from './notifications.ts'
import type { QueuedOperation } from './offlineQueue.ts'
import type {
  Booking,
  BookingInput,
  Client,
  ClientPhoto,
  Sale,
  SaleCreated,
  SaleInput,
  Service,
  ServiceInput,
  ServiceUpdateInput,
} from '../types'

const DEFAULT_BASE_URL = 'http://localhost:8080'

function resolveBaseUrl(): string {
  const env = (import.meta as ImportMeta & { env?: Record<string, string | undefined> }).env
  const configured = env?.['VITE_API_URL']?.trim()
  const base = configured && configured !== '' ? configured : DEFAULT_BASE_URL
  return base.replace(/\/+$/, '')
}

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
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

async function errorMessage(response: Response): Promise<string> {
  const fallback = `El servidor respondió ${response.status}. Intenta nuevamente.`
  try {
    const text = await response.text()
    if (text.trim() === '') {
      return fallback
    }
    const parsed: unknown = JSON.parse(text)
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

function signalUnauthorized(): void {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
  }
}

/** Performs one request and returns the parsed JSON body. */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const token = getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  const init: RequestInit = {
    method: options.method ?? 'GET',
    headers,
    signal: options.signal,
  }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(options.body)
  }

  let response: Response
  try {
    response = await fetch(`${API_BASE}${path}`, init)
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw error
    }
    throw new NetworkError(
      'No pudimos conectar con el servidor. Revisa tu conexión o la URL de la API.',
    )
  }

  if (response.status === 401) {
    clearToken()
    signalUnauthorized()
  }
  if (!response.ok) {
    throw new ApiError(response.status, await errorMessage(response))
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

// --- auth ---

export async function login(email: string, password: string): Promise<string> {
  const payload = await request<{ token?: string }>('/api/v1/auth/login', {
    method: 'POST',
    body: { email, password },
  })
  if (!payload || typeof payload.token !== 'string' || payload.token === '') {
    throw new ApiError(200, 'La respuesta de inicio de sesión no incluyó un token.')
  }
  return payload.token
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
  await request<unknown>(salonPath(salonId, '/services'), { method: 'POST', body: input })
}

export async function updateService(
  salonId: string,
  serviceId: string,
  input: ServiceUpdateInput,
): Promise<void> {
  await request<unknown>(salonPath(salonId, `/services/${encodeURIComponent(serviceId)}`), {
    method: 'PUT',
    body: input,
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
    throw new ApiError(response.status, await errorMessage(response))
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
