import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { formatNotificationDate } from '../../lib/notifications'
import { REALTIME_STATUS_LABEL, useNotifications } from '../NotificationsContext'

const RECENT_LIMIT = 5

/**
 * Bell with the unread badge and a panel with the most recent notifications.
 * The full list lives at /panel/avisos.
 */
export function NotificationCenter() {
  const { notifications, unread, loading, error, realtimeStatus, markRead } =
    useNotifications()
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!open) {
      return
    }
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        setOpen(false)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open])

  const recent = notifications.slice(0, RECENT_LIMIT)
  const badge = unread > 9 ? '9+' : String(unread)

  return (
    <div className="notif">
      <button
        type="button"
        className="notif__trigger"
        aria-label={unread > 0 ? `Notificaciones, ${unread} sin leer` : 'Notificaciones'}
        aria-expanded={open}
        aria-controls="panel-notificaciones"
        title={REALTIME_STATUS_LABEL[realtimeStatus]}
        onClick={() => setOpen((value) => !value)}
      >
        <svg
          viewBox="0 0 24 24"
          width="20"
          height="20"
          aria-hidden="true"
          focusable="false"
        >
          <path
            d="M12 3.2a5 5 0 0 0-5 5v3.1l-1.6 3.1a1 1 0 0 0 .9 1.4h11.4a1 1 0 0 0 .9-1.4L17 11.3V8.2a5 5 0 0 0-5-5Z"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinejoin="round"
          />
          <path
            d="M9.6 18.9a2.5 2.5 0 0 0 4.8 0"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
          />
        </svg>
        {unread > 0 ? (
          <span className="notif__badge" aria-hidden="true">
            {badge}
          </span>
        ) : null}
      </button>

      {open ? (
        <section
          className="notif__panel"
          id="panel-notificaciones"
          aria-label="Centro de notificaciones"
        >
          <div className="notif__head">
            <p className="notif__title">Notificaciones</p>
            <button
              type="button"
              className="notif__close"
              aria-label="Cerrar notificaciones"
              onClick={() => setOpen(false)}
            >
              ×
            </button>
          </div>
          <p className="live-status" data-status={realtimeStatus}>
            <span className="live-status__dot" aria-hidden="true" />
            {REALTIME_STATUS_LABEL[realtimeStatus]}
          </p>

          {loading && recent.length === 0 ? (
            <p className="notif__empty">Cargando avisos…</p>
          ) : null}
          {error ? (
            <p className="notif__empty" role="alert">
              {error}
            </p>
          ) : null}
          {!loading && recent.length === 0 && !error ? (
            <p className="notif__empty">No hay avisos por ahora.</p>
          ) : null}

          <ul className="notif__list">
            {recent.map((notification) => (
              <li
                key={notification.id}
                className="notif__item"
                data-unread={!notification.read}
              >
                <p className="notif__item-title">{notification.title}</p>
                {notification.body ? (
                  <p className="notif__item-body">{notification.body}</p>
                ) : null}
                <div className="notif__item-meta">
                  <span>{formatNotificationDate(notification.created_at)}</span>
                  {notification.read ? (
                    <span>Leída</span>
                  ) : (
                    <button
                      type="button"
                      className="notif__mark"
                      onClick={() => void markRead(notification.id)}
                    >
                      Marcar leída
                    </button>
                  )}
                </div>
              </li>
            ))}
          </ul>

          <Link className="notif__link" to="/panel/avisos" onClick={() => setOpen(false)}>
            Ver todos los avisos →
          </Link>
        </section>
      ) : null}
    </div>
  )
}
