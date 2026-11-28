import type { ApiErrorNotice } from '../../lib/api'

export function LoadingState({ label = 'Cargando…' }: { label?: string }) {
  return (
    <p className="loading" role="status">
      <span className="loading__dot" aria-hidden="true" />
      {label}
    </p>
  )
}

/**
 * Error of a page load. The retry policy travels with the message: a 403 is a
 * permission problem, so it is explained without a button that cannot help.
 */
export function ErrorNotice({
  error,
  onRetry,
}: {
  error: ApiErrorNotice
  onRetry?: () => void
}) {
  return (
    <div className="notice notice--error" role="alert">
      <p>{error.message}</p>
      {error.retryable && onRetry ? (
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
        sigue, cerrá sesión y volvé a entrar, o pedile a la administración que revise tu acceso.
      </p>
    </div>
  )
}
