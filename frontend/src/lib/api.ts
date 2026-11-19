/**
 * Single API client for the salon backend.
 * - one place for the base URL (`VITE_API_URL`, default http://localhost:8080)
 * - one place for the Authorization header
 * - one place for error handling (`ApiError` / `NetworkError`)
 */

import { clearToken, getToken } from './session.ts'
import { normalizeDashboard, type DashboardMetrics } from './metrics.ts'
import type {
  Booking,
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
