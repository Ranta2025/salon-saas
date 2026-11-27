import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { RECOVERY_MESSAGES, requestPasswordReset, validateForgotEmail } from '../lib/recovery'
import { RecoveryCard } from '../components/RecoveryCard'

/**
 * Public `/recuperar` page (F15, D2, D11). A 202 ALWAYS shows the same sentence:
 * the page never tells a registered email from an unknown one. A 400 (malformed
 * email) shows the server sentence verbatim. No session is read or stored.
 */
export function ForgotPasswordPage() {
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [sent, setSent] = useState(false)

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (pending) {
      return
    }
    const checked = validateForgotEmail(email)
    if (!checked.ok) {
      setError(checked.message)
      return
    }
    setError(null)
    setPending(true)
    try {
      await requestPasswordReset(checked.email)
      setSent(true)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'No pudimos enviar la solicitud.')
    } finally {
      setPending(false)
    }
  }

  return (
    <RecoveryCard eyebrow="Acceso" title="Recuperar contraseña">
      {sent ? (
        <>
          <p className="notice notice--success" role="status">
            {RECOVERY_MESSAGES.forgotAccepted}
          </p>
          <p className="recovery__actions">
            <button type="button" className="btn btn--quiet" onClick={() => setSent(false)}>
              Probar con otro correo
            </button>
            <Link className="btn btn--wine" to="/login">
              Volver a iniciar sesión
            </Link>
          </p>
        </>
      ) : (
        <form className="recovery__form" onSubmit={handleSubmit} noValidate>
          <p>
            Escribe el correo de tu cuenta y te enviamos un enlace para elegir una contraseña
            nueva. El enlace vence en una hora.
          </p>
          <div className="field">
            <label htmlFor="recovery-email">Correo electrónico</label>
            <input
              id="recovery-email"
              className="input"
              type="email"
              name="email"
              autoComplete="username"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              aria-invalid={error !== null}
              aria-describedby={error ? 'recovery-email-error' : undefined}
              required
            />
          </div>
          {error ? (
            <p id="recovery-email-error" className="notice notice--error" role="alert">
              {error}
            </p>
          ) : null}
          <button type="submit" className="btn btn--wine" disabled={pending}>
            {pending ? 'Enviando…' : 'Enviar enlace'}
          </button>
          <p className="recovery__actions">
            <Link className="btn btn--quiet" to="/login">
              ← Volver a iniciar sesión
            </Link>
          </p>
        </form>
      )}
    </RecoveryCard>
  )
}
