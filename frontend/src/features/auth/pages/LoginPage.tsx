import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { fetchMe, login } from '@/lib/api'
import { pickSalonId } from '@/lib/me'
import { takeResetNotice } from '../lib/recovery'
import { challengeFailure, completeLogin2fa, validateVerificationCode } from '../lib/twofa'
import {
  clearSalonId,
  getSalonId,
  getToken,
  setSalonId,
  setToken,
  setUserId,
} from '@/lib/session'
import { TwoFactorStep } from '../components/TwoFactorStep'

type LocationState = { from?: string } | null

export function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  // Kept in a ref: a 401 of the second step makes the shell navigate to /login
  // again, which drops the router state, and the person would lose where they
  // were going.
  const fromRef = useRef((location.state as LocationState)?.from ?? '/panel')
  const from = fromRef.current
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  // Second step (contract F10, D8/D11): the challenge lives in memory only. It is
  // not a session, so no token is stored until the second call succeeds.
  const [challenge, setChallenge] = useState<string | null>(null)
  const [code, setCode] = useState('')
  // One-shot notice after a password reset (F15, D11). It is read in an effect and
  // never overwritten with null, so React StrictMode's second run (which finds the
  // key already cleared) keeps what the first run read.
  const [notice, setNotice] = useState<string | null>(null)
  useEffect(() => {
    const taken = takeResetNotice()
    if (taken !== null) {
      setNotice(taken)
    }
  }, [])

  if (getToken()) {
    return <Navigate to={from} replace />
  }

  /** Stores the session and enters the panel (the same steps as before F10). */
  const enterWithToken = async (token: string): Promise<void> => {
    setToken(token)
    // GET /me selects the salon of the account; nobody pastes an id anymore.
    // If the identity endpoint is not available yet, the stored salon and the
    // panel fallback keep the session usable.
    try {
      const me = await fetchMe()
      setUserId(me.user.id)
      const nextSalon = pickSalonId(getSalonId(), me.salons)
      if (nextSalon) {
        setSalonId(nextSalon)
      } else {
        clearSalonId()
      }
    } catch (meError) {
      if (!getToken()) {
        setError(
          meError instanceof Error
            ? meError.message
            : 'No pudimos leer tu cuenta. Intenta de nuevo.',
        )
        return
      }
    }
    navigate(from, { replace: true })
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError(null)
    setPending(true)
    try {
      const result = await login(email.trim(), password)
      if (result.kind === 'challenge') {
        // 2FA account: ask for the code; nothing is stored yet.
        setChallenge(result.challenge)
        setCode('')
        return
      }
      await enterWithToken(result.token)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'No pudimos iniciar sesión.')
    } finally {
      setPending(false)
    }
  }

  const handleVerify = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (challenge === null || pending) {
      return
    }
    const check = validateVerificationCode(code)
    if (!check.ok) {
      setError(check.message)
      return
    }
    setError(null)
    setPending(true)
    try {
      const token = await completeLogin2fa(challenge, check.code)
      setChallenge(null)
      setCode('')
      await enterWithToken(token)
    } catch (err) {
      const failure = challengeFailure(err)
      setError(failure.message)
      if (failure.next === 'credentials') {
        // The challenge is invalid or expired: it is discarded and the person
        // starts again from the first step, with the server sentence on screen.
        setChallenge(null)
        setCode('')
        setPassword('')
      }
    } finally {
      setPending(false)
    }
  }

  /** «Volver»: discards the challenge and the code; the email stays, the password does not. */
  const handleBack = () => {
    setChallenge(null)
    setCode('')
    setPassword('')
    setError(null)
  }

  return (
    <main className="login">
      <section className="login__brand on-dark" aria-labelledby="login-brand-title">
        <p className="eyebrow">Panel del salón</p>
        <h1 id="login-brand-title">
          Todo el día del salón,
          <br /> en una sola pantalla
        </h1>
        <p className="login__tagline">
          Agenda, punto de venta y catálogo de servicios de tu salón. Ingresa con la cuenta que te
          dio la administración del salón.
        </p>
        <ul className="login__list">
          <li>Agenda del día por profesional</li>
          <li>Cobro con total calculado en el servidor</li>
          <li>Catálogo de servicios y precios en dólares</li>
        </ul>
      </section>

      <section className="login__panel" aria-labelledby="login-title">
        {challenge !== null ? (
          <TwoFactorStep
            code={code}
            onCodeChange={setCode}
            onSubmit={handleVerify}
            onBack={handleBack}
            pending={pending}
            error={error}
          />
        ) : (
          <form className="login__form" onSubmit={handleSubmit} noValidate>
            <p className="eyebrow">Acceso</p>
            <h2 id="login-title">Inicia sesión</h2>
            <div className="field">
              <label htmlFor="login-email">Correo electrónico</label>
              <input
                id="login-email"
                className="input"
                type="email"
                name="email"
                autoComplete="username"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
              />
            </div>
            <div className="field">
              <label htmlFor="login-password">Contraseña</label>
              <input
                id="login-password"
                className="input"
                type="password"
                name="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
            </div>

            {notice ? (
              <p className="notice notice--success" role="status">
                {notice}
              </p>
            ) : null}

            {error ? (
              <p className="notice notice--error" role="alert">
                {error}
              </p>
            ) : null}

            <button type="submit" className="btn btn--wine" disabled={pending}>
              {pending ? 'Entrando…' : 'Entrar al panel'}
            </button>
            <Link className="login__forgot" to="/recuperar">
              Olvidé mi contraseña
            </Link>
            <a className="login__back" href="/">
              ← Volver al sitio del salón
            </a>
          </form>
        )}
      </section>
    </main>
  )
}
