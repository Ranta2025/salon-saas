import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { formatPublicInstant } from '../lib/deposits'
import { formatCents } from '../lib/money'
import { visitorTimeZone } from '../lib/publicBooking'
import {
  chargeResultText,
  createSimbankGate,
  describeSimbankError,
  formatCardNumber,
  formatExpiry,
  lookupCharge,
  payCharge,
  TEST_CARDS,
  validateCardDraft,
  type CardDraft,
  type ChargeLookup,
  type PayResult,
  type SimbankFailure,
} from '../lib/simbank'
import { BankBanner } from './components/BankBanner'
import { BankOutcome } from './components/BankOutcome'
import { BankTestCards } from './components/BankTestCards'
import { LoadingState } from './components/Feedback'
import { useFragmentToken } from './components/RecoveryToken'

const EMPTY_CARD: CardDraft = { number: '', exp: '', cvc: '', name: '' }

type LookupState =
  | { kind: 'loading' }
  | { kind: 'ready'; charge: ChargeLookup }
  | { kind: 'error'; message: string }

/**
 * Public `/pagar` page: the simulated bank of contract F11 (D6, D13-c).
 *
 * The charge id comes in the FRAGMENT of the link the salon returned
 * (`/pagar#<charge_id>`): `useFragmentToken` (the F15 helper) reads it once and wipes
 * it from the address bar with `history.replaceState` before any request. The id
 * travels in the BODY of both calls (`lookup` and `pay`), never in a URL.
 *
 * The card lives only in React state: it is not stored, not logged, not put in the
 * URL and it is dropped as soon as the bank answers. The form is validated before
 * sending (Luhn is only a hint) and a second «Pagar» press is refused by a
 * synchronous gate while the first call is in flight.
 */
export function BankPage() {
  const chargeId = useFragmentToken()
  const [lookup, setLookup] = useState<LookupState>(
    chargeId === null ? { kind: 'error', message: '' } : { kind: 'loading' },
  )
  const [card, setCard] = useState<CardDraft>(EMPTY_CARD)
  const [fieldError, setFieldError] = useState<{ field: string; message: string } | null>(null)
  const [luhnHint, setLuhnHint] = useState<string | null>(null)
  const [paying, setPaying] = useState(false)
  const [failure, setFailure] = useState<SimbankFailure | null>(null)
  const [result, setResult] = useState<PayResult | null>(null)
  // A POST is sent ONCE (React StrictMode runs the effect twice in development): the
  // ref guards the request and `mounted` guards the write, which is why the second
  // StrictMode run re-arms `mounted` instead of cancelling the answer of the first.
  const startedRef = useRef(false)
  const mountedRef = useRef(false)
  const gate = useRef(createSimbankGate())

  useEffect(() => {
    mountedRef.current = true
    if (chargeId === null || startedRef.current) {
      return () => {
        mountedRef.current = false
      }
    }
    startedRef.current = true
    lookupCharge(chargeId)
      .then((charge) => {
        if (mountedRef.current) {
          setLookup({ kind: 'ready', charge })
        }
      })
      .catch((error: unknown) => {
        if (mountedRef.current) {
          setLookup({ kind: 'error', message: describeSimbankError(error).message })
        }
      })
    return () => {
      mountedRef.current = false
    }
  }, [chargeId])

  if (chargeId === null) {
    return (
      <main className="bank">
        <section className="bank__card" aria-labelledby="bank-title">
          <BankBanner />
          <h1 id="bank-title">Enlace de pago incompleto</h1>
          <p className="bank__lede">
            Este enlace no trae el cobro del salón. Volvé a la página del salón y reservá de nuevo
            para generar otro pago simulado.
          </p>
          <p className="bank__actions">
            <Link className="btn btn--quiet" to="/">
              Ir al salón
            </Link>
          </p>
        </section>
      </main>
    )
  }

  const charge = lookup.kind === 'ready' ? lookup.charge : null
  // A charge that already had a result BEFORE this page is not payable again. Our own
  // payment is painted by `BankOutcome`, so it is not mixed with that notice.
  const decidedElsewhere = charge !== null && result === null && charge.status !== 'pending'
  const timeZone = visitorTimeZone()

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (paying || decidedElsewhere) {
      return
    }
    const check = validateCardDraft(card)
    if (!check.ok) {
      setFieldError({ field: check.field, message: check.message })
      setLuhnHint(null)
      document.getElementById(`bank-${check.field}`)?.focus()
      return
    }
    setFieldError(null)
    setLuhnHint(check.luhnHint)
    // A second tap never becomes a second payment.
    if (!gate.current.start()) {
      return
    }
    setPaying(true)
    setFailure(null)
    void (async () => {
      try {
        const paid = await payCharge(chargeId, check.card)
        setResult(paid)
        // The card is no longer needed: it is dropped from memory as soon as it was used.
        setCard(EMPTY_CARD)
      } catch (error: unknown) {
        setFailure(describeSimbankError(error))
      } finally {
        gate.current.finish()
        setPaying(false)
      }
    })()
  }

  const change = (patch: Partial<CardDraft>): void => {
    setCard((current) => ({ ...current, ...patch }))
    setFieldError(null)
    setFailure(null)
  }

  const errorText = (field: string): string | null =>
    fieldError?.field === field ? fieldError.message : null

  const fullTestCard = TEST_CARDS[0]

  return (
    <main className="bank">
      <section className="bank__card" aria-labelledby="bank-title">
        <BankBanner />
        <p className="eyebrow">Banco simulado</p>
        <h1 id="bank-title">Pagar la seña</h1>

        {lookup.kind === 'loading' ? <LoadingState label="Buscando el cobro…" /> : null}

        {lookup.kind === 'error' ? (
          <div className="notice notice--error" role="alert">
            <p>{lookup.message}</p>
            <Link to="/">Ir al salón</Link>
          </div>
        ) : null}

        {charge !== null ? (
          <>
            <dl className="bank__summary">
              <div>
                <dt>Monto</dt>
                <dd className="bank__amount">{formatCents(charge.amountCents)}</dd>
              </div>
              <div>
                <dt>Concepto</dt>
                <dd>{charge.description === '' ? 'Seña del salón' : charge.description}</dd>
              </div>
              {/* Once there is an outcome the card below says it: no doubled state. */}
              {result === null ? (
                <div>
                  <dt>Estado</dt>
                  <dd>{chargeResultText(charge.status)}</dd>
                </div>
              ) : null}
              {charge.expiresAt !== null ? (
                <div>
                  <dt>Vence</dt>
                  <dd>
                    <time dateTime={charge.expiresAt}>
                      {formatPublicInstant(charge.expiresAt, timeZone)}
                    </time>
                  </dd>
                </div>
              ) : null}
            </dl>

            {decidedElsewhere ? (
              <p className="notice notice--info" role="status">
                Este cobro ya tiene un resultado y no se puede volver a pagar.
              </p>
            ) : null}

            {result !== null ? <BankOutcome chargeId={chargeId} result={result} /> : null}

            {!decidedElsewhere && result === null ? (
              <form className="bank__form" onSubmit={handleSubmit} noValidate>
                <h2 className="bank__form-title">Datos de la tarjeta</h2>
                <div className="field">
                  <label htmlFor="bank-number">Número de tarjeta</label>
                  <input
                    id="bank-number"
                    className="input bank__number"
                    type="text"
                    inputMode="numeric"
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="4242 4242 4242 4242"
                    value={card.number}
                    onChange={(event) => change({ number: formatCardNumber(event.target.value) })}
                    aria-invalid={errorText('number') ? true : undefined}
                    aria-describedby={errorText('number') ? 'bank-number-error' : 'bank-number-hint'}
                  />
                  {errorText('number') ? (
                    <p className="field__error" id="bank-number-error" role="alert">
                      {errorText('number')}
                    </p>
                  ) : (
                    <p className="field__hint" id="bank-number-hint">
                      Solo dígitos. El número nunca se guarda: el banco simulado devuelve los
                      últimos cuatro.
                    </p>
                  )}
                </div>
                <div className="bank__row">
                  <div className="field">
                    <label htmlFor="bank-exp">Vencimiento</label>
                    <input
                      id="bank-exp"
                      className="input"
                      type="text"
                      inputMode="numeric"
                      autoComplete="off"
                      placeholder="MM/AA"
                      value={card.exp}
                      onChange={(event) => change({ exp: formatExpiry(event.target.value) })}
                      aria-invalid={errorText('exp') ? true : undefined}
                    />
                    {errorText('exp') ? (
                      <p className="field__error" role="alert">
                        {errorText('exp')}
                      </p>
                    ) : null}
                  </div>
                  <div className="field">
                    <label htmlFor="bank-cvc">Código de seguridad</label>
                    <input
                      id="bank-cvc"
                      className="input"
                      type="text"
                      inputMode="numeric"
                      autoComplete="off"
                      placeholder="123"
                      value={card.cvc}
                      onChange={(event) => change({ cvc: event.target.value.replace(/[^0-9]/g, '') })}
                      aria-invalid={errorText('cvc') ? true : undefined}
                    />
                    {errorText('cvc') ? (
                      <p className="field__error" role="alert">
                        {errorText('cvc')}
                      </p>
                    ) : null}
                  </div>
                </div>
                <div className="field">
                  <label htmlFor="bank-name">Nombre como figura en la tarjeta</label>
                  <input
                    id="bank-name"
                    className="input"
                    type="text"
                    autoComplete="off"
                    value={card.name}
                    onChange={(event) => change({ name: event.target.value })}
                    aria-invalid={errorText('name') ? true : undefined}
                  />
                  {errorText('name') ? (
                    <p className="field__error" role="alert">
                      {errorText('name')}
                    </p>
                  ) : null}
                </div>

                <BankTestCards
                  onPick={(digits) =>
                    change({
                      number: formatCardNumber(digits),
                      exp: '12/27',
                      cvc: '123',
                      name: card.name.trim() === '' ? 'Prueba Simulada' : card.name,
                    })
                  }
                />

                {luhnHint !== null ? (
                  <p className="field__hint bank__luhn" role="status">
                    {luhnHint}
                  </p>
                ) : null}

                {failure !== null ? (
                  <div className="notice notice--error" role="alert">
                    <p>{failure.message}</p>
                    {failure.kind === 'too_many' ? (
                      <p className="field__hint">
                        Cerrá esta página y generá una seña nueva desde el salón.
                      </p>
                    ) : null}
                  </div>
                ) : null}

                <button type="submit" className="btn btn--wine bank__pay" disabled={paying}>
                  {paying ? 'Procesando el pago…' : `Pagar ${formatCents(charge.amountCents)}`}
                </button>
                <p className="bank__fineprint">
                  Pago de prueba: usá una de las tarjetas de arriba. La aprobada es{' '}
                  <button
                    type="button"
                    className="bank__ink"
                    onClick={() => change({ number: formatCardNumber(fullTestCard.digits) })}
                  >
                    {fullTestCard.number}
                  </button>
                  : tocá el número para completar el formulario.
                </p>
              </form>
            ) : null}

            {/* The outcome card already carries «Volver al salon» once there is one. */}
            {result === null ? (
              <p className="bank__actions">
                <Link className="btn btn--quiet" to={`/pago-resultado#${chargeId}`}>
                  Volver al salon
                </Link>
              </p>
            ) : null}
          </>
        ) : null}
      </section>
    </main>
  )
}
