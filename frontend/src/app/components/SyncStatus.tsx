import { dropReasonLabel } from '../../lib/offlineQueue'
import { useOffline } from '../OfflineContext'

/** Compact pending counter for the app header; always visible when work is waiting. */
export function SyncStatusChip() {
  const { online, pendingCount, syncing, paused } = useOffline()
  if (pendingCount === 0 && online && !paused) {
    return null
  }
  const label = paused
    ? 'Sincronización en pausa'
    : pendingCount === 0
      ? 'Sin conexión'
      : `${pendingCount} ${pendingCount === 1 ? 'operación pendiente' : 'operaciones pendientes'}`
  return (
    <span className={`sync-chip${online ? '' : ' sync-chip--offline'}`} role="status">
      <span className="sync-chip__dot" aria-hidden="true" />
      {label}
      {syncing ? ' · sincronizando' : ''}
    </span>
  )
}

/**
 * Banner under the app header. Explains the offline mode, how many operations
 * are waiting and warns when the queue had to drop something for good.
 */
export function OfflineBanner() {
  const { online, pendingCount, dropped, syncing, paused, syncNow, dismissDropped } =
    useOffline()

  if (online && pendingCount === 0 && dropped.length === 0 && !paused) {
    return null
  }

  return (
    <div
      className={`offline-banner${online ? '' : ' offline-banner--offline'}`}
      role="status"
    >
      <p className="offline-banner__title">
        {paused
          ? 'Sincronización en pausa'
          : !online
            ? 'Estás sin conexión'
            : pendingCount > 0
              ? 'Sincronización pendiente'
              : 'Operaciones descartadas'}
      </p>
      {paused || !online || pendingCount > 0 ? (
        <p className="offline-banner__text">
          {paused
            ? 'Tu sesión venció. Volvé a iniciar sesión: las operaciones guardadas no se perdieron ni gastaron intentos.'
            : online
            ? `${pendingCount} ${pendingCount === 1 ? 'operación espera' : 'operaciones esperan'} al servidor.`
            : 'Podés seguir cobrando: las ventas se guardan en este dispositivo y se envían solas al volver la red.'}
          {!online && pendingCount > 0
            ? ` ${pendingCount} ${pendingCount === 1 ? 'operación pendiente' : 'operaciones pendientes'}.`
            : ''}
        </p>
      ) : null}
      {online && pendingCount > 0 ? (
        <button
          type="button"
          className="btn btn--quiet btn--sm"
          onClick={() => void syncNow()}
          disabled={syncing}
        >
          {syncing ? 'Sincronizando…' : 'Sincronizar ahora'}
        </button>
      ) : null}

      {dropped.length > 0 ? (
        <div className="offline-banner__dropped">
          <p>
            <strong>Atención:</strong> {dropped.length}{' '}
            {dropped.length === 1 ? 'operación no llegó' : 'operaciones no llegaron'} al servidor.
          </p>
          <ul>
            {dropped.map((operation) => (
              <li key={operation.client_op_id}>
                <code>{operation.client_op_id.slice(0, 8)}</code>{' '}
                {operation.kind === 'sale' ? 'venta' : 'reserva'} · {dropReasonLabel(operation)}
              </li>
            ))}
          </ul>
          <button type="button" className="btn btn--quiet btn--sm" onClick={dismissDropped}>
            Entendido
          </button>
        </div>
      ) : null}
    </div>
  )
}
