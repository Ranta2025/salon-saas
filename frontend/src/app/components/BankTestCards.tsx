import { TEST_CARDS } from '../../lib/simbank'

type BankTestCardsProps = {
  /** Fills the form with the digits of the card (never submits anything). */
  onPick: (digits: string) => void
}

/**
 * The three test cards of D6 as buttons. They only FILL the form: the person still
 * presses «Pagar» and the bank is the one that decides the outcome.
 */
export function BankTestCards({ onPick }: BankTestCardsProps) {
  return (
    <div className="bank-testcards">
      <p className="bank-testcards__title">Tarjetas de prueba</p>
      <ul>
        {TEST_CARDS.map((card) => (
          <li key={card.digits}>
            <button
              type="button"
              className="btn btn--quiet btn--sm bank-testcards__btn"
              onClick={() => onPick(card.digits)}
            >
              <span className="bank-testcards__number">{card.number}</span>
              <span className="bank-testcards__label">{card.label}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}
