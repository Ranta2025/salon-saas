import test from 'node:test'
import assert from 'node:assert/strict'
import {
  RealtimeClient,
  buildWebSocketUrl,
  type RealtimeSocket,
  type RealtimeStatus,
  type TypedRealtimeEvent,
} from './realtime.ts'
import type { Notification } from './notifications.ts'

type Scheduled = { delay: number; callback: () => void; cancelled: boolean }

function fakeTimers() {
  const scheduled: Scheduled[] = []
  return {
    scheduled,
    timers: {
      setTimeout: (callback: () => void, delay: number) => {
        const handle: Scheduled = { delay, callback, cancelled: false }
        scheduled.push(handle)
        return handle
      },
      clearTimeout: (handle: unknown) => {
        ;(handle as Scheduled).cancelled = true
      },
    },
    active: () => scheduled.filter((item) => !item.cancelled),
  }
}

class FakeSocket implements RealtimeSocket {
  readonly url: string
  readyState = 0
  closed = false
  onopen: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null

  constructor(url: string) {
    this.url = url
  }

  close(): void {
    this.closed = true
    this.readyState = 3
  }

  open(): void {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }

  drop(code = 1006): void {
    this.readyState = 3
    this.closed = true
    this.onclose?.({ code } as CloseEvent)
  }

  push(payload: unknown): void {
    this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent)
  }

  pushRaw(data: string): void {
    this.onmessage?.({ data } as MessageEvent)
  }
}

function harness() {
  const timers = fakeTimers()
  const target = new EventTarget()
  const sockets: FakeSocket[] = []
  let online = true
  let token: string | null = 'jwt-test'
  const client = new RealtimeClient({
    token: () => token,
    baseUrl: 'http://localhost:8080',
    onlineTarget: target,
    isOnline: () => online,
    timers: timers.timers,
    random: () => 0,
    baseDelayMs: 500,
    maxDelayMs: 4000,
    socketFactory: (url) => {
      const socket = new FakeSocket(url)
      sockets.push(socket)
      return socket
    },
  })
  return {
    client,
    sockets,
    timers,
    setOnline(value: boolean) {
      online = value
    },
    goOffline() {
      online = false
      target.dispatchEvent(new Event('offline'))
    },
    goOnline() {
      online = true
      target.dispatchEvent(new Event('online'))
    },
    setToken(value: string | null) {
      token = value
    },
  }
}

test('buildWebSocketUrl maps http to ws and puts the token in the query', () => {
  assert.equal(
    buildWebSocketUrl('http://localhost:8080', '/api/v1/ws', 'a b'),
    'ws://localhost:8080/api/v1/ws?token=a%20b',
  )
  assert.equal(
    buildWebSocketUrl('https://api.salon.mx/', '/api/v1/ws', 'jwt-123'),
    'wss://api.salon.mx/api/v1/ws?token=jwt-123',
  )
  assert.equal(
    buildWebSocketUrl('wss://api.salon.mx', '/api/v1/ws', 'x'),
    'wss://api.salon.mx/api/v1/ws?token=x',
  )
})

test('start opens the socket with the token and delivers typed events', () => {
  const h = harness()
  const received: TypedRealtimeEvent<'notify'>[] = []
  const unsubscribe = h.client.subscribe('notify', (event) => received.push(event))
  const statuses: RealtimeStatus[] = []
  h.client.onStatus((status) => statuses.push(status))

  h.client.start()
  assert.equal(h.sockets.length, 1)
  assert.equal(h.sockets[0]?.url, 'ws://localhost:8080/api/v1/ws?token=jwt-test')
  assert.deepEqual(statuses, ['connecting'])

  h.sockets[0]?.open()
  assert.deepEqual(statuses, ['connecting', 'open'])

  h.sockets[0]?.push({
    type: 'notify',
    salon_id: 'salon-1',
    ts: '2025-03-14T16:00:00Z',
    data: { notification: { id: 'n1', title: 'Nueva cita', body: 'Ana', kind: 'booking', read: false } },
  })

  assert.equal(received.length, 1)
  assert.equal(received[0]?.type, 'notify')
  assert.equal(received[0]?.salon_id, 'salon-1')
  const notification: Notification | undefined = received[0]?.data.notification
  assert.equal(notification?.title, 'Nueva cita')
  assert.equal(notification?.read, false)

  // Other event types reach their own subscribers, not the notify ones.
  const sales: TypedRealtimeEvent<'sale.created'>[] = []
  h.client.subscribe('sale.created', (event) => sales.push(event))
  h.sockets[0]?.push({ type: 'sale.created', salon_id: 'salon-1', ts: 'now', data: { sale_id: 's1' } })
  assert.equal(sales.length, 1)
  assert.equal(received.length, 1)

  // Unknown and malformed frames are ignored instead of breaking the app.
  h.sockets[0]?.push({ type: 'unknown.event', salon_id: 'salon-1', data: {} })
  h.sockets[0]?.pushRaw('not json')
  h.sockets[0]?.push({
    type: 'notify',
    salon_id: 'salon-1',
    ts: 'now',
    data: { notification: { title: 'sin id' } },
  })
  assert.equal(received.length, 1)

  unsubscribe()
  h.sockets[0]?.push({
    type: 'notify',
    salon_id: 'salon-1',
    ts: 'now',
    data: { notification: { id: 'n2', title: 'Otra', kind: 'notify', read: false } },
  })
  assert.equal(received.length, 1)
  h.client.stop()
})

test('reconnects with exponential backoff between the fixed delays', () => {
  const h = harness()
  h.client.start()
  h.sockets[0]?.open()

  const delays: number[] = []
  for (let attempt = 0; attempt < 5; attempt += 1) {
    const current = h.sockets[h.sockets.length - 1]
    current?.drop()
    const timers = h.timers.active()
    const timer = timers[timers.length - 1]
    assert.ok(timer)
    delays.push(timer.delay)
    timer.callback()
  }

  assert.deepEqual(delays, [500, 1000, 2000, 4000, 4000])
  assert.equal(h.sockets.length, 6)
  h.client.stop()
})

test('resubscribes after a reconnect and resets the backoff on a successful open', () => {
  const h = harness()
  const received: string[] = []
  h.client.subscribe('notify', (event) => received.push(event.data.notification.id))

  h.client.start()
  h.sockets[0]?.open()
  h.sockets[0]?.drop()

  const timer = h.timers.active()[0]
  assert.ok(timer)
  timer.callback()
  assert.equal(h.sockets.length, 2)
  h.sockets[1]?.open()

  // The subscription registered before the drop still receives live events.
  h.sockets[1]?.push({
    type: 'notify',
    salon_id: 'salon-1',
    ts: 'now',
    data: { notification: { id: 'n1', title: 'Después del corte', kind: 'notify', read: false } },
  })
  assert.deepEqual(received, ['n1'])

  // attempt was reset by the successful open: the next retry is 500 ms again.
  h.sockets[1]?.drop()
  const afterReset = h.timers.active()
  assert.equal(afterReset[afterReset.length - 1]?.delay, 500)
  h.client.stop()
})

test('going offline closes the socket and pauses retries until the browser is back', () => {
  const h = harness()
  const statuses: RealtimeStatus[] = []
  h.client.onStatus((status) => statuses.push(status))
  h.client.start()
  h.sockets[0]?.open()

  h.goOffline()
  assert.equal(h.sockets[0]?.closed, true)
  assert.equal(statuses[statuses.length - 1], 'offline')
  assert.equal(h.timers.active().length, 0)

  h.goOnline()
  assert.equal(h.sockets.length, 2)
  assert.equal(statuses[statuses.length - 1], 'connecting')
  h.client.stop()
})

test('starting while offline waits for the online event instead of failing', () => {
  const h = harness()
  h.setOnline(false)
  h.client.start()
  assert.equal(h.sockets.length, 0)
  h.client.onStatus(() => undefined)

  h.goOnline()
  assert.equal(h.sockets.length, 1)
  h.client.stop()
})

test('an unavailable socket never throws and keeps the app usable', () => {
  const client = new RealtimeClient({
    token: () => 'jwt',
    socketFactory: () => null,
    isOnline: () => true,
  })
  assert.doesNotThrow(() => client.start())
  assert.doesNotThrow(() => client.stop())
  assert.equal(client.getStatus(), 'unavailable')
})

test('stop closes the socket, cancels the retry timer and ignores later frames', () => {
  const h = harness()
  const received: string[] = []
  h.client.subscribe('notify', (event) => received.push(event.data.notification.id))
  h.client.start()
  const socket = h.sockets[0]
  socket?.open()
  socket?.drop()
  const pending = h.timers.active()
  assert.ok(pending.length > 0)

  h.client.stop()
  assert.equal(socket?.closed, true)
  assert.equal(h.client.getStatus(), 'stopped')
  socket?.push({
    type: 'notify',
    salon_id: 'salon-1',
    ts: 'now',
    data: { notification: { id: 'late', title: 'Tarde', kind: 'notify', read: false } },
  })
  assert.deepEqual(received, [])
})

test('an authentication failure stops reconnecting instead of hammering the server', () => {
  const h = harness()
  const statuses: RealtimeStatus[] = []
  h.client.onStatus((status) => statuses.push(status))

  h.client.start()
  h.sockets[0]?.open()
  h.sockets[0]?.drop(1008)

  assert.equal(h.client.getStatus(), 'stopped')
  assert.equal(statuses[statuses.length - 1], 'stopped')
  assert.equal(h.timers.active().length, 0)
  assert.equal(h.sockets.length, 1)
})

test('a close after the token disappears stops the client too', () => {
  const h = harness()
  h.client.start()
  h.sockets[0]?.open()

  h.setToken(null)
  h.sockets[0]?.drop()

  assert.equal(h.client.getStatus(), 'stopped')
  assert.equal(h.timers.active().length, 0)
})
