import { Link } from 'react-router-dom'
import { chargeResultText, isApproved, resultPath, type PayResult } from '../lib/simbank'

type BankOutcomeProps = {
  chargeId: string
  result: PayResult
}

/**
 * Result of the payment (contract F11, D13-c): the outcome in Spanish, the sentence
 * of the bank verbatim when it sent one, and the link back to the salon with the
 * charge id in the fragment (`/pago-resultado#<id>`).
 */
export function BankOutcome({ chargeId, result }: BankOutcomeProps) {
  const approved = isApproved(result.status)
  return (
    <div
      className={`bank-outcome ${approved ? 'bank-outcome--ok' : 'bank-outcome--ko'}`}
      role={approved ? 'status' : 'alert'}
    >
      <h3 className="bank-outcome__title">{chargeResultText(result.status)}</h3>
      {/* The sentence of the bank verbatim, unless it repeats the title word for word. */}
      {result.message !== null && result.message !== chargeResultText(result.status) ? (
        <p className="bank-outcome__message">{result.message}</p>
      ) : null}
      {result.last4 !== null ? <p className="bank-outcome__card">Tarjeta •••• {result.last4}</p> : null}
      <p className="bank-outcome__note">
        {approved
          ? 'El salón recibe la confirmación del pago simulado. Podés volver para ver el estado de tu turno.'
          : 'No se cobró nada. Podés volver al salón para ver el estado de la reserva o intentar con otra tarjeta de prueba.'}
      </p>
      <Link className="btn btn--wine" to={resultPath(chargeId)}>
        Volver al salón
      </Link>
    </div>
  )
}
