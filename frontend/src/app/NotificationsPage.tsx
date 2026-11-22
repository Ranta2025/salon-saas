import { formatNotificationDate } from '../lib/notifications'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { REALTIME_STATUS_LABEL, useNotifications } from './NotificationsContext'
import { useSalon } from './SalonContext'

export function NotificationsPage() {
  const { salonId } = useSalon()
  const { notifications, unread, loading, error, realtimeStatus, reload, markRead } =
    useNotifications()

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Avisos" title="Notificaciones" />
        <SalonRequired />
      </>
    )
  }

  return (
    <>
      <PageHeader
        eyebrow="Avisos"
        title="Notificaciones del salón"
        description="Avisos persistidos en el servidor y empujados en vivo por el canal websocket del salón."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      <p className="live-status" data-status={realtimeStatus}>
        <span className="live-status__dot" aria-hidden="true" />
        {REALTIME_STATUS_LABEL[realtimeStatus]} · {unread} sin leer
      </p>

      {loading ? <LoadingState label="Cargando avisos…" /> : null}
      {error ? <ErrorNotice error={{ message: error, retryable: true }} onRetry={reload} /> : null}

      <section className="panel-card" aria-labelledby="notifications-title">
        <div className="panel-card__head">
          <h2 id="notifications-title">Todos los avisos</h2>
          <span className="panel-card__meta">{notifications.length} en total</span>
        </div>

        {notifications.length === 0 && !loading ? (
          <div className="empty-state">
            <h2>Sin avisos por ahora</h2>
            <p>
              Cuando se cree una cita, se registre una venta o se cierre la caja, el aviso
              aparece aquí sin recargar la página.
            </p>
          </div>
        ) : (
          <ul className="notification-list">
            {notifications.map((notification) => (
              <li
                key={notification.id}
                className="notification-row"
                data-unread={!notification.read}
              >
                <div className="notification-row__head">
                  <h3>{notification.title}</h3>
                  <span className="notification-row__date">
                    {formatNotificationDate(notification.created_at)}
                  </span>
                </div>
                {notification.body ? (
                  <p className="notification-row__body">{notification.body}</p>
                ) : null}
                <div className="notification-row__foot">
                  <span className="tag">{notification.kind}</span>
                  {notification.read ? (
                    <span className="notification-row__state">Leída</span>
                  ) : (
                    <button
                      type="button"
                      className="btn btn--quiet btn--sm"
                      onClick={() => void markRead(notification.id)}
                    >
                      Marcar como leída
                    </button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  )
}
