import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  depositStatusLabel,
  depositStatusSentence,
  formatPublicInstant,
  lookupPublicDeposit,
  PAYMENT_POLL_INTERVAL_MS,
  PAYMENT_POLL_TIMEOUT_MS,
  pollDecision,
  type PublicDepositLookup,
} from '../lib/deposits'
import { describeSimbankError } from '../lib/simbank'
import { formatCents } from '../lib/money'
import { visitorTimeZone } from '../lib/publicBooking'
import { LoadingState } from './components/Feedback'
import { useFragmentToken } from './components/RecoveryToken'

/** Once the polling stops, the page says so instead of spinning forever. */
const POLL_GIVE_UP_MESSAGE =
  'Seguimos sin la confirmación del banco. El salón la recibe igual: podés cerrar esta página y consultar tu turno más tarde.'

type View =
  | { kind: 'loading' }
  | { kind: 'ready'; deposit: PublicDepositLookup }
  | { kind: 'error'; message: string }

/**
 * Public `/pago-resultado` page (contract F11, D8, D13-c): reads the charge id from
 * the fragment (the F15 helper wipes it from the address bar), asks the salon for the
 * state with `POST /api/v1/public/deposits/lookup` and, while it is `pending`, asks
 * again every 3 s up to 60 s — then it stops and says so instead of polling forever.
 *
 * The state is the server's: this page never decides that a payment went through.
 * The 404 `Pago no encontrado` is shown verbatim.
 */
export function PaymentResultPage() {
  const chargeId = useFragmentToken()
  const [view, setView] = useState<View>(
    chargeId === null
      ? { kind: 'error', message: 'Este enlace no trae el cobro del salón.' }
      : { kind: 'loading' },
  )
  const [givenUp, setGivenUp] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const startedRef = useRef(false)
  const mountedRef = useRef(false)
  const startedAtRef = useRef(0)

  // First read (StrictMode runs the effect twice: the ref keeps it to one request).
  useEffect(() => {
    mountedRef.current = true
    if (chargeId === null || startedRef.current) {
      return
    }
    startedRef.current = true
    startedAtRef.current = Date.now()
    lookupPublicDeposit(chargeId)
      .then((deposit) => {
        if (!mountedRef.current) {
          return
        }
        if (deposit === null) {
          setView({ kind: 'error', message: 'El salón no devolvió el estado del pago.' })
          return
        }
        setView({ kind: 'ready', deposit })
        setAttempt(1)
      })
      .catch((error: unknown) => {
        if (mountedRef.current) {
          setView({ kind: 'error', message: describeSimbankError(error).message })
        }
      })
    return () => {
      mountedRef.current = false
    }
  }, [chargeId])

  // While the deposit is pending: one more read every 3 s, at most until the 60 s budget.
  const pending = view.kind === 'ready' && view.deposit.status === 'pending'
  useEffect(() => {
    if (chargeId === null || !pending || attempt === 0) {
      return
    }
    const elapsed = Date.now() - startedAtRef.current
    if (pollDecision({ status: 'pending', elapsedMs: elapsed, attempt }) === 'stop') {
      setGivenUp(true)
      return
    }
    const timer = setTimeout(() => {
      lookupPublicDeposit(chargeId)
        .then((deposit) => {
          if (!mountedRef.current || deposit === null) {
            return
          }
          setView({ kind: 'ready', deposit })
          setAttempt((current) => current + 1)
        })
        .catch(() => {
          // A failed poll is not a failed payment: the next one asks again.
          if (mountedRef.current) {
            setAttempt((current) => current + 1)
          }
        })
    }, PAYMENT_POLL_INTERVAL_MS)
    return () => clearTimeout(timer)
  }, [chargeId, pending, attempt])

  if (chargeId === null) {
    return (
      <main className="bank">
        <section className="bank__card" aria-labelledby="result-title">
          <p className="eyebrow">Pago de la seña</p>
          <h1 id="result-title">Enlace incompleto</h1>
          <p className="bank__lede">{view.kind === 'error' ? view.message : ''}</p>
          <p className="bank__actions">
            <Link className="btn btn--quiet" to="/">
              Ir al salón
            </Link>
          </p>
        </section>
      </main>
    )
  }

  const deposit = view.kind === 'ready' ? view.deposit : null
  const timeZone = visitorTimeZone()

  return (
    <main className="bank">
      <section className="bank__card" aria-labelledby="result-title">
        <p className="eyebrow">Pago de la seña</p>
        <h1 id="result-title">Estado del pago</h1>

        {view.kind === 'loading' ? <LoadingState label="Consultando el pago…" /> : null}

        {view.kind === 'error' ? (
          <div className="notice notice--error" role="alert">
            <p>{view.message}</p>
            <Link to="/">Ir al salón</Link>
          </div>
        ) : null}

        {deposit !== null ? (
          <>
            <p
              className={`pill pill--deposit-${deposit.status === 'paid' ? 'on' : deposit.status === 'pending' ? 'warn' : 'off'}`}
            >
              {depositStatusLabel(deposit.status)}
            </p>
            <p className="bank__lede" role="status" aria-live="polite">
              {depositStatusSentence(deposit.status)}
            </p>
            <dl className="bank__summary">
              <div>
                <dt>Seña</dt>
                <dd className="bank__amount">{formatCents(deposit.amountCents)}</dd>
              </div>
              <div>
                <dt>Turno</dt>
                <dd>
                  <time dateTime={deposit.startsAt}>
                    {formatPublicInstant(deposit.startsAt, timeZone)}
                  </time>
                </dd>
              </div>
              <div>
                <dt>Plazo del pago</dt>
                <dd>
                  <time dateTime={deposit.expiresAt}>
                    {formatPublicInstant(deposit.expiresAt, timeZone)}
                  </time>
                </dd>
              </div>
            </dl>

            {deposit.status === 'pending' ? (
              <p className="bank__fineprint" role="status">
                {givenUp
                  ? POLL_GIVE_UP_MESSAGE
                  : `Consultamos el estado cada ${PAYMENT_POLL_INTERVAL_MS / 1000} segundos, hasta ${PAYMENT_POLL_TIMEOUT_MS / 1000} segundos.`}
              </p>
            ) : null}
          </>
        ) : null}

        <p className="bank__actions">
          <Link className="btn btn--quiet" to="/">
            Ir al salón
          </Link>
        </p>
      </section>
    </main>
  )
}
