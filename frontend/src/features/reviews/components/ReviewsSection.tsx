import type { LandingReviews } from '@/lib/publicSalon'
import { reviewDayLabel } from '../lib/reviews'
import { ReviewStars } from './ReviewStars'

type ReviewsSectionProps = {
  reviews: LandingReviews
}

/**
 * «Reseñas» of the public landing (contract F13, D7/D8). Rendered ONLY when the
 * public payload carries `reviews` (the salon has at least one visible review): the
 * average and the count are the ones the SERVER computed over every visible review,
 * and at most the 6 most recent ones are listed. The comment is printed as plain
 * text — React escapes it — so a review can never inject HTML or a link.
 */
export function ReviewsSection({ reviews }: ReviewsSectionProps) {
  const averageStars = Math.round(reviews.average)
  return (
    <section className="section reviews" id="resenas" aria-labelledby="reviews-title">
      <div className="container">
        <div className="reviews__head">
          <div className="section-head">
            <p className="eyebrow">Reseñas</p>
            <h2 id="reviews-title">Lo que dicen las clientas</h2>
          </div>
          <p className="reviews__summary">
            <span className="reviews__average">{reviews.averageLabel}</span>
            <ReviewStars className="reviews__stars" rating={averageStars} />
            <span className="reviews__count">{reviews.countLabel}</span>
          </p>
        </div>

        {reviews.items.length > 0 ? (
          <ul className="reviews__list">
            {reviews.items.map((item, index) => {
              const day = reviewDayLabel(item.date)
              return (
                <li key={`${item.author}-${item.date}-${String(index)}`} className="review-card">
                  <ReviewStars className="review-card__stars" rating={item.rating} />
                  {item.comment !== '' ? <p className="review-card__comment">{item.comment}</p> : null}
                  <p className="review-card__meta">
                    <span className="review-card__author">{item.author}</span>
                    {day !== '' ? (
                      <>
                        <span aria-hidden="true"> · </span>
                        <time dateTime={item.date}>{day}</time>
                      </>
                    ) : null}
                  </p>
                </li>
              )
            })}
          </ul>
        ) : null}
      </div>
    </section>
  )
}
