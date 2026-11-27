import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { RECOVERY_MESSAGES, verifyEmail } from '../lib/recovery'
import { getToken } from '@/lib/session'
import { RecoveryCard } from '../components/RecoveryCard'
import { useFragmentToken } from '@/app/components/RecoveryToken'

type View =
  | { kind: 'loading' }
  | { kind: 'ok' }
  | { kind: 'error'; message: string }
  | { kind: 'no-token' }

/**
 * Public `/verificar-email` page (F15, D7, D11). Reads the token from the
 * fragment, wipes it from the address bar and calls `POST /auth/verify-email`
 * EXACTLY once (a ref guards React StrictMode's double effect: the token is
 * single-use). Shows the success or the server sentence verbatim. Stores nothing.
 */
export function VerifyEmailPage() {
  const token = useFragmentToken()

  const [view, setView] = useState<View>(token === null ? { kind: 'no-token' } : { kind: 'loading' })
  const startedRef = useRef(false)
  const mountedRef = useRef(false)

  useEffect(() => {
    mountedRef.current = true
    if (token !== null && !startedRef.current) {
      startedRef.current = true
      verifyEmail(token)
        .then(() => {
          if (mountedRef.current) {
            setView({ kind: 'ok' })
          }
        })
        .catch((err: unknown) => {
          if (mountedRef.current) {
            setView({
              kind: 'error',
              message: err instanceof Error ? err.message : 'No pudimos verificar tu email.',
            })
          }
        })
    }
    return () => {
      mountedRef.current = false
    }
  }, [token])

  const signedIn = getToken() !== null
  const exit = signedIn ? (
    <Link className="btn btn--wine" to="/panel">
      Ir al panel
    </Link>
  ) : (
    <Link className="btn btn--wine" to="/login">
      Iniciar sesión
    </Link>
  )

  let title = 'Verificando tu email'
  if (view.kind === 'ok') {
    title = 'Email verificado'
  } else if (view.kind === 'error') {
    title = 'No pudimos verificar tu email'
  } else if (view.kind === 'no-token') {
    title = 'Enlace incompleto'
  }

  return (
    <RecoveryCard eyebrow="Cuenta" title={title}>
      {view.kind === 'loading' ? (
        <p className="loading" role="status">
          <span className="loading__dot" aria-hidden="true" />
          Verificando tu email…
        </p>
      ) : null}
      {view.kind === 'ok' ? (
        <>
          <p className="notice notice--success" role="status">
            Tu email quedó verificado. Gracias.
          </p>
          <p className="recovery__actions">{exit}</p>
        </>
      ) : null}
      {view.kind === 'error' ? (
        <>
          <p className="notice notice--error" role="alert">
            {view.message}
          </p>
          <p className="recovery__actions">
            {exit}
            {signedIn ? null : (
              <Link className="btn btn--quiet" to="/recuperar">
                Recuperar contraseña
              </Link>
            )}
          </p>
        </>
      ) : null}
      {view.kind === 'no-token' ? (
        <>
          <p className="notice notice--error" role="alert">
            {RECOVERY_MESSAGES.noToken}
          </p>
          <p className="recovery__actions">{exit}</p>
        </>
      ) : null}
    </RecoveryCard>
  )
}
