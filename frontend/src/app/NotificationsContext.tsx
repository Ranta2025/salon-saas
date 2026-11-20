import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { fetchNotifications, markNotificationRead } from '../lib/api'
import {
  normalizeNotification,
  unreadCount,
  type Notification,
} from '../lib/notifications'
import { RealtimeClient, type RealtimeStatus } from '../lib/realtime'
import { useSalon } from './SalonContext'

/** Human label for the live channel (bell panel and notifications page). */
export const REALTIME_STATUS_LABEL: Record<RealtimeStatus, string> = {
  idle: 'Tiempo real inactivo',
  connecting: 'Conectando el tiempo real…',
  open: 'Tiempo real en vivo',
  reconnecting: 'Reconectando el tiempo real…',
  offline: 'Sin conexión: sin tiempo real',
  unavailable: 'Este navegador no tiene tiempo real',
  stopped: 'Tiempo real detenido',
}

type NotificationsContextValue = {
  notifications: Notification[]
  unread: number
  loading: boolean
  error: string | null
  realtimeStatus: RealtimeStatus
  reload: () => void
  markRead: (notificationId: string) => Promise<void>
}

const NotificationsContext = createContext<NotificationsContextValue | null>(null)

/**
 * Owns the notification list: loads it from the API, prepends live `notify`
 * events from the websocket and keeps the read state optimistic.
 */
export function NotificationsProvider({ children }: { children: ReactNode }) {
  const { salonId } = useSalon()
  const [notifications, setNotifications] = useState<Notification[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [realtimeStatus, setRealtimeStatus] = useState<RealtimeStatus>('idle')
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId) {
      setNotifications([])
      return
    }
    let active = true
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    fetchNotifications(salonId, controller.signal)
      .then((list) => {
        if (active) {
          setNotifications(list)
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setError(
            err instanceof Error ? err.message : 'No pudimos cargar las notificaciones.',
          )
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [salonId, reloadKey])

  useEffect(() => {
    if (!salonId) {
      return
    }
    const client = new RealtimeClient()
    const offStatus = client.onStatus(setRealtimeStatus)
    const offNotify = client.subscribe('notify', (event) => {
      // The hub already isolates salons; this is a second guard on our side.
      if (event.salon_id !== '' && event.salon_id !== salonId) {
        return
      }
      const notification = normalizeNotification(event.data.notification)
      if (!notification) {
        return
      }
      setNotifications((current) => [
        notification,
        ...current.filter((item) => item.id !== notification.id),
      ])
    })
    client.start()
    return () => {
      offNotify()
      offStatus()
      client.stop()
    }
  }, [salonId])

  const markRead = useCallback(
    async (notificationId: string): Promise<void> => {
      setNotifications((current) =>
        current.map((item) =>
          item.id === notificationId ? { ...item, read: true } : item,
        ),
      )
      try {
        await markNotificationRead(notificationId)
      } catch (err) {
        setNotifications((current) =>
          current.map((item) =>
            item.id === notificationId ? { ...item, read: false } : item,
          ),
        )
        setError(
          err instanceof Error ? err.message : 'No pudimos marcar el aviso como leído.',
        )
      }
    },
    [],
  )

  const value = useMemo<NotificationsContextValue>(
    () => ({
      notifications,
      unread: unreadCount(notifications),
      loading,
      error,
      realtimeStatus,
      reload,
      markRead,
    }),
    [notifications, loading, error, realtimeStatus, reload, markRead],
  )

  return (
    <NotificationsContext.Provider value={value}>{children}</NotificationsContext.Provider>
  )
}

export function useNotifications(): NotificationsContextValue {
  const value = useContext(NotificationsContext)
  if (!value) {
    throw new Error('useNotifications must be used inside <NotificationsProvider>')
  }
  return value
}
