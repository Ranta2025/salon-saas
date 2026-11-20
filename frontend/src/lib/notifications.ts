/**
 * Notifications of the salon. The API persists them and the websocket pushes
 * them live; both shapes are normalized here so the UI renders one type.
 */

import { TIME_ZONE } from './datetime.ts'

export type Notification = {
  id: string
  title: string
  body: string
  kind: string
  read: boolean
  created_at: string
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** The persisted row uses `read_at`; the websocket payload uses `read`. */
function readFlag(raw: Record<string, unknown>): boolean {
  if (raw['read'] === true) {
    return true
  }
  return text(raw['read_at']) !== ''
}

/** Returns null for anything the UI could not render instead of guessing. */
export function normalizeNotification(raw: unknown): Notification | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  const title = text(raw['title'])
  if (id === '' || title === '') {
    return null
  }
  return {
    id,
    title,
    body: typeof raw['body'] === 'string' ? raw['body'] : '',
    kind: text(raw['kind']) === '' ? 'notify' : text(raw['kind']),
    read: readFlag(raw),
    created_at: text(raw['created_at']),
  }
}

/** Newest first; unusable rows are dropped, never rendered as `undefined`. */
export function normalizeNotifications(raw: unknown): Notification[] {
  const rows = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw['notifications'])
      ? raw['notifications']
      : []
  const list: Notification[] = []
  for (const row of rows) {
    const notification = normalizeNotification(row)
    if (notification) {
      list.push(notification)
    }
  }
  return list.sort((a, b) => {
    const timeA = Date.parse(a.created_at)
    const timeB = Date.parse(b.created_at)
    const safeA = Number.isNaN(timeA) ? Number.NEGATIVE_INFINITY : timeA
    const safeB = Number.isNaN(timeB) ? Number.NEGATIVE_INFINITY : timeB
    return safeB - safeA
  })
}

export function unreadCount(notifications: readonly Notification[]): number {
  return notifications.filter((notification) => !notification.read).length
}

/** `2025-03-14T16:30:00Z` → `14 mar, 10:30` in the salon timezone. */
export function formatNotificationDate(iso: string, timeZone: string = TIME_ZONE): string {
  const date = new Date(iso)
  if (iso === '' || Number.isNaN(date.getTime())) {
    return ''
  }
  return new Intl.DateTimeFormat('es-MX', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
    timeZone,
  }).format(date)
}
