/**
 * Offline queue for mutations (POS sales, bookings). Operations are persisted in
 * localStorage with a `client_op_id` so a reload or a retry can never duplicate
 * a sale: the id travels with the request and the server deduplicates it.
 *
 * The queue is transport agnostic: `flush(send)` receives the function that
 * performs the request, which keeps this module free of `fetch` and testable
 * with a fake clock.
 */

import type { BookingInput, SaleInput } from '../types'

export const QUEUE_STORAGE_KEY = 'salon.offline_queue'
const QUEUE_STORAGE_VERSION = 1
/** After this many failed attempts the operation is dropped instead of looping. */
export const MAX_ATTEMPTS = 8
export const BASE_BACKOFF_MS = 5_000
export const MAX_BACKOFF_MS = 300_000
/** How many dropped operations the UI may still show as "no se pudo enviar". */
export const MAX_DROPPED = 20

export type QueuedKind = 'sale' | 'booking'
export type QueuePayload = SaleInput | BookingInput

export type QueuedOperation = {
  client_op_id: string
  kind: QueuedKind
  salon_id: string
  payload: QueuePayload
  created_at: string
  attempts: number
  next_attempt_at: number
  last_error: string | null
}

export type DroppedOperation = QueuedOperation & {
  dropped_at: number
  reason: string
}

export type QueueSnapshot = {
  operations: QueuedOperation[]
  dropped: DroppedOperation[]
  pendingCount: number
  syncing: boolean
  /** Operations confirmed in this session (not persisted). */
  syncedTotal: number
}

export type FlushResult = {
  synced: number
  failed: number
  dropped: number
  pending: number
}

export type QueuedSender = (operation: QueuedOperation) => Promise<void>

export type QueueStorage = {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
  removeItem?: (key: string) => void
}

export type QueueTimers = {
  setTimeout: (callback: () => void, delayMs: number) => unknown
  clearTimeout: (handle: unknown) => void
}

export type OfflineQueueOptions = {
  storage?: QueueStorage
  send?: QueuedSender
  /** When it returns false, a flush waits for the next online event (no attempts used). */
  canSend?: () => boolean
  now?: () => number
  timers?: QueueTimers
  maxAttempts?: number
  baseBackoffMs?: number
  maxBackoffMs?: number
}

const memoryFallback = new Map<string, string>()

function defaultStorage(): QueueStorage {
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage
    }
  } catch {
    // localStorage can be blocked (private mode, sandboxed iframe)
  }
  return {
    getItem: (key) => memoryFallback.get(key) ?? null,
    setItem: (key, value) => void memoryFallback.set(key, value),
    removeItem: (key) => void memoryFallback.delete(key),
  }
}

const defaultTimers: QueueTimers = {
  setTimeout: (callback, delayMs) => globalThis.setTimeout(callback, delayMs),
  clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isKind(value: unknown): value is QueuedKind {
  return value === 'sale' || value === 'booking'
}

function asNumber(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function isQueuedOperation(value: unknown): value is QueuedOperation {
  if (!isRecord(value)) {
    return false
  }
  return (
    typeof value['client_op_id'] === 'string' &&
    value['client_op_id'] !== '' &&
    isKind(value['kind']) &&
    typeof value['salon_id'] === 'string' &&
    isRecord(value['payload']) &&
    typeof value['created_at'] === 'string' &&
    typeof value['attempts'] === 'number' &&
    typeof value['next_attempt_at'] === 'number'
  )
}

function toOperation(value: unknown): QueuedOperation | null {
  if (!isQueuedOperation(value)) {
    return null
  }
  return {
    client_op_id: value['client_op_id'] as string,
    kind: value['kind'] as QueuedKind,
    salon_id: value['salon_id'] as string,
    payload: value['payload'] as QueuePayload,
    created_at: value['created_at'] as string,
    attempts: Math.max(0, Math.trunc(value['attempts'] as number)),
    next_attempt_at: value['next_attempt_at'] as number,
    last_error: typeof value['last_error'] === 'string' ? value['last_error'] : null,
  }
}

function toDropped(value: unknown): DroppedOperation | null {
  const operation = toOperation(value)
  if (!operation || !isRecord(value)) {
    return null
  }
  return {
    ...operation,
    dropped_at: asNumber(value['dropped_at'], 0),
    reason: typeof value['reason'] === 'string' ? value['reason'] : 'Se agotaron los intentos',
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error && error.message !== ''
    ? error.message
    : 'No pudimos sincronizar la operación.'
}

/** Delay before retry number `attempts` (1 → base, 2 → 2×base, capped). */
export function nextBackoffMs(
  attempts: number,
  baseBackoffMs: number = BASE_BACKOFF_MS,
  maxBackoffMs: number = MAX_BACKOFF_MS,
): number {
  const safeAttempts = Math.max(1, Math.trunc(attempts))
  return Math.min(maxBackoffMs, baseBackoffMs * 2 ** (safeAttempts - 1))
}

/** UUID v4 for `client_op_id`; falls back to a manual generator without crypto. */
export function newClientOpId(): string {
  const cryptoApi = typeof crypto !== 'undefined' ? crypto : undefined
  if (cryptoApi && typeof cryptoApi.randomUUID === 'function') {
    return cryptoApi.randomUUID()
  }
  const bytes = new Uint8Array(16)
  if (cryptoApi && typeof cryptoApi.getRandomValues === 'function') {
    cryptoApi.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256)
    }
  }
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

/**
 * Persistent FIFO queue. One instance is enough for the whole app; components
 * read it through `snapshot()` (stable reference while nothing changed) and
 * `subscribe()`.
 */
export class OfflineQueue {
  private readonly storage: QueueStorage
  private readonly timers: QueueTimers
  private readonly clock: () => number
  private readonly canSend: (() => boolean) | null
  private readonly maxAttempts: number
  private readonly baseBackoffMs: number
  private readonly maxBackoffMs: number

  private operations: QueuedOperation[] = []
  private droppedOperations: DroppedOperation[] = []
  private listeners = new Set<(snapshot: QueueSnapshot) => void>()
  private cachedSnapshot: QueueSnapshot | null = null
  private syncing = false
  private syncedTotal = 0
  private inFlight: Promise<FlushResult> | null = null
  private retryHandle: unknown = null
  private lastSender: QueuedSender | null

  constructor(options: OfflineQueueOptions = {}) {
    this.storage = options.storage ?? defaultStorage()
    this.timers = options.timers ?? defaultTimers
    this.clock = options.now ?? (() => Date.now())
    this.canSend = options.canSend ?? null
    this.maxAttempts = options.maxAttempts ?? MAX_ATTEMPTS
    this.baseBackoffMs = options.baseBackoffMs ?? BASE_BACKOFF_MS
    this.maxBackoffMs = options.maxBackoffMs ?? MAX_BACKOFF_MS
    this.lastSender = options.send ?? null
    this.load()
  }

  /**
   * Adds an operation. The same `client_op_id` twice returns the operation that
   * is already waiting instead of queueing (and later submitting) a duplicate.
   */
  enqueue(
    kind: QueuedKind,
    salonId: string,
    payload: QueuePayload,
    clientOpId?: string,
  ): QueuedOperation {
    const id = clientOpId && clientOpId.trim() !== '' ? clientOpId.trim() : newClientOpId()
    const existing = this.operations.find((operation) => operation.client_op_id === id)
    if (existing) {
      return existing
    }
    const now = this.clock()
    const operation: QueuedOperation = {
      client_op_id: id,
      kind,
      salon_id: salonId,
      payload,
      created_at: new Date(now).toISOString(),
      attempts: 0,
      next_attempt_at: now,
      last_error: null,
    }
    this.operations.push(operation)
    this.commit()
    return operation
  }

  pending(): QueuedOperation[] {
    return [...this.operations]
  }

  dropped(): DroppedOperation[] {
    return [...this.droppedOperations]
  }

  /** Hides the "could not be sent" list once the user acknowledged it. */
  clearDropped(): void {
    if (this.droppedOperations.length === 0) {
      return
    }
    this.droppedOperations = []
    this.commit()
  }

  /** Stable snapshot for `useSyncExternalStore`. */
  snapshot(): QueueSnapshot {
    if (!this.cachedSnapshot) {
      this.cachedSnapshot = {
        operations: [...this.operations],
        dropped: [...this.droppedOperations],
        pendingCount: this.operations.length,
        syncing: this.syncing,
        syncedTotal: this.syncedTotal,
      }
    }
    return this.cachedSnapshot
  }

  subscribe(listener: (snapshot: QueueSnapshot) => void): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /**
   * Sends pending operations in insertion order. Stops at the first failure so
   * the server sees them in the same order the receptionist created them; the
   * retry is scheduled with exponential backoff and dropped after the cap.
   */
  flush(send?: QueuedSender): Promise<FlushResult> {
    if (this.inFlight) {
      return this.inFlight
    }
    const sender = send ?? this.lastSender
    if (!sender) {
      return Promise.resolve({
        synced: 0,
        failed: 0,
        dropped: 0,
        pending: this.operations.length,
      })
    }
    this.lastSender = sender
    this.clearRetry()
    const run = this.drain(sender).finally(() => {
      this.inFlight = null
    })
    this.inFlight = run
    return run
  }

  private async drain(send: QueuedSender): Promise<FlushResult> {
    let synced = 0
    let failed = 0
    let dropped = 0
    this.syncing = true
    this.commit()
    try {
      while (this.operations.length > 0) {
        // Offline: keep the operation untouched and wait for the online event.
        if (this.canSend && !this.canSend()) {
          break
        }
        const operation = this.operations[0]
        if (!operation) {
          break
        }
        const now = this.clock()
        if (operation.next_attempt_at > now) {
          this.scheduleRetry(send, operation.next_attempt_at - now)
          break
        }
        try {
          await send(operation)
          this.operations.shift()
          synced += 1
          this.syncedTotal += 1
          this.commit()
        } catch (error) {
          failed += 1
          operation.attempts += 1
          operation.last_error = errorMessage(error)
          if (operation.attempts >= this.maxAttempts) {
            this.operations.shift()
            this.droppedOperations.push({
              ...operation,
              dropped_at: this.clock(),
              reason: `Se descartó después de ${this.maxAttempts} intentos`,
            })
            if (this.droppedOperations.length > MAX_DROPPED) {
              this.droppedOperations.splice(0, this.droppedOperations.length - MAX_DROPPED)
            }
            dropped += 1
            this.commit()
            continue
          }
          operation.next_attempt_at = now + this.backoff(operation.attempts)
          this.commit()
          this.scheduleRetry(send, operation.next_attempt_at - now)
          break
        }
      }
    } finally {
      this.syncing = false
      this.commit()
    }
    return { synced, failed, dropped, pending: this.operations.length }
  }

  private backoff(attempts: number): number {
    return nextBackoffMs(attempts, this.baseBackoffMs, this.maxBackoffMs)
  }

  private scheduleRetry(send: QueuedSender, delayMs: number): void {
    this.clearRetry()
    const delay = Math.max(0, Math.trunc(delayMs))
    this.retryHandle = this.timers.setTimeout(() => {
      this.retryHandle = null
      void this.flush(send)
    }, delay)
  }

  private clearRetry(): void {
    if (this.retryHandle !== null) {
      this.timers.clearTimeout(this.retryHandle)
      this.retryHandle = null
    }
  }

  private commit(): void {
    this.cachedSnapshot = null
    this.persist()
    const snapshot = this.snapshot()
    for (const listener of this.listeners) {
      try {
        listener(snapshot)
      } catch {
        // a broken subscriber must not stop the queue
      }
    }
  }

  private load(): void {
    let raw: string | null = null
    try {
      raw = this.storage.getItem(QUEUE_STORAGE_KEY)
    } catch {
      return
    }
    if (!raw) {
      return
    }
    try {
      const parsed: unknown = JSON.parse(raw)
      if (!isRecord(parsed)) {
        return
      }
      const operations = Array.isArray(parsed['operations']) ? parsed['operations'] : []
      const dropped = Array.isArray(parsed['dropped']) ? parsed['dropped'] : []
      this.operations = operations
        .map(toOperation)
        .filter((operation): operation is QueuedOperation => operation !== null)
      this.droppedOperations = dropped
        .map(toDropped)
        .filter((operation): operation is DroppedOperation => operation !== null)
        .slice(-MAX_DROPPED)
    } catch {
      // corrupted state must never break the app
      this.operations = []
      this.droppedOperations = []
    }
  }

  private persist(): void {
    const payload = JSON.stringify({
      version: QUEUE_STORAGE_VERSION,
      operations: this.operations,
      dropped: this.droppedOperations,
    })
    try {
      this.storage.setItem(QUEUE_STORAGE_KEY, payload)
    } catch {
      // quota/security errors: the in-memory queue still works this session
    }
  }
}
