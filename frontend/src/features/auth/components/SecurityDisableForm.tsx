import { useState, type FormEvent } from 'react'
import { validateDisableDraft } from '../lib/twofa'

type SecurityDisableFormProps = {
  pending: boolean
  /** Sentence of the server (401 `Credenciales invalidas`, 400 `Codigo incorrecto`) verbatim. */
  serverError: string | null
  onSubmit: (draft: { password: string; code: string }) => void
  onCancel: () => void
}

/**
 * «Desactivar» (contract F10, D7/D12): the password and a code (the 6 digits of the
 * app or a recovery code). The server answers 204 and deletes the secret and the
 * recovery codes; the check here only avoids sending an empty field.
 */
export function SecurityDisableForm({
  pending,
  serverError,
  onSubmit,
  onCancel,
}: SecurityDisableFormProps) {
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)
  const error = localError ?? serverError

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (pending) {
      return
    }
    const check = validateDisableDraft({ password, code })
    if (!check.ok) {
      setLocalError(check.message)
      return
    }
    setLocalError(null)
    onSubmit(check.value)
  }

  return (
    <form className="security-form" onSubmit={handleSubmit} noValidate aria-label="Desactivar la verificación en dos pasos">
      <p className="field__hint">
        Para desactivarla escribí tu contraseña y un código: el de 6 dígitos de tu app o uno de
        recuperación.
      </p>
      <div className="field">
        <label htmlFor="security-disable-password">Contraseña</label>
        <input
          id="security-disable-password"
          className="input"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(event) => {
            setPassword(event.target.value)
            setLocalError(null)
          }}
        />
      </div>
      <div className="field">
        <label htmlFor="security-disable-code">Código de verificación</label>
        <input
          id="security-disable-code"
          className="input security-form__code"
          type="text"
          autoComplete="one-time-code"
          autoCapitalize="characters"
          spellCheck={false}
          maxLength={24}
          value={code}
          onChange={(event) => {
            setCode(event.target.value)
            setLocalError(null)
          }}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? 'security-disable-error' : undefined}
        />
      </div>
      {error ? (
        <p className="field__error" id="security-disable-error" role="alert">
          {error}
        </p>
      ) : null}
      <div className="security-actions">
        <button type="submit" className="btn btn--wine" disabled={pending}>
          {pending ? 'Desactivando…' : 'Desactivar verificación'}
        </button>
        <button type="button" className="btn btn--quiet" onClick={onCancel} disabled={pending}>
          Cancelar
        </button>
      </div>
    </form>
  )
}
