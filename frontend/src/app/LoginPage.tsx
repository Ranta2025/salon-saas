import { useState, type FormEvent } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { fetchMe, login } from '../lib/api'
import { pickSalonId } from '../lib/me'
import {
  clearSalonId,
  getSalonId,
  getToken,
  setSalonId,
  setToken,
  setUserId,
} from '../lib/session'

type LocationState = { from?: string } | null

export function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as LocationState)?.from ?? '/panel'
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  if (getToken()) {
    return <Navigate to={from} replace />
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError(null)
    setPending(true)
    try {
      const token = await login(email.trim(), password)
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
    } catch (err) {
      setError(err instanceof Error ? err.message : 'No pudimos iniciar sesión.')
    } finally {
      setPending(false)
    }
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
          Agenda, punto de venta y catálogo de servicios de Aurelia. Ingresa con la cuenta que te
          dio la administración del salón.
        </p>
        <ul className="login__list">
          <li>Agenda del día por profesional</li>
          <li>Cobro con total calculado en el servidor</li>
          <li>Catálogo de servicios y precios en dólares</li>
        </ul>
      </section>

      <section className="login__panel" aria-labelledby="login-title">
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

          {error ? (
            <p className="notice notice--error" role="alert">
              {error}
            </p>
          ) : null}

          <button type="submit" className="btn btn--wine" disabled={pending}>
            {pending ? 'Entrando…' : 'Entrar al panel'}
          </button>
          <p className="field__hint">
            ¿Olvidaste tu contraseña? Pídele a la administración que la restablezca.
          </p>
          <a className="login__back" href="/">
            ← Volver al sitio del salón
          </a>
        </form>
      </section>
    </main>
  )
}
