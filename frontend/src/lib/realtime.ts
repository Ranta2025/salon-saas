/**
 * Websocket client for the salon realtime channel.
 *
 * Contract: `WS /api/v1/ws?token=<jwt>` pushes `{type, salon_id, data, ts}`.
 * - reconnects with exponential backoff and jitter
 * - keeps the subscriber registry across reconnects ("re-subscribes")
 * - pauses while the browser is offline and reconnects on `online`
 * - never throws when the socket is unavailable: the app keeps working without
 *   realtime (the UI just does not receive live updates)
 */

import { API_BASE } from './api.ts'
import { normalizeNotification, type Notification } from './notifications.ts'
import { getToken } from './session.ts'

export const WS_PATH = '/api/v1/ws'

export type RealtimeEventData = {
  'booking.created': Record<string, unknown>
  'booking.updated': Record<string, unknown>
  'sale.created': Record<string, unknown>
  'cash_closed': Record<string, unknown>
  notify: { notification: Notification }
}

export type RealtimeEventType = keyof RealtimeEventData

export type TypedRealtimeEvent<K extends RealtimeEventType = RealtimeEventType> = {
  type: K
  salon_id: string
  data: RealtimeEventData[K]
  ts: string
}

export const REALTIME_EVENT_TYPES: readonly RealtimeEventType[] = [
  'booking.created',
  'booking.updated',
  'sale.created',
  'cash_closed',
  'notify',
]

export type RealtimeStatus =
  | 'idle'
  | 'connecting'
  | 'open'
  | 'reconnecting'
  | 'offline'
  | 'unavailable'
  | 'stopped'

/** Minimal surface of `WebSocket`, so tests can supply a fake socket. */
export type RealtimeSocket = {
  readonly readyState: number
  close: () => void
  onopen: ((event: Event) => void) | null
  onclose: ((event: CloseEvent) => void) | null
  onerror: ((event: Event) => void) | null
  onmessage: ((event: MessageEvent) => void) | null
}

export type RealtimeSocketFactory = (url: string) => RealtimeSocket | null

export type RealtimeTimers = {
  setTimeout: (callback: () => void, delayMs: number) => unknown
  clearTimeout: (handle: unknown) => void
}

export type RealtimeOptions = {
  /** HTTP(S) base URL of the API; it is converted to ws(s)://. */
  baseUrl?: string
  token?: () => string | null
  socketFactory?: RealtimeSocketFactory
  isOnline?: () => boolean
  /** Element that emits `online`/`offline`; defaults to `window`. */
  onlineTarget?: EventTarget | null
  timers?: RealtimeTimers
  /** 0..1 source of jitter; tests pass `() => 0` for deterministic delays. */
  random?: () => number
  baseDelayMs?: number
  maxDelayMs?: number
}

type AnyHandler = (event: TypedRealtimeEvent) => void

const defaultTimers: RealtimeTimers = {
  setTimeout: (callback, delayMs) => globalThis.setTimeout(callback, delayMs),
  clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isOnline(): boolean {
  return typeof navigator === 'undefined' || navigator.onLine !== false
}

function defaultSocketFactory(url: string): RealtimeSocket | null {
  if (typeof WebSocket === 'undefined') {
    return null
  }
  return new WebSocket(url) as unknown as RealtimeSocket
}

/** `http://host` + token → `ws://host/api/v1/ws?token=…` (idempotent for ws/wss). */
export function buildWebSocketUrl(
  baseUrl: string,
  path: string = WS_PATH,
  token: string,
): string {
  const trimmed = baseUrl.trim().replace(/\/+$/, '')
  let wsBase: string
  if (trimmed.startsWith('https://')) {
    wsBase = `wss://${trimmed.slice('https://'.length)}`
  } else if (trimmed.startsWith('http://')) {
    wsBase = `ws://${trimmed.slice('http://'.length)}`
  } else {
    wsBase = trimmed
  }
  const suffix = path.startsWith('/') ? path : `/${path}`
  return `${wsBase}${suffix}?token=${encodeURIComponent(token)}`
}

/** Parses one frame; returns null for unknown or unrenderable payloads. */
function parseEvent(value: unknown): TypedRealtimeEvent | null {
  if (!isRecord(value)) {
    return null
  }
  const type = value['type']
  if (typeof type !== 'string' || !REALTIME_EVENT_TYPES.includes(type as RealtimeEventType)) {
    return null
  }
  const eventType = type as RealtimeEventType
  const salonId = typeof value['salon_id'] === 'string' ? value['salon_id'] : ''
  const ts = typeof value['ts'] === 'string' ? value['ts'] : ''
  const data = isRecord(value['data']) ? value['data'] : {}
  if (eventType === 'notify') {
    const notification = normalizeNotification(data['notification'])
    if (!notification) {
      return null
    }
    const event: TypedRealtimeEvent<'notify'> = {
      type: 'notify',
      salon_id: salonId,
      ts,
      data: { notification },
    }
    return event
  }
  return { type: eventType, salon_id: salonId, ts, data } as TypedRealtimeEvent
}

export class RealtimeClient {
  private readonly baseUrl: string
  private readonly token: () => string | null
  private readonly socketFactory: RealtimeSocketFactory
  private readonly onlineCheck: () => boolean
  private readonly timers: RealtimeTimers
  private readonly random: () => number
  private readonly baseDelayMs: number
  private readonly maxDelayMs: number

  private socket: RealtimeSocket | null = null
  private status: RealtimeStatus = 'idle'
  private attempts = 0
  private retryHandle: unknown = null
  private started = false

  private readonly handlers = new Map<RealtimeEventType, Set<AnyHandler>>()
  private readonly statusHandlers = new Set<(status: RealtimeStatus) => void>()

  private onlineTarget: EventTarget | null = null
  private listening = false
  private readonly configuredOnlineTarget: EventTarget | null | undefined

  constructor(options: RealtimeOptions = {}) {
    this.baseUrl = options.baseUrl ?? API_BASE
    this.token = options.token ?? (() => getToken())
    this.socketFactory = options.socketFactory ?? defaultSocketFactory
    this.onlineCheck = options.isOnline ?? isOnline
    this.timers = options.timers ?? defaultTimers
    this.random = options.random ?? Math.random
    this.baseDelayMs = options.baseDelayMs ?? 500
    this.maxDelayMs = options.maxDelayMs ?? 30_000
    this.configuredOnlineTarget = options.onlineTarget
  }

  getStatus(): RealtimeStatus {
    return this.status
  }

  /** Registers a typed handler. The subscription survives reconnects. */
  subscribe<K extends RealtimeEventType>(
    type: K,
    handler: (event: TypedRealtimeEvent<K>) => void,
  ): () => void {
    const wrapped: AnyHandler = (event) => handler(event as TypedRealtimeEvent<K>)
    const bucket = this.handlers.get(type) ?? new Set<AnyHandler>()
    bucket.add(wrapped)
    this.handlers.set(type, bucket)
    return () => {
      bucket.delete(wrapped)
    }
  }

  onStatus(handler: (status: RealtimeStatus) => void): () => void {
    this.statusHandlers.add(handler)
    return () => {
      this.statusHandlers.delete(handler)
    }
  }

  start(): void {
    if (this.started) {
      return
    }
    this.started = true
    this.attempts = 0
    this.attachOnlineListeners()
    if (!this.onlineCheck()) {
      this.setStatus('offline')
      return
    }
    this.connect()
  }

  stop(): void {
    const previous = this.status
    this.started = false
    this.clearRetry()
    this.closeSocket()
    this.detachOnlineListeners()
    if (
      previous === 'connecting' ||
      previous === 'open' ||
      previous === 'reconnecting' ||
      previous === 'offline'
    ) {
      this.setStatus('stopped')
    }
  }

  private connect(): void {
    if (!this.started) {
      return
    }
    if (!this.onlineCheck()) {
      this.setStatus('offline')
      return
    }
    const token = this.token()
    if (!token) {
      // Without a session there is nothing to authorize; a restart reconnects.
      this.setStatus('idle')
      return
    }
    this.setStatus(this.attempts === 0 ? 'connecting' : 'reconnecting')
    let socket: RealtimeSocket | null = null
    try {
      socket = this.socketFactory(buildWebSocketUrl(this.baseUrl, WS_PATH, token))
    } catch {
      socket = null
    }
    if (!socket) {
      this.setStatus('unavailable')
      return
    }
    this.socket = socket
    socket.onopen = () => {
      if (this.socket !== socket) {
        return
      }
      this.attempts = 0
      this.setStatus('open')
    }
    socket.onclose = () => {
      if (this.socket !== socket) {
        return
      }
      this.socket = null
      if (!this.started) {
        return
      }
      if (!this.onlineCheck()) {
        this.setStatus('offline')
        return
      }
      this.scheduleReconnect()
    }
    // The close event that follows an error drives the retry; never throw.
    socket.onerror = () => undefined
    socket.onmessage = (event) => {
      if (typeof event.data === 'string') {
        this.handleMessage(event.data)
      }
    }
  }

  private handleMessage(raw: string): void {
    if (!this.started || this.status !== 'open') {
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      return
    }
    const event = parseEvent(parsed)
    if (!event) {
      return
    }
    const bucket = this.handlers.get(event.type)
    if (!bucket) {
      return
    }
    for (const handler of bucket) {
      try {
        handler(event)
      } catch {
        // a broken subscriber must not kill the stream
      }
    }
  }

  private scheduleReconnect(): void {
    this.clearRetry()
    const exponential = Math.min(this.maxDelayMs, this.baseDelayMs * 2 ** this.attempts)
    const jitter = Math.floor(this.random() * this.baseDelayMs)
    this.attempts += 1
    this.setStatus('reconnecting')
    this.retryHandle = this.timers.setTimeout(() => {
      this.retryHandle = null
      this.connect()
    }, exponential + jitter)
  }

  private clearRetry(): void {
    if (this.retryHandle !== null) {
      this.timers.clearTimeout(this.retryHandle)
      this.retryHandle = null
    }
  }

  private closeSocket(): void {
    const socket = this.socket
    this.socket = null
    if (!socket) {
      return
    }
    socket.onopen = null
    socket.onclose = null
    socket.onerror = null
    socket.onmessage = null
    try {
      socket.close()
    } catch {
      // already closed
    }
  }

  private setStatus(next: RealtimeStatus): void {
    if (this.status === next) {
      return
    }
    this.status = next
    for (const handler of this.statusHandlers) {
      try {
        handler(next)
      } catch {
        // ignore subscriber errors
      }
    }
  }

  private readonly handleOnline = (): void => {
    if (!this.started) {
      return
    }
    this.clearRetry()
    this.attempts = 0
    if (this.status === 'open' || this.status === 'connecting') {
      return
    }
    this.connect()
  }

  private readonly handleOffline = (): void => {
    if (!this.started) {
      return
    }
    this.clearRetry()
    this.attempts = 0
    this.closeSocket()
    this.setStatus('offline')
  }

  private attachOnlineListeners(): void {
    if (this.listening) {
      return
    }
    const target =
      this.configuredOnlineTarget !== undefined
        ? this.configuredOnlineTarget
        : typeof window !== 'undefined'
          ? window
          : null
    this.onlineTarget = target
    if (!target) {
      return
    }
    target.addEventListener('online', this.handleOnline)
    target.addEventListener('offline', this.handleOffline)
    this.listening = true
  }

  private detachOnlineListeners(): void {
    if (!this.listening || !this.onlineTarget) {
      return
    }
    this.onlineTarget.removeEventListener('online', this.handleOnline)
    this.onlineTarget.removeEventListener('offline', this.handleOffline)
    this.onlineTarget = null
    this.listening = false
  }
}
