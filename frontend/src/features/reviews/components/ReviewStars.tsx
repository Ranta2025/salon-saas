import { reviewStarsText } from '../lib/reviews'

type ReviewStarsProps = {
  /** Rating 1..5. */
  rating: number
  /** Extra class of the layout that hosts the stars. */
  className?: string
}

/**
 * Five read-only stars (F13, D5/D7): one image with an accessible name, never five
 * separate characters read one by one. Used by the landing and the panel list; the
 * default class is the star row the landing already styles (`.stars`, wine on cream
 * for a readable contrast), so the stars of a review look like the rest of the site.
 */
export function ReviewStars({ rating, className }: ReviewStarsProps) {
  const label = `${rating} de 5 estrellas`
  return (
    <span className={className ?? 'stars'} role="img" aria-label={label}>
      <span aria-hidden="true">{reviewStarsText(rating)}</span>
    </span>
  )
}
