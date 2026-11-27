import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { describeApiError, fetchMe, login, type ApiErrorNotice } from '@/lib/api'
import { normalizeEmail } from '@/lib/email'
import {
  INVITATION_MESSAGES,
  acceptInvitation,
  acceptInvitationSignedIn,
  expiryLabel,
  invitationErrorMessage,
  invitationLandingPath,
  lookupInvitation,
  readInvitationHash,
  validateSignup,
  type InvitationLookup,
} from '@/features/team/lib/invitations'
import { pickSalonId, type SalonRole } from '@/lib/me'
import { TWO_FACTOR_MESSAGES } from '../lib/twofa'
import {
  clearSalonId,
  clearToken,
  clearUserId,
  getSalonId,
  getToken,
  setSalonId,
  setToken,
  setUserId,
} from '@/lib/session'
import { LoadingState } from '@/app/components/Feedback'

/**
 * Public `/invitacion` page (S-F2g1-6). The token comes in the FRAGMENT of the
 * link (`/invitacion#<token>`) so it never reaches a server or proxy log:
 *
 * 1. the token is read from `location.hash` during the first render and wiped
 *    from the address bar with `history.replaceState` in the same tick, BEFORE
 *    any network call;
 * 2. it lives only in a ref of this component: never localStorage, never
 *    sessionStorage, never the console, never an error message, never the query
 *    string and never the route path;
 * 3. it travels only in request bodies (`{token}`);
 * 4. an invalid, expired, used or revoked token gets the ONE generic sentence of
 *    the server plus how to ask the salon for a new invitation: the page never
 *    distinguishes the causes.
 */
export function InvitationPage() {
  const navigate = useNavigate()

  // Read + clean before anything else runs. The ref survives React's double
  // render in development, so the token is never lost on the second pass.
  const tokenRef = useRef<{ token: string | null } | null>(null)
  if (tokenRef.current === null) {
    const read = readInvitationHash(window.location.href)
    if (read.url !== window.location.href) {
      window.history.replaceState(null, '', read.url)
    }
    tokenRef.current = { token: read.token }
  }
  const token = tokenRef.current.token

  const [lookup, setLookup] = useState<InvitationLookup | null>(null)
  const [lookupError, setLookupError] = useState<ApiErrorNotice | null>(null)
  const [loading, setLoading] = useState(token !== null)
  const [attempt, setAttempt] = useState(0)
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [loginEmail, setLoginEmail] = useState('')
  const [loginPassword, setLoginPassword] = useState('')
  const [formError, setFormError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [hasSession, setHasSession] = useState(() => getToken() !== null)
  const [now] = useState(() => new Date())

  useEffect(() => {
    if (token === null) {
      return
    }
    let active = true
    setLoading(true)
    setLookupError(null)
    lookupInvitation(token)
      .then((result) => {
        if (active) {
          setLookup(result)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setLookupError(
            describeApiError(err, INVITATION_MESSAGES.invalid),
          )
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [token, attempt])

  /** Stores the session of the new member and lands on their panel. */
  const enterPanel = async (jwt: string | null, fallbackRole: SalonRole): Promise<void> => {
    if (jwt) {
      setToken(jwt)
    }
    let role = fallbackRole
    try {
      const me = await fetchMe()
      setUserId(me.user.id)
      const salonId = pickSalonId(getSalonId(), me.salons)
      if (salonId) {
        setSalonId(salonId)
        const membership = me.salons.find((salon) => salon.id === salonId)
        if (membership) {
          role = membership.role
        }
      } else {
        clearSalonId()
      }
    } catch {
      // The session is already stored: land by the role of the invitation
      // (a professional goes to Mi agenda) instead of failing here.
    }
    navigate(invitationLandingPath(role), { replace: true })
  }

  /** "Creá tu cuenta": same name and password rules as the registration endpoint. */
  const handleSignup = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (token === null) {
      return
    }
    const checked = validateSignup({ name, password })
    if (!checked.ok) {
      setFormError(checked.message)
      return
    }
    setPending(true)
    setFormError(null)
    try {
      const jwt = await acceptInvitation(token, checked.value.name, checked.value.password)
      await enterPanel(jwt, lookup?.role ?? 'professional')
    } catch (err: unknown) {
      setFormError(invitationErrorMessage(err, 'No pudimos aceptar la invitación.'))
    } finally {
      setPending(false)
    }
  }

  /** Existing account with a session in this browser: accept with that session. */
  const handleAcceptSignedIn = async (): Promise<void> => {
    if (token === null) {
      return
    }
    setPending(true)
    setFormError(null)
    try {
      await acceptInvitationSignedIn(token)
      await enterPanel(null, lookup?.role ?? 'professional')
    } catch (err: unknown) {
      setFormError(invitationErrorMessage(err, 'No pudimos aceptar la invitación.'))
    } finally {
      setPending(false)
    }
  }

  /** Existing account without a session: log in here, then accept in one step. */
  const handleLogin = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (token === null) {
      return
    }
    const email = normalizeEmail(loginEmail)
    if (email === '' || loginPassword === '') {
      setFormError('Escribí tu correo y tu contraseña.')
      return
    }
    setPending(true)
    setFormError(null)
    try {
      const result = await login(email, loginPassword)
      if (result.kind !== 'token') {
        // An account with 2FA answers with a challenge, not a session. This page
        // has no second step: nothing is stored and the person is told what to do.
        setFormError(TWO_FACTOR_MESSAGES.invitationNeedsSignIn)
        return
      }
      const jwt = result.token
      setToken(jwt)
      setHasSession(true)
      await acceptInvitationSignedIn(token)
      await enterPanel(jwt, lookup?.role ?? 'professional')
    } catch (err: unknown) {
      setFormError(invitationErrorMessage(err, 'No pudimos aceptar la invitación.'))
    } finally {
      setPending(false)
    }
  }

  /** Way out of a wrong session: the link still works when opened again. */
  const handleLogout = (): void => {
    clearToken()
    clearUserId()
    clearSalonId()
    setHasSession(false)
    setFormError(null)
  }

  const expiry = lookup ? expiryLabel(lookup.expires_at, { now }) : null

  // An invalid, expired, used or revoked token all arrive as the SAME sentence:
  // the four causes share one screen, one message and no Retry button. A
  // connectivity problem is the only other state, and it can be retried.
  const genericInvalid = lookupError?.message === INVITATION_MESSAGES.invalid
  let title = 'Invitación de equipo'
  if (token === null) {
    title = INVITATION_MESSAGES.invalid
  } else if (loading) {
    title = 'Revisando tu invitación'
  } else if (lookup) {
    title = `Invitación a ${lookup.salon_name}`
  } else if (genericInvalid) {
    title = INVITATION_MESSAGES.invalid
  } else {
    title = 'No pudimos abrir la invitación'
  }

  return (
    <main className="invite">
      <section className="invite__card" aria-labelledby="invite-title">
        <p className="eyebrow">Invitación de equipo</p>
        <h1 id="invite-title">{title}</h1>

        {token === null ? (
          <>
            <p>
              Pedile a la administración del salón que te envíe una invitación nueva. El enlace
              sirve una sola vez, vence a los 7 días y no se puede recuperar: si ya lo usaste, abre
              el panel con tu cuenta.
            </p>
            <p className="invite__actions">
              <a className="btn btn--quiet" href="/">
                Ir al sitio del salón
              </a>
              <a className="btn btn--quiet" href="/login">
                Ya tengo cuenta
              </a>
            </p>
          </>
        ) : null}

        {token !== null && loading ? <LoadingState label="Revisando la invitación…" /> : null}

        {token !== null && !loading && lookupError && !genericInvalid ? (
          <>
            <p className="notice notice--error" role="alert">
              {lookupError.message}
            </p>
            <p>
              Pedile a la administración del salón que te envíe una invitación nueva. El enlace
              sirve una sola vez y vence a los 7 días.
            </p>
            <p className="invite__actions">
              <button
                type="button"
                className="btn btn--wine"
                onClick={() => setAttempt((value) => value + 1)}
              >
                Reintentar
              </button>
              <a className="btn btn--quiet" href="/login">
                Ya tengo cuenta
              </a>
            </p>
          </>
        ) : null}

        {token !== null && !loading && genericInvalid ? (
          <>
            <p>
              Pedile a la administración del salón que te envíe una invitación nueva. El enlace
              sirve una sola vez, vence a los 7 días y no se puede recuperar: si ya lo usaste, abre
              el panel con tu cuenta.
            </p>
            <p className="invite__actions">
              <a className="btn btn--quiet" href="/">
                Ir al sitio del salón
              </a>
              <a className="btn btn--quiet" href="/login">
                Ya tengo cuenta
              </a>
            </p>
          </>
        ) : null}

        {token !== null && !loading && lookup ? (
          <>
            <p className="invite__meta">
              Rol: <strong>{lookup.role_label}</strong>
              <span>Invitación para {lookup.email_hint}</span>
              {expiry ? <span>{expiry}</span> : null}
            </p>

            {formError ? (
              <p className="notice notice--error" role="alert">
                {formError}
              </p>
            ) : null}

            {lookup.has_account ? (
              <section aria-labelledby="invite-login-title">
                <h2 id="invite-login-title">Iniciá sesión para aceptar</h2>
                <p>
                  Ya tenés cuenta con este correo. Entrá con tu contraseña y aceptás la invitación
                  en el mismo paso; la vas a ver como un salón más de tu cuenta.
                </p>
                {hasSession ? (
                  <div className="invite__form">
                    <button
                      type="button"
                      className="btn btn--wine"
                      disabled={pending}
                      onClick={() => void handleAcceptSignedIn()}
                    >
                      {pending ? 'Aceptando…' : 'Aceptar la invitación'}
                    </button>
                    <button type="button" className="btn btn--quiet" onClick={handleLogout}>
                      Cerrar sesión y entrar con otra cuenta
                    </button>
                  </div>
                ) : (
                  <form className="invite__form" method="post" onSubmit={handleLogin} noValidate>
                    <div className="field">
                      <label htmlFor="invite-login-email">Correo electrónico</label>
                      <input
                        id="invite-login-email"
                        className="input"
                        type="email"
                        name="email"
                        autoComplete="username"
                        value={loginEmail}
                        onChange={(event) => setLoginEmail(event.target.value)}
                        required
                      />
                    </div>
                    <div className="field">
                      <label htmlFor="invite-login-password">Contraseña</label>
                      <input
                        id="invite-login-password"
                        className="input"
                        type="password"
                        name="password"
                        autoComplete="current-password"
                        value={loginPassword}
                        onChange={(event) => setLoginPassword(event.target.value)}
                        required
                      />
                    </div>
                    <button type="submit" className="btn btn--wine" disabled={pending}>
                      {pending ? 'Entrando…' : 'Entrar y aceptar'}
                    </button>
                  </form>
                )}
              </section>
            ) : (
              <section aria-labelledby="invite-signup-title">
                <h2 id="invite-signup-title">Creá tu cuenta</h2>
                <p>
                  El correo de la invitación ya queda asociado a tu cuenta nueva: no hace falta
                  escribirlo de nuevo. Al crearla entrás al panel con el rol de la invitación.
                </p>
                <form className="invite__form" method="post" onSubmit={handleSignup} noValidate>
                  <div className="field">
                    <label htmlFor="invite-name">Tu nombre</label>
                    <input
                      id="invite-name"
                      className="input"
                      type="text"
                      name="name"
                      autoComplete="name"
                      value={name}
                      onChange={(event) => setName(event.target.value)}
                      required
                    />
                  </div>
                  <div className="field">
                    <label htmlFor="invite-password">Contraseña</label>
                    <input
                      id="invite-password"
                      className="input"
                      type="password"
                      name="password"
                      autoComplete="new-password"
                      value={password}
                      onChange={(event) => setPassword(event.target.value)}
                      aria-describedby="invite-password-hint"
                      required
                    />
                    <p id="invite-password-hint" className="field__hint">
                      Al menos 8 caracteres. Con esta contraseña vas a entrar al panel desde ahora.
                    </p>
                  </div>
                  <button type="submit" className="btn btn--wine" disabled={pending}>
                    {pending ? 'Creando tu cuenta…' : 'Crear cuenta y entrar'}
                  </button>
                </form>
              </section>
            )}
          </>
        ) : null}
      </section>
    </main>
  )
}
