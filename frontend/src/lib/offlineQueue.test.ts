import test from 'node:test'
import assert from 'node:assert/strict'
import {
  BASE_BACKOFF_MS,
  MAX_ATTEMPTS,
  MAX_BACKOFF_MS,
  OfflineQueue,
  QUEUE_STORAGE_KEY,
  dropReasonLabel,
  findDropped,
  newClientOpId,
  nextBackoffMs,
  scopedStorageKey,
  type QueueStorage,
  type QueueTimers,
  type QueuedOperation,
} from './offlineQueue.ts'
import type { BookingInput, SaleInput } from '../types.ts'

type Scheduled = { delay: number; callback: () => void; cancelled: boolean }

function fakeTimers() {
  const scheduled: Scheduled[] = []
  const timers: QueueTimers = {
    setTimeout: (callback, delay) => {
      const handle: Scheduled = { delay, callback, cancelled: false }
      scheduled.push(handle)
      return handle
    },
    clearTimeout: (handle) => {
      ;(handle as Scheduled).cancelled = true
    },
  }
  return { timers, scheduled, active: () => scheduled.filter((item) => !item.cancelled) }
}

function memoryStorage() {
  const data = new Map<string, string>()
  const storage: QueueStorage = {
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => void data.set(key, value),
    removeItem: (key) => void data.delete(key),
  }
  return { storage, data }
}

const sale: SaleInput = {
  professional_id: 'prof-1',
  client_name: 'Ana',
  method: 'cash',
  items: [{ service_id: 'svc-1', qty: 1, unit_price_cents: 35000 }],
}

const booking: BookingInput = {
  professional_id: 'prof-1',
  client_name: 'Ana',
  starts_at: '2025-03-14T16:00:00Z',
  ends_at: '2025-03-14T17:00:00Z',
}

function setup(options: { now?: () => number; maxAttempts?: number } = {}) {
  const { storage, data } = memoryStorage()
  const timers = fakeTimers()
  let now = options.now?.() ?? 1_000_000
  const queue = new OfflineQueue({
    storage,
    timers: timers.timers,
    now: () => now,
    maxAttempts: options.maxAttempts ?? MAX_ATTEMPTS,
  })
  return {
    queue,
    storage,
    data,
    timers,
    advance(ms: number) {
      now += ms
    },
    get now() {
      return now
    },
  }
}

test('enqueue stores the operation with a client_op_id and does not touch the network', () => {
  const h = setup()
  const op = h.queue.enqueue('sale', 'salon-1', sale)

  assert.equal(op.kind, 'sale')
  assert.equal(op.salon_id, 'salon-1')
  assert.equal(op.attempts, 0)
  assert.equal(op.last_error, null)
  assert.equal(op.next_attempt_at, h.now)
  assert.equal(op.client_op_id, op.client_op_id.trim())
  assert.ok(op.client_op_id.length >= 16)
  assert.equal(h.queue.pending().length, 1)
  assert.equal(h.queue.snapshot().pendingCount, 1)

  // The queue never calls fetch/send by itself: the caller decides when to sync.
  const persisted = JSON.parse(h.data.get(QUEUE_STORAGE_KEY) ?? '{}') as {
    operations?: unknown[]
  }
  assert.equal(persisted.operations?.length, 1)
})

test('the queue survives a page reload (same storage, new instance)', () => {
  const h = setup()
  h.queue.enqueue('sale', 'salon-1', sale, 'op-1')
  h.queue.enqueue('booking', 'salon-1', booking, 'op-2')

  const reloaded = new OfflineQueue({ storage: h.storage, now: () => h.now })
  assert.deepEqual(
    reloaded.pending().map((op) => op.client_op_id),
    ['op-1', 'op-2'],
  )
})

test('flush sends pending operations in order and removes them', async () => {
  const h = setup()
  h.queue.enqueue('sale', 'salon-1', sale, 'op-1')
  h.queue.enqueue('booking', 'salon-1', booking, 'op-2')
  h.queue.enqueue('sale', 'salon-1', sale, 'op-3')

  const sent: QueuedOperation[] = []
  const result = await h.queue.flush(async (op) => {
    sent.push(op)
  })

  assert.deepEqual(
    sent.map((op) => op.client_op_id),
    ['op-1', 'op-2', 'op-3'],
  )
  assert.deepEqual(result, { synced: 3, failed: 0, dropped: 0, pending: 0 })
  assert.equal(h.queue.pending().length, 0)
  assert.equal(h.queue.snapshot().pendingCount, 0)
  assert.equal(h.queue.snapshot().syncedTotal, 3)
})

test('a failed flush retries with exponential backoff before the next attempt', async () => {
  const h = setup()
  h.queue.enqueue('sale', 'salon-1', sale, 'op-1')

  let calls = 0
  const send = async (): Promise<void> => {
    calls += 1
    if (calls === 1) {
      throw new Error('sin red')
    }
  }

  const first = await h.queue.flush(send)
  assert.deepEqual(first, { synced: 0, failed: 1, dropped: 0, pending: 1 })
  const waiting = h.queue.pending()[0]
  assert.equal(waiting?.attempts, 1)
  assert.equal(waiting?.last_error, 'sin red')
  assert.equal(waiting?.next_attempt_at, h.now + BASE_BACKOFF_MS)
  const timer = h.timers.active()[0]
  assert.equal(timer?.delay, BASE_BACKOFF_MS)

  // Still not due: flush keeps the operation and does not call the API again.
  await h.queue.flush(send)
  assert.equal(calls, 1)
  assert.equal(h.queue.pending().length, 1)

  h.advance(BASE_BACKOFF_MS)
  const second = await h.queue.flush(send)
  assert.equal(calls, 2)
  assert.deepEqual(second, { synced: 1, failed: 0, dropped: 0, pending: 0 })
})

test('backoff doubles per attempt and is capped', () => {
  assert.equal(nextBackoffMs(1), BASE_BACKOFF_MS)
  assert.equal(nextBackoffMs(2), BASE_BACKOFF_MS * 2)
  assert.equal(nextBackoffMs(3), BASE_BACKOFF_MS * 4)
  assert.equal(nextBackoffMs(10), MAX_BACKOFF_MS)
})

test('an operation is dropped after the attempt cap so the queue never loops forever', async () => {
  const h = setup({ maxAttempts: 3 })
  h.queue.enqueue('sale', 'salon-1', sale, 'op-1')

  let calls = 0
  const send = async (): Promise<void> => {
    calls += 1
    throw new Error('servidor caído')
  }

  await h.queue.flush(send)
  h.advance(nextBackoffMs(1))
  await h.queue.flush(send)
  h.advance(nextBackoffMs(2))
  const result = await h.queue.flush(send)

  assert.equal(calls, 3)
  assert.equal(h.queue.pending().length, 0)
  assert.deepEqual(result, { synced: 0, failed: 1, dropped: 1, pending: 0 })
  assert.equal(h.queue.dropped().length, 1)
  assert.equal(h.queue.dropped()[0]?.client_op_id, 'op-1')
  assert.equal(h.queue.dropped()[0]?.attempts, 3)
  assert.equal(h.queue.dropped()[0]?.cause, 'attempts_exhausted')
  assert.match(h.queue.dropped()[0]?.reason ?? '', /Se agotaron los intentos: servidor caído/)
  assert.equal(h.queue.snapshot().dropped.length, 1)
})

test('the same client_op_id is never enqueued or submitted twice', async () => {
  const h = setup()
  const first = h.queue.enqueue('sale', 'salon-1', sale, 'op-dup')
  const second = h.queue.enqueue('sale', 'salon-1', sale, 'op-dup')
  assert.equal(first.client_op_id, second.client_op_id)
  assert.equal(h.queue.pending().length, 1)

  const sent: string[] = []
  await h.queue.flush(async (op) => {
    sent.push(op.client_op_id)
  })
  assert.deepEqual(sent, ['op-dup'])

  // Calling flush twice while one is in flight must not double-submit either.
  h.queue.enqueue('sale', 'salon-1', sale, 'op-2')
  let release: () => void = () => undefined
  const slowSend = (op: QueuedOperation): Promise<void> =>
    new Promise((resolve) => {
      sent.push(op.client_op_id)
      release = resolve
    })
  const a = h.queue.flush(slowSend)
  const b = h.queue.flush(slowSend)
  assert.equal(sent.filter((id) => id === 'op-2').length, 1)
  release()
  await Promise.all([a, b])
  assert.equal(h.queue.pending().length, 0)
})

test('newClientOpId returns a unique uuid-shaped identifier', () => {
  const a = newClientOpId()
  const b = newClientOpId()
  assert.notEqual(a, b)
  assert.match(a, /^[0-9a-zA-Z-]{16,}$/)
})

test('corrupted persisted state is ignored instead of breaking the app', () => {
  const { storage, data } = memoryStorage()
  data.set(QUEUE_STORAGE_KEY, '{not json')
  const queue = new OfflineQueue({ storage, now: () => 1 })
  assert.deepEqual(queue.pending(), [])

  data.set(QUEUE_STORAGE_KEY, JSON.stringify({ operations: [{ kind: 'sale' }, null, 'x'] }))
  const second = new OfflineQueue({ storage, now: () => 1 })
  assert.deepEqual(second.pending(), [])
})

function httpError(status: number, message: string): Error {
  const error = new Error(message)
  ;(error as Error & { status?: number }).status = status
  return error
}

test('a definitive 4xx is dropped immediately and the queue moves on', async () => {
  const h = setup()
  h.queue.enqueue('sale', 'salon-1', sale, 'op-rejected')
  h.queue.enqueue('sale', 'salon-1', sale, 'op-ok')

  const sent: string[] = []
  const result = await h.queue.flush(async (operation) => {
    sent.push(operation.client_op_id)
    if (operation.client_op_id === 'op-rejected') {
      throw httpError(422, 'El servicio ya no está activo')
    }
  })

  assert.deepEqual(sent, ['op-rejected', 'op-ok'])
  assert.deepEqual(result, { synced: 1, failed: 0, dropped: 1, pending: 0 })
  assert.equal(h.queue.pending().length, 0)
  const dropped = h.queue.dropped()
  assert.equal(dropped.length, 1)
  assert.equal(dropped[0]?.client_op_id, 'op-rejected')
  // A rejection carries the server reason, not the attempts-exhausted wording.
  assert.equal(dropped[0]?.cause, 'server_rejected')
  assert.equal(dropped[0]?.reason, 'El servicio ya no está activo')
  assert.equal(h.timers.active().length, 0)
})

test('408 and 429 are transient server answers, not definitive rejections', async () => {
  for (const status of [408, 429]) {
    const h = setup()
    h.queue.enqueue('sale', 'salon-1', sale, 'op-1')
    const result = await h.queue.flush(async () => {
      throw httpError(status, 'intenta de nuevo')
    })

    assert.deepEqual(result, { synced: 0, failed: 1, dropped: 0, pending: 1 })
    assert.equal(h.queue.dropped().length, 0)
    assert.equal(h.queue.pending()[0]?.attempts, 1)
    assert.equal(h.timers.active().length, 1)
  }
})

test('a 401 pauses the queue without consuming attempts or dropping work', async () => {
  const h = setup()
  h.queue.enqueue('sale', 'salon-1', sale, 'op-1')
  h.queue.enqueue('sale', 'salon-1', sale, 'op-2')

  const result = await h.queue.flush(async () => {
    throw httpError(401, 'token vencido')
  })

  assert.deepEqual(result, { synced: 0, failed: 0, dropped: 0, pending: 2 })
  assert.equal(h.queue.pending()[0]?.attempts, 0)
  assert.equal(h.queue.pending()[0]?.next_attempt_at, h.now)
  assert.equal(h.queue.dropped().length, 0)
  assert.equal(h.queue.snapshot().paused, true)
  assert.equal(h.timers.active().length, 0)

  // A new session can retry at once: the pause is not a permanent state.
  const sent: string[] = []
  const retried = await h.queue.flush(async (operation) => {
    sent.push(operation.client_op_id)
  })
  assert.deepEqual(sent, ['op-1', 'op-2'])
  assert.deepEqual(retried, { synced: 2, failed: 0, dropped: 0, pending: 0 })
  assert.equal(h.queue.snapshot().paused, false)
})

test('the queue is scoped per user and logout can clear only the current one', () => {
  const h = setup()
  h.queue.setScope('user-a')
  h.queue.enqueue('sale', 'salon-1', sale, 'op-a')

  h.queue.setScope('user-b')
  assert.deepEqual(h.queue.pending(), [])
  h.queue.enqueue('sale', 'salon-1', sale, 'op-b')

  h.queue.setScope('user-a')
  assert.deepEqual(
    h.queue.pending().map((operation) => operation.client_op_id),
    ['op-a'],
  )

  h.queue.clear()
  assert.deepEqual(h.queue.pending(), [])
  assert.deepEqual(h.queue.dropped(), [])

  h.queue.setScope('user-b')
  assert.deepEqual(
    h.queue.pending().map((operation) => operation.client_op_id),
    ['op-b'],
  )
})

test('scoped queues never share the persisted storage key', () => {
  const h = setup()
  h.queue.setScope('user-a')
  h.queue.enqueue('sale', 'salon-1', sale, 'op-a')
  assert.ok(h.data.has(scopedStorageKey('user-a')))
  assert.equal(h.data.has(QUEUE_STORAGE_KEY), false)
  assert.notEqual(scopedStorageKey('user-a'), scopedStorageKey('user-b'))
  assert.equal(scopedStorageKey(null), QUEUE_STORAGE_KEY)
})

test('a dropped operation is found by its client_op_id', () => {
  const base = {
    kind: 'sale' as const,
    salon_id: 'salon-1',
    payload: sale,
    created_at: '2026-11-21T10:00:00.000Z',
    attempts: 1,
    next_attempt_at: 2,
    last_error: null,
  }
  const rejected = {
    ...base,
    client_op_id: 'op-rejected',
    dropped_at: 10,
    cause: 'server_rejected' as const,
    reason: 'La profesional ya no pertenece al salón.',
  }
  const exhausted = {
    ...base,
    client_op_id: 'op-exhausted',
    dropped_at: 11,
    cause: 'attempts_exhausted' as const,
    reason: 'Se agotaron los intentos: servidor caído',
  }
  assert.equal(findDropped([rejected, exhausted], 'op-rejected')?.cause, 'server_rejected')
  assert.equal(findDropped([rejected, exhausted], 'op-exhausted')?.reason, 'Se agotaron los intentos: servidor caído')
  assert.equal(findDropped([rejected, exhausted], 'op-other'), null)
  assert.equal(findDropped([], 'op-rejected'), null)
})

test('the copy of a dropped operation names who rejected it', () => {
  const base = {
    kind: 'sale' as const,
    salon_id: 'salon-1',
    payload: sale,
    client_op_id: 'op-1',
    created_at: '2026-11-21T10:00:00.000Z',
    attempts: 4,
    next_attempt_at: 2,
    last_error: null,
    dropped_at: 10,
  }
  assert.equal(
    dropReasonLabel({
      ...base,
      cause: 'server_rejected',
      reason: 'La profesional ya no pertenece al salón.',
    }),
    'El servidor rechazó esta operación: La profesional ya no pertenece al salón.',
  )
  // The retry cap already explains itself in `reason`.
  assert.equal(
    dropReasonLabel({ ...base, cause: 'attempts_exhausted', reason: 'Se agotaron los intentos: servidor caído' }),
    'Se agotaron los intentos: servidor caído',
  )
})
