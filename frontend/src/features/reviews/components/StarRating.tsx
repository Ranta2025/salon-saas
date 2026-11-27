import { MAX_REVIEW_RATING, reviewRatingLabel } from '../lib/reviews'

type StarRatingProps = {
  /** 0 = nothing chosen yet. */
  value: number
  onChange: (rating: number) => void
  disabled?: boolean
  /** Message about the rating, linked with `aria-describedby`. */
  errorId?: string
  invalid?: boolean
}

const STARS = [1, 2, 3, 4, 5] as const

/**
 * Accessible star rating of the public `/resena` page (contract F13, D4/D8): a
 * `radiogroup` of five native radios (arrow keys move and choose, Tab leaves the
 * group), each one with a real label — «4 de 5: Buena» — so a screen reader does not
 * read five loose stars. The glyph only paints the state; the radio is the control.
 */
export function StarRating({ value, onChange, disabled = false, errorId, invalid = false }: StarRatingProps) {
  return (
    <fieldset
      className="review-stars"
      role="radiogroup"
      aria-describedby={errorId}
      data-invalid={invalid ? 'true' : undefined}
    >
      <legend className="review-stars__legend">Calificación</legend>
      <div className="review-stars__row">
        {STARS.map((star) => (
          <label key={star} className="review-star" data-filled={star <= value ? 'true' : undefined}>
            <input
              className="review-star__input visually-hidden"
              type="radio"
              name="review-rating"
              value={star}
              checked={value === star}
              disabled={disabled}
              onChange={() => onChange(star)}
            />
            <span className="review-star__glyph" aria-hidden="true">
              ★
            </span>
            <span className="visually-hidden">
              {star} de 5: {reviewRatingLabel(star)}
            </span>
          </label>
        ))}
      </div>
      <p className="review-stars__hint">
        {value === 0
          ? 'Elegí de 1 a 5 estrellas'
          : `${value} de ${MAX_REVIEW_RATING}: ${reviewRatingLabel(value)}`}
      </p>
    </fieldset>
  )
}
