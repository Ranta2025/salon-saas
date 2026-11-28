import { useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  RECOVERY_MESSAGES,
  rememberResetNotice,
  resetPassword,
  validateNewPassword,
} from '../lib/recovery'
import { RecoveryCard } from '../components/RecoveryCard'
import { useFragmentToken } from '@/app/components/RecoveryToken'

/**
 * Public `/restablecer` page (F15, D3, D11). The token comes in the FRAGMENT of the
 * emailed link: `useFragmentToken` reads it and wipes it from the address bar with
 * `history.replaceState` before any request; it lives only in memory (never
 * storage, console or message) and travels only in the request body. On a 204 NO
 * session is stored: the person goes to /login with the one-shot notice. A server
 * error is shown verbatim.
 */
export function ResetPasswordPage() {
  const navigate = useNavigate()
  const token = useFragmentToken()

  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [fieldError, setFieldError] = useState<{ field: 'password' | 'confirm'; message: string } | null>(null)
  const [serverError, setServerError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  if (token === null) {
    return (
      <RecoveryCard eyebrow="Acceso" title="Enlace incompleto">
        <p className="notice notice--error" role="alert">
          {RECOVERY_MESSAGES.noToken}
        </p>
        <p className="recovery__actions">
          <Link className="btn btn--wine" to="/recuperar">
            Pedir un enlace nuevo
          </Link>
          <Link className="btn btn--quiet" to="/login">
            Volver a iniciar sesión
          </Link>
        </p>
      </RecoveryCard>
    )
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (pending) {
      return
    }
    const checked = validateNewPassword({ password, confirm })
    if (!checked.ok) {
      setFieldError({ field: checked.field, message: checked.message })
      setServerError(null)
      return
    }
    setFieldError(null)
    setServerError(null)
    setPending(true)
    try {
      await resetPassword(token, checked.password)
      rememberResetNotice()
      navigate('/login', { replace: true })
    } catch (err: unknown) {
      setServerError(err instanceof Error ? err.message : 'No pudimos restablecer la contraseña.')
      setPending(false)
    }
  }

  return (
    <RecoveryCard eyebrow="Acceso" title="Elegí una contraseña nueva">
      <form className="recovery__form" onSubmit={handleSubmit} noValidate>
        <div className="field">
          <label htmlFor="reset-password">Contraseña nueva</label>
          <input
            id="reset-password"
            className="input"
            type="password"
            name="password"
            autoComplete="new-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            aria-describedby="reset-password-hint"
            aria-invalid={fieldError?.field === 'password'}
            required
          />
          <p id="reset-password-hint" className="field__hint">
            Entre 8 y 72 caracteres. Con ella vas a iniciar sesión desde ahora.
          </p>
        </div>
        <div className="field">
          <label htmlFor="reset-confirm">Repite la contraseña</label>
          <input
            id="reset-confirm"
            className="input"
            type="password"
            name="confirm"
            autoComplete="new-password"
            value={confirm}
            onChange={(event) => setConfirm(event.target.value)}
            aria-invalid={fieldError?.field === 'confirm'}
            required
          />
        </div>
        {fieldError ? (
          <p className="notice notice--error" role="alert">
            {fieldError.message}
          </p>
        ) : null}
        {serverError ? (
          <div className="notice notice--error" role="alert">
            <p>{serverError}</p>
            <Link to="/recuperar">Pedir un enlace nuevo</Link>
          </div>
        ) : null}
        <button type="submit" className="btn btn--wine" disabled={pending}>
          {pending ? 'Guardando…' : 'Guardar contraseña'}
        </button>
      </form>
    </RecoveryCard>
  )
}
