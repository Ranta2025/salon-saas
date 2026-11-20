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
      <h2>Tu cuenta no tiene un salón activo</h2>
      <p>
        Al iniciar sesión cargamos los salones de tu cuenta y elegimos el primero. Si el problema
        sigue, cierra sesión y vuelve a entrar, o pídele a la administración que revise tu acceso.
      </p>
    </div>
  )
}
