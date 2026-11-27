import { useState, type FormEvent } from 'react'
import { validateSetupCode } from '../lib/twofa'
import { SecurityCopyButton } from './SecurityCopyButton'

type SecuritySetupCardProps = {
  secret: string
  otpauthUrl: string
  pending: boolean
  /** Sentence of the server (400 `Codigo incorrecto`, 409...) or the offline notice, verbatim. */
  serverError: string | null
  onConfirm: (code: string) => void
  onCancel: () => void
}

/**
 * Step «Activar» of `/panel/seguridad` (contract F10, D7/D12): the secret and the
 * `otpauth://` address as copyable text (no QR code, so no extra dependency) and the
 * field for the first 6-digit code, which proves the app was set up right.
 */
export function SecuritySetupCard({
  secret,
  otpauthUrl,
  pending,
  serverError,
  onConfirm,
  onCancel,
}: SecuritySetupCardProps) {
  const [code, setCode] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)
  const error = localError ?? serverError

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (pending) {
      return
    }
    const check = validateSetupCode(code)
    if (!check.ok) {
      setLocalError(check.message)
      document.getElementById('security-setup-code')?.focus()
      return
    }
    setLocalError(null)
    onConfirm(check.code)
  }

  return (
    <section className="panel-card" aria-labelledby="security-setup-title">
      <div className="panel-card__head">
        <h2 id="security-setup-title">Configurá tu app de autenticación</h2>
      </div>
      <ol className="security-steps">
        <li>
          Abrí tu app de autenticación (Google Authenticator, Authy, 1Password u otra) y elegí
          «Ingresar una clave de configuración».
        </li>
        <li>
          Cargá esta clave. Si tu app lo permite, también podés usar la dirección completa.
        </li>
        <li>Escribí abajo el código de 6 dígitos que muestra la app.</li>
      </ol>

      <div className="security-secret">
        <p className="security-secret__label" id="security-secret-label">
          Clave secreta
        </p>
        <code className="security-secret__value" aria-labelledby="security-secret-label">
          {secret}
        </code>
        <SecurityCopyButton text={secret} label="Copiar clave" />
      </div>
      <div className="security-secret">
        <p className="security-secret__label" id="security-url-label">
          Dirección otpauth
        </p>
        <code className="security-secret__value" aria-labelledby="security-url-label">
          {otpauthUrl}
        </code>
        <SecurityCopyButton text={otpauthUrl} label="Copiar dirección" />
      </div>
      <p className="field__hint">
        No la compartas con nadie: quien tenga esta clave puede generar tus códigos.
      </p>

      <form className="security-form" onSubmit={handleSubmit} noValidate>
        <div className="field">
          <label htmlFor="security-setup-code">Código de 6 dígitos</label>
          <input
            id="security-setup-code"
            className="input security-form__code"
            type="text"
            inputMode="numeric"
            autoComplete="one-time-code"
            spellCheck={false}
            maxLength={12}
            placeholder="123456"
            value={code}
            onChange={(event) => {
              setCode(event.target.value)
              setLocalError(null)
            }}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? 'security-setup-error' : undefined}
          />
          {error ? (
            <p className="field__error" id="security-setup-error" role="alert">
              {error}
            </p>
          ) : null}
        </div>
        <div className="security-actions">
          <button type="submit" className="btn btn--wine" disabled={pending}>
            {pending ? 'Activando…' : 'Activar verificación'}
          </button>
          <button type="button" className="btn btn--quiet" onClick={onCancel} disabled={pending}>
            Cancelar
          </button>
        </div>
      </form>
    </section>
  )
}
