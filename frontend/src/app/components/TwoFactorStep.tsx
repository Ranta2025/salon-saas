import { useEffect, useRef, type FormEvent } from 'react'
import { MAX_VERIFICATION_CODE_LENGTH } from '../../lib/twofa'

type TwoFactorStepProps = {
  code: string
  onCodeChange: (code: string) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  /** «Volver»: discards the challenge and goes back to email and password. */
  onBack: () => void
  pending: boolean
  /** Sentence of the server (or of the check before sending), verbatim. */
  error: string | null
}

/**
 * Second step of the login (contract F10, D8/D11): the account has the two-step
 * verification on, so the password alone did not open a session. It takes the 6-digit
 * code of the authenticator app or a recovery code. The server decides if it is right;
 * the form only trims what was typed (the screen does) and shows the server sentence.
 */
export function TwoFactorStep({
  code,
  onCodeChange,
  onSubmit,
  onBack,
  pending,
  error,
}: TwoFactorStepProps) {
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    input.current?.focus()
  }, [])

  return (
    <form className="login__form" onSubmit={onSubmit} noValidate>
      <p className="eyebrow">Verificación en dos pasos</p>
      <h2 id="login-title">Código de verificación</h2>
      <p className="field__hint">
        Escribí el código de 6 dígitos de tu app de autenticación. Si no la tenés a mano, usá uno
        de tus códigos de recuperación (cada uno sirve una sola vez).
      </p>
      <div className="field">
        <label htmlFor="login-code">Código de verificación</label>
        <input
          id="login-code"
          ref={input}
          className="input login__code"
          type="text"
          name="code"
          inputMode="text"
          autoComplete="one-time-code"
          autoCapitalize="characters"
          autoCorrect="off"
          spellCheck={false}
          maxLength={MAX_VERIFICATION_CODE_LENGTH}
          value={code}
          onChange={(event) => onCodeChange(event.target.value)}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? 'login-code-error' : undefined}
          required
        />
      </div>

      {error ? (
        <p className="notice notice--error" id="login-code-error" role="alert">
          {error}
        </p>
      ) : null}

      <button type="submit" className="btn btn--wine" disabled={pending}>
        {pending ? 'Verificando…' : 'Verificar y entrar'}
      </button>
      <button type="button" className="btn btn--quiet" onClick={onBack} disabled={pending}>
        Volver
      </button>
    </form>
  )
}
