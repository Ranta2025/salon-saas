import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeNotification, normalizeNotifications, unreadCount, formatNotificationDate } from './notifications.ts'

test('normalizeNotification accepts the websocket shape and the persisted shape', () => {
  assert.deepEqual(
    normalizeNotification({
      id: 'n1',
      title: 'Nueva cita',
      body: 'Ana a las 10:00',
      kind: 'booking',
      read: false,
      created_at: '2025-03-14T16:00:00Z',
    }),
    {
      id: 'n1',
      title: 'Nueva cita',
      body: 'Ana a las 10:00',
      kind: 'booking',
      read: false,
      created_at: '2025-03-14T16:00:00Z',
    },
  )
  // The table stores read_at, not a boolean.
  const read = normalizeNotification({
    id: 'n2',
    title: 'Cierre de caja',
    body: '',
    kind: 'cash',
    read_at: '2025-03-14T20:00:00Z',
    created_at: '2025-03-14T19:00:00Z',
  })
  assert.equal(read?.read, true)
  assert.equal(normalizeNotification({ id: 'n3', title: 'Aviso', read_at: null })?.read, false)
})

test('normalizeNotification drops rows that cannot be rendered', () => {
  assert.equal(normalizeNotification(null), null)
  assert.equal(normalizeNotification('nope'), null)
  assert.equal(normalizeNotification({ title: 'sin id' }), null)
  assert.equal(normalizeNotification({ id: '', title: 'vacío' }), null)
})

test('normalizeNotifications keeps valid rows, newest first, and counts unread', () => {
  const list = normalizeNotifications([
    { id: 'old', title: 'Vieja', created_at: '2025-03-01T10:00:00Z', read: true },
    { id: 'new', title: 'Nueva', created_at: '2025-03-14T10:00:00Z', read: false },
    null,
    { id: 'bad' },
  ])
  assert.deepEqual(
    list.map((item) => item.id),
    ['new', 'old'],
  )
  assert.equal(unreadCount(list), 1)
  assert.deepEqual(normalizeNotifications(undefined), [])
  assert.deepEqual(normalizeNotifications({ notifications: [] }), [])
})

test('normalizeNotifications also accepts the {notifications:[...]} envelope', () => {
  const list = normalizeNotifications({
    notifications: [{ id: 'n1', title: 'Aviso', kind: 'notify', read: false }],
  })
  assert.equal(list.length, 1)
  assert.equal(list[0]?.body, '')
  assert.equal(list[0]?.created_at, '')
})

test('formatNotificationDate renders the salon local time and tolerates bad input', () => {
  assert.equal(formatNotificationDate('2025-03-14T16:30:00Z'), '14 mar, 10:30')
  assert.equal(formatNotificationDate(''), '')
  assert.equal(formatNotificationDate('not a date'), '')
})
