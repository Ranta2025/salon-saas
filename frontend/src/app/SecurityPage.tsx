import { useCallback, useEffect, useReducer, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '../lib/api'
import {
  createSecurityGate,
  disableTwoFactor,
  enableTwoFactor,
  fetchTwoFactorStatus,
  runSecurityAction,
  securityReducer,
  shouldWarnBeforeLeaving,
  startTwoFactorSetup,
  type SecurityView,
} from '../lib/twofa'
import { useOffline } from './OfflineContext'
import { ErrorNotice, LoadingState } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { SecurityDisableForm } from './components/SecurityDisableForm'
import { SecurityRecoveryCodes } from './components/SecurityRecoveryCodes'
import { SecuritySetupCard } from './components/SecuritySetupCard'

const INITIAL_VIEW: SecurityView = { kind: 'loading' }

/**
 * `/panel/seguridad` (contract F10, D7/D12): the two-step verification (TOTP) of
 * the signed-in account. It belongs to the person, not to a salon, so every member
 * sees it. The state comes from `GET /me/2fa`; activating shows the secret and the
 * otpauth address as plain text (no QR library), then the 8 recovery codes ONCE;
 * deactivating asks for the password and a code.
 *
 * Online only: offline nothing is sent and nothing is queued. Server sentences are
 * printed verbatim.
 */
export function SecurityPage() {
  const { online } = useOffline()
  const [view, dispatch] = useReducer(securityReducer, INITIAL_VIEW)
  const [loadError, setLoadError] = useState<ApiErrorNotice | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  // The gate lives outside React state: two presses of the same render would both
  // see `pending === false` and send two requests.
  const gate = useRef(createSecurityGate())

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    const controller = new AbortController()
    setLoadError(null)
    fetchTwoFactorStatus(controller.signal)
      .then((status) => dispatch({ type: 'status', enabled: status.enabled }))
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setLoadError(describeApiError(err, 'No pudimos leer el estado de la verificación.'))
        }
      })
    return () => controller.abort()
  }, [reloadKey])

  // Leaving or reloading while the one-time codes are shown would lose them.
  const warn = shouldWarnBeforeLeaving(view)
  useEffect(() => {
    if (!warn) {
      return
    }
    const handler = (event: BeforeUnloadEvent): void => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [warn])

  const run = async <T,>(action: () => Promise<T>): Promise<{ ok: true; value: T } | { ok: false }> => {
    setPending(true)
    setActionError(null)
    setNotice(null)
    try {
      const outcome = await runSecurityAction(gate.current, action)
      if (outcome.status === 'ok') {
        return { ok: true, value: outcome.value }
      }
      if (outcome.status !== 'busy') {
        setActionError(outcome.message)
        // A 200 that could not be read: the server may have changed state.
        if (outcome.status === 'failed' && outcome.httpStatus === 200) {
          reload()
        }
      }
      return { ok: false }
    } finally {
      setPending(false)
    }
  }

  const handleActivate = (): void => {
    void (async () => {
      const result = await run(startTwoFactorSetup)
      if (result.ok) {
        dispatch({
          type: 'setup_started',
          secret: result.value.secret,
          otpauthUrl: result.value.otpauth_url,
        })
      }
    })()
  }

  const handleConfirm = (code: string): void => {
    void (async () => {
      const result = await run(() => enableTwoFactor(code))
      if (result.ok) {
        dispatch({ type: 'enabled', codes: result.value })
      }
    })()
  }

  const handleDisable = (draft: { password: string; code: string }): void => {
    void (async () => {
      const result = await run(() => disableTwoFactor(draft))
      if (result.ok) {
        dispatch({ type: 'disabled' })
        setNotice('La verificación en dos pasos quedó desactivada.')
      }
    })()
  }

  const enabled = view.kind === 'active' || view.kind === 'disabling'

  return (
    <>
      <PageHeader
        eyebrow="Cuenta"
        title="Seguridad"
        description="La verificación en dos pasos agrega un código de tu celular al iniciar sesión. Es opcional y vale para tu cuenta en todos tus salones."
      />

      {!online ? (
        <p className="notice notice--info" role="status">
          Sin conexión. La seguridad de la cuenta solo se cambia en línea: no se guarda nada para
          más tarde ni queda en cola.
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}
      {actionError && view.kind !== 'setup' && view.kind !== 'disabling' ? (
        <p className="notice notice--error" role="alert">
          {actionError}
        </p>
      ) : null}

      {view.kind === 'loading' && !loadError ? <LoadingState label="Leyendo tu seguridad…" /> : null}
      {loadError ? <ErrorNotice error={loadError} onRetry={reload} /> : null}

      {view.kind === 'inactive' ? (
        <section className="panel-card" aria-labelledby="security-status-title">
          <div className="panel-card__head">
            <h2 id="security-status-title">Verificación en dos pasos</h2>
            <span className="pill pill--off">Desactivada</span>
          </div>
          <p>
            Tu cuenta se protege solo con la contraseña. Activá la verificación en dos pasos para
            que, además, haga falta un código de 6 dígitos de tu app de autenticación.
          </p>
          <div className="security-actions">
            <button type="button" className="btn btn--wine" onClick={handleActivate} disabled={pending}>
              {pending ? 'Preparando…' : 'Activar'}
            </button>
          </div>
        </section>
      ) : null}

      {view.kind === 'setup' ? (
        <SecuritySetupCard
          key={view.secret}
          secret={view.secret}
          otpauthUrl={view.otpauthUrl}
          pending={pending}
          serverError={actionError}
          onConfirm={handleConfirm}
          onCancel={() => {
            setActionError(null)
            dispatch({ type: 'setup_cancelled' })
          }}
        />
      ) : null}

      {view.kind === 'codes' ? (
        <SecurityRecoveryCodes
          codes={view.codes}
          onAcknowledge={() => {
            setNotice('Verificación en dos pasos activada. Desde ahora te pediremos un código al iniciar sesión.')
            dispatch({ type: 'codes_acknowledged' })
          }}
        />
      ) : null}

      {enabled ? (
        <section className="panel-card" aria-labelledby="security-status-title">
          <div className="panel-card__head">
            <h2 id="security-status-title">Verificación en dos pasos</h2>
            <span className="pill pill--on">Activada</span>
          </div>
          <p>
            Al iniciar sesión te pedimos, además de la contraseña, un código de tu app de
            autenticación o uno de tus códigos de recuperación.
          </p>
          {view.kind === 'disabling' ? (
            <SecurityDisableForm
              pending={pending}
              serverError={actionError}
              onSubmit={handleDisable}
              onCancel={() => {
                setActionError(null)
                dispatch({ type: 'disable_cancelled' })
              }}
            />
          ) : (
            <div className="security-actions">
              <button
                type="button"
                className="btn btn--ghost"
                onClick={() => {
                  setActionError(null)
                  setNotice(null)
                  dispatch({ type: 'disable_opened' })
                }}
              >
                Desactivar
              </button>
            </div>
          )}
        </section>
      ) : null}
    </>
  )
}
