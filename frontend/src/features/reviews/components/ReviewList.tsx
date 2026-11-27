import { ReviewStars } from './ReviewStars'
import { reviewCreatedText, type PanelReview } from '../lib/reviews'

type ReviewListProps = {
  items: readonly PanelReview[]
  /** IANA zone of the salon: the date and time are shown in it. */
  timeZone: string
  /** Owner/admin hide and show; reception only reads (D6). */
  canManage: boolean
  /** Id of the row whose hide/show is in flight (one at a time). */
  pendingId: string | null
  onToggle: (review: PanelReview) => void
}

/**
 * Rows of `/panel/resenas` (contract F13, D6/D8): stars, the comment as PLAIN text
 * (never HTML), the author the server decided, the date in the zone of the salon and
 * the service. Owner and admin get the «Ocultar» / «Mostrar» button; the state of the
 * row is the one the server answered (the page paints it optimistically and rolls it
 * back on error).
 */
export function ReviewList({ items, timeZone, canManage, pendingId, onToggle }: ReviewListProps) {
  return (
    <ul className="reviews-list" aria-label="Reseñas del salón">
      {items.map((review) => (
        <li key={review.id} className="reviews-row" data-hidden={review.hidden ? 'true' : undefined}>
          <div className="reviews-row__main">
            <ReviewStars className="reviews-row__stars" rating={review.rating} />
            {review.comment !== '' ? (
              <p className="reviews-row__comment">{review.comment}</p>
            ) : (
              <p className="reviews-row__comment reviews-row__comment--empty">Sin comentario</p>
            )}
            <p className="reviews-row__meta">
              <span>{review.author_name === '' ? 'Persona sin nombre' : review.author_name}</span>
              <span aria-hidden="true"> · </span>
              <time dateTime={review.created_at}>{reviewCreatedText(review.created_at, timeZone)}</time>
              {review.service_name !== '' ? (
                <>
                  <span aria-hidden="true"> · </span>
                  <span>{review.service_name}</span>
                </>
              ) : null}
            </p>
          </div>
          <div className="reviews-row__side">
            <span className={`pill ${review.hidden ? 'pill--off' : 'pill--on'}`}>
              {review.hidden ? 'Oculta' : 'Publicada'}
            </span>
            {canManage ? (
              <button
                type="button"
                className="btn btn--quiet btn--sm"
                onClick={() => onToggle(review)}
                disabled={pendingId !== null}
                aria-label={
                  review.hidden
                    ? `Mostrar en la página pública la reseña de ${review.author_name || 'esta persona'}`
                    : `Ocultar de la página pública la reseña de ${review.author_name || 'esta persona'}`
                }
              >
                {pendingId === review.id ? 'Guardando…' : review.hidden ? 'Mostrar' : 'Ocultar'}
              </button>
            ) : null}
          </div>
        </li>
      ))}
    </ul>
  )
}
