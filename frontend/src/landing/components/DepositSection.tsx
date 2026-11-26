import type { LandingDeposit } from '../../lib/publicSalon'

type DepositSectionProps = {
  deposit: LandingDeposit
}

/**
 * Section «Seña» of the landing (contract F11, D11). Rendered ONLY when the public
 * payload carries `deposit` (a switched-off policy or a salon without one renders
 * nothing at all). Every number of the section comes from that payload and the
 * amounts of each service are the `deposit_cents` the server computed.
 */
export function DepositSection({ deposit }: DepositSectionProps) {
  return (
    <section className="section deposit" id="sena" aria-labelledby="deposit-title">
      <div className="container">
        <div className="section-head">
          <p className="eyebrow">Seña</p>
          <h2 id="deposit-title">Reserva con seña</h2>
        </div>
        <div className="deposit-block">
          {deposit.simulated ? <p className="landing-badge">Pago simulado</p> : null}
          <p className="deposit-block__sentence">{deposit.sentence}</p>
          <ul className="deposit-block__facts">
            <li>{deposit.holdLabel}</li>
            <li>{deposit.refundLabel}</li>
            <li>El monto de cada servicio está en la lista de servicios.</li>
          </ul>
          <p className="deposit-block__note">
            La seña se paga en el banco simulado de esta instalación: no se cobra dinero real. El
            salón descuenta la seña del precio cuando cobras el turno.
          </p>
        </div>
      </div>
    </section>
  )
}
