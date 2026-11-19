export function LoadingState({ label = 'Cargando…' }: { label?: string }) {
  return (
    <p className="loading" role="status">
      <span className="loading__dot" aria-hidden="true" />
      {label}
    </p>
  )
}

export function ErrorNotice({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="notice notice--error" role="alert">
      <p>{message}</p>
      {onRetry ? (
        <button type="button" className="btn btn--quiet btn--sm" onClick={onRetry}>
          Reintentar
        </button>
      ) : null}
    </div>
  )
}

export function SalonRequired() {
  return (
    <div className="empty-state">
      <h2>Falta elegir el salón</h2>
      <p>
        La API todavía no expone la lista de salones de tu cuenta. Escribe el <strong>ID del salón</strong>{' '}
        en el selector de la barra superior (arriba a la izquierda) y esta pantalla carga sola.
      </p>
    </div>
  )
}
