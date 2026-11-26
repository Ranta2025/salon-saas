import { SIMULATED_BANNER } from '../../lib/simbank'

/**
 * Permanent banner of the simulated bank (contract F11, D13-c). It is not a toast
 * and it cannot be dismissed: whoever lands on `/pagar` must read, at any screen
 * size, that no real money is charged.
 */
export function BankBanner() {
  return (
    <p className="bank-banner" role="note">
      {SIMULATED_BANNER}
    </p>
  )
}
