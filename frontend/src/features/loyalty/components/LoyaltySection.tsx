import type { LandingLoyalty } from '@/lib/publicSalon'

type LoyaltySectionProps = {
  loyalty: LandingLoyalty
}

/**
 * «Programa de fidelidad» (contract F9, D8). Rendered only when the public payload
 * carries `loyalty` (the program is on and its service is active). The public has
 * no identity, so there is no progress here: only the rule the salon set.
 */
export function LoyaltySection({ loyalty }: LoyaltySectionProps) {
  return (
    <section className="section loyalty" id="fidelidad" aria-labelledby="loyalty-title">
      <div className="container">
        <div className="section-head">
          <p className="eyebrow">Fidelidad</p>
          <h2 id="loyalty-title">Programa de fidelidad</h2>
        </div>
        <p className="loyalty__sentence">{loyalty.sentence}</p>
      </div>
    </section>
  )
}
